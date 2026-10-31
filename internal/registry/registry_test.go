package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lwasak/fv-cli/internal/fsafe"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/exitcode"
)

const (
	sellerNIP = "5260250274"
	buyerNIP  = "5252248481"
	soleNIP   = "7740001454"
)

var t0 = time.Date(2026, 10, 31, 10, 0, 0, 0, Warsaw())

func testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeServer stands in for both registries behind one TLS server.
type fakeServer struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	wlHits   []string // "nip?date"
	viesReqs []viesRequest
	wlStatus int
	wlFile   map[string]string // nip -> testdata file
	wlBody   string            // overrides wlFile when set
	viesFile string
	viesCode int
	drop     bool
}

func newFake(t *testing.T) *fakeServer {
	f := &fakeServer{t: t, wlStatus: 200, viesCode: 200, wlFile: map[string]string{
		buyerNIP: "wl_working.json", soleNIP: "wl_residence.json", sellerNIP: "wl_seller.json",
	}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.drop {
		hj, _ := w.(http.Hijacker)
		c, _, _ := hj.Hijack()
		_ = c.Close()
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/search/nip/"):
		nip := strings.TrimPrefix(r.URL.Path, "/api/search/nip/")
		f.wlHits = append(f.wlHits, nip+"?"+r.URL.Query().Get("date"))
		if f.wlStatus != 200 {
			w.WriteHeader(f.wlStatus)
			_, _ = io.WriteString(w, f.wlBody)
			return
		}
		body := f.wlBody
		if body == "" {
			body = testdata(f.t, f.wlFile[nip])
		}
		_, _ = io.WriteString(w, body)
	case r.URL.Path == "/check-vat-number":
		var req viesRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.viesReqs = append(f.viesReqs, req)
		w.WriteHeader(f.viesCode)
		_, _ = io.WriteString(w, testdata(f.t, f.viesFile))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeServer) wlCalls() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.wlHits) }

func (f *fakeServer) endpoints() Endpoints {
	return Endpoints{WhiteList: f.srv.URL, VIES: f.srv.URL, Client: f.srv.Client()}
}

type fakeCounter struct {
	mu    sync.Mutex
	days  map[string]int
	calls []bool // essential flags
}

func (c *fakeCounter) WhiteListCall(day string, essential bool) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.days == nil {
		c.days = map[string]int{}
	}
	if !essential && c.days[day] >= DailyLimit {
		return c.days[day], fmt.Errorf("store: %w", ErrQuota)
	}
	c.days[day]++
	c.calls = append(c.calls, essential)
	return c.days[day], nil
}

type env struct {
	f   *fakeServer
	c   *fakeCounter
	r   *Resolver
	dir string
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{f: newFake(t), c: &fakeCounter{}, dir: t.TempDir(), now: t0}
	e.r = NewResolver(e.dir, e.f.endpoints(), e.c, 30*24*time.Hour, func() time.Time { return e.now }, nil, sellerNIP)
	return e
}

func nipParty(nip string) config.Party { return config.Party{NIP: nip} }

func TestWhiteList_WorkingAddressPreferred(t *testing.T) {
	e := newEnv(t)
	p, err := e.r.Resolve(context.Background(), "acme", nipParty(buyerNIP), t0, PurposeInvoiceBuyer)
	if err != nil {
		t.Fatal(err)
	}
	if p.Address != "ul. Prosta 51, 00-838 Warszawa" || p.StatusVat != "Czynny" || p.RequestID != "a1b2c-d3e4f5g" {
		t.Errorf("party = %+v", p)
	}
	if !strings.HasPrefix(p.Name, "ACME SPÓŁKA") || p.Provenance["name"] != SourceWhiteList || p.Provenance["accounts"] != SourceWhiteList {
		t.Errorf("name/provenance = %q %v", p.Name, p.Provenance)
	}
	if len(p.AccountNumbers) != 2 || p.AccountNumbers[0] != "61109010140000071219812874" {
		t.Errorf("accounts = %v", p.AccountNumbers)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("unexpected warnings %v", p.Warnings)
	}
}

