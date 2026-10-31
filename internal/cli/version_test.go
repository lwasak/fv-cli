package cli

import (
	"bytes"
	"fmt"
	"runtime"
	"runtime/debug"
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

func TestWithBuildInfo(t *testing.T) {
	bi := func(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: mainVersion}, Settings: settings}
	}
	rev := "0123456789abcdef0123456789abcdef01234567"
	for name, c := range map[string]struct {
		version, commit, date string
		bi                    *debug.BuildInfo
		wantV, wantC, wantD   string
	}{
		"go install of a release candidate": {"dev", "unknown", "unknown", bi("v0.1.0-rc.1"), "0.1.0-rc.1", "unknown", "unknown"},
		"go install of a release":           {"dev", "unknown", "unknown", bi("v1.2.3"), "1.2.3", "unknown", "unknown"},
		"pseudo version":                    {"dev", "unknown", "unknown", bi("v0.0.0-20261031120000-abcdef123456"), "0.0.0-20261031120000-abcdef123456", "unknown", "unknown"},
		"build in a checked out tree": {"dev", "unknown", "unknown",
			bi("(devel)", debug.BuildSetting{Key: "vcs.revision", Value: rev}, debug.BuildSetting{Key: "vcs.time", Value: "2026-10-30T12:00:00Z"}, debug.BuildSetting{Key: "vcs.modified", Value: "false"}),
			"dev", "0123456", "2026-10-30T12:00:00Z"},
		"dirty tree": {"dev", "unknown", "unknown",
			bi("(devel)", debug.BuildSetting{Key: "vcs.revision", Value: rev}, debug.BuildSetting{Key: "vcs.modified", Value: "true"}),
			"dev", "0123456-dirty", "unknown"},
		"module version and vcs together": {"dev", "unknown", "unknown",
			bi("v0.1.0", debug.BuildSetting{Key: "vcs.revision", Value: rev}, debug.BuildSetting{Key: "vcs.time", Value: "2026-10-30T12:00:00Z"}),
			"0.1.0", "0123456", "2026-10-30T12:00:00Z"},
		"short revision":              {"dev", "unknown", "unknown", bi("", debug.BuildSetting{Key: "vcs.revision", Value: "abc"}), "dev", "abc", "unknown"},
		"ldflags win":                 {"1.0.0", "deadbee", "2026-01-01T00:00:00Z", bi("v9.9.9", debug.BuildSetting{Key: "vcs.revision", Value: rev}, debug.BuildSetting{Key: "vcs.time", Value: "x"}), "1.0.0", "deadbee", "2026-01-01T00:00:00Z"},
		"ldflags version only":        {"1.0.0", "unknown", "unknown", bi("v9.9.9", debug.BuildSetting{Key: "vcs.revision", Value: rev}), "1.0.0", "0123456", "unknown"},
		"nothing embedded":            {"dev", "unknown", "unknown", bi(""), "dev", "unknown", "unknown"},
		"no build info at all":        {"dev", "unknown", "unknown", nil, "dev", "unknown", "unknown"},
		"unversioned devel and tests": {"dev", "unknown", "unknown", bi("(devel)"), "dev", "unknown", "unknown"},
	} {
		v, cm, d := withBuildInfo(c.version, c.commit, c.date, c.bi)
		if v != c.wantV || cm != c.wantC || d != c.wantD {
			t.Errorf("%s: got (%s, %s, %s), want (%s, %s, %s)", name, v, cm, d, c.wantV, c.wantC, c.wantD)
		}
	}
}
