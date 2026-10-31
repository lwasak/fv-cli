package config

import "fmt"

// Layer names the source a resolved setting came from, highest precedence first.
type Layer int

// Precedence layers: flag > party override > [defaults] > registry.
const (
	LayerFlag Layer = iota + 1
	LayerParty
	LayerDefaults
	LayerRegistry
)

// String returns the layer name.
func (l Layer) String() string {
	switch l {
	case LayerFlag:
		return "flag"
	case LayerParty:
		return "party"
	case LayerDefaults:
		return "defaults"
	case LayerRegistry:
		return "registry"
	}
	return "unknown"
}

// RegistrySource supplies per-party values from the registry (implemented in the registry package).
type RegistrySource interface {
	Lookup(partyAlias, key string) (any, bool)
}

// NoRegistry is a RegistrySource that never has a value.
type NoRegistry struct{}

// Lookup always reports not found.
func (NoRegistry) Lookup(string, string) (any, bool) { return nil, false }

// Resolver resolves a setting through the precedence layers for one party.
type Resolver struct {
	cfg      *Config
	alias    string
	flags    map[string]any
	registry RegistrySource
}

// NewResolver builds a Resolver. flags and registry may be nil.
func NewResolver(cfg *Config, partyAlias string, flags map[string]any, registry RegistrySource) *Resolver {
	if registry == nil {
		registry = NoRegistry{}
	}
	if cfg == nil {
		cfg = &Config{}
	}
	return &Resolver{cfg: cfg, alias: partyAlias, flags: flags, registry: registry}
}

// String resolves a string setting. ok is false when no layer sets it; err when a layer
// holds a value of the wrong type.
func (r *Resolver) String(key string) (string, Layer, bool, error) {
	v, layer, ok := r.lookup(key)
	if !ok {
		return "", 0, false, nil
	}
	s, isString := v.(string)
	if !isString {
		return "", layer, false, fmt.Errorf("setting %q from %s: want string, got %T", key, layer, v)
	}
	return s, layer, true, nil
}

// Int resolves an integer setting. ok is false when no layer sets it; err when a layer
// holds a value of the wrong type.
func (r *Resolver) Int(key string) (int, Layer, bool, error) {
	v, layer, ok := r.lookup(key)
	if !ok {
		return 0, 0, false, nil
	}
	switch n := v.(type) {
	case int:
		return n, layer, true, nil
	case int64:
		return int(n), layer, true, nil
	}
	return 0, layer, false, fmt.Errorf("setting %q from %s: want integer, got %T", key, layer, v)
}

// StringFrom reads one string key from one layer only, without falling through to the
// others. It exists for keys that must come from the same layer as another key, such as
// bank_name and swift, which describe the bank_account of that layer. ok is false when the
// layer does not set the key.
func (r *Resolver) StringFrom(key string, layer Layer) (string, bool, error) {
	var (
		v  any
		ok bool
	)
	switch layer {
	case LayerFlag:
		v, ok = r.flags[key]
	case LayerParty:
		if p, found := r.cfg.Parties[r.alias]; found {
			v, ok = partyValue(p, key)
		}
	case LayerDefaults:
		v, ok = defaultsValue(r.cfg.Defaults, key)
	case LayerRegistry:
		v, ok = r.registry.Lookup(r.alias, key)
	}
	if !ok {
		return "", false, nil
	}
	s, isString := v.(string)
	if !isString {
		return "", false, fmt.Errorf("setting %q from %s: want string, got %T", key, layer, v)
	}
	return s, true, nil
}

func (r *Resolver) lookup(key string) (any, Layer, bool) {
	if v, ok := r.flags[key]; ok {
		return v, LayerFlag, true
	}
	if p, ok := r.cfg.Parties[r.alias]; ok {
		if v, ok := partyValue(p, key); ok {
			return v, LayerParty, true
		}
	}
	if v, ok := defaultsValue(r.cfg.Defaults, key); ok {
		return v, LayerDefaults, true
	}
	if v, ok := r.registry.Lookup(r.alias, key); ok {
		return v, LayerRegistry, true
	}
	return nil, 0, false
}

func partyValue(p Party, key string) (any, bool) {
	return pick(key, p.PaymentDays, map[string]string{
		"vat_rate": p.VatRate, "bank_account": p.BankAccount, "currency": p.Currency,
		"sale_date": p.SaleDate, "line_template": p.LineTemplate, "unit": p.Unit,
		"exemption_basis": p.ExemptionBasis, "bank_name": p.BankName, "swift": p.SWIFT,
	})
}

func defaultsValue(d Defaults, key string) (any, bool) {
	return pick(key, d.PaymentDays, map[string]string{
		"vat_rate": d.VatRate, "bank_account": d.BankAccount, "currency": d.Currency,
		"sale_date": d.SaleDate, "line_template": d.LineTemplate, "unit": d.Unit,
		"exemption_basis": d.ExemptionBasis, "bank_name": d.BankName, "swift": d.SWIFT,
	})
}

// pick returns the value of key from one layer. An empty string means "not set" for every
// string key; payment_days is set when its pointer is non-nil.
func pick(key string, paymentDays *int, strs map[string]string) (any, bool) {
	if key == "payment_days" {
		if paymentDays == nil {
			return nil, false
		}
		return *paymentDays, true
	}
	if v, ok := strs[key]; ok {
		return v, v != ""
	}
	return nil, false
}
