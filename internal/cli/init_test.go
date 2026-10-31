package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runInit(t *testing.T, stdin string, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errb,
		Getenv: func(k string) string { return env[k] },
		GOOS:   "linux",
		Home:   func() (string, error) { return t.TempDir(), nil },
	}
	code = Run(deps, args)
	return code, out.String(), errb.String()
}

func TestInit_WritesValidMinimalConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	code, _, stderr := runInit(t, "5260250274\ndemo\n", nil, "--config", p, "init")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	code, stdout, stderr := runCLI(t, nil, "--config", p, "config", "validate")
	if code != 0 {
		t.Fatalf("validate exit = %d, stderr = %q", code, stderr)
	}
	if stdout != "config OK: "+p+"\n" {
		t.Errorf("stdout = %q", stdout)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `environment = "demo"`) || !strings.Contains(string(data), `nip = "5260250274"`) {
		t.Errorf("config = %q", data)
	}
}

func TestInit_EmptyEnvSelectsTest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	code, _, stderr := runInit(t, "5260250274\n\n", nil, "--config", p, "init")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), `environment = "test"`) {
		t.Errorf("config = %q", data)
	}
}

func TestInit_InvalidNIPReasks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	code, stdout, stderr := runInit(t, "5260250275\n5260250274\ntest\n", nil, "--config", p, "init")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "checksum") {
		t.Errorf("stdout lacks reason: %q", stdout)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), `nip = "5260250274"`) {
		t.Errorf("config = %q", data)
	}
}

func TestInit_EOFWritesNothing(t *testing.T) {
	for _, in := range []string{"", "5260250274"} {
		dir := t.TempDir()
		p := filepath.Join(dir, "sub", "config.toml")
		code, _, _ := runInit(t, in, nil, "--config", p, "init")
		if code != 1 {
			t.Errorf("input %q: exit = %d, want 1", in, code)
		}
		if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
			t.Errorf("input %q: dir created (err = %v)", in, err)
		}
	}
}

func TestInit_ExistingConfigDeclinedUnchanged(t *testing.T) {
	for _, answer := range []string{"no\n", "\n", ""} {
		p := writeConfig(t, validConfigBody)
		code, stdout, stderr := runInit(t, answer+"5260250274\ndemo\n", nil, "--config", p, "init")
		if code != 0 {
			t.Fatalf("answer %q: exit = %d, stderr = %q", answer, code, stderr)
		}
		if !strings.Contains(stdout, "Overwrite "+p+"? [y/N]") || !strings.Contains(stdout, "config unchanged") {
			t.Errorf("answer %q: stdout = %q", answer, stdout)
		}
		data, _ := os.ReadFile(p)
		if string(data) != validConfigBody {
			t.Errorf("answer %q: config changed: %q", answer, data)
		}
	}
}

func TestInit_ExistingConfigYesReplaces(t *testing.T) {
	p := writeConfig(t, validConfigBody)
	code, _, stderr := runInit(t, "YES\n5260250274\nprod\n", nil, "--config", p, "init")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	data, _ := os.ReadFile(p)
	if string(data) == validConfigBody || !strings.Contains(string(data), `environment = "prod"`) {
		t.Errorf("config = %q", data)
	}
}

func TestInit_RespectsConfigFlagAndFVConfig(t *testing.T) {
	dir := t.TempDir()
	viaEnv := filepath.Join(dir, "env.toml")
	code, _, stderr := runInit(t, "5260250274\n\n", map[string]string{"FV_CONFIG": viaEnv}, "init")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(viaEnv); err != nil {
		t.Errorf("FV_CONFIG file missing: %v", err)
	}
	viaFlag := filepath.Join(dir, "flag.toml")
	code, _, stderr = runInit(t, "5260250274\n\n", map[string]string{"FV_CONFIG": viaEnv + "x"}, "--config", viaFlag, "init")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(viaFlag); err != nil {
		t.Errorf("--config file missing: %v", err)
	}
	if _, err := os.Stat(viaEnv + "x"); !os.IsNotExist(err) {
		t.Errorf("FV_CONFIG file written despite --config")
	}
}

const initIBAN = "PL61 1090 1014 0000 0712 1981 2874"

func initDefaultsBank(t *testing.T, p string) string {
	t.Helper()
	code, _, stderr := runCLI(t, nil, "--config", p, "config", "validate")
	if code != 0 {
		t.Fatalf("validate exit = %d, stderr = %q", code, stderr)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInit_AsksBankAccountAndWritesDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	// lower case and no spaces are accepted and written in the grouped form
	code, stdout, stderr := runInit(t, "5260250274\ndemo\npl61109010140000071219812874\n", nil, "--config", p, "init")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "Seller bank account") {
		t.Errorf("stdout = %q", stdout)
	}
	data := initDefaultsBank(t, p)
	if !strings.Contains(data, "[defaults]") || !strings.Contains(data, `bank_account = "`+initIBAN+`"`) {
		t.Errorf("config = %q", data)
	}
}

func TestInit_EmptyBankAccountSkips(t *testing.T) {
	for _, in := range []string{"5260250274\ntest\n\n", "5260250274\ntest\n"} {
		p := filepath.Join(t.TempDir(), "config.toml")
		code, _, stderr := runInit(t, in, nil, "--config", p, "init")
		if code != 0 {
			t.Fatalf("input %q: exit = %d, stderr = %q", in, code, stderr)
		}
		data, _ := os.ReadFile(p)
		if strings.Contains(string(data), "bank_account") || strings.Contains(string(data), "[defaults]") {
			t.Errorf("input %q: config = %q", in, data)
		}
	}
}

func TestInit_InvalidBankAccountReasks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	code, stdout, stderr := runInit(t, "5260250274\ntest\nPL61109010140000071219812875\n"+initIBAN+"\n", nil, "--config", p, "init")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "invalid IBAN") || !strings.Contains(stdout, "checksum") {
		t.Errorf("stdout lacks reason: %q", stdout)
	}
	if data := initDefaultsBank(t, p); !strings.Contains(data, initIBAN) {
		t.Errorf("config = %q", data)
	}
}

func TestInit_BankAccountFlagSkipsQuestion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	code, stdout, stderr := runInit(t, "5260250274\ntest\n", nil, "--config", p, "init", "--bank-account", "pl61 1090 1014 0000 0712 1981 2874")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "Seller bank account") {
		t.Errorf("question asked despite the flag: %q", stdout)
	}
	if data := initDefaultsBank(t, p); !strings.Contains(data, `bank_account = "`+initIBAN+`"`) {
		t.Errorf("config = %q", data)
	}
}

func TestInit_BankAccountFlagInvalidWritesNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "config.toml")
	code, _, stderr := runInit(t, "5260250274\ntest\n", nil, "--config", p, "init", "--bank-account", "PL00 1234")
	if code != 1 || !strings.Contains(stderr, "--bank-account") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Errorf("dir created (err = %v)", err)
	}
}

func TestInit_BankAccountIgnoredWhenConfigKept(t *testing.T) {
	p := writeConfig(t, validConfigBody)
	code, stdout, stderr := runInit(t, "n\n", nil, "--config", p, "init", "--bank-account", initIBAN)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "--bank-account ignored") {
		t.Errorf("stdout = %q", stdout)
	}
	data, _ := os.ReadFile(p)
	if string(data) != validConfigBody {
		t.Errorf("config changed: %q", data)
	}
}