func TestWhiteList_ResidenceFallback(t *testing.T) {
	e := newEnv(t)
	p, err := e.r.Resolve(context.Background(), "jan", nipParty(soleNIP), t0, PurposeInvoiceBuyer)
	if err != nil {
		t.Fatal(err)
	}
	if p.Address != "ul. Długa 1/2, 80-001 Gdańsk" {
		t.Errorf("address = %q", p.Address)
	}
	if !strings.Contains(strings.Join(p.Warnings, "|"), "Zwolniony") {
		t.Errorf("warnings = %v", p.Warnings)
	}
}

func TestWhiteList_DateCappedAtToday(t *testing.T) {
	e := newEnv(t)
	future := time.Date(2026, 11, 15, 0, 0, 0, 0, Warsaw())
	past := time.Date(2026, 10, 1, 0, 0, 0, 0, Warsaw())
	if _, err := e.r.Resolve(context.Background(), "acme", nipParty(buyerNIP), future, PurposeAdd); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Resolve(context.Background(), "acme", nipParty(buyerNIP), past, PurposeAdd); err != nil {
		t.Fatal(err)
	}
	want := []string{buyerNIP + "?2026-10-31", buyerNIP + "?2026-10-01"}
	if fmt.Sprint(e.f.wlHits) != fmt.Sprint(want) {
		t.Errorf("hits = %v, want %v", e.f.wlHits, want)
	}
}

func TestWhiteList_SubjectNullIsNotRegistered(t *testing.T) {
	e := newEnv(t)
	e.f.wlFile[buyerNIP] = "wl_subject_null.json"
	_, err := e.r.Resolve(context.Background(), "acme", nipParty(buyerNIP), t0, PurposeAdd)
	if !errors.Is(err, ErrNotRegistered) || exitcode.Code(err) != 2 {
		t.Fatalf("err = %v, code %d", err, exitcode.Code(err))
	}
}

// D1: a NIP missing from the White List is invoiceable when the config gives name and address.
func TestWhiteList_NotRegisteredWithOverridesResolves(t *testing.T) {
	e := newEnv(t)
	e.f.wlFile[buyerNIP] = "wl_subject_null.json"
	p, err := e.r.Resolve(context.Background(), "fund",
		config.Party{NIP: buyerNIP, Name: "Fundacja X", Address: "ul. A 1, 00-001 Warszawa"}, t0, PurposeInvoiceBuyer)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if p.Name != "Fundacja X" || p.Address != "ul. A 1, 00-001 Warszawa" || p.StatusVat != StatusNotRegistered {
		t.Errorf("party = %+v", p)
	}
	for _, f := range []string{"name", "address", "statusVat"} {
		if p.Provenance[f] != SourceConfig {
			t.Errorf("provenance[%s] = %q, want config", f, p.Provenance[f])
		}
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "not on the VAT White List") {
		t.Errorf("warnings = %v", p.Warnings)
	}
	if p.RequestID != "n0n0n-n0n0n0n" {
		t.Errorf("requestId = %q", p.RequestID)
	}
	if _, ok := e.r.Cached(CacheKey(buyerNIP, "")); ok {
		t.Error("a not-registered answer must not be cached as a registry entry")
	}
}

// Without both overrides the White List's answer stays final (exit 2).
func TestWhiteList_NotRegisteredNeedsBothOverrides(t *testing.T) {
	for name, p := range map[string]config.Party{
		"none":      {NIP: buyerNIP},
		"name only": {NIP: buyerNIP, Name: "Fundacja X"},
		"addr only": {NIP: buyerNIP, Address: "ul. A 1, 00-001 Warszawa"},
	} {
		e := newEnv(t)
		e.f.wlFile[buyerNIP] = "wl_subject_null.json"
		_, err := e.r.Resolve(context.Background(), "fund", p, t0, PurposeInvoiceBuyer)
		if !errors.Is(err, ErrNotRegistered) || exitcode.Code(err) != 2 {
			t.Errorf("%s: err = %v, code %d", name, err, exitcode.Code(err))
		}
	}
}

func TestWhiteList_RefusalIsExit2NotStale(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeAdd); err != nil {
		t.Fatal(err)
	}
	e.f.wlStatus, e.f.wlBody = 400, testdata(t, "wl_error_date_future.json")
	e.now = t0.Add(48 * time.Hour)
	_, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), e.now, PurposeInvoiceBuyer)
	if exitcode.Code(err) != 2 || !strings.Contains(err.Error(), "WL-103") {
		t.Fatalf("err = %v", err)
	}
}

