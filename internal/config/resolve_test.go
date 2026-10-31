package config

import "testing"

type fakeRegistry map[string]any

func (f fakeRegistry) Lookup(partyAlias, key string) (any, bool) {
	v, ok := f[partyAlias+"/"+key]
	return v, ok
}

func intp(i int) *int { return &i }

func TestResolver_Precedence(t *testing.T) {
	reg := fakeRegistry{"acme/vat_rate": "8", "acme/payment_days": 30}
	withDefaults := &Config{Defaults: Defaults{VatRate: "5", PaymentDays: intp(14)}}
	withParty := &Config{
		Defaults: Defaults{VatRate: "5", PaymentDays: intp(14)},
		Parties:  map[string]Party{"acme": {VatRate: "zw", PaymentDays: intp(7)}},
	}
	flags := map[string]any{"vat_rate": "23", "payment_days": 1}

	tests := []struct {
		name      string
		cfg       *Config
		alias     string
		flags     map[string]any
		registry  RegistrySource
		wantRate  string
		wantDays  int
		wantLayer Layer
		wantSet   bool
	}{
		{"registry only", &Config{}, "acme", nil, reg, "8", 30, LayerRegistry, true},
		{"defaults beat registry", withDefaults, "acme", nil, reg, "5", 14, LayerDefaults, true},
		{"party beats defaults and registry", withParty, "acme", nil, reg, "zw", 7, LayerParty, true},
		{"flag beats all", withParty, "acme", flags, reg, "23", 1, LayerFlag, true},
		{"nowhere", &Config{}, "acme", nil, NoRegistry{}, "", 0, 0, false},
		{"unknown alias skips party", withParty, "other", nil, NoRegistry{}, "5", 14, LayerDefaults, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewResolver(tc.cfg, tc.alias, tc.flags, tc.registry)
			s, l, ok, err := r.String("vat_rate")
			if err != nil || ok != tc.wantSet || s != tc.wantRate || (ok && l != tc.wantLayer) {
				t.Errorf("String = %q,%v,%v,%v", s, l, ok, err)
			}
			n, l, ok, err := r.Int("payment_days")
			if err != nil || ok != tc.wantSet || n != tc.wantDays || (ok && l != tc.wantLayer) {
				t.Errorf("Int = %d,%v,%v,%v", n, l, ok, err)
			}
		})
	}
}

func TestResolver_TypeMismatchFromRegistryIsError(t *testing.T) {
	reg := fakeRegistry{"acme/vat_rate": 8, "acme/payment_days": "thirty"}
	r := NewResolver(&Config{}, "acme", nil, reg)
	if _, _, _, err := r.String("vat_rate"); err == nil {
		t.Error("String: want error for int registry value")
	}
	if _, _, _, err := r.Int("payment_days"); err == nil {
		t.Error("Int: want error for string registry value")
	}
}

func TestResolver_NewKeys(t *testing.T) {
	cfg := &Config{
		Defaults: Defaults{Currency: "PLN", SaleDate: "issue_date", LineTemplate: "D {year}", Unit: "usł.", ExemptionBasis: "d-basis"},
		Parties: map[string]Party{
			"acme": {Currency: "EUR", Unit: "godz.", SaleDate: "last_day_of_month"},
			"bare": {},
		},
	}
	flags := map[string]any{"currency": "USD"}
	tests := []struct {
		alias string
		flags map[string]any
		key   string
		want  string
		layer Layer
		set   bool
	}{
		{"acme", flags, "currency", "USD", LayerFlag, true},
		{"acme", nil, "currency", "EUR", LayerParty, true},
		{"bare", nil, "currency", "PLN", LayerDefaults, true},
		{"acme", nil, "unit", "godz.", LayerParty, true},
		{"acme", nil, "sale_date", "last_day_of_month", LayerParty, true},
		{"acme", nil, "line_template", "D {year}", LayerDefaults, true},
		{"acme", nil, "exemption_basis", "d-basis", LayerDefaults, true},
		{"acme", nil, "bank_account", "", 0, false},
		{"acme", nil, "nonsense", "", 0, false},
	}
	for _, tc := range tests {
		r := NewResolver(cfg, tc.alias, tc.flags, nil)
		got, layer, ok, err := r.String(tc.key)
		if err != nil || ok != tc.set || got != tc.want || (ok && layer != tc.layer) {
			t.Errorf("%s/%s: got %q,%v,%v,%v; want %q,%v,%v", tc.alias, tc.key, got, layer, ok, err, tc.want, tc.layer, tc.set)
		}
	}
	// empty string means "not set": the party value does not shadow defaults.
	r := NewResolver(&Config{Defaults: Defaults{Unit: "x"}, Parties: map[string]Party{"p": {Unit: ""}}}, "p", nil, nil)
	if got, layer, ok, _ := r.String("unit"); !ok || got != "x" || layer != LayerDefaults {
		t.Errorf("empty party unit: %q %v %v", got, layer, ok)
	}
}

