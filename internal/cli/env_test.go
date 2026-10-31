package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &errb,
		Getenv: func(k string) string { return env[k] },
		GOOS:   "linux",
		Home:   func() (string, error) { return t.TempDir(), nil },
	}
	code = Run(deps, args)
	return code, out.String(), errb.String()
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoot_InvalidEnvFlagExits1(t *testing.T) {
	code, _, stderr := runCLI(t, nil, "--env", "staging", "version")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "staging") || !strings.Contains(stderr, "test, demo, prod") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRoot_InvalidEnvInConfigExits1(t *testing.T) {
	p := writeConfig(t, "[ksef]\nenvironment = \"staging\"\n")
	code, _, stderr := runCLI(t, nil, "--config", p, "config", "validate")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "ksef.environment") || !strings.Contains(stderr, "test, demo, prod") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestConfigValidate_MissingConfigSuggestsInit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.toml")
	code, _, stderr := runCLI(t, nil, "--config", p, "config", "validate")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, p) || !strings.Contains(stderr, "fv init") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestConfigValidate_LoadsValidConfig(t *testing.T) {
	p := writeConfig(t, "[ksef]\nenvironment = \"demo\"\n")
	code, _, stderr := runCLI(t, nil, "--config", p, "config", "validate")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestConfigValidate_EnvFlagOverridesFileEnv(t *testing.T) {
	p := writeConfig(t, "[ksef]\nenvironment = \"demo\"\n")
	code, _, stderr := runCLI(t, nil, "--config", p, "--env", "prod", "config", "validate")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}
