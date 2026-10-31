package money

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// The supported amount forms and what each must parse to.
var shellForms = []struct {
	arg    string
	grosze string
}{
	{"18500", "1850000"},
	{"18500.43", "1850043"},
	{"18500,43", "1850043"},
	{"18_500", "1850000"},
	{"18_500,43", "1850043"},
	{"12_000_000,01", "1200000001"},
	{"18500,05", "1850005"},
}

var shellModes = []string{"bare", "double", "single"}

type shellSpec struct {
	name   string
	exe    string // looked up on PATH
	ext    string // script extension
	script func(exe, args string) string
	args   func(script string) []string
}

var shellSpecs = []shellSpec{
	{
		name: "powershell", exe: "powershell.exe", ext: ".ps1",
		script: func(exe, args string) string { return "& '" + exe + "' " + args + "\r\n" },
		args: func(s string) []string {
			return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", s}
		},
	},
	{
		name: "pwsh", exe: "pwsh", ext: ".ps1",
		script: func(exe, args string) string { return "& '" + exe + "' " + args + "\r\n" },
		args:   func(s string) []string { return []string{"-NoProfile", "-NonInteractive", "-File", s} },
	},
	{
		name: "cmd", exe: "cmd.exe", ext: ".cmd",
		script: func(exe, args string) string { return "@\"" + exe + "\" " + args + "\r\n" },
		args:   func(s string) []string { return []string{"/d", "/c", s} },
	},
	{
		name: "bash", exe: "bash", ext: ".sh",
		script: func(exe, args string) string { return "'" + filepath.ToSlash(exe) + "' " + args + "\n" },
		args:   func(s string) []string { return []string{filepath.ToSlash(s)} },
	},
	{
		name: "zsh", exe: "zsh", ext: ".zsh",
		script: func(exe, args string) string { return "'" + filepath.ToSlash(exe) + "' " + args + "\n" },
		args:   func(s string) []string { return []string{"-f", filepath.ToSlash(s)} },
	},
}

// lookShell finds the shell executable. On Windows, bash is taken from Git's
// install folder first, because the bash on PATH is often WSL bash, which cannot
// read the Windows paths these tests pass. A WSL bash is reported as unusable.
func lookShell(sp shellSpec) (string, error) {
	if runtime.GOOS == "windows" && sp.name == "bash" {
		if git, err := exec.LookPath("git"); err == nil {
			root := filepath.Dir(filepath.Dir(git))
			for _, rel := range []string{`bin\bash.exe`, `usr\bin\bash.exe`} {
				cand := filepath.Join(root, rel)
				if _, err := os.Stat(cand); err == nil {
					return cand, nil
				}
			}
		}
		p, err := exec.LookPath(sp.exe)
		if err != nil {
			return "", err
		}
		lower := strings.ToLower(filepath.ToSlash(p))
		if strings.Contains(lower, "/windows/system32/") || strings.Contains(lower, "/windowsapps/") {
			return "", errors.New("only WSL bash found (" + p + "); it cannot read Windows paths, install Git Bash")
		}
		return p, nil
	}
	return exec.LookPath(sp.exe)
}

// TestMain removes the argecho helper's temp dir after the run.
func TestMain(m *testing.M) {
	code := m.Run()
	if echoDir != "" {
		_ = os.RemoveAll(echoDir)
	}
	os.Exit(code)
}

func quoteFor(mode, arg string) string {
	switch mode {
	case "double":
		return `"` + arg + `"`
	case "single":
		return `'` + arg + `'`
	}
	return arg
}

var (
	echoOnce sync.Once
	echoPath string
	echoErr  error
	echoDir  string
)

// argecho builds the helper once into a temp dir that outlives the subtests.
func argecho(t *testing.T) string {
	t.Helper()
	echoOnce.Do(func() {
		echoDir, echoErr = os.MkdirTemp("", "argecho")
		if echoErr != nil {
			return
		}
		name := "argecho"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		echoPath = filepath.Join(echoDir, name)
		out, err := exec.Command("go", "build", "-o", echoPath, "./testdata/argecho").CombinedOutput()
		if err != nil {
			echoErr = &buildErr{err, string(out)}
		}
	})
	if echoErr != nil {
		t.Fatalf("build argecho: %v", echoErr)
	}
	return echoPath
}

type buildErr struct {
	err error
	out string
}

func (e *buildErr) Error() string { return e.err.Error() + ": " + e.out }

