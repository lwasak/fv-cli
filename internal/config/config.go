package config

// Config is the content of config.toml. Pointer fields distinguish "not set" from a zero
// value. Every key here must appear in the config reference.
type Config struct {
	Ksef      Ksef             `toml:"ksef"`
	Seller    Seller           `toml:"seller"`
	Defaults  Defaults         `toml:"defaults"`
	Numbering Numbering        `toml:"numbering"`
	Registry  Registry         `toml:"registry"`
	Parties   map[string]Party `toml:"parties"`
}

// Ksef holds the [ksef] table.
type Ksef struct {
	Environment *string `toml:"environment"`
}

// Seller holds the [seller] table.
type Seller struct {
	NIP          string `toml:"nip"`
	Name         string `toml:"name"`
	Address      string `toml:"address"`
	Email        string `toml:"email"`
	PlaceOfIssue string `toml:"place_of_issue"`
}

// Defaults holds the [defaults] table.
type Defaults struct {
	VatRate        string `toml:"vat_rate"`
	PaymentDays    *int   `toml:"payment_days"`
	BankAccount    string `toml:"bank_account"`
	Currency       string `toml:"currency"`
	SaleDate       string `toml:"sale_date"`
	LineTemplate   string `toml:"line_template"`
	Unit           string `toml:"unit"`
	ExemptionBasis string `toml:"exemption_basis"`
}

// Numbering holds the [numbering] table.
type Numbering struct {
	Pattern string `toml:"pattern"`
	Reset   string `toml:"reset"`
}

// Registry holds the [registry] table.
type Registry struct {
	CacheTTL *int `toml:"cache_ttl"`
}

// Party holds one [parties.<alias>] table.
type Party struct {
	NIP            string `toml:"nip"`
	VatID          string `toml:"vat_id"`
	Name           string `toml:"name"`
	Address        string `toml:"address"`
	VatRate        string `toml:"vat_rate"`
	PaymentDays    *int   `toml:"payment_days"`
	BankAccount    string `toml:"bank_account"`
	Currency       string `toml:"currency"`
	SaleDate       string `toml:"sale_date"`
	LineTemplate   string `toml:"line_template"`
	Unit           string `toml:"unit"`
	ExemptionBasis string `toml:"exemption_basis"`
}
