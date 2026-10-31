package validate

import (
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
)

func TestSWIFT(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"BREXPLPW", "BREXPLPW", true},
		{"BREXPLPWXXX", "BREXPLPWXXX", true},
		{"  brex plpw ", "BREXPLPW", true},
		{"brexplpw123", "BREXPLPW123", true},
		{"BREXPLPW1", "", false},  // 9 characters
		{"BREXPLPWXX", "", false}, // 10 characters
		{"BR3XPLPW", "", false},   // digit in the bank code
		{"BREXP1PW", "", false},   // digit in the country
		{"BREXPLPWXXXX", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, err := SWIFT(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("SWIFT(%q) = %q, %v; want %q ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "BREXPLPW") {
			t.Errorf("SWIFT(%q) error lacks an example: %v", tt.in, err)
		}
	}
}

func TestGTIN(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{"5901234123457", true},  // EAN-13
		{"96385074", true},       // EAN-8
		{"036000291452", true},   // UPC-A
		{"15901234123454", true}, // GTIN-14
		{"5901234123458", false}, // bad check digit
		{"590123412345", false},  // 12 digits, wrong check
		{"59012341234", false},   // 11 digits
		{"59012341234a7", false}, // not digits
		{"", false},
	}
	for _, tt := range tests {
		if err := GTIN(tt.in); (err == nil) != tt.ok {
			t.Errorf("GTIN(%q) = %v, want ok=%v", tt.in, err, tt.ok)
		}
	}
}

func TestToken(t *testing.T) {
	for in, ok := range map[string]bool{"x": true, "  x ": true, "": false, "   ": false, "\t\n": false} {
		if (Token(in, 5) == nil) != ok {
			t.Errorf("Token(%q) ok=%v", in, ok)
		}
	}
	if err := Token(strings.Repeat("ł", 6), 5); err == nil || !strings.Contains(err.Error(), "1-5") {
		t.Errorf("Token too long = %v", err)
	}
	if err := Token(strings.Repeat("ł", 5), 5); err != nil {
		t.Errorf("Token at limit = %v", err)
	}
	if err := Token("  ", 5); err == nil || !strings.Contains(err.Error(), "blank") {
		t.Errorf("Token blank = %v", err)
	}
}

func TestItemKey(t *testing.T) {
	for k, ok := range map[string]bool{"dev": true, "a-1_b": true, "Dev": false, "": false, "-x": false, strings.Repeat("a", 33): false} {
		if (ItemKey(k) == nil) != ok {
			t.Errorf("ItemKey(%q) ok=%v", k, ok)
		}
	}
	if err := ItemKey("Dev"); err == nil || !strings.Contains(err.Error(), "item key") {
		t.Errorf("message = %v", err)
	}
}

func findingKeys(fs []Finding) map[string]string {
	m := map[string]string{}
	for _, f := range fs {
		m[f.Key] += f.Message + "|"
	}
	return m
}

func TestConfig_BankFields(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{BankName: "  ", SWIFT: "BREXPLPW9"},
		Parties: map[string]config.Party{
			"noacct": {NIP: "5260250274", BankName: "ING", SWIFT: "INGBPLPW"},
			"acct":   {NIP: "5260250274", BankAccount: "PL61 1090 1014 0000 0712 1981 2874", BankName: "ING", SWIFT: "ingb plpw"},
		},
	}
	got := findingKeys(Config(cfg))
	for _, k := range []string{"defaults.bank_name", "defaults.swift", "parties.noacct.bank_name", "parties.noacct.swift"} {
		if got[k] == "" {
			t.Errorf("missing finding %s in %v", k, got)
		}
	}
	if !strings.Contains(got["parties.noacct.bank_name"], "set bank_account in this table too") {
		t.Errorf("message = %q", got["parties.noacct.bank_name"])
	}
	for k := range got {
		if strings.HasPrefix(k, "parties.acct") {
			t.Errorf("unexpected finding %s", k)
		}
	}
}

func TestConfig_PartyItems(t *testing.T) {
	items := map[string]config.Item{"dev": {Name: "Dev", UnitPrice: "1"}, "support": {Name: "S", UnitPrice: "1"}}
	cfg := &config.Config{
		Items: items,
		Parties: map[string]config.Party{
			"empty":   {NIP: "5260250274", Items: []string{}},
			"unknown": {NIP: "5260250274", Items: []string{"dev", "nope"}},
			"ok":      {NIP: "5260250274", Items: []string{"dev", "support"}},
			"unset":   {NIP: "5260250274"},
		},
	}
	got := findingKeys(Config(cfg))
	if !strings.Contains(got["parties.empty.items"], "empty list; remove the key or name at least one item") {
		t.Errorf("empty: %v", got)
	}
	if !strings.Contains(got["parties.unknown.items"], `unknown item "nope"; defined: dev, support`) {
		t.Errorf("unknown: %v", got)
	}
	if len(got) != 2 {
		t.Errorf("findings = %v", got)
	}
	cfg.Items = nil
	cfg.Parties = map[string]config.Party{"p": {NIP: "5260250274", Items: []string{"x"}}}
	got = findingKeys(Config(cfg))
	if !strings.Contains(got["parties.p.items"], "none (add an [items.<key>] table)") {
		t.Errorf("no items: %v", got)
	}
}

