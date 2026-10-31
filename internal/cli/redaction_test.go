package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/logging"
)

func TestRedaction_SecretAbsentFromLogAndStderr(t *testing.T) {
	dataDir := t.TempDir()
	var out, errb bytes.Buffer
	deps := Deps{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string {
			if k == "FV_DATA_DIR" {
				return dataDir
			}
			return ""
		},
		GOOS: "linux",
		Home: func() (string, error) { return t.TempDir(), nil },
	}
	logging.Default.Register("s3cr3t-value")
	root, s := newRoot(deps)
	root.AddCommand(&cobra.Command{
		Use: "leak", Short: "s", Long: "l", Example: "e",
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := s.resolvePaths(cmd)
			if err != nil {
				return err
			}
			l, closeFn := s.logger(paths)
			defer closeFn()
			l.Info("token s3cr3t-value used")
			return errors.New("failed with s3cr3t-value")
		},
	})
	code := execute(deps, root, []string{"leak"})
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(errb.String(), "s3cr3t-value") || !strings.Contains(errb.String(), "[REDACTED]") {
		t.Errorf("stderr = %q", errb.String())
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "fv.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cr3t-value") || !strings.Contains(string(data), "[REDACTED]") {
		t.Errorf("log = %q", data)
	}
}

func TestVersionAndHelp_CreateNoFiles(t *testing.T) {
	dataDir := t.TempDir()
	cfgDir := t.TempDir()
	env := map[string]string{"FV_DATA_DIR": dataDir, "FV_CONFIG": filepath.Join(cfgDir, "config.toml")}
	for _, args := range [][]string{{"version"}, {"--help"}, {"help", "config"}, {"config", "--help"}} {
		if code, _, stderr := runCLI(t, env, args...); code != 0 {
			t.Fatalf("%v exit = %d, stderr %q", args, code, stderr)
		}
	}
	for _, d := range []string{dataDir, cfgDir} {
		entries, _ := os.ReadDir(d)
		if len(entries) != 0 {
			t.Errorf("%s not empty: %v", d, entries)
		}
	}
}

func TestInitAndValidate_LogOneRecord(t *testing.T) {
	dataDir := t.TempDir()
	p := filepath.Join(t.TempDir(), "config.toml")
	env := map[string]string{"FV_DATA_DIR": dataDir}
	if code, _, e := runInit(t, "5260250274\ndemo\n", env, "--config", p, "init"); code != 0 {
		t.Fatalf("init exit %d %q", code, e)
	}
	if code, _, e := runCLI(t, env, "--config", p, "config", "validate"); code != 0 {
		t.Fatalf("validate exit %d %q", code, e)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "fv.log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n != 2 {
		t.Errorf("log has %d lines: %q", n, data)
	}
}