func TestWhiteList_PESELNotStored(t *testing.T) {
	e := newEnv(t)
	if _, err := e.r.Resolve(context.Background(), "jan", nipParty(soleNIP), t0, PurposeAdd); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(e.dir, "parties.json"))
	if strings.Contains(string(b), "44051401359") || strings.Contains(string(b), "pesel") {
		t.Errorf("parties.json holds the PESEL:\n%s", b)
	}
}

func TestVIES_RequesterAndConsultationStored(t *testing.T) {
	e := newEnv(t)
	e.f.viesFile = "vies_de.json"
	p, err := e.r.Resolve(context.Background(), "de", config.Party{VatID: "de 123456789"}, t0, PurposeAdd)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.f.viesReqs) != 1 {
		t.Fatal("no VIES request")
	}
	rq := e.f.viesReqs[0]
	if rq.CountryCode != "DE" || rq.VatNumber != "123456789" || rq.RequesterMemberStateCode != "PL" || rq.RequesterNumber != sellerNIP {
		t.Errorf("request = %+v", rq)
	}
	if p.ConsultationNumber != "WAPIAAAAZ1b2c3d4" || p.Name != "Beispiel GmbH" || p.Address != "Hauptstrasse 1, 10115 BERLIN" {
		t.Errorf("party = %+v", p)
	}
	if p.EUCode != "DE" || p.VatNumber != "123456789" || p.CountryCode != "DE" || p.Provenance["name"] != SourceVIES {
		t.Errorf("party = %+v", p)
	}
	if e.c.days[t0.Format("2006-01-02")] != 0 {
		t.Error("VIES call was counted as a White List call")
	}
	if ce, ok := e.r.Cached("vat:DE123456789"); !ok || ce.ConsultationNumber != "WAPIAAAAZ1b2c3d4" || len(ce.Raw) == 0 {
		t.Errorf("cache entry = %+v", ce)
	}
}

func TestVIES_GreeceELtoGR(t *testing.T) {
	e := newEnv(t)
	e.f.viesFile = "vies_gr.json"
	p, err := e.r.Resolve(context.Background(), "gr", config.Party{VatID: "EL123456783"}, t0, PurposeAdd)
	if err != nil {
		t.Fatal(err)
	}
	if p.EUCode != "EL" || p.CountryCode != "GR" || p.VatNumber != "123456783" {
		t.Errorf("party = %+v", p)
	}
	if e.f.viesReqs[0].CountryCode != "EL" {
		t.Errorf("VIES got %q", e.f.viesReqs[0].CountryCode)
	}
	_, err = e.r.Resolve(context.Background(), "gr", config.Party{VatID: "GR123456783"}, t0, PurposeAdd)
	if err == nil || !strings.Contains(err.Error(), "EL") {
		t.Errorf("GR prefix err = %v", err)
	}
}

func TestVIES_DashNameNeedsOverride(t *testing.T) {
	e := newEnv(t)
	e.f.viesFile = "vies_dash.json"
	ctx := context.Background()
	_, err := e.r.Resolve(ctx, "de2", config.Party{VatID: "DE987654321"}, t0, PurposeInvoiceBuyer)
	if exitcode.Code(err) != 2 || !strings.Contains(err.Error(), "parties.de2.name") {
		t.Fatalf("err = %v", err)
	}
	p, err := e.r.Resolve(ctx, "de2", config.Party{VatID: "DE987654321", Name: "Muster AG", Address: "Weg 2, 80331 München"}, t0, PurposeInvoiceBuyer)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Muster AG" || p.Provenance["name"] != SourceConfig || p.Provenance["address"] != SourceConfig {
		t.Errorf("party = %+v", p)
	}
}

func TestVIES_InvalidAndErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.f.viesFile = "vies_invalid.json"
	if _, err := e.r.Resolve(ctx, "x", config.Party{VatID: "DE000000000"}, t0, PurposeAdd); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("invalid: %v", err)
	}
	e.f.viesFile, e.f.viesCode = "vies_error_input.json", 400
	if _, err := e.r.Resolve(ctx, "x", config.Party{VatID: "DE000000000"}, t0, PurposeAdd); exitcode.Code(err) != 2 || errors.Is(err, errUnavailable) {
		t.Errorf("input error: %v", err)
	}
	e.f.viesFile, e.f.viesCode = "vies_error_unavailable.json", 200
	if _, err := e.r.Resolve(ctx, "x", config.Party{VatID: "DE000000000"}, t0, PurposeAdd); !errors.Is(err, errUnavailable) || exitcode.Code(err) != 2 {
		t.Errorf("unavailable, no cache: %v", err)
	}
}

