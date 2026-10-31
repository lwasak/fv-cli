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
