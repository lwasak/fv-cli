package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_Minimal(t *testing.T) {
	p := writeCfg(t, "[ksef]\nenvironment = \"demo\"\n[seller]\nnip = \"5260250274\"\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Ksef.Environment == nil || *cfg.Ksef.Environment != "demo" {
		t.Errorf("environment = %v", cfg.Ksef.Environment)
	}
	if cfg.Seller.NIP != "5260250274" {
		t.Errorf("nip = %q", cfg.Seller.NIP)
	}
}

func TestLoad_FullSchema(t *testing.T) {
	p := writeCfg(t, `
[defaults]
vat_rate = "23"
payment_days = 14
bank_account = "PL61109010140000071219812874"
[numbering]
pattern = "FV/{YYYY}/{SEQ:4}"
[parties.acme]
nip = "5260250274"
payment_days = 30
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.PaymentDays == nil || *cfg.Defaults.PaymentDays != 14 {
		t.Errorf("payment_days = %v", cfg.Defaults.PaymentDays)
	}
	if cfg.Parties["acme"].PaymentDays == nil || *cfg.Parties["acme"].PaymentDays != 30 {
		t.Errorf("party payment_days = %v", cfg.Parties["acme"].PaymentDays)
	}
}

func TestLoad_UnknownKeyNamesKey(t *testing.T) {
	p := writeCfg(t, "[seller]\nnip = \"5260250274\"\nnipp = \"x\"\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "seller.nipp") {
		t.Errorf("error does not name the key: %v", err)
	}
}

func TestLoad_SyntaxErrorNamesLine(t *testing.T) {
	p := writeCfg(t, "[seller]\nnip = \"5260250274\"\n[ksef\nenvironment = \"test\"\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error does not name line 3: %v", err)
	}
}

func TestLoad_MissingFileIsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if !os.IsNotExist(unwrapAll(err)) {
		t.Errorf("want not-exist error, got %v", err)
	}
}

func unwrapAll(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok || u.Unwrap() == nil {
			return err
		}
		err = u.Unwrap()
	}
}

func TestLoad_StripsUTF8BOM(t *testing.T) {
	p := writeCfg(t, "\xef\xbb\xbf[ksef]\nenvironment = \"demo\"\n[seller]\nnip = \"5260250274\"\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Seller.NIP != "5260250274" {
		t.Errorf("nip = %q", cfg.Seller.NIP)
	}
}

func TestLoad_ItemsAndBank(t *testing.T) {
	p := writeCfg(t, `
[defaults]
bank_account = "PL61109010140000071219812874"
bank_name = "mBank S.A."
swift = "BREXPLPW"
[items.dev]
name = "Programming {month_name} {year}"
unit_price = "120,00"
quantity = "160"
gtin = "5901234123457"
[parties.acme]
nip = "5260250274"
items = ["dev"]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.BankName != "mBank S.A." || cfg.Defaults.SWIFT != "BREXPLPW" {
		t.Errorf("bank = %+v", cfg.Defaults)
	}
	it := cfg.Items["dev"]
	if it.UnitPrice != "120,00" || it.Quantity != "160" || it.GTIN != "5901234123457" {
		t.Errorf("item = %+v", it)
	}
	if got := cfg.Parties["acme"].Items; len(got) != 1 || got[0] != "dev" {
		t.Errorf("party items = %v", got)
	}
}

func TestLoad_UnquotedItemPriceIsAnError(t *testing.T) {
	p := writeCfg(t, "[items.dev]\nname = \"x\"\nunit_price = 120.5\n")
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "unit_price") {
		t.Fatalf("err = %v, want it to name unit_price", err)
	}
}
