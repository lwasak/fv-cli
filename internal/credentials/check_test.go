package credentials_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
)

func TestCheck_ReturnsFindingsWithoutDecrypting(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	nip := "5260250274"

	// Nothing imported: a warning, no finding.
	res := credentials.Check(dir, config.EnvTest, nip, now)
	if len(res.Findings) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "fv init") {
		t.Fatalf("no credential: %+v", res)
	}

	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour)})
	// The passphrase is never given to Check, so success proves it does not decrypt.
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, c, "pw-1234")}, dir, config.EnvTest, nip, "pw-1234"); err != nil {
		t.Fatal(err)
	}
	res = credentials.Check(dir, config.EnvTest, nip, now)
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %v", res.Findings)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "expires") {
		t.Errorf("warnings = %v (48h left is within 30 days)", res.Warnings)
	}

	// Expired.
	res = credentials.Check(dir, config.EnvTest, nip, now.Add(72*time.Hour))
	if len(res.Findings) != 1 {
		t.Errorf("expired findings = %v", res.Findings)
	}
	// Other seller NIP.
	other := "5260250275"
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, c, "pw-1234")}, dir, config.EnvTest, other, "pw-1234"); err != nil {
		t.Fatal(err)
	}
	res = credentials.Check(dir, config.EnvTest, other, now)
	if len(res.Findings) != 1 {
		t.Errorf("mismatch findings = %v", res.Findings)
	}
	// Corrupt certificate file.
	if err := os.WriteFile(credentials.Dir(dir, config.EnvTest, nip)+"/cert.pem", []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	res = credentials.Check(dir, config.EnvTest, nip, now)
	if len(res.Findings) != 1 {
		t.Errorf("corrupt findings = %v", res.Findings)
	}
}
