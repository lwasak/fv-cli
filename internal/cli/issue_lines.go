package cli

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/invoice"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/validate"
)

// lineKind tells --item from --line.
type lineKind int

const (
	kindItem lineKind = iota + 1
	kindSpec
)

// lineArg is one --item or --line value as typed, in command-line order.
type lineArg struct {
	kind lineKind
	raw  string
}

// lineArgs is a pflag.Value that appends every use of a flag to one shared slice, so that
// --item and --line keep the order in which they were typed. It is not a StringSlice on
// purpose: that would split a value such as "1,5" at the comma.
type lineArgs struct {
	dst  *[]lineArg
	kind lineKind
}

func (l *lineArgs) String() string { return "" }

func (l *lineArgs) Set(v string) error {
	*l.dst = append(*l.dst, lineArg{kind: l.kind, raw: v})
	return nil
}

func (l *lineArgs) Type() string {
	if l.kind == kindItem {
		return "item"
	}
	return "spec"
}

// flagText quotes a raw flag value for an error message, shortened when it is long.
func flagText(name, raw string) string {
	r := []rune(raw)
	if len(r) > 60 {
		raw = string(r[:57]) + "..."
	}
	// A value with a quote or an unprintable character is shown escaped, so an error message
	// never sends raw control characters to the terminal.
	if strings.ContainsRune(raw, '"') || strings.IndexFunc(raw, func(r rune) bool { return !unicode.IsPrint(r) && r != ' ' }) >= 0 {
		return name + " " + fmt.Sprintf("%q", raw)
	}
	return name + ` "` + raw + `"`
}

// parseItemArg splits an --item value, <key> or <key>=<qty>. The quantity, when present, is
// parsed with money.ParseQuantity, so "1,5" works.
func parseItemArg(raw string) (key string, qty *money.Quantity, err error) {
	what := flagText("--item", raw)
	key, q, hasQty := strings.Cut(raw, "=")
	key = strings.TrimSpace(key)
	if key == "" {
		return "", nil, fmt.Errorf(`%s: the item key is empty; use --item <key> or --item <key>=<qty>`, what)
	}
	if !hasQty {
		return key, nil, nil
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return "", nil, fmt.Errorf(`%s: the quantity after "=" is empty`, what)
	}
	n, err := money.ParseQuantity(q)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", what, err)
	}
	return key, &n, nil
}

// lineFields is a parsed --line value. Rate stays a code: it is parsed together with the
// invoice currency, which decides the wording of a rejection.
type lineFields struct {
	Desc               string
	Qty                *money.Quantity
	Unit               string
	Price, Net         *money.Amount
	Rate               string
	Index, PKWiU, GTIN string
}

const lineFieldNames = "desc, qty, unit, price, net, rate, index, pkwiu, gtin"

// parseLineArg parses `field=value; field=value; ...`. Field names are case-sensitive, may not
// repeat, and the values may not contain ';'.
func parseLineArg(raw string) (lineFields, error) {
	what := flagText("--line", raw)
	fail := func(format string, a ...any) (lineFields, error) {
		return lineFields{}, fmt.Errorf("%s: %s", what, fmt.Sprintf(format, a...))
	}
	var f lineFields
	seen := map[string]bool{}
	prev := ""
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, val, ok := strings.Cut(part, "=")
		name, val = strings.TrimSpace(name), strings.TrimSpace(val)
		if !ok {
			hint := ""
			if prev == "desc" {
				hint = `; a ";" cannot be part of a value, so define such a line in an [items.<key>] table`
			}
			return fail(`%q is not field=value%s`, part, hint)
		}
		switch name {
		case "desc", "qty", "unit", "price", "net", "rate", "index", "pkwiu", "gtin":
		default:
			return fail("unknown field %q; use %s", name, lineFieldNames)
		}
		if seen[name] {
			return fail(`field %q appears twice`, name)
		}
		seen[name] = true
		prev = name
		if val == "" {
			return fail(`field %q is empty`, name)
		}
		var err error
		switch name {
		case "desc":
			if err = validate.LineTemplate(val); err == nil {
				err = validate.Token(val, 512)
			}
			f.Desc = val
		case "qty":
			var q money.Quantity
			if q, err = money.ParseQuantity(val); err == nil {
				f.Qty = &q
			}
		case "unit":
			err = validate.Token(val, 256)
			f.Unit = val
		case "price", "net":
			var a money.Amount
			if a, err = money.ParseAmount(val); err == nil {
				if name == "price" {
					f.Price = &a
				} else {
					f.Net = &a
				}
			}
		case "rate":
			f.Rate = val
		case "index":
			err = validate.Token(val, 50)
			f.Index = val
		case "pkwiu":
			err = validate.Token(val, 50)
			f.PKWiU = val
		case "gtin":
			err = validate.GTIN(val)
			f.GTIN = val
		}
		if err != nil {
			return lineFields{}, fmt.Errorf("%s: field %q: %w", what, name, err)
		}
	}
	switch {
	case !seen["desc"]:
		return fail(`field "desc" is missing`)
	case f.Price != nil && f.Net != nil:
		return fail("give price or net, not both")
	case f.Price == nil && f.Net == nil:
		return fail("give price or net")
	case f.Qty != nil && f.Net != nil:
		return fail(`"qty" cannot be used with "net"`)
	}
	return f, nil
}

