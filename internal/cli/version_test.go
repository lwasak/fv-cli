package cli

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
)

func run(t *testing.T, deps Deps, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	deps.Stdout = &out
	deps.Stderr = &errb
	code := Run(deps, args)
	return out.String(), errb.String(), code
}

func TestVersion_DevDefaults(t *testing.T) {
	out, _, code := run(t, DefaultDeps(), "version")
	want := fmt.Sprintf("fv dev (unknown, unknown, %s/%s)\n", runtime.GOOS, runtime.GOARCH)
	if code != 0 || out != want {
		t.Fatalf("code=%d out=%q want %q", code, out, want)
	}
}

func TestVersion_LdflagValues(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldV, oldC, oldD })
	Version, Commit, Date = "1.2.3", "abc1234", "2026-10-29"
	out, _, code := run(t, DefaultDeps(), "version")
	want := fmt.Sprintf("fv 1.2.3 (abc1234, 2026-10-29, %s/%s)\n", runtime.GOOS, runtime.GOARCH)
	if code != 0 || out != want {
		t.Fatalf("code=%d out=%q want %q", code, out, want)
	}
}

func TestExecute_UnknownCommandExits1(t *testing.T) {
	_, errb, code := run(t, DefaultDeps(), "nosuchcommand")
	if code != 1 {
		t.Fatalf("code=%d want 1", code)
	}
	if errb == "" {
		t.Fatal("expected an error message on stderr")
	}
}
