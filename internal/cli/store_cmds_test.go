package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/numbering"
	"github.com/lwasak/fv-cli/internal/store"
)

type storeEnv struct {
	t     *testing.T
	data  string
	cfg   string
	store *store.Store
}

func newStoreEnv(t *testing.T) *storeEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, config.MinimalTOML("1234563218", config.EnvTest), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	now := func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC) }
	return &storeEnv{t: t, data: data, cfg: cfg, store: store.Open(data, now)}
}

// run builds a root with the store commands (A-int registers them in newRoot later).
func (e *storeEnv) run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	deps := Deps{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string {
			if k == "FV_DATA_DIR" {
				return e.data
			}
			return ""
		},
		GOOS: "linux",
		Home: func() (string, error) { return e.t.TempDir(), nil },
		Now:  func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC) },
	}
	root, s := newRoot(deps)
	root.AddCommand(newListCmd(s), newDiscardCmd(s))
	code = execute(deps, root, append([]string{"--config", e.cfg}, args...))
	return code, out.String(), errb.String()
}

func (e *storeEnv) draft(env config.Env, issue string, gross int64, cur string) string {
	e.t.Helper()
	d, err := time.Parse("2006-01-02", issue)
	if err != nil {
		e.t.Fatal(err)
	}
	r, err := e.store.Reserve(context.Background(), env, numbering.DefaultPattern, numbering.ResetMonthly, d)
	if err != nil {
		e.t.Fatal(err)
	}
	m := store.Meta{Currency: cur, Gross: moneyAmount(gross)}
	if err := e.store.SaveDraft(env, r.Number, []byte("<x/>"), m); err != nil {
		e.t.Fatal(err)
	}
	return r.Number
}

