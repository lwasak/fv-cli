package validate

import (
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
)

func TestNIP(t *testing.T) {
	tests := []struct {
		name, in, wantErr string
	}{
		{"valid", "5260250274", ""},
		{"checksum", "5260250275", "checksum"},
		{"separators", "526-025-02-74", "separators"},
		{"length", "123456789", "10 digits"},
		{"letters", "52602502ab", "10 digits"},
		{"sum mod 11 is 10", "0005000000", "checksum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NIP(tt.in)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestIBAN(t *testing.T) {
	tests := []struct {
		name, in string
		ok       bool
	}{
		{"valid spaced", "PL61 1090 1014 0000 0712 1981 2874", true},
		{"lower case", "pl61109010140000071219812874", true},
		{"bad check digit", "PL61 1090 1014 0000 0712 1981 2875", false},
		{"wrong PL length", "PL61 1090 1014 0000 0712 1981 287", false},
		{"other country valid", "DE89 3704 0044 0532 0130 00", true},
		{"garbage", "hello", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := IBAN(tt.in)
			if (err == nil) != tt.ok {
				t.Fatalf("IBAN(%q) err = %v, want ok=%v", tt.in, err, tt.ok)
			}
		})
	}
}

func TestRateCode(t *testing.T) {
	for _, c := range []string{"23", "8", "5", "zw"} {
		if err := RateCode(c); err != nil {
			t.Errorf("RateCode(%q) = %v", c, err)
		}
	}
	for _, c := range []string{"0", "np"} {
		err := RateCode(c)
		if err == nil || !strings.Contains(err.Error(), "not supported yet") {
			t.Errorf("RateCode(%q) = %v, want not supported yet", c, err)
		}
	}
	if err := RateCode("7"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("RateCode(7) = %v, want unknown", err)
	}
}

func TestNumberingPattern(t *testing.T) {
	tests := []struct {
		name, in, wantErr string
	}{
		{"simple", "FV/{YYYY}/{MM}/{SEQ}", ""},
		{"padded", "{SEQ:4}/{YY}-{DD}", ""},
		{"no seq", "FV/{YYYY}", "{SEQ}"},
		{"two seq", "{SEQ}/{SEQ:3}", "exactly one"},
		{"unknown token", "{FOO}{SEQ}", "unknown token"},
		{"bad width", "{SEQ:0}", "width"},
		{"bad width 10", "{SEQ:10}", "width"},
		{"bad literal", "FV #{SEQ}", "literal"},
		{"unclosed", "FV{SEQ", "unclosed"},
		{"empty", "", "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NumberingPattern(tt.in)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestPartyIdentity(t *testing.T) {
	tests := []struct {
		name, nip, vat string
		ok             bool
	}{
		{"nip only", "5260250274", "", true},
		{"vat only", "", "DE123456789", true},
		{"both", "5260250274", "DE123456789", false},
		{"neither", "", "", false},
		{"bad vat shape", "", "1234", false},
		{"bad nip", "123", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PartyIdentity(tt.nip, tt.vat)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestConfig_CollectsAllFindings(t *testing.T) {
	neg := -1
	cfg := &config.Config{
		Seller:    config.Seller{NIP: "5260250275"},
		Defaults:  config.Defaults{VatRate: "7", PaymentDays: &neg, BankAccount: "PL00"},
		Numbering: config.Numbering{Pattern: "FV"},
		Parties: map[string]config.Party{
			"a": {},
			"b": {NIP: "5260250274", VatRate: "np"},
		},
	}
	got := Config(cfg)
	keys := map[string]bool{}
	for _, f := range got {
		keys[f.Key] = true
	}
	for _, k := range []string{"seller.nip", "defaults.vat_rate", "defaults.payment_days",
		"defaults.bank_account", "numbering.pattern", "parties.a", "parties.b.vat_rate"} {
		if !keys[k] {
			t.Errorf("missing finding for %s; got %v", k, got)
		}
	}
	if len(got) != 7 {
		t.Errorf("findings = %d, want 7: %v", len(got), got)
	}
}

func TestConfig_EmptyIsValid(t *testing.T) {
	if got := Config(&config.Config{}); len(got) != 0 {
		t.Fatalf("findings = %v", got)
	}
}

func TestValidate_ResetMustFitPattern(t *testing.T) {
	tests := []struct {
		pattern, reset string
		ok             bool
	}{
		{"FV/{YYYY}/{MM}/{SEQ:3}", "monthly", true},
		{"FV/{YY}{MM}/{SEQ}", "monthly", true},
		{"FV/{YYYY}/{SEQ}", "monthly", false},
		{"FV/{MM}/{SEQ}", "monthly", false},
		{"FV/{YYYY}/{SEQ}", "yearly", true},
		{"FV/{SEQ}", "yearly", false},
		{"FV/{MM}/{SEQ}", "yearly", false},
		{"FV/{SEQ}", "never", true},
		{"FV/{SEQ}", "", true},
		{"FV/{YYYY}/{SEQ}", "weekly", false},
	}
	for _, tt := range tests {
		if err := NumberingReset(tt.pattern, tt.reset); (err == nil) != tt.ok {
			t.Errorf("NumberingReset(%q,%q) = %v, want ok=%v", tt.pattern, tt.reset, err, tt.ok)
		}
	}
	got := Config(&config.Config{Numbering: config.Numbering{Pattern: "FV/{SEQ}", Reset: "monthly"}})
	if len(got) != 1 || got[0].Key != "numbering.reset" {
		t.Errorf("findings = %v", got)
	}
	// reset checked against the default pattern when none is set
	if got := Config(&config.Config{Numbering: config.Numbering{Reset: "monthly"}}); len(got) != 0 {
		t.Errorf("default pattern + monthly: %v", got)
	}
}

func TestValidate_NewKeys(t *testing.T) {
	zero := 0
	bad := &config.Config{
		Seller: config.Seller{Name: strings.Repeat("x", 513), Email: "nobody", PlaceOfIssue: strings.Repeat("y", 257)},
		Defaults: config.Defaults{Currency: "eur", SaleDate: "tomorrow", LineTemplate: "{day}",
			Unit: strings.Repeat("u", 257), ExemptionBasis: strings.Repeat("b", 257)},
		Registry: config.Registry{CacheTTL: &zero},
		Parties: map[string]config.Party{
			"Acme!": {NIP: "5260250274"},
			"ok":    {NIP: "5260250274", Currency: "EURO", Name: strings.Repeat("n", 513), Address: strings.Repeat("a", 513), LineTemplate: "{year"},
		},
	}
	keys := map[string]bool{}
	for _, f := range Config(bad) {
		keys[f.Key] = true
	}
	for _, k := range []string{"seller.name", "seller.email", "seller.place_of_issue", "defaults.currency",
		"defaults.sale_date", "defaults.line_template", "defaults.unit", "defaults.exemption_basis",
		"registry.cache_ttl", "parties.Acme!", "parties.ok.currency", "parties.ok.name",
		"parties.ok.address", "parties.ok.line_template"} {
		if !keys[k] {
			t.Errorf("missing finding for %s", k)
		}
	}
	good := &config.Config{
		Seller: config.Seller{Name: "N", Address: "A", Email: "a@b.pl", PlaceOfIssue: "Gdańsk"},
		Defaults: config.Defaults{Currency: "EUR", SaleDate: "last_day_of_previous_month",
			LineTemplate: "Usługi za {month_name} {year} / {month_name_en}", Unit: "usł.", ExemptionBasis: "art. 43"},
		Registry: config.Registry{CacheTTL: func() *int { n := 30; return &n }()},
		Parties:  map[string]config.Party{"acme-1": {NIP: "5260250274", Currency: "USD"}},
	}
	if got := Config(good); len(got) != 0 {
		t.Errorf("good config findings: %v", got)
	}
}

func TestPartyAliasCurrencyEmailTemplate(t *testing.T) {
	for a, ok := range map[string]bool{"acme": true, "a1_b-c": true, "Acme!": false, "-a": false, "": false, strings.Repeat("a", 33): false, strings.Repeat("a", 32): true} {
		if (PartyAlias(a) == nil) != ok {
			t.Errorf("PartyAlias(%q) ok=%v", a, ok)
		}
	}
	for c, ok := range map[string]bool{"PLN": true, "eur": false, "EURO": false, "": false} {
		if (Currency(c) == nil) != ok {
			t.Errorf("Currency(%q) ok=%v", c, ok)
		}
	}
	for e, ok := range map[string]bool{"a@b": true, "ab": false, "@b.c": false, "a@": false, "a@b@c": false, "a b@c.d": false} {
		if (Email(e) == nil) != ok {
			t.Errorf("Email(%q) ok=%v", e, ok)
		}
	}
	for tpl, ok := range map[string]bool{"x {year}": true, "{month_name}{month_name_en}": true, "{foo}": false, "{year": false, "a}": false, "": false} {
		if (LineTemplate(tpl) == nil) != ok {
			t.Errorf("LineTemplate(%q) ok=%v", tpl, ok)
		}
	}
}
