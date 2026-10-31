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