func TestResolver_StringFrom(t *testing.T) {
	cfg := &Config{
		Defaults: Defaults{BankAccount: "D-IBAN", BankName: "Default Bank", SWIFT: "DEFAPLPW"},
		Parties: map[string]Party{
			"own":  {BankAccount: "P-IBAN", BankName: "Party Bank"},
			"bare": {},
		},
	}
	reg := fakeRegistry{"own/bank_name": "Registry Bank"}
	r := NewResolver(cfg, "own", map[string]any{"bank_name": "Flag Bank"}, reg)
	tests := []struct {
		key   string
		layer Layer
		want  string
		ok    bool
	}{
		{"bank_name", LayerFlag, "Flag Bank", true},
		{"bank_name", LayerParty, "Party Bank", true},
		{"swift", LayerParty, "", false}, // no fall-through to defaults
		{"bank_name", LayerDefaults, "Default Bank", true},
		{"swift", LayerDefaults, "DEFAPLPW", true},
		{"bank_name", LayerRegistry, "Registry Bank", true},
		{"swift", LayerRegistry, "", false},
		{"bank_name", Layer(0), "", false},
	}
	for _, tc := range tests {
		got, ok, err := r.StringFrom(tc.key, tc.layer)
		if err != nil || ok != tc.ok || got != tc.want {
			t.Errorf("StringFrom(%s,%v) = %q,%v,%v; want %q,%v", tc.key, tc.layer, got, ok, err, tc.want, tc.ok)
		}
	}
	// a party that is not in the config has no party layer
	if _, ok, _ := NewResolver(cfg, "ghost", nil, nil).StringFrom("bank_name", LayerParty); ok {
		t.Error("unknown party must not have a party layer")
	}
	// wrong type from a layer is an error
	bad := NewResolver(cfg, "own", map[string]any{"bank_name": 5}, nil)
	if _, _, err := bad.StringFrom("bank_name", LayerFlag); err == nil {
		t.Error("want a type error")
	}
}

// The bank group rule: a party's own account must not pick up the defaults' bank name.
func TestResolver_BankGroupFollowsAccountLayer(t *testing.T) {
	cfg := &Config{
		Defaults: Defaults{BankAccount: "D-IBAN", BankName: "Default Bank"},
		Parties:  map[string]Party{"own": {BankAccount: "P-IBAN"}},
	}
	r := NewResolver(cfg, "own", nil, nil)
	_, layer, ok, _ := r.String("bank_account")
	if !ok || layer != LayerParty {
		t.Fatalf("account layer = %v %v", layer, ok)
	}
	if name, ok, _ := r.StringFrom("bank_name", layer); ok {
		t.Errorf("party account got bank name %q from another layer", name)
	}
	// plain String still falls through; the rule is applied by the caller via StringFrom.
	if name, l, ok, _ := r.String("bank_name"); !ok || name != "Default Bank" || l != LayerDefaults {
		t.Errorf("String(bank_name) = %q %v %v", name, l, ok)
	}
}
