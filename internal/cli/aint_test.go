package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/registry"
	"github.com/lwasak/fv-cli/internal/store"
)

func TestQuotaCounter_StoreQuotaIsRegistryQuota(t *testing.T) {
	st := store.Open(t.TempDir(), func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC) })
	q := quotaCounter{st: st}
	for i := 1; i <= registry.DailyLimit; i++ {
		if n, err := q.WhiteListCall("2026-10-31", false); err != nil || n != i {
			t.Fatalf("call %d: n=%d err=%v", i, n, err)
		}
	}
	n, err := q.WhiteListCall("2026-10-31", false)
	if !errors.Is(err, registry.ErrQuota) || !errors.Is(err, store.ErrQuota) || n != registry.DailyLimit {
		t.Fatalf("non-essential at limit: n=%d err=%v", n, err)
	}
	if !strings.Contains(err.Error(), "90 calls") {
		t.Errorf("message should show the count: %v", err)
	}
	if n, err := q.WhiteListCall("2026-10-31", true); err != nil || n != registry.DailyLimit+1 {
		t.Errorf("essential must pass: n=%d err=%v", n, err)
	}
}

func TestDefaultCounter_IsStoreBacked(t *testing.T) {
	e := newStoreEnv(t)
	s := &session{deps: Deps{Now: func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC) }}}
	rc := &runContext{Paths: config.Paths{DataDir: e.data}}
	c := whiteListCounter(s, rc)
	if _, ok := c.(quotaCounter); !ok {
		t.Fatalf("default counter is %T, want the store-backed adapter", c)
	}
	if n, err := c.WhiteListCall("2026-10-31", true); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if n, _ := store.Open(e.data, nil).WhiteListCall("2026-10-31", true); n != 2 {
		t.Errorf("count not shared through state.json: %d", n)
	}
}

func TestHelp_BuiltinsHaveExamples(t *testing.T) {
	root := NewRootCmd(DefaultDeps())
	for _, path := range [][]string{{"help"}, {"completion"}, {"completion", "bash"}, {"completion", "zsh"},
		{"completion", "fish"}, {"completion", "powershell"}} {
		c, _, err := root.Find(path)
		if err != nil || c.Name() != path[len(path)-1] {
			t.Fatalf("%v: not found (%v)", path, err)
		}
		if strings.TrimSpace(c.Example) == "" {
			t.Errorf("%v: no example", path)
		}
	}
	for _, name := range []string{"party", "list", "discard"} {
		if c, _, _ := root.Find([]string{name}); c == nil || c.Name() != name {
			t.Errorf("%s is not registered", name)
		}
	}
}