func TestCache_TTLRefresh(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	seller := config.Seller{NIP: sellerNIP}
	if _, err := e.r.ResolveSeller(ctx, seller, t0); err != nil {
		t.Fatal(err)
	}
	e.now = t0.Add(29 * 24 * time.Hour)
	if _, err := e.r.ResolveSeller(ctx, seller, e.now); err != nil {
		t.Fatal(err)
	}
	if e.f.wlCalls() != 1 {
		t.Fatalf("within TTL: %d calls, want 1", e.f.wlCalls())
	}
	e.now = t0.Add(31 * 24 * time.Hour)
	p, err := e.r.ResolveSeller(ctx, seller, e.now)
	if err != nil {
		t.Fatal(err)
	}
	if e.f.wlCalls() != 2 || p.Stale {
		t.Fatalf("after TTL: %d calls, stale=%v", e.f.wlCalls(), p.Stale)
	}
	if p.Address != "ul. Świętojańska 10/3, 81-368 Gdynia" {
		t.Errorf("seller address = %q (working address expected)", p.Address)
	}
}

func TestCache_Buyer24hRecheck(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeInvoiceBuyer); err != nil {
		t.Fatal(err)
	}
	e.now = t0.Add(23 * time.Hour)
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeInvoiceBuyer); err != nil {
		t.Fatal(err)
	}
	if e.f.wlCalls() != 1 {
		t.Fatalf("within 24h: %d calls", e.f.wlCalls())
	}
	// another query date forces a re-fetch even within 24 h
	other := t0.AddDate(0, 0, -3)
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), other, PurposeInvoiceBuyer); err != nil {
		t.Fatal(err)
	}
	if e.f.wlCalls() != 2 {
		t.Fatalf("other query date: %d calls", e.f.wlCalls())
	}
	e.now = t0.Add(23*time.Hour + 25*time.Hour)
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), other, PurposeInvoiceBuyer); err != nil {
		t.Fatal(err)
	}
	if e.f.wlCalls() != 3 {
		t.Fatalf("older than 24h: %d calls", e.f.wlCalls())
	}
}

func TestCache_ShowReadsCacheOnlyAndFlagsStale(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeShow); err != nil { // no entry: fetches
		t.Fatal(err)
	}
	e.now = t0.Add(40 * 24 * time.Hour)
	p, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), e.now, PurposeShow)
	if err != nil {
		t.Fatal(err)
	}
	if e.f.wlCalls() != 1 || !p.Stale {
		t.Errorf("calls=%d stale=%v", e.f.wlCalls(), p.Stale)
	}
}

func TestCache_StaleOnNetworkError(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeInvoiceBuyer); err != nil {
		t.Fatal(err)
	}
	e.f.drop = true
	e.now = t0.Add(3 * 24 * time.Hour)
	p, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), e.now, PurposeInvoiceBuyer)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Stale || len(p.Warnings) == 0 || !strings.Contains(p.Warnings[0], "3 d ago") || !strings.Contains(p.Warnings[0], "2026-10-31 10:00") {
		t.Errorf("stale=%v warnings=%v", p.Stale, p.Warnings)
	}
	if p.Name == "" || p.RequestID != "a1b2c-d3e4f5g" {
		t.Errorf("party = %+v", p)
	}
}

func TestCache_StaleOnQuota429(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeInvoiceBuyer); err != nil {
		t.Fatal(err)
	}
	e.f.wlStatus, e.f.wlBody = 429, `{"code":"WL-190","message":"too many requests"}`
	e.now = t0.Add(2 * time.Hour * 24)
	p, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), e.now, PurposeInvoiceBuyer)
	if err != nil || !p.Stale || len(p.Warnings) == 0 {
		t.Fatalf("p.Stale=%v warnings=%v err=%v", p.Stale, p.Warnings, err)
	}
	// 5xx behaves the same
	e.f.wlStatus = 503
	p, err = e.r.Resolve(ctx, "acme", nipParty(buyerNIP), e.now, PurposeInvoiceBuyer)
	if err != nil || !p.Stale {
		t.Fatalf("5xx: stale=%v err=%v", p.Stale, err)
	}
}

