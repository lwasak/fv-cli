package validate

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/money"
)

// Finding is one problem in the config, tied to a key path.
type Finding struct {
	Key     string
	Message string
}

func (f Finding) String() string { return f.Key + ": " + f.Message }

// DefaultNumberingPattern is the pattern used when numbering.pattern is unset.
const DefaultNumberingPattern = "FV/{YYYY}/{MM}/{SEQ:3}"

// settings are the per-layer keys shared by [defaults] and [parties.<alias>].
type settings struct {
	rate     string
	days     *int
	iban     string
	currency string
	saleDate string
	template string
	unit     string
	basis    string
	bankName string
	swift    string
}

// Config checks every setting and returns all findings, ordered by key.
func Config(cfg *config.Config) []Finding {
	var out []Finding
	add := func(key string, err error) {
		if err != nil {
			out = append(out, Finding{Key: key, Message: err.Error()})
		}
	}
	if cfg.Seller.NIP != "" {
		add("seller.nip", NIP(cfg.Seller.NIP))
	}
	checkTextKey := func(key, v string, max int) {
		if v != "" {
			add(key, Text(v, max))
		}
	}
	checkSettings := func(prefix string, st settings) {
		if st.rate != "" {
			add(prefix+"vat_rate", RateCode(st.rate))
		}
		if st.days != nil && *st.days < 0 {
			add(prefix+"payment_days", fmt.Errorf("must be 0 or more, got %d", *st.days))
		}
		if st.iban != "" {
			add(prefix+"bank_account", IBAN(st.iban))
		}
		if st.currency != "" {
			add(prefix+"currency", Currency(st.currency))
		}
		if st.saleDate != "" {
			add(prefix+"sale_date", SaleDateRule(st.saleDate))
		}
		if st.template != "" {
			add(prefix+"line_template", LineTemplate(st.template))
		}
		checkTextKey(prefix+"unit", st.unit, 256)
		checkTextKey(prefix+"exemption_basis", st.basis, 256)
		if st.bankName != "" {
			add(prefix+"bank_name", Token(st.bankName, 256))
		}
		if st.swift != "" {
			_, err := SWIFT(st.swift)
			add(prefix+"swift", err)
		}
	}
	checkTextKey("seller.name", cfg.Seller.Name, 512)
	checkTextKey("seller.address", cfg.Seller.Address, 512)
	checkTextKey("seller.place_of_issue", cfg.Seller.PlaceOfIssue, 256)
	if cfg.Seller.Email != "" {
		add("seller.email", Email(cfg.Seller.Email))
	}
	d := cfg.Defaults
	checkSettings("defaults.", settings{d.VatRate, d.PaymentDays, d.BankAccount, d.Currency, d.SaleDate, d.LineTemplate, d.Unit, d.ExemptionBasis, d.BankName, d.SWIFT})
	if cfg.Numbering.Pattern != "" {
		add("numbering.pattern", NumberingPattern(cfg.Numbering.Pattern))
	}
	if cfg.Numbering.Reset != "" {
		// A bad pattern is reported on its own key; check the reset against the effective one.
		pattern := cfg.Numbering.Pattern
		if pattern == "" {
			pattern = DefaultNumberingPattern
		}
		add("numbering.reset", NumberingReset(pattern, cfg.Numbering.Reset))
	}
	if cfg.Registry.CacheTTL != nil {
		add("registry.cache_ttl", CacheTTL(*cfg.Registry.CacheTTL))
	}
	aliases := make([]string, 0, len(cfg.Parties))
	for a := range cfg.Parties {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	for _, a := range aliases {
		p := cfg.Parties[a]
		if err := PartyAlias(a); err != nil {
			add("parties."+a, err)
		}
		add("parties."+a, PartyIdentity(p.NIP, p.VatID))
		checkSettings("parties."+a+".", settings{p.VatRate, p.PaymentDays, p.BankAccount, p.Currency, p.SaleDate, p.LineTemplate, p.Unit, p.ExemptionBasis, p.BankName, p.SWIFT})
		checkTextKey("parties."+a+".name", p.Name, 512)
		checkTextKey("parties."+a+".address", p.Address, 512)
		if p.BankAccount == "" {
			for _, k := range []struct{ key, v string }{{"bank_name", p.BankName}, {"swift", p.SWIFT}} {
				if k.v != "" {
					add("parties."+a+"."+k.key, errors.New("set bank_account in this table too: bank_name and swift describe that account"))
				}
			}
		}
		if p.Items != nil {
			checkPartyItems("parties."+a+".items", p.Items, cfg.Items, add)
		}
	}
	keys := make([]string, 0, len(cfg.Items))
	for k := range cfg.Items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		checkItem("items."+k, k, cfg.Items[k], add)
	}
	return out
}

// ItemKeys returns the keys of items in sorted order.
func ItemKeys(items map[string]config.Item) []string {
	keys := make([]string, 0, len(items))
	for k := range items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// UnknownItem formats the error for an item key that is not defined in [items].
func UnknownItem(key string, items map[string]config.Item) error {
	defined := "none (add an [items.<key>] table)"
	if len(items) > 0 {
		defined = strings.Join(ItemKeys(items), ", ")
	}
	return fmt.Errorf("unknown item %q; defined: %s", key, defined)
}

func checkPartyItems(key string, list []string, items map[string]config.Item, add func(string, error)) {
	if len(list) == 0 {
		add(key, errors.New("empty list; remove the key or name at least one item"))
		return
	}
	for _, k := range list {
		if _, ok := items[k]; !ok {
			add(key, UnknownItem(k, items))
		}
	}
}

func checkItem(prefix, k string, it config.Item, add func(string, error)) {
	if err := ItemKey(k); err != nil {
		add(prefix, err)
	}
	if it.Name == "" {
		add(prefix+".name", errors.New("required"))
	} else {
		if err := LineTemplate(it.Name); err != nil {
			add(prefix+".name", err)
		} else {
			add(prefix+".name", Token(it.Name, 512))
		}
	}
	if it.UnitPrice == "" {
		add(prefix+".unit_price", errors.New(`required; quote it, e.g. unit_price = "120,00"`))
	} else if _, err := money.ParseAmount(it.UnitPrice); err != nil {
		add(prefix+".unit_price", fmt.Errorf(`%w; quote it, e.g. unit_price = "120,00"`, err))
	}
	if it.Quantity != "" {
		if _, err := money.ParseQuantity(it.Quantity); err != nil {
			add(prefix+".quantity", fmt.Errorf(`%w; quote it, e.g. quantity = "160"`, err))
		}
	}
	if it.VatRate != "" {
		add(prefix+".vat_rate", RateCode(it.VatRate))
	}
	if it.Currency != "" {
		add(prefix+".currency", Currency(it.Currency))
	}
	for _, f := range []struct {
		key, v string
		max    int
	}{{"unit", it.Unit, 256}, {"index", it.Index, 50}, {"pkwiu", it.PKWiU, 50}} {
		if f.v != "" {
			add(prefix+"."+f.key, Token(f.v, f.max))
		}
	}
	if it.GTIN != "" {
		add(prefix+".gtin", GTIN(it.GTIN))
	}
}