func TestConfig_Items(t *testing.T) {
	cfg := &config.Config{Items: map[string]config.Item{
		"noname":  {UnitPrice: "1"},
		"noprice": {Name: "N"},
		"Bad":     {Name: "N", UnitPrice: "1"},
		"badprice": {Name: "N", UnitPrice: "12 zł", Quantity: "0", VatRate: "7", Currency: "pln",
			Unit: " ", Index: strings.Repeat("i", 51), PKWiU: strings.Repeat("p", 51), GTIN: "123"},
		"badtpl": {Name: "{day}", UnitPrice: "1"},
		"blank":  {Name: "   ", UnitPrice: "1"},
		"good": {Name: "Dev {month_name} {year}", UnitPrice: "120,00", Quantity: "1,5", VatRate: "23",
			Currency: "EUR", Unit: "h", Index: "DEV-01", PKWiU: "62.01.11.0", GTIN: "5901234123457"},
	}}
	got := findingKeys(Config(cfg))
	want := []string{"items.noname.name", "items.noprice.unit_price", "items.Bad",
		"items.badprice.unit_price", "items.badprice.quantity", "items.badprice.vat_rate", "items.badprice.currency",
		"items.badprice.unit", "items.badprice.index", "items.badprice.pkwiu", "items.badprice.gtin",
		"items.badtpl.name", "items.blank.name"}
	for _, k := range want {
		if got[k] == "" {
			t.Errorf("missing finding %s", k)
		}
	}
	for k := range got {
		if strings.HasPrefix(k, "items.good") {
			t.Errorf("unexpected finding %s", k)
		}
	}
	if !strings.Contains(got["items.noprice.unit_price"], `quote it, e.g. unit_price = "120,00"`) {
		t.Errorf("price hint: %q", got["items.noprice.unit_price"])
	}
	if !strings.Contains(got["items.badprice.unit_price"], `quote it, e.g. unit_price = "120,00"`) {
		t.Errorf("price hint: %q", got["items.badprice.unit_price"])
	}
	// findings come out grouped per item in sorted key order
	var order []string
	for _, f := range Config(cfg) {
		if len(order) == 0 || order[len(order)-1] != strings.SplitN(f.Key, ".", 3)[1] {
			order = append(order, strings.SplitN(f.Key, ".", 3)[1])
		}
	}
	for i := 1; i < len(order); i++ {
		if order[i-1] > order[i] {
			t.Errorf("items not in sorted order: %v", order)
		}
	}
}

func TestNoControl(t *testing.T) {
	for _, s := range []string{"", "Programowanie za październik", "zażółć gęślą jaźń", "a b", "emoji 😀"} {
		if err := NoControl(s); err != nil {
			t.Errorf("NoControl(%q) = %v", s, err)
		}
	}
	bad := map[string]string{
		"a\x01b":       "control characters are not allowed (U+0001 at character 2)",
		"\x1b[31m":     "U+001B at character 1",
		"a\tb":         "control characters",
		"a\nb":         "control characters",
		"x\x7f":        "U+007F",
		"x\u0085":      "U+0085",
		"x\u202ey":     "invisible formatting characters are not allowed (U+202E at character 2)",
		"x\u2066y":     "U+2066",
		"x\u200by":     "U+200B",
		"\ufeffx":      "U+FEFF",
		"x\u2028y":     "U+2028",
		"x\ufffey":     "U+FFFE",
		"x\xffy":       "invalid UTF-8",
		"zażółć\x00ok": "U+0000 at character 7",
	}
	for s, want := range bad {
		if err := NoControl(s); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("NoControl(%q) = %v, want %q", s, err, want)
		}
	}
}

func TestTextFieldsShareTheControlRule(t *testing.T) {
	for name, err := range map[string]error{
		"Token": Token("a\x01b", 10), "Text": Text("a\x01b", 10), "LineTemplate": LineTemplate("a\x01 {year}"),
		"Email": Email("a\x01@b.pl"),
	} {
		if err == nil || !strings.Contains(err.Error(), "control characters are not allowed") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestConfig_ControlCharactersInEveryTextKey(t *testing.T) {
	cfg := &config.Config{
		Seller:   config.Seller{Name: "a\x01", Address: "a\x01", PlaceOfIssue: "a\x01"},
		Defaults: config.Defaults{BankName: "a\x01", Unit: "a\x01", LineTemplate: "a\x01", ExemptionBasis: "a\x01"},
		Parties:  map[string]config.Party{"p": {NIP: "5260250274", Name: "a\x1b", Address: "a\u202e"}},
		Items:    map[string]config.Item{"i": {Name: "a\x01", UnitPrice: "1", Unit: "a\x01", Index: "a\x01", PKWiU: "a\x01"}},
	}
	got := findingKeys(Config(cfg))
	for _, k := range []string{"seller.name", "seller.address", "seller.place_of_issue", "defaults.bank_name", "defaults.unit",
		"defaults.line_template", "defaults.exemption_basis", "parties.p.name", "parties.p.address",
		"items.i.name", "items.i.unit", "items.i.index", "items.i.pkwiu"} {
		if !strings.Contains(got[k], "not allowed") {
			t.Errorf("%s: %q", k, got[k])
		}
	}
}

// N1: a padded value must pass or fail the same way in `fv config validate` and in fa3.Build.
func TestTokenCountsTrimmedLikeBuild(t *testing.T) {
	padded := " " + strings.Repeat("I", 50) + " "
	if err := Token(padded, 50); err != nil {
		t.Errorf("validate rejects a padded 50-character token: %v", err)
	}
	if err := Token(" "+strings.Repeat("I", 51)+" ", 50); err == nil {
		t.Error("validate accepts 51 characters")
	}
}
