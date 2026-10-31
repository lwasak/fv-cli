package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMinimalTOML_RoundTripsThroughLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, MinimalTOML("5260250274", EnvDemo), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Seller.NIP != "5260250274" || cfg.Ksef.Environment == nil || *cfg.Ksef.Environment != "demo" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestMinimalTOMLWithBank_RoundTripsThroughLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := MinimalTOMLWithBank("5260250274", EnvTest, "PL61 1090 1014 0000 0712 1981 2874")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.BankAccount != "PL61 1090 1014 0000 0712 1981 2874" {
		t.Errorf("bank = %q", cfg.Defaults.BankAccount)
	}
	if string(MinimalTOMLWithBank("5260250274", EnvTest, "")) != string(MinimalTOML("5260250274", EnvTest)) {
		t.Error("an empty account must not add a [defaults] table")
	}
}