// runShell runs one script in the shell and returns the helper's output lines.
func runShell(t *testing.T, sp shellSpec, shellPath, argLine string) []string {
	t.Helper()
	exe := argecho(t)
	// Not t.TempDir(): its name carries the subtest name, whose comma cmd.exe splits on.
	dir, err := os.MkdirTemp("", "shq")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	script := filepath.Join(dir, "run"+sp.ext)
	if err := os.WriteFile(script, []byte(sp.script(exe, argLine)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(shellPath, sp.args(script)...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", sp.name, err, out)
	}
	s := strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n"))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func shellVersion(sp shellSpec, path string) string {
	var c *exec.Cmd
	switch sp.name {
	case "powershell", "pwsh":
		c = exec.Command(path, "-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()")
	case "cmd":
		c = exec.Command(path, "/d", "/c", "ver")
	default:
		c = exec.Command(path, "--version")
	}
	out, err := c.CombinedOutput()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// shellExpected is the observed outcome where it differs from "the argument
// arrives unchanged and parses": the key is shell/mode/form.
//
// Observed 2026-10-30 on Windows PowerShell 5.1.26100, PowerShell 7.6.9,
// cmd.exe and GNU bash 5.2.37:
//   - Windows PowerShell 5.1, bare: the comma makes an array of numbers, so a
//     leading zero in the decimals is lost and the value changes silently
//     (18500,05 arrives as 18500,5 = 185 00,50). Quoting fixes it.
//   - cmd: single quotes are not quoting; the quote characters reach fv and the
//     amount is rejected. Double quotes work.
//   - Everything else arrives unchanged.
var shellExpected = map[string][]string{
	"powershell/bare/12_000_000,01": {"12_000_000,1=1200000010"},
	"powershell/bare/18500,05":      {"18500,5=1850050"},
	"cmd/single/18500":              {"'18500'=ERR"},
	"cmd/single/18500.43":           {"'18500.43'=ERR"},
	"cmd/single/18500,43":           {"'18500,43'=ERR"},
	"cmd/single/18_500":             {"'18_500'=ERR"},
	"cmd/single/18_500,43":          {"'18_500,43'=ERR"},
	"cmd/single/12_000_000,01":      {"'12_000_000,01'=ERR"},
	"cmd/single/18500,05":           {"'18500,05'=ERR"},
}

func expectedLines(sp shellSpec, mode string, arg, grosze string) []string {
	if e, ok := shellExpected[sp.name+"/"+mode+"/"+arg]; ok {
		return e
	}
	if sp.name == "zsh" {
		if e, ok := shellExpected["bash/"+mode+"/"+arg]; ok {
			return e
		}
	}
	return []string{arg + "=" + grosze}
}

// The help advises double quotes everywhere, so every form must parse right that way.
// The quoting mode is taken from QuotingAdvice, not hardcoded.
func TestShellQuoting_HelpAdviceWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("shell matrix is slow")
	}
	if !strings.Contains(QuotingAdvice, "double quotes") {
		t.Fatal("QuotingAdvice no longer recommends double quotes; update this test")
	}
	mode := "double"
	for _, sp := range shellSpecs {
		path, err := lookShell(sp)
		if err != nil {
			t.Logf("%s skipped: %v", sp.name, err)
			continue
		}
		for _, f := range shellForms {
			t.Run(sp.name+"/"+f.arg, func(t *testing.T) {
				got := runShell(t, sp, path, quoteFor(mode, f.arg))
				want := f.arg + "=" + f.grosze
				if len(got) != 1 || got[0] != want {
					t.Errorf("got %q, want %q", got, want)
				}
			})
		}
	}
}

func TestShellQuoting_Matrix(t *testing.T) {
	if testing.Short() {
		t.Skip("shell matrix is slow")
	}
	for _, sp := range shellSpecs {
		path, err := lookShell(sp)
		if err != nil {
			t.Logf("%s skipped: %v", sp.name, err)
			continue
		}
		t.Logf("%s: %s", sp.name, shellVersion(sp, path))
		for _, mode := range shellModes {
			for _, f := range shellForms {
				t.Run(sp.name+"/"+mode+"/"+f.arg, func(t *testing.T) {
					got := runShell(t, sp, path, quoteFor(mode, f.arg))
					t.Logf("OBSERVED %s/%s/%s => %q", sp.name, mode, f.arg, got)
					want := expectedLines(sp, mode, f.arg, f.grosze)
					if strings.Join(got, "|") != strings.Join(want, "|") {
						t.Errorf("got %q, want %q", got, want)
					}
				})
			}
		}
	}
}
