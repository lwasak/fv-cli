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