func TestList_EmptyPrintsNoInvoices(t *testing.T) {
	e := newStoreEnv(t)
	code, out, errs := e.run("list")
	if code != 0 || strings.TrimSpace(out) != "no invoices" {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
}

func TestList_FilterMonthEnv(t *testing.T) {
	e := newStoreEnv(t)
	a := e.draft(config.EnvTest, "2026-10-02", 123000, "PLN")
	e.draft(config.EnvTest, "2026-11-02", 5000, "EUR")
	e.draft(config.EnvProd, "2026-10-03", 100, "PLN")
	if _, err := e.store.Transition(config.EnvTest, a, store.StateSending, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Transition(config.EnvTest, a, store.StateAccepted, func(m *store.Meta) error {
		m.KSeF = &store.KSeFRef{KSeFNumber: "1234563218-20261002-ABC-01"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Without flags: the environment in use (test, from the config) and the default range
	// (August to October), so the prod invoice is not listed. The November draft is outside
	// the range but not settled, so it is listed with a hint. No certificate is imported, so
	// KSeF is not asked and the command still succeeds.
	code, out, errs := e.run("list")
	if code != 0 {
		t.Fatalf("code %d %s", code, errs)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	if f := strings.Fields(lines[2]); strings.Join(f, " ") != "2026-11-02 FV/2026/11/001 sale - - 50,00 EUR draft" {
		t.Errorf("out-of-range draft row = %q", lines[2])
	}
	if !strings.Contains(errs, "note: FV/2026/11/001 (draft, issued 2026-11-02) is outside the listed range") {
		t.Errorf("stderr lacks the hint: %q", errs)
	}
	if f := strings.Fields(lines[0]); strings.Join(f, " ") != "ISSUED NUMBER DIR SELLER BUYER GROSS CUR STATE" {
		t.Errorf("header = %q", lines[0])
	}
	if f := strings.Fields(lines[1]); strings.Join(f, " ") != "2026-10-02 FV/2026/10/001 sale - - 1230,00 PLN accepted" {
		t.Errorf("row = %q", lines[1])
	}
	for _, want := range []string{"TEST: issue dates 2026-08-01 to 2026-10-31", "no certificate for test"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q: %q", want, errs)
		}
	}

	_, out, _ = e.run("list", "--from", "2026-10-01", "--to", "2026-11-30", "--wide")
	if n := strings.Count(out, "\n"); n != 3 || !strings.Contains(out, "1234563218-20261002-ABC-01") || !strings.Contains(out, "KSEF-NUMBER") {
		t.Errorf("wide out = %q", out)
	}
	_, out, _ = e.run("--env", "prod", "list")
	if n := strings.Count(out, "\n"); n != 2 || strings.Contains(out, "accepted") || !strings.Contains(out, "2026-10-03") {
		t.Errorf("prod out = %q", out)
	}
	_, out, _ = e.run("list", "--month", "2026-10")
	// the November draft is unsettled, so it is listed outside the month as well
	if n := strings.Count(out, "\n"); n != 3 || !strings.Contains(out, "FV/2026/11/001") {
		t.Errorf("month out = %q", out)
	}
	_, out, _ = e.run("list", "--month", "2026-12")
	if n := strings.Count(out, "\n"); n != 2 || strings.Contains(out, "accepted") || !strings.Contains(out, "draft") {
		t.Errorf("out = %q", out)
	}
	_, out, _ = e.run("list", "--month", "2026-12", "--as", "buyer")
	if strings.TrimSpace(out) != "no invoices" {
		t.Errorf("buyer out = %q", out)
	}
	if code, _, errs := e.run("list", "--month", "2026-13"); code != 1 || !strings.Contains(errs, "YYYY-MM") {
		t.Errorf("bad month: %d %q", code, errs)
	}
	if code, _, errs := e.run("list", "--month", "2026-10", "--from", "2026-10-01"); code != 1 || !strings.Contains(errs, "--month cannot be combined") {
		t.Errorf("month and from: %d %q", code, errs)
	}
	if code, _, errs := e.run("list", "--as", "everyone"); code != 1 || !strings.Contains(errs, "seller, buyer or all") {
		t.Errorf("bad --as: %d %q", code, errs)
	}
	if code, _, _ := e.run("--env", "staging", "list"); code != 1 {
		t.Errorf("bad env: %d", code)
	}
}

func TestList_WithoutConfigListsTheArchiveOfEveryEnvironment(t *testing.T) {
	e := newStoreEnv(t)
	e.draft(config.EnvTest, "2026-10-02", 123000, "PLN")
	e.draft(config.EnvProd, "2026-10-03", 100, "PLN")
	if err := os.Remove(e.cfg); err != nil {
		t.Fatal(err)
	}
	code, out, errs := e.run("list")
	if code != 0 {
		t.Fatalf("code %d %s", code, errs)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "ENV") || !strings.Contains(lines[1], "prod") || !strings.Contains(lines[2], "test") {
		t.Errorf("out = %q", out)
	}
	if !strings.Contains(errs, "no config file") || !strings.Contains(errs, "fv init") {
		t.Errorf("stderr = %q", errs)
	}
	// a range wider than the KSeF limit is fine here: nothing is sent to KSeF
	if code, _, errs := e.run("list", "--from", "2020-01-01"); code != 0 {
		t.Errorf("wide range: %d %s", code, errs)
	}
}

func TestDiscard_Latest(t *testing.T) {
	e := newStoreEnv(t)
	e.draft(config.EnvTest, "2026-10-02", 100, "PLN")
	n := e.draft(config.EnvTest, "2026-10-03", 100, "PLN")
	code, out, errs := e.run("discard", n)
	if code != 0 || !strings.Contains(out, "released") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if _, err := e.store.Load(config.EnvTest, n); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("still there: %v", err)
	}
}

func TestDiscard_NotLatestIsVoid(t *testing.T) {
	e := newStoreEnv(t)
	first := e.draft(config.EnvTest, "2026-10-02", 100, "PLN")
	e.draft(config.EnvTest, "2026-10-03", 100, "PLN")
	code, out, errs := e.run("discard", first)
	if code != 0 || !strings.Contains(out, "void-draft") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	m, err := e.store.Load(config.EnvTest, first)
	if err != nil || m.State != store.StateVoid {
		t.Fatalf("%v %v", m, err)
	}
	_, out, _ = e.run("list")
	if !strings.Contains(out, "void-draft") {
		t.Errorf("list = %q", out)
	}
}

func TestDiscard_RefusesSentAndUnknownNumbers(t *testing.T) {
	e := newStoreEnv(t)
	n := e.draft(config.EnvTest, "2026-10-02", 100, "PLN")
	if _, err := e.store.Transition(config.EnvTest, n, store.StateSending, nil); err != nil {
		t.Fatal(err)
	}
	code, _, errs := e.run("discard", n)
	if code != 1 || !strings.Contains(errs, "sending") {
		t.Errorf("sending: %d %q", code, errs)
	}
	if code, _, errs := e.run("discard", "FV/2026/10/099"); code != 1 || !strings.Contains(errs, "not found") {
		t.Errorf("missing: %d %q", code, errs)
	}
	if code, _, _ := e.run("discard"); code != 1 {
		t.Errorf("no arg: %d", code)
	}
}

// S6: discard is destructive, so a number that only exists in another environment is never
// discarded by accident, with or without an explicit --env.
func TestDiscard_NeverTouchesAnotherEnv(t *testing.T) {
	e := newStoreEnv(t)
	n := e.draft(config.EnvProd, "2026-10-02", 100, "PLN")
	for _, args := range [][]string{
		{"discard", n},                  // the config env is test
		{"--env", "test", "discard", n}, // explicit
		{"--env", "demo", "discard", n},
	} {
		code, out, errs := e.run(args...)
		if code != 1 || !strings.Contains(errs, "prod") || out != "" {
			t.Errorf("%v: exit %d, out %q, err %q", args, code, out, errs)
		}
		if m, err := e.store.Load(config.EnvProd, n); err != nil || m.State != store.StateDraft {
			t.Fatalf("%v: the prod draft was touched: %v %v", args, m, err)
		}
	}
	code, out, errs := e.run("--env", "prod", "discard", n)
	if code != 0 || !strings.Contains(out, "(prod)") {
		t.Fatalf("explicit prod: %d %q %q", code, out, errs)
	}
}

func TestShow_ExplicitEnvMustMatch(t *testing.T) {
	e := newStoreEnv(t)
	n := e.draft(config.EnvProd, "2026-10-02", 100, "PLN")
	if code, _, errs := e.run("--env", "demo", "show", n); code != 1 || !strings.Contains(errs, "belongs to the prod environment") {
		t.Errorf("exit %d: %s", code, errs)
	}
}

func TestStoreCommands_HaveHelp(t *testing.T) {
	root, s := newRoot(Deps{})
	for _, c := range []*cobra.Command{newListCmd(s), newDiscardCmd(s)} {
		root.AddCommand(c)
		if c.Short == "" || c.Long == "" || c.Example == "" {
			t.Errorf("%s: missing help text", c.Name())
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Usage == "" {
				t.Errorf("%s --%s: no usage", c.Name(), f.Name)
			}
		})
	}
}

func moneyAmount(v int64) money.Amount { return money.Amount(v) }

func TestList_OneBadMetaIsSkippedWithWarning(t *testing.T) {
	e := newStoreEnv(t)
	good := e.draft(config.EnvTest, "2026-10-03", 100, "PLN")
	e.draft(config.EnvTest, "2026-10-02", 100, "PLN")
	dirs, _ := filepath.Glob(filepath.Join(e.data, "invoices", "test", "*", "*"))
	var bad string
	for _, d := range dirs {
		if strings.HasSuffix(d, "002") {
			bad = d
		}
	}
	if bad == "" {
		t.Fatalf("second invoice folder not found in %v", dirs)
	}
	if err := os.WriteFile(filepath.Join(bad, "meta.json"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := e.run("list")
	if code != 0 || !strings.Contains(out, good) || !strings.Contains(errs, "warning: skipped") {
		t.Errorf("exit %d\nout %s\nerr %s", code, out, errs)
	}
}
