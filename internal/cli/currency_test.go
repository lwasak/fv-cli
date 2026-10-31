package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fa3"
	"github.com/lwasak/fv-cli/internal/store"
)

const nbpTestdata = "../nbp/testdata/"

// fakeNBP serves recorded NBP answers by date (others 404) and records the request paths.
type fakeNBP struct {
	mu    sync.Mutex
	paths []string
	byDay map[string]string
	code  int // when non-zero, every request gets this status
}

func (f *fakeNBP) attach(t *testing.T, fx *issueFx) {
	t.Helper()
	notFound, err := os.ReadFile(nbpTestdata + "404.txt")
	if err != nil {
		t.Fatal(err)
	}
	fx.nbp = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		if f.code != 0 {
			http.Error(w, "boom", f.code)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if file, ok := f.byDay[parts[len(parts)-1]]; ok {
			b, err := os.ReadFile(nbpTestdata + file)
			if err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(notFound)
	}))
	t.Cleanup(fx.nbp.Close)
}

func (f *fakeNBP) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func TestIssue_EURMatchesGoldenShape(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	nbpFake := &fakeNBP{byDay: map[string]string{"2026-10-30": "eur_200.json"}}
	nbpFake.attach(t, fx)

	code, out, errs := fx.run("issue", "--to", "acme", "--net", "1000", "--currency", "EUR")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := nbpFake.requests(); len(got) != 1 || !strings.Contains(got[0], "/rates/A/EUR/2026-10-30") {
		t.Errorf("NBP requests = %v, want one for 2026-10-30", got)
	}
	if !strings.Contains(out, "Currency:  EUR, rate 4.2512 PLN (NBP 211/A/NBP/2026, 2026-10-30)") {
		t.Errorf("preview lacks the rate line:\n%s", out)
	}
	if !strings.Contains(out, "VAT PLN") || !strings.Contains(out, "977,78") {
		t.Errorf("preview lacks the PLN VAT (230,00 EUR x 4.2512 = 977,78):\n%s", out)
	}

	m, err := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if err != nil {
		t.Fatal(err)
	}
	want := store.Exchange{Currency: "EUR", Table: "211/A/NBP/2026", EffectiveDate: "2026-10-30", Mid: "4.2512", MidMicro: 4251200}
	if m.Exchange == nil || *m.Exchange != want {
		t.Fatalf("meta.Exchange = %+v, want %+v", m.Exchange, want)
	}
	if m.Currency != "EUR" || len(m.Totals) != 1 || m.Totals[0].VATPLN == nil || *m.Totals[0].VATPLN != 97778 {
		t.Errorf("meta totals: %+v currency %s", m.Totals, m.Currency)
	}

	xml := fx.xmlOf(config.EnvTest, m.Number)
	for _, frag := range []string{
		"<KodWaluty>EUR</KodWaluty>", "<P_14_1>230.00</P_14_1>", "<P_14_1W>977.78</P_14_1W>",
		"<KursWaluty>4.2512</KursWaluty>", "<Klucz>Kurs waluty</Klucz>",
		"<Wartosc>1 EUR = 4.2512 PLN, tabela 211/A/NBP/2026 z 2026-10-30</Wartosc>",
	} {
		if !strings.Contains(string(xml), frag) {
			t.Errorf("XML lacks %s:\n%s", frag, xml)
		}
	}
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Log("xmllint not on PATH: schema validation skipped")
	} else if err := fa3.ValidateWithXmllint(context.Background(), xml, ""); err != nil {
		t.Errorf("EUR invoice does not validate: %v", err)
	}

	// fv show prints the same facts from the archive.
	code, shown, errs := fx.run("show", m.Number)
	if code != 0 || !strings.Contains(shown, "rate 4.2512 PLN (NBP 211/A/NBP/2026, 2026-10-30)") || !strings.Contains(shown, "977,78") {
		t.Errorf("show: exit %d %s\n%s", code, errs, shown)
	}
}