func TestCache_NoCacheExit2(t *testing.T) {
	e := newEnv(t)
	e.f.drop = true
	_, err := e.r.Resolve(context.Background(), "acme", nipParty(buyerNIP), t0, PurposeInvoiceBuyer)
	if err == nil || exitcode.Code(err) != 2 || !strings.Contains(err.Error(), "no cached copy") {
		t.Fatalf("err = %v (code %d)", err, exitcode.Code(err))
	}
}

func TestCache_RefreshAndAddDoNotUseStale(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeAdd); err != nil {
		t.Fatal(err)
	}
	e.f.drop = true
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeRefresh); exitcode.Code(err) != 2 {
		t.Fatalf("refresh err = %v", err)
	}
}

func TestCache_FileIsPrivateAndHasAllFields(t *testing.T) {
	e := newEnv(t)
	if _, err := e.r.Resolve(context.Background(), "acme", nipParty(buyerNIP), t0, PurposeAdd); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.dir, "parties.json")
	// CheckPrivate checks POSIX modes on unix and the ACL on Windows.
	if err := fsafe.CheckPrivate(path); err != nil {
		t.Errorf("parties.json is not user-only: %v", err)
	}
	ce, ok := e.r.Cached(CacheKey(buyerNIP, ""))
	if !ok {
		t.Fatal("no entry")
	}
	var raw map[string]any
	if err := json.Unmarshal(ce.Raw, &raw); err != nil || raw["result"] == nil {
		t.Errorf("raw = %s (%v)", ce.Raw, err)
	}
	if ce.RequestID != "a1b2c-d3e4f5g" || !ce.FetchedAt.Equal(t0) || ce.QueryDate != "2026-10-31" || ce.Party.StatusVat != "Czynny" || ce.Source != SourceWhiteList {
		t.Errorf("entry = %+v", ce)
	}
}

func TestQuota_RefreshRefusedAt90_InvoiceAllowed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	day := t0.Format("2006-01-02")
	e.c.days = map[string]int{day: 90}
	_, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeRefresh)
	if !errors.Is(err, ErrQuota) || exitcode.Code(err) != 2 || !strings.Contains(err.Error(), "90") {
		t.Fatalf("refresh err = %v", err)
	}
	if e.f.wlCalls() != 0 {
		t.Error("HTTP call made after quota refusal")
	}
	if _, err := e.r.Resolve(ctx, "acme", nipParty(buyerNIP), t0, PurposeInvoiceBuyer); err != nil {
		t.Fatalf("invoice-time lookup refused: %v", err)
	}
	if e.c.calls[len(e.c.calls)-1] != true || e.c.days[day] != 91 {
		t.Errorf("counter calls = %v days=%v", e.c.calls, e.c.days)
	}
}

func TestQuota_CountedBeforeCall(t *testing.T) {
	e := newEnv(t)
	e.f.drop = true // the call fails, but the counter was already incremented
	_, _ = e.r.Resolve(context.Background(), "acme", nipParty(buyerNIP), t0, PurposeAdd)
	if e.c.days[t0.Format("2006-01-02")] != 1 {
		t.Errorf("days = %v", e.c.days)
	}
}

func TestWarnings_StatusNotCzynny(t *testing.T) {
	e := newEnv(t)
	e.f.wlBody = strings.Replace(testdata(t, "wl_working.json"), `"Czynny"`, `"Niezarejestrowany"`, 1)
	p, err := e.r.Resolve(context.Background(), "acme", nipParty(buyerNIP), t0, PurposeInvoiceBuyer)
	if err != nil {
		t.Fatalf("a status warning must not fail: %v", err)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "not Czynny") {
		t.Errorf("warnings = %v", p.Warnings)
	}
}

