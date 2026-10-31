package validate

import (
	"fmt"
	"sort"

	"github.com/lwasak/fv-cli/internal/config"
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
	}
	checkTextKey("seller.name", cfg.Seller.Name, 512)
	checkTextKey("seller.address", cfg.Seller.Address, 512)
	checkTextKey("seller.place_of_issue", cfg.Seller.PlaceOfIssue, 256)
	if cfg.Seller.Email != "" {
		add("seller.email", Email(cfg.Seller.Email))
	}
	d := cfg.Defaults
	checkSettings("defaults.", settings{d.VatRate, d.PaymentDays, d.BankAccount, d.Currency, d.SaleDate, d.LineTemplate, d.Unit, d.ExemptionBasis})
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
		checkSettings("parties."+a+".", settings{p.VatRate, p.PaymentDays, p.BankAccount, p.Currency, p.SaleDate, p.LineTemplate, p.Unit, p.ExemptionBasis})
		checkTextKey("parties."+a+".name", p.Name, 512)
		checkTextKey("parties."+a+".address", p.Address, 512)
	}
	return out
}