// parseLineRate parses a VAT rate code. A rate that is known but unsupported (0, np) gets the
// explanation fv issue always gives; the wording depends on the invoice currency.
func parseLineRate(code, currency string) (money.Rate, error) {
	r, err := money.ParseRate(code)
	if err == nil {
		return r, nil
	}
	if errors.Is(err, money.ErrRateNotSupported) {
		if currency != "PLN" {
			return money.Rate{}, fmt.Errorf("VAT rate %q with currency %s is not supported in v1: reverse charge (np) and 0%% rates are out of scope, so foreign-currency invoices need a Polish VAT rate (23, 8, 5 or zw); issue in PLN or use one of those rates", code, currency)
		}
		return money.Rate{}, fmt.Errorf("VAT rate %q is not supported in v1 (rates 0 and np, such as reverse charge and export, are out of scope); use 23, 8, 5 or zw", code)
	}
	return money.Rate{}, fmt.Errorf("VAT rate: %w", err)
}

// usesSingleLine reports whether any single-line flag is set.
func (f issueFlags) usesSingleLine() bool {
	return f.net != "" || f.qty != "" || f.unitPrice != "" || f.desc != ""
}

// resolveLines turns the flags, or the party's saved items, into line specs in order.
func (s *session) resolveLines(cfg *config.Config, f issueFlags, set issueSettings) ([]invoice.LineSpec, error) {
	party := cfg.Parties[f.to]
	if f.usesSingleLine() && len(f.lines) > 0 {
		return nil, errors.New("use --net/--qty/--unit-price/--desc for a single line, or --item/--line for several lines, not both")
	}
	switch {
	case len(f.lines) > 0:
		return linesFromFlags(cfg, f, set)
	case f.usesSingleLine():
		spec, err := singleLine(f, set)
		if err != nil {
			if len(party.Items) > 0 && f.net == "" && f.qty == "" && f.unitPrice == "" {
				return nil, errors.New("--desc describes a single line: add --net, or --qty with --unit-price (the saved items of [parties." + f.to + "] are used only when no line flag is given)")
			}
			return nil, err
		}
		return []invoice.LineSpec{spec}, nil
	case len(party.Items) > 0:
		return linesFromPartyItems(cfg, f, set)
	}
	return nil, errors.New("give a net amount, or quantity with unit price, or --item/--line, or set items in [parties." + f.to + "]")
}

// singleLine builds the one line the --net, --qty, --unit-price and --desc flags describe.
func singleLine(f issueFlags, set issueSettings) (invoice.LineSpec, error) {
	spec := invoice.LineSpec{Template: set.template, Unit: set.unit}
	if err := validate.NoControl(f.desc); err != nil {
		return spec, fmt.Errorf("--desc: %w", err)
	}
	if strings.TrimSpace(f.desc) != "" {
		spec.Template = f.desc
	}
	if f.net != "" {
		a, err := money.ParseAmount(f.net)
		if err != nil {
			return spec, fmt.Errorf("--net: %w", err)
		}
		spec.Net = &a
	}
	if f.qty != "" {
		q, err := money.ParseQuantity(f.qty)
		if err != nil {
			return spec, fmt.Errorf("--qty: %w", err)
		}
		spec.Qty = &q
	}
	if f.unitPrice != "" {
		a, err := money.ParseAmount(f.unitPrice)
		if err != nil {
			return spec, fmt.Errorf("--unit-price: %w", err)
		}
		spec.UnitPrice = &a
	}
	if f.unitPrice != "" && f.qty == "" && f.net == "" {
		return spec, errors.New("quantity and unit price must be given together")
	}
	if f.net == "" && f.qty == "" && f.unitPrice == "" {
		return spec, errors.New("give a net amount, or quantity with unit price, or --item/--line, or set items in [parties." + f.to + "]")
	}
	if set.defaultRate == nil {
		return spec, errors.New("no VAT rate: pass --rate (23, 8, 5 or zw) or set vat_rate in [defaults] or the party")
	}
	spec.Rate = *set.defaultRate
	return spec, nil
}