func TestIssue_EURHolidayWalkBack(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	fx.now = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	nbpFake := &fakeNBP{byDay: map[string]string{"2026-11-10": "eur_nov10_200.json"}}
	nbpFake.attach(t, fx)

	code, out, errs := fx.run("issue", "--to", "acme", "--net", "1000", "--currency", "EUR")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	got := nbpFake.requests()
	if len(got) != 2 || !strings.HasSuffix(got[0], "/2026-11-11/") || !strings.HasSuffix(got[1], "/2026-11-10/") {
		t.Errorf("NBP requests = %v, want 11-11 (holiday, 404) then 11-10", got)
	}
	if !strings.Contains(out, "rate 4.2340 PLN (NBP 217/A/NBP/2026, 2026-11-10)") || !strings.Contains(out, "973,82") {
		t.Errorf("preview:\n%s", out)
	}
}

func TestIssue_PLNMakesNoNBPRequest(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	nbpFake := &fakeNBP{byDay: map[string]string{}}
	nbpFake.attach(t, fx)
	for _, args := range [][]string{{}, {"--currency", "PLN"}} {
		if code, _, errs := fx.run(append([]string{"issue", "--to", "acme", "--net", "1000"}, args...)...); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errs)
		}
	}
	if got := nbpFake.requests(); len(got) != 0 {
		t.Errorf("PLN invoices made NBP requests: %v", got)
	}
	m, err := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if err != nil {
		t.Fatal(err)
	}
	if m.Exchange != nil || m.Totals[0].VATPLN != nil {
		t.Errorf("PLN meta carries exchange data: %+v", m)
	}
}

func TestIssue_PartyCurrencySetting(t *testing.T) {
	cfg := strings.Replace(issueConfig, "[parties.acme]\n", "[parties.acme]\ncurrency = \"EUR\"\n", 1)
	fx := newIssueFx(t, cfg)
	nbpFake := &fakeNBP{byDay: map[string]string{"2026-10-30": "eur_200.json"}}
	nbpFake.attach(t, fx)
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "1000"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	m, err := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if err != nil {
		t.Fatal(err)
	}
	if m.Currency != "EUR" || m.Exchange == nil {
		t.Errorf("party currency not applied: %+v", m)
	}
	// The flag overrides the party setting.
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "1000", "--currency", "PLN"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if m2, err := fx.store().Load(config.EnvTest, "FV/2026/10/002"); err != nil || m2.Currency != "PLN" || m2.Exchange != nil {
		t.Errorf("flag must win: %+v %v", m2, err)
	}
}

func TestIssue_ForeignCurrencyErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		nbp   *fakeNBP
		now   time.Time
		code  int
		wants string
	}{
		{"np", []string{"--currency", "EUR", "--rate", "np"}, &fakeNBP{}, time.Time{}, 1, "reverse charge"},
		{"unknown currency", []string{"--currency", "XXX"}, &fakeNBP{code: 400}, time.Time{}, 1, "no such currency"},
		{"no rate in 10 days", []string{"--currency", "EUR"}, &fakeNBP{byDay: map[string]string{}}, time.Time{}, 1, "no NBP rate found"},
		{"not yet published", []string{"--currency", "EUR", "--issue-date", "2026-11-20"}, &fakeNBP{byDay: map[string]string{}}, time.Time{}, 1, "not yet published"},
		{"network failure", []string{"--currency", "EUR"}, &fakeNBP{code: 500}, time.Time{}, 2, "NBP exchange rate lookup failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newIssueFx(t, issueConfig)
			tc.nbp.attach(t, fx)
			code, _, errs := fx.run(append([]string{"issue", "--to", "acme", "--net", "100"}, tc.args...)...)
			if code != tc.code || !strings.Contains(errs, tc.wants) {
				t.Errorf("exit %d, stderr %q (want %d and %q)", code, errs, tc.code, tc.wants)
			}
			if _, out, _ := fx.run("list"); strings.TrimSpace(out) != "no invoices" {
				t.Errorf("a number was reserved: %q", out)
			}
		})
	}
}

func TestExchangeFor_PLNIsNil(t *testing.T) {
	s := &session{deps: Deps{Now: time.Now}}
	for _, c := range []string{"", "PLN"} {
		if x, err := s.exchangeFor(context.Background(), nil, c, time.Now(), time.Time{}); x != nil || err != nil {
			t.Errorf("%q: %v %v", c, x, err)
		}
	}
}
