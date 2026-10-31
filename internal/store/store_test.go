package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fsafe"
	"github.com/lwasak/fv-cli/internal/numbering"
)

var fixedNow = time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)

func clock() time.Time { return fixedNow }

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func newStore(t *testing.T) *Store {
	t.Helper()
	return Open(filepath.Join(t.TempDir(), "data"), clock)
}

func reserve(t *testing.T, s *Store, env config.Env, issue time.Time) Reservation {
	t.Helper()
	r, err := s.Reserve(context.Background(), env, numbering.DefaultPattern, numbering.ResetMonthly, issue)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReserve_SequenceAndPeriods(t *testing.T) {
	s := newStore(t)
	want := []struct {
		issue time.Time
		num   string
	}{
		{day(2026, 10, 30), "FV/2026/10/001"},
		{day(2026, 10, 31), "FV/2026/10/002"},
		{day(2026, 11, 1), "FV/2026/11/001"},
		{day(2026, 10, 31), "FV/2026/10/003"}, // keyed by issue date, not by order
	}
	for _, w := range want {
		r := reserve(t, s, config.EnvTest, w.issue)
		if r.Number != w.num {
			t.Errorf("got %s, want %s", r.Number, w.num)
		}
		m, err := s.Load(config.EnvTest, r.Number)
		if err != nil {
			t.Fatal(err)
		}
		if m.State != StateReserved || m.Seq != r.Seq || m.IssueDate != w.issue.Format("2006-01-02") || len(m.History) != 1 {
			t.Errorf("meta = %+v", m)
		}
	}
}

func TestReserve_YearlyAndNeverReset(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a, _ := s.Reserve(ctx, config.EnvTest, "FV/{YYYY}/{SEQ}", numbering.ResetYearly, day(2026, 12, 31))
	b, _ := s.Reserve(ctx, config.EnvTest, "FV/{YYYY}/{SEQ}", numbering.ResetYearly, day(2027, 1, 1))
	c, err := s.Reserve(ctx, config.EnvTest, "N/{SEQ:3}", numbering.ResetNever, day(2027, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s.Reserve(ctx, config.EnvTest, "N/{SEQ:3}", numbering.ResetNever, day(2030, 5, 5))
	got := []string{a.Number, b.Number, c.Number, d.Number}
	want := []string{"FV/2026/1", "FV/2027/1", "N/001", "N/002"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", got, want)
	}
	if _, err := s.Reserve(ctx, config.EnvTest, "FV/{SEQ}", numbering.ResetMonthly, day(2027, 1, 1)); err == nil {
		t.Error("reset that does not fit the pattern must be refused")
	}
}

func TestReserve_CountersPerEnv(t *testing.T) {
	s := newStore(t)
	reserve(t, s, config.EnvTest, day(2026, 10, 1))
	reserve(t, s, config.EnvTest, day(2026, 10, 1))
	p := reserve(t, s, config.EnvProd, day(2026, 10, 1))
	if p.Number != "FV/2026/10/001" {
		t.Errorf("prod number = %s; test reservations must not touch prod", p.Number)
	}
	d := reserve(t, s, config.EnvDemo, day(2026, 10, 1))
	if d.Number != "FV/2026/10/001" {
		t.Errorf("demo number = %s", d.Number)
	}
	if next := reserve(t, s, config.EnvTest, day(2026, 10, 1)); next.Number != "FV/2026/10/003" {
		t.Errorf("test number = %s", next.Number)
	}
}

func TestReserve_RejectsBadEnv(t *testing.T) {
	s := newStore(t)
	if _, err := s.Reserve(context.Background(), config.Env("../x"), numbering.DefaultPattern, numbering.ResetMonthly, fixedNow); err == nil {
		t.Fatal("bad env accepted")
	}
}

func TestReserve_LayoutAndPermissions(t *testing.T) {
	s := newStore(t)
	r := reserve(t, s, config.EnvDemo, day(2026, 10, 5))
	want := filepath.Join(s.dataDir, "invoices", "demo", "2026", "FV_2026_10_001")
	if r.Dir != want {
		t.Fatalf("Dir = %s, want %s", r.Dir, want)
	}
	if _, err := os.Stat(filepath.Join(want, "meta.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dataDir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for p, mode := range map[string]os.FileMode{
		s.dataDir: 0o700, filepath.Join(s.dataDir, "invoices"): 0o700, want: 0o700,
		filepath.Join(want, "meta.json"):       0o600,
		filepath.Join(s.dataDir, "state.json"): 0o600,
		filepath.Join(s.dataDir, "state.lock"): 0o600,
	} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != mode {
			t.Errorf("%s mode = %o, want %o", p, fi.Mode().Perm(), mode)
		}
	}
	m := Meta{Currency: "PLN"}
	if err := s.SaveDraft(config.EnvDemo, r.Number, []byte("<x/>"), m); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(want, "invoice.xml"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("invoice.xml: %v %v", fi, err)
	}
}

func TestReserve_OrphanReservedIsTakenOver(t *testing.T) {
	s := newStore(t)
	r := reserve(t, s, config.EnvTest, day(2026, 10, 1))
	// Simulate a crash between the meta write and the counter write.
	if err := os.Remove(s.statePath()); err != nil {
		t.Fatal(err)
	}
	again := reserve(t, s, config.EnvTest, day(2026, 10, 1))
	if again.Number != r.Number {
		t.Errorf("got %s, want %s", again.Number, r.Number)
	}
}

func TestReserve_CollisionWithRealInvoiceRefused(t *testing.T) {
	s := newStore(t)
	r := reserve(t, s, config.EnvTest, day(2026, 10, 1))
	if err := s.SaveDraft(config.EnvTest, r.Number, []byte("<x/>"), Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.statePath()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(context.Background(), config.EnvTest, numbering.DefaultPattern, numbering.ResetMonthly, day(2026, 10, 1)); err == nil {
		t.Fatal("expected a collision error")
	}
}

func TestStore_MetaRoundTrip(t *testing.T) {
	s := newStore(t)
	r := reserve(t, s, config.EnvTest, day(2026, 10, 30))
	yes := true
	in := Meta{
		SaleDate: "2026-10-31", DueDate: "2026-11-13", Currency: "EUR", Gross: 12300,
		Seller:      PartySnapshot{NIP: "1234563218", Name: "Seller", CheckedAt: fixedNow},
		Buyer:       PartySnapshot{Alias: "acme", VatID: "DE123", RequestID: "abc"},
		Lines:       []LineSnapshot{{No: 1, Description: "Usługi", Unit: "usł.", Quantity: "1.5", UnitPrice: 8200, Net: 12300, Rate: "23"}},
		Totals:      []RateTotal{{Rate: "23", Net: 10000, VAT: 2300, Gross: 12300}},
		BankAccount: "PL61109010140000071219812874", BankOnWhiteList: &yes,
		Exchange: &Exchange{Currency: "EUR", Table: "211/A/NBP/2026", EffectiveDate: "2026-10-30", Mid: "4.2512", MidMicro: 4251200},
		Warnings: []string{"w"},
	}
	xml := []byte("<Faktura/>\n")
	if err := s.SaveDraft(config.EnvTest, r.Number, xml, in); err != nil {
		t.Fatal(err)
	}
	m, err := s.Load(config.EnvTest, r.Number)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != StateDraft || m.Number != r.Number || m.Seq != 1 || m.XMLSize != len(xml) || m.XMLSHA256 == "" {
		t.Errorf("meta = %+v", m)
	}
	if m.Exchange == nil || m.Exchange.MidMicro != 4251200 || m.BankOnWhiteList == nil || !*m.BankOnWhiteList ||
		m.Lines[0].Quantity != "1.5" || m.Buyer.RequestID != "abc" || !m.Seller.CheckedAt.Equal(fixedNow) {
		t.Errorf("round trip lost data: %+v", m)
	}
	if len(m.History) != 2 || m.History[1].From != StateReserved || m.History[1].To != StateDraft {
		t.Errorf("history = %+v", m.History)
	}
	got, err := s.XML(config.EnvTest, r.Number)
	if err != nil || string(got) != string(xml) {
		t.Errorf("XML = %q, %v", got, err)
	}
	// Re-saving a draft rewrites the XML and keeps history.
	if err := s.SaveDraft(config.EnvTest, r.Number, []byte("<Faktura>2</Faktura>"), in); err != nil {
		t.Fatal(err)
	}
	m2, _ := s.Load(config.EnvTest, r.Number)
	if len(m2.History) != 2 || m2.XMLSHA256 == m.XMLSHA256 {
		t.Errorf("re-save: %+v", m2)
	}
}

func TestStore_XMLHashMismatchDetected(t *testing.T) {
	s := newStore(t)
	r := reserve(t, s, config.EnvTest, fixedNow)
	if err := s.SaveDraft(config.EnvTest, r.Number, []byte("<a/>"), Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "invoice.xml"), []byte("<b/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.XML(config.EnvTest, r.Number); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err = %v, want ErrHashMismatch", err)
	}
}

func TestStore_LocateAcrossEnvs(t *testing.T) {
	s := newStore(t)
	a := reserve(t, s, config.EnvTest, day(2026, 10, 1)) // FV/2026/10/001 in test
	reserve(t, s, config.EnvDemo, day(2026, 10, 1))      // same number in demo
	reserve(t, s, config.EnvProd, day(2026, 10, 1))
	reserve(t, s, config.EnvProd, day(2026, 10, 1)) // 002 only in prod

	m, err := s.Locate(a.Number, config.EnvDemo)
	if err != nil || m.Env != config.EnvDemo {
		t.Fatalf("prefer demo: %v %v", m, err)
	}
	m, err = s.Locate("FV/2026/10/002", config.EnvTest)
	if err != nil || m.Env != config.EnvProd {
		t.Fatalf("fallback to prod: %v %v", m, err)
	}
	_, err = s.Locate(a.Number, "")
	if err == nil || !strings.Contains(err.Error(), "test, demo, prod") {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, err := s.Locate("FV/2026/10/099", config.EnvTest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// The sanitized folder name finds it too; traversal is refused.
	if _, err := s.Locate("FV_2026_10_002", config.EnvProd); err != nil {
		t.Errorf("sanitized form: %v", err)
	}
	for _, bad := range []string{"..", ".", ""} {
		if _, err := s.Locate(bad, config.EnvTest); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Locate(%q) = %v, want an invalid-number error", bad, err)
		}
	}
}

func TestTransition_Table(t *testing.T) {
	states := []State{StateReserved, StateDraft, StateSending, StateAccepted, StateRejected, StateUnknown, StateVoid}
	ok := map[string]bool{}
	for _, p := range []string{
		"reserved>draft", "reserved>void-draft", "draft>sending", "draft>void-draft",
		"sending>accepted", "sending>rejected", "sending>unknown", "sending>draft",
		"unknown>accepted", "unknown>rejected", "unknown>draft", "rejected>void-draft",
	} {
		ok[p] = true
	}
	for _, from := range states {
		for _, to := range states {
			if got, want := canTransition(from, to), ok[string(from)+">"+string(to)]; got != want {
				t.Errorf("%s -> %s: got %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestTransition_FlowHistoryAndImmutability(t *testing.T) {
	s := newStore(t)
	r := reserve(t, s, config.EnvTest, fixedNow)
	xml := []byte("<a/>")
	if err := s.SaveDraft(config.EnvTest, r.Number, xml, Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(config.EnvTest, r.Number, StateAccepted, nil); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("draft->accepted: %v", err)
	}
	m, err := s.Transition(config.EnvTest, r.Number, StateSending, func(m *Meta) error {
		m.KSeF = &KSeFRef{SessionRef: "S1"}
		return nil
	})
	if err != nil || m.State != StateSending || m.KSeF.SessionRef != "S1" {
		t.Fatalf("sending: %v %v", m, err)
	}
	if err := s.SaveDraft(config.EnvTest, r.Number, []byte("<changed/>"), Meta{}); !errors.Is(err, ErrImmutable) {
		t.Fatalf("SaveDraft at sending: %v", err)
	}
	if _, err := s.Transition(config.EnvTest, r.Number, StateUnknown, func(m *Meta) error {
		m.XMLSHA256 = "x"
		return nil
	}); !errors.Is(err, ErrImmutable) {
		t.Fatalf("hash change: %v", err)
	}
	failing := errors.New("boom")
	if _, err := s.Transition(config.EnvTest, r.Number, StateUnknown, func(*Meta) error { return failing }); !errors.Is(err, failing) {
		t.Fatalf("mutate error: %v", err)
	}
	if cur, _ := s.Load(config.EnvTest, r.Number); cur.State != StateSending {
		t.Fatalf("state changed by failed transition: %s", cur.State)
	}
	if _, err := s.Transition(config.EnvTest, r.Number, StateUnknown, nil); err != nil {
		t.Fatal(err)
	}
	m, err = s.Transition(config.EnvTest, r.Number, StateAccepted, func(m *Meta) error {
		m.KSeF.KSeFNumber = "123-2026"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var path []string
	for _, h := range m.History {
		path = append(path, string(h.To))
		if h.At.IsZero() {
			t.Error("history entry without time")
		}
	}
	if got := strings.Join(path, ">"); got != "reserved>draft>sending>unknown>accepted" {
		t.Errorf("history = %s", got)
	}
	if !m.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v", m.UpdatedAt)
	}
	for _, to := range []State{StateDraft, StateVoid, StateSending} {
		if _, err := s.Transition(config.EnvTest, r.Number, to, nil); !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("accepted->%s: %v", to, err)
		}
	}
	if err := s.SaveDraft(config.EnvTest, r.Number, xml, Meta{}); !errors.Is(err, ErrImmutable) {
		t.Errorf("SaveDraft at accepted: %v", err)
	}
	if err := s.WriteUPO(config.EnvTest, r.Number, []byte("<upo/>")); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Load(config.EnvTest, r.Number)
	if m.UPOSHA256 == "" {
		t.Error("UPO hash missing")
	}
	if b, err := os.ReadFile(filepath.Join(r.Dir, "upo.xml")); err != nil || string(b) != "<upo/>" {
		t.Errorf("upo.xml: %q %v", b, err)
	}
}

func TestTransition_SendingBackToDraftAllowsRewrite(t *testing.T) {
	s := newStore(t)
	r := reserve(t, s, config.EnvTest, fixedNow)
	_ = s.SaveDraft(config.EnvTest, r.Number, []byte("<a/>"), Meta{})
	_, _ = s.Transition(config.EnvTest, r.Number, StateSending, nil)
	if _, err := s.Transition(config.EnvTest, r.Number, StateDraft, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDraft(config.EnvTest, r.Number, []byte("<b/>"), Meta{}); err != nil {
		t.Fatalf("rewrite after proven-not-received: %v", err)
	}
}

func TestDiscard_LatestIsReleased(t *testing.T) {
	s := newStore(t)
	a := reserve(t, s, config.EnvTest, fixedNow)
	b := reserve(t, s, config.EnvTest, fixedNow)
	_ = s.SaveDraft(config.EnvTest, b.Number, []byte("<a/>"), Meta{})
	res, err := s.Discard(config.EnvTest, b.Number)
	if err != nil || res != Released {
		t.Fatalf("%v %v", res, err)
	}
	if _, err := os.Stat(b.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("folder still there: %v", err)
	}
	if _, err := s.Load(config.EnvTest, a.Number); err != nil {
		t.Error(err)
	}
	if c := reserve(t, s, config.EnvTest, fixedNow); c.Number != b.Number {
		t.Errorf("released number not reused: %s", c.Number)
	}
}

func TestDiscard_NotLatestIsVoided(t *testing.T) {
	s := newStore(t)
	a := reserve(t, s, config.EnvTest, fixedNow)
	_ = s.SaveDraft(config.EnvTest, a.Number, []byte("<a/>"), Meta{})
	b := reserve(t, s, config.EnvTest, fixedNow)
	res, err := s.Discard(config.EnvTest, a.Number)
	if err != nil || res != Voided {
		t.Fatalf("%v %v", res, err)
	}
	m, err := s.Load(config.EnvTest, a.Number)
	if err != nil || m.State != StateVoid || m.History[len(m.History)-1].From != StateDraft {
		t.Fatalf("%+v %v", m, err)
	}
	if c := reserve(t, s, config.EnvTest, fixedNow); c.Seq != b.Seq+1 {
		t.Errorf("counter must keep going: %d", c.Seq)
	}
	if _, err := s.Discard(config.EnvTest, a.Number); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("second discard: %v", err)
	}
}

func TestDiscard_DuplicateIsVoidedEvenWhenLatest(t *testing.T) {
	s := newStore(t)
	r := reserve(t, s, config.EnvTest, fixedNow)
	_ = s.SaveDraft(config.EnvTest, r.Number, []byte("<a/>"), Meta{})
	for _, to := range []State{StateSending, StateRejected} {
		if _, err := s.Transition(config.EnvTest, r.Number, to, func(m *Meta) error {
			m.KSeF = &KSeFRef{Duplicate: to == StateRejected, OriginalKSeFNumber: "orig"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.Discard(config.EnvTest, r.Number)
	if err != nil || res != Voided {
		t.Fatalf("%v %v", res, err)
	}
	if _, err := os.Stat(r.Dir); err != nil {
		t.Errorf("folder must stay: %v", err)
	}
	if c := reserve(t, s, config.EnvTest, fixedNow); c.Seq != r.Seq+1 {
		t.Errorf("the duplicate's number was handed out again: %s", c.Number)
	}
}

func TestDiscard_ReleasedOnlyPerPeriodCounter(t *testing.T) {
	s := newStore(t)
	oct := reserve(t, s, config.EnvTest, day(2026, 10, 31))
	reserve(t, s, config.EnvTest, day(2026, 11, 1)) // another counter
	res, err := s.Discard(config.EnvTest, oct.Number)
	if err != nil || res != Released {
		t.Fatalf("%v %v", res, err)
	}
}

func TestDiscard_RejectedAndRefusals(t *testing.T) {
	s := newStore(t)
	mk := func(to ...State) string {
		r := reserve(t, s, config.EnvTest, fixedNow)
		_ = s.SaveDraft(config.EnvTest, r.Number, []byte("<a/>"), Meta{})
		for _, st := range to {
			if _, err := s.Transition(config.EnvTest, r.Number, st, nil); err != nil {
				t.Fatal(err)
			}
		}
		return r.Number
	}
	for name, n := range map[string]string{
		"sending":  mk(StateSending),
		"unknown":  mk(StateSending, StateUnknown),
		"accepted": mk(StateSending, StateAccepted),
	} {
		if _, err := s.Discard(config.EnvTest, n); !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := s.Load(config.EnvTest, n); err != nil {
			t.Errorf("%s: folder must stay: %v", name, err)
		}
	}
	rej := mk(StateSending, StateRejected) // latest
	if res, err := s.Discard(config.EnvTest, rej); err != nil || res != Released {
		t.Errorf("rejected latest: %v %v", res, err)
	}
	if _, err := s.Discard(config.EnvTest, "FV/2026/10/099"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestList_FilterMonthEnv(t *testing.T) {
	s := newStore(t)
	reserve(t, s, config.EnvProd, day(2026, 10, 3))
	reserve(t, s, config.EnvTest, day(2026, 11, 3))
	reserve(t, s, config.EnvTest, day(2026, 10, 9))
	reserve(t, s, config.EnvTest, day(2026, 10, 2))
	reserve(t, s, config.EnvDemo, day(2027, 1, 2))
	nums := func(f Filter) string {
		ms, err := s.List(f)
		if err != nil {
			t.Fatal(err)
		}
		var o []string
		for _, m := range ms {
			o = append(o, string(m.Env)+":"+m.Number)
		}
		return strings.Join(o, " ")
	}
	// Sorted by issue date, then sequence: test 10/002 was issued on 10-02, before 10/001 on 10-09.
	if got, want := nums(Filter{}), "demo:FV/2027/01/001 prod:FV/2026/10/001 test:FV/2026/10/002 test:FV/2026/10/001 test:FV/2026/11/001"; got != want {
		t.Errorf("all = %s", got)
	}
	test := config.EnvTest
	if got := nums(Filter{Env: &test, Month: "2026-10"}); got != "test:FV/2026/10/002 test:FV/2026/10/001" {
		t.Errorf("test/2026-10 = %s", got)
	}
	if got := nums(Filter{Month: "2026-12"}); got != "" {
		t.Errorf("empty month = %q", got)
	}
	if got := nums(Filter{Month: "2027-01"}); got != "demo:FV/2027/01/001" {
		t.Errorf("2027-01 = %q", got)
	}
}

func TestList_SkipsCorruptMetaWithWarning(t *testing.T) {
	s := newStore(t)
	a := reserve(t, s, config.EnvTest, day(2026, 10, 2))
	b := reserve(t, s, config.EnvTest, day(2026, 10, 3))
	if err := os.WriteFile(filepath.Join(a.Dir, "meta.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	ms, warns, err := s.ListWithWarnings(Filter{})
	if err != nil || len(ms) != 1 || ms[0].Number != b.Number {
		t.Fatalf("list = %v %v", ms, err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "001") || !strings.Contains(warns[0], "corrupt") {
		t.Errorf("warnings = %v", warns)
	}
	if ms, err := s.List(Filter{}); err != nil || len(ms) != 1 {
		t.Errorf("List = %v %v", ms, err)
	}
}

func TestWhiteListCall_RollsOverDaily(t *testing.T) {
	s := newStore(t)
	for i := 1; i <= 90; i++ {
		n, err := s.WhiteListCall("2026-10-31", i%2 == 0)
		if err != nil || n != i {
			t.Fatalf("call %d: %d %v", i, n, err)
		}
	}
	if n, err := s.WhiteListCall("2026-10-31", false); !errors.Is(err, ErrQuota) || n != 90 {
		t.Fatalf("non-essential at 90: %d %v", n, err)
	}
	if n, err := s.WhiteListCall("2026-10-31", true); err != nil || n != 91 {
		t.Fatalf("essential at 90: %d %v", n, err)
	}
	if n, err := s.WhiteListCall("2026-11-01", false); err != nil || n != 1 {
		t.Fatalf("next day: %d %v", n, err)
	}
	// Counters and the White List counter live in the same file without disturbing each other.
	r := reserve(t, s, config.EnvTest, fixedNow)
	if r.Seq != 1 {
		t.Errorf("seq = %d", r.Seq)
	}
	if n, _ := s.WhiteListCall("2026-11-01", true); n != 2 {
		t.Errorf("calls = %d", n)
	}
}

func TestLock_TimeoutErrorSurfaced(t *testing.T) {
	s := newStore(t)
	s.lockTimeout = 100 * time.Millisecond
	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	unlock, err := fsafe.Lock(filepath.Join(s.dataDir, "state.lock"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, err = s.Reserve(context.Background(), config.EnvTest, numbering.DefaultPattern, numbering.ResetMonthly, fixedNow)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("err = %v", err)
	}
}

func TestLock_CancelledContextEndsTheWait(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	unlock, err := fsafe.Lock(filepath.Join(s.dataDir, "state.lock"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err2 := s.WithContext(ctx).Discard(config.EnvTest, "FV/2026/10/001")
	if time.Since(start) > 3*time.Second {
		t.Errorf("did not stop waiting")
	}
	if err2 == nil || !errors.Is(err2, context.Canceled) {
		t.Errorf("discard err = %v", err2)
	}
}

func TestReserve_ConcurrentStoresInOneProcess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	const n = 50
	var wg sync.WaitGroup
	nums := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := Open(dir, clock)
			r, err := s.Reserve(context.Background(), config.EnvTest, numbering.DefaultPattern, numbering.ResetMonthly, day(2026, 10, 31))
			nums[i], errs[i] = r.Number, err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	checkContiguous(t, nums, n)
}

func checkContiguous(t *testing.T, nums []string, n int) {
	t.Helper()
	if len(nums) != n {
		t.Fatalf("got %d numbers, want %d", len(nums), n)
	}
	seqs := make([]int, 0, n)
	seen := map[string]bool{}
	for _, num := range nums {
		if seen[num] {
			t.Fatalf("duplicate number %s", num)
		}
		seen[num] = true
		v, err := strconv.Atoi(num[strings.LastIndex(num, "/")+1:])
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, v)
	}
	sort.Ints(seqs)
	for i, v := range seqs {
		if v != i+1 {
			t.Fatalf("sequence not contiguous at %d: %v", i, seqs)
		}
	}
}

func TestReserve_ConcurrentProcesses(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "data")
	const procs, each = 8, 20
	type result struct {
		out []byte
		err error
	}
	results := make([]result, procs)
	var wg sync.WaitGroup
	for i := 0; i < procs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(exe, "-test.run=^TestHelperProcess$")
			cmd.Env = append(os.Environ(), "FV_STORE_HELPER=1", "FV_STORE_DIR="+dir, "FV_STORE_COUNT="+strconv.Itoa(each))
			out, err := cmd.CombinedOutput()
			results[i] = result{out, err}
		}()
	}
	wg.Wait()
	var nums []string
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("helper %d: %v\n%s", i, r.err, r.out)
		}
		sc := bufio.NewScanner(strings.NewReader(string(r.out)))
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "NUMBER "); ok {
				nums = append(nums, v)
			}
		}
	}
	checkContiguous(t, nums, procs*each)
}

// TestHelperProcess is not a test: it is the body of the subprocesses started by
// TestReserve_ConcurrentProcesses.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("FV_STORE_HELPER") != "1" {
		t.Skip("helper process only")
	}
	count, _ := strconv.Atoi(os.Getenv("FV_STORE_COUNT"))
	s := Open(os.Getenv("FV_STORE_DIR"), clock)
	for i := 0; i < count; i++ {
		r, err := s.Reserve(context.Background(), config.EnvTest, numbering.DefaultPattern, numbering.ResetMonthly, day(2026, 10, 31))
		if err != nil {
			fmt.Fprintln(os.Stderr, "reserve:", err)
			os.Exit(2)
		}
		fmt.Println("NUMBER", r.Number)
	}
}

func TestUpdateKSeF_OnlyWhileSendingOrUnknownKeepsHistory(t *testing.T) {
	s := newStore(t)
	r := reserve(t, s, config.EnvTest, fixedNow)
	if err := s.SaveDraft(config.EnvTest, r.Number, []byte("<a/>"), Meta{}); err != nil {
		t.Fatal(err)
	}
	set := func(m *KSeFRef) error { m.InvoiceRef = "INV-1"; return nil }
	if _, err := s.UpdateKSeF(config.EnvTest, r.Number, set); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("draft: %v", err)
	}
	if _, err := s.Transition(config.EnvTest, r.Number, StateSending, func(m *Meta) error {
		m.KSeF = &KSeFRef{SessionRef: "S1"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m, err := s.UpdateKSeF(config.EnvTest, r.Number, set)
	if err != nil || m.State != StateSending || m.KSeF.InvoiceRef != "INV-1" || m.KSeF.SessionRef != "S1" {
		t.Fatalf("update: %+v %v", m, err)
	}
	cur, _ := s.Load(config.EnvTest, r.Number)
	if cur.KSeF.InvoiceRef != "INV-1" || len(cur.History) != len(m.History) || cur.History[len(cur.History)-1].To != StateSending {
		t.Errorf("stored = %+v", cur)
	}
	if _, err := s.Transition(config.EnvTest, r.Number, StateRejected, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateKSeF(config.EnvTest, r.Number, set); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("rejected: %v", err)
	}
}
