package cli

import (
	"strings"
	"testing"
)

const validConfigBody = `[ksef]
environment = "demo"

[seller]
nip = "5260250274"

[defaults]
vat_rate = "23"
payment_days = 14
bank_account = "PL61 1090 1014 0000 0712 1981 2874"

[numbering]
pattern = "FV/{YYYY}/{MM}/{SEQ:4}"

[parties.acme]
nip = "5260250274"
vat_rate = "8"
`

const badConfigBody = `[seller]
nip = "5260250275"

[defaults]
vat_rate = "7"
payment_days = -1
bank_account = "PL00"

[numbering]
pattern = "FV"

[parties.a]

[parties.b]
nip = "5260250274"
vat_rate = "np"
`

func TestConfigValidate_ValidExits0(t *testing.T) {
	p := writeConfig(t, validConfigBody)
	code, stdout, stderr := runCLI(t, nil, "--config", p, "config", "validate")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if stdout != "config OK: "+p+"\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestConfigValidate_AllSevenFindingsExit1(t *testing.T) {
	p := writeConfig(t, badConfigBody)
	code, stdout, stderr := runCLI(t, nil, "--config", p, "config", "validate")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if strings.Contains(stdout, "config OK") {
		t.Errorf("stdout = %q", stdout)
	}
	for _, k := range []string{"seller.nip: ", "defaults.vat_rate: ", "defaults.payment_days: ",
		"defaults.bank_account: ", "numbering.pattern: ", "parties.a: ", "parties.b.vat_rate: "} {
		if !strings.Contains(stderr, k) {
			t.Errorf("stderr lacks %q: %q", k, stderr)
		}
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) != 7 {
		t.Errorf("stderr lines = %d, want 7: %q", len(lines), stderr)
	}
}