func linesFromFlags(cfg *config.Config, f issueFlags, set issueSettings) ([]invoice.LineSpec, error) {
	var out []invoice.LineSpec
	for _, a := range f.lines {
		var (
			spec invoice.LineSpec
			err  error
		)
		switch a.kind {
		case kindItem:
			spec, err = itemLine(cfg, f, set, a.raw)
		default:
			spec, err = adHocLine(set, a.raw)
		}
		if err != nil {
			return nil, err
		}
		if spec.Rate.Code == "" {
			return nil, fmt.Errorf("line %d (%s): %s", len(out)+1, spec.Source, noRateHint(spec.Item != ""))
		}
		out = append(out, spec)
	}
	return out, nil
}

func noRateHint(item bool) string {
	if item {
		return "no VAT rate: set vat_rate in the item, pass --rate, or set vat_rate in [defaults] or the party"
	}
	return "no VAT rate: add rate=…, pass --rate, or set vat_rate in [defaults] or the party"
}

// itemLine builds a line from an --item value.
func itemLine(cfg *config.Config, f issueFlags, set issueSettings, raw string) (invoice.LineSpec, error) {
	key, qty, err := parseItemArg(raw)
	if err != nil {
		return invoice.LineSpec{}, err
	}
	source := flagText("--item", raw)
	if _, ok := cfg.Items[key]; !ok {
		return invoice.LineSpec{}, fmt.Errorf("%s: %w", source, validate.UnknownItem(key, cfg.Items))
	}
	return fromItem(cfg, key, qty, source, set)
}

// fromItem builds a line from a saved item. qty, when not nil, replaces the item's quantity.
func fromItem(cfg *config.Config, key string, qty *money.Quantity, source string, set issueSettings) (invoice.LineSpec, error) {
	it := cfg.Items[key]
	if it.Currency != "" && it.Currency != set.currency {
		return invoice.LineSpec{}, fmt.Errorf("item %q is priced in %s, but this invoice is in %s; use a --line or an item in %s", key, it.Currency, set.currency, set.currency)
	}
	price, err := money.ParseAmount(it.UnitPrice)
	if err != nil {
		return invoice.LineSpec{}, fmt.Errorf("items.%s.unit_price: %w", key, err)
	}
	spec := invoice.LineSpec{Source: source, Item: key, Template: it.Name, Unit: firstNonEmpty(it.Unit, set.unit),
		UnitPrice: &price, Index: it.Index, PKWiU: it.PKWiU, GTIN: it.GTIN}
	switch {
	case qty != nil:
		spec.Qty = qty
	case it.Quantity != "":
		q, err := money.ParseQuantity(it.Quantity)
		if err != nil {
			return invoice.LineSpec{}, fmt.Errorf("items.%s.quantity: %w", key, err)
		}
		spec.Qty = &q
	}
	if it.VatRate != "" {
		if spec.Rate, err = parseLineRate(it.VatRate, set.currency); err != nil {
			return invoice.LineSpec{}, fmt.Errorf("items.%s.vat_rate: %w", key, err)
		}
	} else if set.defaultRate != nil {
		spec.Rate = *set.defaultRate
	}
	return spec, nil
}

// adHocLine builds a line from a --line value.
func adHocLine(set issueSettings, raw string) (invoice.LineSpec, error) {
	lf, err := parseLineArg(raw)
	if err != nil {
		return invoice.LineSpec{}, err
	}
	spec := invoice.LineSpec{Source: flagText("--line", raw), Template: lf.Desc, Unit: firstNonEmpty(lf.Unit, set.unit),
		Qty: lf.Qty, UnitPrice: lf.Price, Net: lf.Net, Index: lf.Index, PKWiU: lf.PKWiU, GTIN: lf.GTIN}
	if lf.Rate != "" {
		if spec.Rate, err = parseLineRate(lf.Rate, set.currency); err != nil {
			return invoice.LineSpec{}, fmt.Errorf("%s: %w", spec.Source, err)
		}
	} else if set.defaultRate != nil {
		spec.Rate = *set.defaultRate
	}
	return spec, nil
}

func linesFromPartyItems(cfg *config.Config, f issueFlags, set issueSettings) ([]invoice.LineSpec, error) {
	var out []invoice.LineSpec
	for _, key := range cfg.Parties[f.to].Items {
		source := fmt.Sprintf("items.%s (parties.%s.items)", key, f.to)
		spec, err := fromItem(cfg, key, nil, source, set)
		if err != nil {
			return nil, err
		}
		if spec.Rate.Code == "" {
			return nil, fmt.Errorf("line %d (%s): %s", len(out)+1, source, noRateHint(true))
		}
		out = append(out, spec)
	}
	return out, nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