func TestWarnings_AccountNotOnWhiteList(t *testing.T) {
	e := newEnv(t)
	seller, err := e.r.ResolveSeller(context.Background(), config.Seller{NIP: sellerNIP}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if found, known := AccountOnWhiteList(seller, "PL61 1090 1014 0000 0712 1981 2874"); !found || !known {
		t.Errorf("listed account: found=%v known=%v", found, known)
	}
	if w := AccountWarning(seller, "PL61 1090 1014 0000 0712 1981 2874"); w != "" {
		t.Errorf("warning for a listed account: %q", w)
	}
	if found, known := AccountOnWhiteList(seller, "PL02 1140 1140 0000 1234 5678 9012"); found || !known {
		t.Errorf("unlisted account: found=%v known=%v", found, known)
	}
	if w := AccountWarning(seller, "PL02114011400000123456789012"); !strings.Contains(w, "not in the White List") {
		t.Errorf("warning = %q", w)
	}
	if _, known := AccountOnWhiteList(Party{}, "PL61109010140000071219812874"); known {
		t.Error("a party without accounts must be unknown")
	}
	if got := NormalizeAccount("pl61 1090 1014 0000 0712 1981 2874"); got != "61109010140000071219812874" {
		t.Errorf("NormalizeAccount = %q", got)
	}
	if got := NormalizeAccount("de89 3704 0044 0532 0130 00"); got != "DE89370400440532013000" {
		t.Errorf("NormalizeAccount DE = %q", got)
	}
}

func TestOverrides_WinAndAreMarked(t *testing.T) {
	e := newEnv(t)
	p, err := e.r.Resolve(context.Background(), "acme", config.Party{NIP: buyerNIP, Name: "Acme Override", Address: "Inna 1, 00-001 Łódź"}, t0, PurposeInvoiceBuyer)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Acme Override" || p.Address != "Inna 1, 00-001 Łódź" ||
		p.Provenance["name"] != SourceConfig || p.Provenance["address"] != SourceConfig || p.Provenance["statusVat"] != SourceWhiteList {
		t.Errorf("party = %+v", p)
	}
}

func TestSanitize(t *testing.T) {
	got := clean("  ACME\u0085 \t\u007f Sp. z o.o.\u0000\n")
	if got != "ACME Sp. z o.o." {
		t.Errorf("clean = %q", got)
	}
	long := strings.Repeat("ą", 600)
	if n := len([]rune(clean(long))); n != 512 {
		t.Errorf("capped to %d runes", n)
	}
}

func TestRegistry_NoLiveHosts(t *testing.T) {
	// The defaults are the fixed production hosts ...
	if DefaultWhiteListURL != "https://wl-api.mf.gov.pl" || !strings.HasPrefix(DefaultVIESURL, "https://ec.europa.eu/") {
		t.Fatalf("default URLs changed: %s %s", DefaultWhiteListURL, DefaultVIESURL)
	}
	// ... a Resolver with zero Endpoints aims at them (the guard transport blocks the call) ...
	var hosts []string
	guard := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		return nil, errors.New("live call blocked by test")
	})}
	r := NewResolver(t.TempDir(), Endpoints{Client: guard}, nil, time.Hour, func() time.Time { return t0 }, nil, sellerNIP)
	_, _ = r.Resolve(context.Background(), "a", nipParty(buyerNIP), t0, PurposeAdd)
	_, _ = r.Resolve(context.Background(), "b", config.Party{VatID: "DE123456789"}, t0, PurposeAdd)
	if fmt.Sprint(hosts) != "[wl-api.mf.gov.pl ec.europa.eu]" {
		t.Errorf("hosts = %v", hosts)
	}
	// ... and every other test in this package talks to its httptest server only.
	e := newEnv(t)
	if _, err := e.r.Resolve(context.Background(), "acme", nipParty(buyerNIP), t0, PurposeAdd); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.f.srv.URL, "https://127.0.0.1:") || e.r.whiteListURL == DefaultWhiteListURL || e.r.viesURL == DefaultVIESURL {
		t.Errorf("test resolver is not on the fake: %s", e.r.whiteListURL)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCache_ConcurrentSaversKeepEveryEntry(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	const n = 12
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := NewResolver(dir, Endpoints{}, nil, time.Hour, nil, nil, "")
			if err := r.save(CacheEntry{Key: fmt.Sprintf("nip:%d", i), Source: SourceWhiteList, FetchedAt: time.Now()}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	r := NewResolver(dir, Endpoints{}, nil, time.Hour, nil, nil, "")
	for i := 0; i < n; i++ {
		if _, ok := r.Cached(fmt.Sprintf("nip:%d", i)); !ok {
			t.Errorf("entry %d lost", i)
		}
	}
}
