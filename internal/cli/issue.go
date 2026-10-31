package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fa3"
	"github.com/lwasak/fv-cli/internal/invoice"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/numbering"
	"github.com/lwasak/fv-cli/internal/registry"
	"github.com/lwasak/fv-cli/internal/store"
	"github.com/lwasak/fv-cli/internal/validate"
)

const (
	defaultPaymentDays  = 14
	defaultLineTemplate = "Usługi za {month_name} {year}"
	defaultUnit         = "usł."
	// mppThreshold is the gross amount (15 000 PLN, in grosze) from which split payment may
	// be mandatory for goods and services listed in Annex 15 to the VAT act.
	mppThreshold money.Amount = 1_500_000
)

// buildFA3 renders the invoice XML; a variable so tests can force a failure after the
// number has been reserved.
var buildFA3 = fa3.Build

// issueFlags are the raw values of the fv issue flags.
type issueFlags struct {
	to, net, qty, unitPrice, rate, currency, issueDate, saleDate, desc string
	lines                                                              []lineArg // --item and --line, in command-line order
	validate, send, yes                                                bool
}

func newIssueCmd(s *session) *cobra.Command {
	var f issueFlags
	cmd := &cobra.Command{
		Use:   "issue --to <alias> (--net <amount> | --qty <n> --unit-price <amount> | --item <key>[=<qty>]... | --line <spec>...)",
		Short: "Issue a VAT invoice as a local draft (nothing is sent to KSeF)",
		Long: "Builds an FA(3) invoice for a configured party and stores it as a draft in the archive. " +
			"fv looks the seller and the buyer up in the registries (White List or VIES) before it takes " +
			"a number, computes the amounts, reserves the next invoice number, writes invoice.xml and " +
			"meta.json and prints the invoice with the environment shown first. It never contacts KSeF " +
			"unless --send is given: then it shows the preview, asks for confirmation and sends the stored draft " +
			"(see fv send; prod needs the typed word PRODUCTION). Without --send the output ends with " +
			"how to send the draft later. Use fv discard to drop a draft.\n\n" +
			"Amounts are in zloty; quote them: --net \"18500,43\" (see fv help amounts). The VAT rate, " +
			"payment terms, bank account, unit and line description come from the flags, then " +
			"[parties.<alias>], then [defaults] (payment_days defaults to 14, the description to " +
			"\"Usługi za {month_name} {year}\"; the tokens {month_name}, {month_name_en} and {year} use the " +
			"sale date). The sale date follows the sale_date setting: issue_date, last_day_of_month or " +
			"last_day_of_previous_month. Rates 23, 8, 5 and zw are supported; zw needs " +
			"exemption_basis. With --currency (or a currency setting) other than PLN fv takes the NBP table A " +
			"mid rate of the last business day before the tax point (the sale date, or the issue date when it is " +
			"earlier) and shows the VAT in PLN too.\n\n" +
			"An invoice has one line from --net (or --qty with --unit-price), or several lines from --item and " +
			"--line, which may be mixed and keep the order you type them in. --item <key>[=<qty>] bills a saved " +
			"[items.<key>] (the quantity after = replaces the item's own). --line takes named fields separated by " +
			"';': --line \"desc=Hosting {month_name_en} {year}; price=19,99; qty=2; unit=mies.; rate=8\" (fields: " +
			"desc, qty, unit, price or net, rate, index, pkwiu, gtin; quote the whole value). Without any of these " +
			"flags the items listed in [parties.<alias>] are billed. The single-line flags and --item/--line cannot be " +
			"mixed. The VAT rate of a line is its own rate= or the item's vat_rate, then --rate, then the party's, " +
			"then [defaults]; the exemption basis is shared by the whole invoice. VAT is computed once per rate from " +
			"the summed line net amounts (fv help amounts, section \"Several lines and rounding\", explains the rule and the quoting of --item and --line; a --line value always needs quotes because of the ';'). bank_name and swift, when set next to the account " +
			"in use, are written on the invoice.\n\n" +
			"Exit codes: 0 draft stored, 1 usage or config error (nothing is stored), 2 a registry or NBP lookup " +
			"failed (no number is taken then). With --send the send exit codes of fv send also apply.",
		Example: "  fv issue --to acme --net \"18500,43\"\n" +
			"  fv issue --to acme --net \"2000\" --currency EUR\n" +
			"  fv issue --to acme --net \"2000\" --send\n" +
			"  fv issue --to acme --item dev=160 --item support\n" +
			"  fv issue --to acme --item dev=160 --line \"desc=Hosting {month_name_en} {year}; price=19,99; unit=mies.\"\n" +
			"  fv issue --to acme --qty 160 --unit-price \"115,50\" --rate 23 --desc \"Consulting {month_name_en} {year}\"\n" +
			"  fv --env demo issue --to acme --net 2000 --issue-date 2026-10-31 --sale-date 2026-10-30 --validate",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return s.runIssue(cmd, f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.to, "to", "", "alias of the buyer in [parties.<alias>] (required; see fv party add)")
	fl.StringVar(&f.net, "net", "", "net amount of the single invoice line, in zloty; quote it, e.g. \"18500,43\" (use this or --qty with --unit-price; default: none; not together with --item or --line)")
	fl.StringVar(&f.qty, "qty", "", "quantity of the line, up to 6 decimals, e.g. 160 or \"1,5\" (use it with --unit-price, never with --net; a --net line has quantity 1)")
	fl.StringVar(&f.unitPrice, "unit-price", "", "net price of one unit, in zloty, e.g. \"115,50\" (needs --qty)")
	fl.StringVar(&f.rate, "rate", "", "VAT rate: 23, 8, 5 or zw (default: vat_rate from the party or [defaults]; 0 and np are not supported in v1)")
	fl.StringVar(&f.currency, "currency", "", "invoice currency, ISO code (default: currency from the party or [defaults], else PLN)")
	fl.StringVar(&f.issueDate, "issue-date", "", "issue date, YYYY-MM-DD (default: today in Europe/Warsaw)")
	fl.StringVar(&f.saleDate, "sale-date", "", "sale date, YYYY-MM-DD (default: by the sale_date setting, which defaults to the issue date)")
	fl.StringVar(&f.desc, "desc", "", "line description; may use {month_name}, {month_name_en}, {year} (default: line_template setting, else \"Usługi za {month_name} {year}\")")
	fl.Var(&lineArgs{dst: &f.lines, kind: kindItem}, "item", "bill a saved item, <key> or <key>=<qty>, e.g. dev=160 or \"dev=1,5\"; repeatable, in order, mixable with --line (default: none; the party's items are used when no line flag is given)")
	fl.Var(&lineArgs{dst: &f.lines, kind: kindSpec}, "line", "add a line from named fields separated by ';': desc (required), price or net (one of them), qty, unit, rate, index, pkwiu, gtin; quote the value; repeatable (default: none)")
	fl.BoolVar(&f.validate, "validate", false, "also validate the XML against the FA(3) schema with xmllint, which must be on PATH (default: off)")
	fl.BoolVar(&f.send, "send", false, "after storing the draft, ask for confirmation and send it to KSeF (default: off, nothing is sent)")
	fl.BoolVar(&f.yes, "yes", false, "with --send: skip the y/N question on test and demo; never skips the PRODUCTION confirmation (default: off)")
	return cmd
}

// settings are the resolved per-invoice settings.
type issueSettings struct {
	defaultRate  *money.Rate // --rate, else the party's or [defaults]'s vat_rate; nil when none is set
	currency     string
	saleRule     string
	template     string
	unit         string
	bank         string
	bankName     string
	swift        string
	paymentDays  int
	exemption    string
	desc, saleFl string
}

func (s *session) resolveSettings(rc *runContext, f issueFlags) (issueSettings, error) {
	flags := map[string]any{}
	if f.rate != "" {
		flags["vat_rate"] = f.rate
	}
	if f.currency != "" {
		flags["currency"] = f.currency
	}
	r := config.NewResolver(rc.Config, f.to, flags, nil)
	str := func(key, def string) (string, error) {
		v, _, ok, err := r.String(key)
		if err != nil {
			return "", err
		}
		if !ok {
			return def, nil
		}
		return v, nil
	}
	var out issueSettings
	var err error
	if out.currency, err = str("currency", "PLN"); err != nil {
		return out, err
	}
	if err := validate.Currency(out.currency); err != nil {
		return out, fmt.Errorf("currency: %w", err)
	}
	rateCode, err := str("vat_rate", "")
	if err != nil {
		return out, err
	}
	if rateCode != "" {
		rate, err := parseLineRate(rateCode, out.currency)
		if err != nil {
			return out, err
		}
		out.defaultRate = &rate
	}
	if out.saleRule, err = str("sale_date", invoice.RuleIssueDate); err != nil {
		return out, err
	}
	if out.template, err = str("line_template", defaultLineTemplate); err != nil {
		return out, err
	}
	if out.unit, err = str("unit", defaultUnit); err != nil {
		return out, err
	}
	bank, acctLayer, _, err := r.String("bank_account")
	if err != nil {
		return out, err
	}
	out.bank = bank
	if out.bank == "" {
		return out, errors.New("no bank account: set bank_account in [defaults] or [parties." + f.to + "]")
	}
	if err := validate.IBAN(out.bank); err != nil {
		return out, fmt.Errorf("bank_account: %w", err)
	}
	out.bank = strings.ToUpper(strings.ReplaceAll(out.bank, " ", ""))
	// bank_name and swift describe the account: they come from the layer the account came
	// from, never from another one, or the invoice would name the wrong bank.
	if out.bankName, _, err = r.StringFrom("bank_name", acctLayer); err != nil {
		return out, err
	}
	swift, _, err := r.StringFrom("swift", acctLayer)
	if err != nil {
		return out, err
	}
	if swift != "" {
		if out.swift, err = validate.SWIFT(swift); err != nil {
			return out, fmt.Errorf("swift: %w", err)
		}
	}
	out.paymentDays = defaultPaymentDays
	if n, _, ok, err := r.Int("payment_days"); err != nil {
		return out, err
	} else if ok {
		out.paymentDays = n
	}
	if out.exemption, err = str("exemption_basis", ""); err != nil {
		return out, err
	}
	return out, nil
}

func (s *session) runIssue(cmd *cobra.Command, f issueFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if f.to == "" {
		return errors.New("--to is required: the alias of the buyer (see fv party add)")
	}
	rc, err := s.requireConfig(cmd)
	if err != nil {
		return err
	}
	if rc.Config.Seller.NIP == "" {
		return errors.New("seller.nip is not set in the config; run fv init or set it")
	}
	if findings := validate.Config(rc.Config); len(findings) > 0 {
		return fmt.Errorf("the config is invalid: %s (run fv config validate)", findings[0])
	}
	party, ok := rc.Config.Parties[f.to]
	if !ok {
		return fmt.Errorf("unknown party %q; known: %s", f.to, knownAliases(rc.Config))
	}
	if f.yes && !f.send {
		return errors.New("--yes only makes sense together with --send")
	}
	set, err := s.resolveSettings(rc, f)
	if err != nil {
		return err
	}
	req, err := s.buildRequest(rc.Config, f, set)
	if err != nil {
		return err
	}
	if f.validate {
		if _, err := exec.LookPath("xmllint"); err != nil {
			return errors.New("--validate needs xmllint on PATH (libxml2); install it or leave --validate out")
		}
	}

	now := s.deps.now()
	// Computing first turns every input mistake into an error before a registry call or a number.
	comp, err := invoice.Compute(req)
	if err != nil {
		return err
	}
	exch, err := s.exchangeFor(ctx, rc, set.currency, comp.IssueDate, comp.SaleDate)
	if err != nil {
		return err
	}

	l, closeLog := s.logger(rc.Paths)
	defer closeLog()
	res := s.registry(rc, l, whiteListCounter(s, rc))
	seller, err := res.ResolveSeller(ctx, rc.Config.Seller, comp.IssueDate)
	if err != nil {
		return err
	}
	buyer, err := res.Resolve(ctx, f.to, party, comp.IssueDate, registry.PurposeInvoiceBuyer)
	if err != nil {
		return err
	}

	pattern := rc.Config.Numbering.Pattern
	if pattern == "" {
		pattern = numbering.DefaultPattern
	}
	reset := numbering.DefaultReset(pattern)
	if rc.Config.Numbering.Reset != "" {
		if reset, err = numbering.ParseReset(rc.Config.Numbering.Reset); err != nil {
			return err
		}
	}

	// Everything that can fail on the inputs has run; only now take a number.
	st := s.store(rc)
	rsv, err := st.Reserve(ctx, rc.Env, pattern, reset, comp.IssueDate)
	if err != nil {
		return err
	}
	meta, xmlBytes, err := s.assemble(ctx, rc, f, set, comp, exch, seller, buyer, rsv.Number, now)
	if err == nil {
		err = st.SaveDraft(rc.Env, rsv.Number, xmlBytes, meta)
	}
	if err != nil {
		if _, derr := st.Discard(rc.Env, rsv.Number); derr != nil {
			return fmt.Errorf("%w (and the reserved number %s could not be released: %v)", err, rsv.Number, derr)
		}
		return fmt.Errorf("%w (nothing was stored, number %s was released)", err, rsv.Number)
	}
	saved, err := st.Load(rc.Env, rsv.Number)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	renderInvoice(out, saved, previewOpts{Color: colorOK(out)})
	if f.send {
		_, _ = fmt.Fprintln(out)
		return s.sendStored(cmd, rc, saved.Number, f.yes)
	}
	_, _ = fmt.Fprintf(out, "\nNothing was sent to KSeF. To send: fv send %s%s\n", saved.Number, s.envHint(rc))
	return nil
}

// envHint is " --env <env>" when the run's environment is not the configured default.
func (s *session) envHint(rc *runContext) string {
	def, err := config.ResolveEnv(nil, rc.Config.Ksef.Environment)
	if err == nil && def == rc.Env {
		return ""
	}
	return " --env " + string(rc.Env)
}

func (s *session) buildRequest(cfg *config.Config, f issueFlags, set issueSettings) (invoice.Request, error) {
	now := s.deps.now()
	req := invoice.Request{
		IssueDate: invoice.Today(now), SaleRule: set.saleRule, PaymentDays: set.paymentDays,
	}
	var err error
	if f.issueDate != "" {
		if req.IssueDate, err = invoice.ParseDate(f.issueDate); err != nil {
			return req, fmt.Errorf("--issue-date: %w", err)
		}
	}
	if f.saleDate != "" {
		d, err := invoice.ParseDate(f.saleDate)
		if err != nil {
			return req, fmt.Errorf("--sale-date: %w", err)
		}
		req.SaleDateFlag = &d
	}
	if req.Lines, err = s.resolveLines(cfg, f, set); err != nil {
		return req, err
	}
	// FA(3) has one exemption basis for the whole invoice, so any zw line needs it.
	for _, l := range req.Lines {
		if l.Rate.Code == "zw" && strings.TrimSpace(set.exemption) == "" {
			return req, errors.New("rate zw needs the legal basis of the exemption: set exemption_basis in [defaults] or [parties." + f.to + "]")
		}
	}
	return req, nil
}

// assemble builds the FA(3) XML and the meta snapshot of a reserved number.
func (s *session) assemble(ctx context.Context, rc *runContext, f issueFlags, set issueSettings, comp invoice.Computed,
	exch *store.Exchange, seller, buyer registry.Party, number string, now time.Time) (store.Meta, []byte, error) {

	in := fa3.Input{
		Seller: fa3.Party{NIP: rc.Config.Seller.NIP, Name: seller.Name, AddressL1: seller.Address, Email: rc.Config.Seller.Email},
		Buyer: fa3.Party{NIP: buyer.NIP, EUCode: buyer.EUCode, VatNumber: buyer.VatNumber, Name: buyer.Name,
			CountryCode: buyer.CountryCode, AddressL1: buyer.Address},
		Number: number, PlaceOfIssue: rc.Config.Seller.PlaceOfIssue, Currency: set.currency,
		SystemInfo: "fv " + Version, ExemptionBasis: set.exemption, BankAccount: set.bank,
		BankName: set.bankName, BankSWIFT: set.swift,
		IssueDate: comp.IssueDate, SaleDate: comp.SaleDate, DueDate: comp.DueDate, CreatedAt: now,
		Summary: comp.Summary,
	}
	for _, l := range comp.Lines {
		in.Lines = append(in.Lines, fa3.Line{No: l.No, Description: l.Description, Unit: l.Unit, Qty: l.Qty,
			UnitPrice: l.UnitPrice, Net: l.Net, Rate: l.Rate, Index: l.Index, GTIN: l.GTIN, PKWiU: l.PKWiU})
	}
	var vatPLN map[string]money.Amount
	if exch != nil {
		in.Exchange = &fa3.Exchange{MidMicro: exch.MidMicro, Table: exch.Table, EffectiveDate: exch.EffectiveDate, Currency: exch.Currency}
		vatPLN = map[string]money.Amount{}
		for _, g := range comp.Summary.Groups {
			if g.Rate.VATField == "" {
				continue
			}
			v, err := money.ConvertMicro(g.VAT, exch.MidMicro)
			if err != nil {
				return store.Meta{}, nil, fmt.Errorf("VAT in PLN for rate %s: %w", g.Rate.Code, err)
			}
			vatPLN[g.Rate.Code] = v
		}
		in.VATPLN = vatPLN
	}
	xmlBytes, err := buildFA3(in)
	if err != nil {
		return store.Meta{}, nil, fmt.Errorf("build invoice: %w", err)
	}
	if f.validate {
		if err := fa3.ValidateWithXmllint(ctx, xmlBytes, ""); err != nil {
			return store.Meta{}, nil, err
		}
	}

	m := store.Meta{
		IssueDate: comp.IssueDate.Format("2006-01-02"), SaleDate: comp.SaleDate.Format("2006-01-02"),
		DueDate:   comp.DueDate.Format("2006-01-02"),
		CreatedAt: now, Seller: snapshot("seller", seller), Buyer: snapshot(f.to, buyer),
		Currency: set.currency, Gross: comp.Summary.Gross, BankAccount: set.bank, BankName: set.bankName,
		BankSWIFT: set.swift, Exchange: exch,
	}
	for _, l := range comp.Lines {
		m.Lines = append(m.Lines, store.LineSnapshot{No: l.No, Description: l.Description, Unit: l.Unit,
			Quantity: money.FormatQuantityXML(l.Qty), UnitPrice: l.UnitPrice, Net: l.Net, Rate: l.Rate.Code,
			Item: l.Item, Index: l.Index, PKWiU: l.PKWiU, GTIN: l.GTIN})
	}
	for _, g := range comp.Summary.Groups {
		rt := store.RateTotal{Rate: g.Rate.Code, Net: g.Net, VAT: g.VAT, Gross: g.Gross}
		if v, ok := vatPLN[g.Rate.Code]; ok {
			rt.VATPLN = &v
		}
		m.Totals = append(m.Totals, rt)
	}
	var warnings []string
	if found, known := registry.AccountOnWhiteList(seller, set.bank); known {
		m.BankOnWhiteList = &found
	}
	if w := registry.AccountWarning(seller, set.bank); w != "" {
		warnings = append(warnings, w+"; check bank_account (the buyer pays to this account)")
	}
	if set.swift != "" && len(set.bank) >= 2 && set.swift[4:6] != set.bank[:2] {
		warnings = append(warnings, fmt.Sprintf("the SWIFT code %s is for country %s, but the account %s is from %s; check swift (it must be the BIC of the bank that holds bank_account)",
			set.swift, set.swift[4:6], set.bank, set.bank[:2]))
	}
	warnings = append(warnings, seller.Warnings...)
	warnings = append(warnings, buyer.Warnings...)
	if comp.Summary.Gross >= mppThreshold && set.currency == "PLN" {
		warnings = append(warnings, "gross is 15 000 PLN or more: split payment (MPP) may be mandatory for goods and services in Annex 15 to the VAT act; fv does not support it in v1")
	}
	if comp.IssueDate.After(invoice.Today(now)) {
		warnings = append(warnings, "the issue date is in the future: KSeF will not accept this invoice until that day")
	}
	m.Warnings = warnings
	return m, xmlBytes, nil
}

func snapshot(alias string, p registry.Party) store.PartySnapshot {
	src := registry.SourceWhiteList
	if p.NIP == "" {
		src = registry.SourceVIES
	}
	if p.Provenance["statusVat"] == registry.SourceConfig {
		src = registry.SourceConfig // not on the White List: the party comes from the config
	}
	return store.PartySnapshot{
		Alias: alias, NIP: p.NIP, VatID: p.VatID, Name: p.Name, Address: p.Address, CountryCode: p.CountryCode,
		StatusVat: p.StatusVat, Source: src, RequestID: p.RequestID, ConsultationNumber: p.ConsultationNumber,
		CheckedAt: p.FetchedAt,
	}
}
