package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
)

func runHelp(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }, GOOS: "linux"}
	if code := Run(deps, args); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	return out.String() + errb.String()
}

func TestHelpConfig_PrintsReference(t *testing.T) {
	for _, args := range [][]string{{"help", "config"}, {"config", "--help"}} {
		out := runHelp(t, args...)
		for _, e := range config.Reference {
			if !strings.Contains(out, e.Key) {
				t.Errorf("fv %s: output lacks key %q", strings.Join(args, " "), e.Key)
			}
		}
	}
}

func TestConfigValidateHelp_IsNotReference(t *testing.T) {
	out := runHelp(t, "config", "validate", "--help")
	if strings.Contains(out, "defaults.payment_days") {
		t.Errorf("config validate --help printed the reference:\n%s", out)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("config validate --help is not the normal help:\n%s", out)
	}
}
