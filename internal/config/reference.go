package config

import (
	"fmt"
	"strings"
)

// Entry documents one config.toml key.
type Entry struct {
	Key         string // key path; <alias> stands for a party alias, <key> for an item key
	Type        string
	Default     string
	Allowed     string // allowed values, empty when any value of the type is accepted
	Example     string
	Description string
}

// Reference lists every key of config.toml. A test keeps it in sync with the Config structs.
var Reference = []Entry{
	{
		Key: "ksef.environment", Type: "string", Default: "test (when unset)",
		Allowed: "test, demo, prod", Example: `environment = "demo"`,
		Description: "KSeF environment; the --env flag overrides it.",
	},
	{
		Key: "seller.nip", Type: "string", Default: "none (required for invoicing)",
		Allowed: "10 digits, no separators, valid checksum", Example: `nip = "5260250274"`,
		Description: "The seller's NIP.",
	},
	{
		Key: "seller.name", Type: "string", Default: "none (from the White List)",
		Allowed: "1-512 characters", Example: `name = "Example Studio Jan Kowalski"`,
		Description: "Seller name; overrides the registry value.",
	},
	{
		Key: "seller.address", Type: "string", Default: "none (from the White List)",
		Allowed: "1-512 characters (one line, AdresL1)", Example: `address = "ul. Długa 1, 80-001 Gdańsk"`,
		Description: "Seller address; overrides the registry value.",
	},
	{
		Key: "seller.email", Type: "string", Default: "none (omitted)",
		Allowed: "3-255 characters, contains @", Example: `email = "faktury@example.com"`,
		Description: "Seller contact e-mail written to the invoice.",
	},
	{
		Key: "seller.place_of_issue", Type: "string", Default: "none (P_1M omitted)",
		Allowed: "1-256 characters", Example: `place_of_issue = "Gdańsk"`,
		Description: "Place of issue (P_1M) written to the invoice.",
	},
	{
		Key: "defaults.vat_rate", Type: "string", Default: "none (not set)",
		Allowed: "23, 8, 5, zw (0 and np are not supported yet)", Example: `vat_rate = "23"`,
		Description: "VAT rate code used when a party or flag does not set one.",
	},
	{
		Key: "defaults.payment_days", Type: "integer", Default: "14 (when unset)",
		Allowed: "0 or more", Example: "payment_days = 14",
		Description: "Days until payment is due.",
	},
	{
		Key: "defaults.bank_account", Type: "string", Default: "none (not set)",
		Allowed: "valid IBAN, spaces allowed", Example: `bank_account = "PL61 1090 1014 0000 0712 1981 2874"`,
		Description: "Seller bank account printed on invoices.",
	},
	{
		Key: "defaults.currency", Type: "string", Default: "PLN",
		Allowed: "3 capital letters (ISO 4217)", Example: `currency = "EUR"`,
		Description: "Invoice currency.",
	},
	{
		Key: "defaults.sale_date", Type: "string", Default: "issue_date",
		Allowed: "issue_date, last_day_of_month, last_day_of_previous_month", Example: `sale_date = "last_day_of_month"`,
		Description: "How the sale date (P_6) is derived from the issue date.",
	},
	{
		Key: "defaults.line_template", Type: "string", Default: "Usługi za {month_name} {year}",
		Allowed: "text with the tokens {month_name} {month_name_en} {year}, 1-512 characters", Example: `line_template = "Consulting for {month_name} {year}"`,
		Description: "Invoice line description template.",
	},
	{
		Key: "defaults.unit", Type: "string", Default: "usł.",
		Allowed: "1-256 characters", Example: `unit = "godz."`,
		Description: "Unit of measure written on the line.",
	},
	{
		Key: "defaults.exemption_basis", Type: "string", Default: "none (required for zw)",
		Allowed: "1-256 characters", Example: `exemption_basis = "art. 43 ust. 1 pkt 37 ustawy o VAT"`,
		Description: "Statutory basis of a VAT exemption (zw), written to P_19A.",
	},
	{
		Key: "defaults.bank_name", Type: "string", Default: "none (NazwaBanku omitted)",
		Allowed: "1-256 characters", Example: `bank_name = "mBank S.A."`,
		Description: "Name of the bank that holds the seller's account, written next to the account.",
	},
	{
		Key: "defaults.swift", Type: "string", Default: "none (SWIFT omitted)",
		Allowed: "8 or 11 characters BIC: 6 letters, 2 letters/digits, optional 3 letters/digits; spaces and lower case are normalised", Example: `swift = "BREXPLPW"`,
		Description: "SWIFT/BIC code of the seller's bank, written next to the account.",
	},
	{
		Key: "numbering.pattern", Type: "string", Default: "FV/{YYYY}/{MM}/{SEQ:3} (when unset)",
		Allowed:     "{YYYY} {YY} {MM} {DD} {SEQ} {SEQ:n} (n 1-9), exactly one SEQ; literals A-Z a-z 0-9 / - _ .",
		Example:     `pattern = "FV/{YYYY}/{MM}/{SEQ:4}"`,
		Description: "Invoice number pattern.",
	},
	{
		Key: "numbering.reset", Type: "string", Default: "derived from the pattern: monthly with {MM}, else yearly with a year token, else never",
		Allowed: "monthly, yearly, never; the period must appear in the pattern", Example: `reset = "yearly"`,
		Description: "When the sequence number restarts at 1.",
	},
	{
		Key: "registry.cache_ttl", Type: "integer", Default: "30",
		Allowed: "1-365 (days)", Example: `cache_ttl = 30`,
		Description: "Days a cached seller registry entry stays fresh.",
	},
	{
		Key: "parties.<alias>.nip", Type: "string", Default: "none",
		Allowed: "10 digits; set exactly one of nip or vat_id", Example: `nip = "5260250274"`,
		Description: "Polish NIP of the party named <alias>.",
	},
	{
		Key: "parties.<alias>.vat_id", Type: "string", Default: "none",
		Allowed: "2-letter country prefix + alphanumerics; set exactly one of nip or vat_id", Example: `vat_id = "DE123456789"`,
		Description: "EU VAT id of the party named <alias>.",
	},
	{
		Key: "parties.<alias>.vat_rate", Type: "string", Default: "none (falls back to defaults.vat_rate)",
		Allowed: "same as defaults.vat_rate", Example: `vat_rate = "8"`,
		Description: "Per-party VAT rate override.",
	},
	{
		Key: "parties.<alias>.payment_days", Type: "integer", Default: "none (falls back to defaults.payment_days)",
		Allowed: "0 or more", Example: "payment_days = 30",
		Description: "Per-party payment term override.",
	},
	{
		Key: "parties.<alias>.bank_account", Type: "string", Default: "none (falls back to defaults.bank_account)",
		Allowed: "valid IBAN", Example: `bank_account = "PL61 1090 1014 0000 0712 1981 2874"`,
		Description: "Per-party bank account override.",
	},
	{
		Key: "parties.<alias>.name", Type: "string", Default: "none (from the registry)",
		Allowed: "1-512 characters", Example: `name = "Acme Sp. z o.o."`,
		Description: "Party name; overrides the registry value.",
	},
	{
		Key: "parties.<alias>.address", Type: "string", Default: "none (from the registry)",
		Allowed: "1-512 characters (one line)", Example: `address = "ul. Prosta 2, 00-001 Warszawa"`,
		Description: "Party address; overrides the registry value.",
	},
	{
		Key: "parties.<alias>.currency", Type: "string", Default: "none (falls back to defaults.currency)",
		Allowed: "same as defaults.currency", Example: `currency = "EUR"`,
		Description: "Per-party currency override.",
	},
	{
		Key: "parties.<alias>.sale_date", Type: "string", Default: "none (falls back to defaults.sale_date)",
		Allowed: "same as defaults.sale_date", Example: `sale_date = "last_day_of_month"`,
		Description: "Per-party sale date rule override.",
	},
	{
		Key: "parties.<alias>.line_template", Type: "string", Default: "none (falls back to defaults.line_template)",
		Allowed: "same as defaults.line_template", Example: `line_template = "Consulting for {month_name} {year}"`,
		Description: "Per-party line description template override.",
	},
	{
		Key: "parties.<alias>.unit", Type: "string", Default: "none (falls back to defaults.unit)",
		Allowed: "same as defaults.unit", Example: `unit = "godz."`,
		Description: "Per-party unit of measure override.",
	},
	{
		Key: "parties.<alias>.exemption_basis", Type: "string", Default: "none (falls back to defaults.exemption_basis)",
		Allowed: "same as defaults.exemption_basis", Example: `exemption_basis = "art. 43 ust. 1 pkt 37 ustawy o VAT"`,
		Description: "Per-party VAT exemption basis override.",
	},
	{
		Key: "parties.<alias>.bank_name", Type: "string", Default: "none (taken with the account from the same table)",
		Allowed: "same as defaults.bank_name; needs bank_account in the same table", Example: `bank_name = "ING Bank Śląski"`,
		Description: "Bank name of the party's own bank_account.",
	},
	{
		Key: "parties.<alias>.swift", Type: "string", Default: "none (taken with the account from the same table)",
		Allowed: "same as defaults.swift; needs bank_account in the same table", Example: `swift = "INGBPLPW"`,
		Description: "SWIFT/BIC of the party's own bank_account.",
	},
	{
		Key: "parties.<alias>.items", Type: "array of strings", Default: "none (lines come from flags)",
		Allowed: "keys of [items.<key>], at least one", Example: `items = ["dev", "support"]`,
		Description: "Saved items billed to this party when no --net, --qty, --item or --line is given.",
	},
	{
		Key: "items.<key>.name", Type: "string", Default: "none (required)",
		Allowed: "text with the tokens {month_name} {month_name_en} {year}, 1-512 characters", Example: `name = "Programming services {month_name} {year}"`,
		Description: "Line description of the saved item.",
	},
	{
		Key: "items.<key>.unit", Type: "string", Default: "none (falls back to the party's or defaults' unit)",
		Allowed: "1-256 characters", Example: `unit = "h"`,
		Description: "Unit of measure of the saved item.",
	},
	{
		Key: "items.<key>.unit_price", Type: "string", Default: "none (required)",
		Allowed: `amount in zloty, e.g. "120,00" or "120.00" (quoted)`, Example: `unit_price = "120,00"`,
		Description: "Net unit price, in the invoice currency; quote it, a TOML number is rejected.",
	},
	{
		Key: "items.<key>.quantity", Type: "string", Default: "1",
		Allowed: "positive, up to 6 decimals (quoted)", Example: `quantity = "160"`,
		Description: "Default quantity; a quantity given on the command line with --item overrides it.",
	},
	{
		Key: "items.<key>.vat_rate", Type: "string", Default: "none (--rate, then party, then defaults)",
		Allowed: "same as defaults.vat_rate", Example: `vat_rate = "23"`,
		Description: "VAT rate of the saved item; it beats the invoice-wide --rate.",
	},
	{
		Key: "items.<key>.currency", Type: "string", Default: "none (the price is in the invoice currency)",
		Allowed: "same as defaults.currency", Example: `currency = "EUR"`,
		Description: "Currency the price is stated in; issuing in another currency is an error, never a conversion.",
	},
	{
		Key: "items.<key>.index", Type: "string", Default: "none (Indeks omitted)",
		Allowed: "1-50 characters", Example: `index = "DEV-01"`,
		Description: "Internal item index (Indeks) written on the line.",
	},
	{
		Key: "items.<key>.pkwiu", Type: "string", Default: "none (PKWiU omitted)",
		Allowed: "1-50 characters", Example: `pkwiu = "62.01.11.0"`,
		Description: "PKWiU classification written on the line.",
	},
	{
		Key: "items.<key>.gtin", Type: "string", Default: "none (GTIN omitted)",
		Allowed: "8, 12, 13 or 14 digits with a valid GS1 check digit", Example: `gtin = "5901234123457"`,
		Description: "GTIN (barcode) written on the line.",
	},
}

// FormatReference renders Reference as text for `fv help config`.
func FormatReference() string {
	var b strings.Builder
	b.WriteString("Config keys (config.toml):\n")
	for _, e := range Reference {
		fmt.Fprintf(&b, "\n  %s\n", e.Key)
		fmt.Fprintf(&b, "    %s\n", e.Description)
		fmt.Fprintf(&b, "    type:    %s\n", e.Type)
		fmt.Fprintf(&b, "    default: %s\n", e.Default)
		if e.Allowed != "" {
			fmt.Fprintf(&b, "    allowed: %s\n", e.Allowed)
		}
		fmt.Fprintf(&b, "    example: %s\n", e.Example)
	}
	return b.String()
}
