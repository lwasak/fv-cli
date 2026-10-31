package nbp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fake struct {
	mu    sync.Mutex
	paths []string
	byDay map[string]string // date -> fixture of a 200 answer; others 404
	c400  bool
}

func (f *fake) start(t *testing.T) *Client {
	t.Helper()
	notFound, bad := fixture(t, "404.txt"), fixture(t, "400.txt")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 6 || parts[3] != "A" {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		if f.c400 {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(400)
			_, _ = w.Write(bad)
			return
		}
		if file, ok := f.byDay[parts[5]]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture(t, file))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(404)
		_, _ = w.Write(notFound)
	}))
	t.Cleanup(srv.Close)
	now := func() time.Time { return time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC) }
	return New(srv.URL, srv.Client(), now)
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestMidBefore_Direct(t *testing.T) {
	f := &fake{byDay: map[string]string{"2026-10-30": "eur_200.json"}}
	c := f.start(t)
	r, err := c.MidBefore(context.Background(), "EUR", day(2026, 10, 31))
	if err != nil {
		t.Fatal(err)
	}
	want := Rate{Currency: "EUR", Table: "211/A/NBP/2026", EffectiveDate: "2026-10-30", Mid: "4.2512", MidMicro: 4251200}
	if r != want {
		t.Fatalf("got %+v want %+v", r, want)
	}
	if len(f.paths) != 1 || f.paths[0] != "/api/exchangerates/rates/A/EUR/2026-10-30/?format=json" {
		t.Fatalf("paths %v", f.paths)
	}
}

func TestMidBefore_IndependenceDayWalkBack(t *testing.T) {
	f := &fake{byDay: map[string]string{"2026-11-10": "eur_nov10_200.json"}}
	c := f.start(t)
	r, err := c.MidBefore(context.Background(), "EUR", day(2026, 11, 12))
	if err != nil {
		t.Fatal(err)
	}
	if r.EffectiveDate != "2026-11-10" || r.Mid != "4.2340" || r.MidMicro != 4234000 {
		t.Fatalf("got %+v", r)
	}
	if len(f.paths) != 2 || !strings.Contains(f.paths[0], "2026-11-11") || !strings.Contains(f.paths[1], "2026-11-10") {
		t.Fatalf("paths %v", f.paths)
	}
}

func TestMidBefore_WeekendWalkBack(t *testing.T) {
	f := &fake{byDay: map[string]string{"2026-11-13": "eur_nov10_200.json"}} // Friday
	c := f.start(t)
	if _, err := c.MidBefore(context.Background(), "EUR", day(2026, 11, 16)); err != nil { // Monday
		t.Fatal(err)
	}
	if len(f.paths) != 3 {
		t.Fatalf("want Sun, Sat, Fri; got %v", f.paths)
	}
	for i, d := range []string{"2026-11-15", "2026-11-14", "2026-11-13"} {
		if !strings.Contains(f.paths[i], d) {
			t.Errorf("request %d = %s, want %s", i, f.paths[i], d)
		}
	}
}

func TestMidBefore_GivesUpAfterTenDays(t *testing.T) {
	f := &fake{}
	c := f.start(t)
	_, err := c.MidBefore(context.Background(), "EUR", day(2026, 11, 12))
	if !errors.Is(err, ErrNoRate) {
		t.Fatalf("err = %v", err)
	}
	if len(f.paths) != MaxWalkBack || !strings.Contains(f.paths[0], "2026-11-11") || !strings.Contains(f.paths[9], "2026-11-02") {
		t.Fatalf("paths %v", f.paths)
	}
}

func TestMidBefore_UnknownCurrency400(t *testing.T) {
	f := &fake{c400: true}
	c := f.start(t)
	_, err := c.MidBefore(context.Background(), "XXX", day(2026, 11, 12))
	if !errors.Is(err, ErrUnknownCurrency) {
		t.Fatalf("err = %v", err)
	}
	if len(f.paths) != 1 {
		t.Fatalf("a 400 must not walk back: %v", f.paths)
	}
}

func TestMidBefore_MalformedCodeNeverCalls(t *testing.T) {
	f := &fake{}
	c := f.start(t)
	for _, code := range []string{"EU", "EURO", "E/R", "../A", ""} {
		if _, err := c.MidBefore(context.Background(), code, day(2026, 11, 12)); !errors.Is(err, ErrUnknownCurrency) {
			t.Errorf("%q: err = %v", code, err)
		}
	}
	if len(f.paths) != 0 {
		t.Fatalf("calls: %v", f.paths)
	}
}

func TestMidBefore_PLNNeverCalled(t *testing.T) {
	f := &fake{}
	c := f.start(t)
	for _, code := range []string{"PLN", "pln"} {
		if _, err := c.MidBefore(context.Background(), code, day(2026, 11, 12)); !errors.Is(err, ErrPLN) {
			t.Fatalf("%s: err = %v", code, err)
		}
	}
	if len(f.paths) != 0 {
		t.Fatalf("calls: %v", f.paths)
	}
}

func TestMidBefore_FutureTaxPoint(t *testing.T) {
	f := &fake{byDay: map[string]string{"2026-11-20": "eur_200.json"}}
	c := f.start(t) // today is 2026-11-20
	if _, err := c.MidBefore(context.Background(), "EUR", day(2026, 11, 22)); !errors.Is(err, ErrNotYetPublished) {
		t.Fatalf("err = %v", err)
	}
	if len(f.paths) != 0 {
		t.Fatalf("calls: %v", f.paths)
	}
	// Tax point today: the day before (yesterday) is fine.
	if _, err := c.MidBefore(context.Background(), "EUR", day(2026, 11, 21)); err != nil {
		t.Fatalf("tax point tomorrow, rate of today: %v", err)
	}
}

// S7: tax point tomorrow, today's table not published yet (404): refuse, do not walk back.
func TestMidBefore_TodayNotYetPublishedIsRefused(t *testing.T) {
	f := &fake{byDay: map[string]string{"2026-11-19": "eur_200.json"}}
	c := f.start(t) // today is 2026-11-20 10:00, table of 11-20 is missing
	_, err := c.MidBefore(context.Background(), "EUR", day(2026, 11, 21))
	if !errors.Is(err, ErrNotYetPublished) || !strings.Contains(err.Error(), "issue later or set --sale-date/--issue-date") {
		t.Fatalf("err = %v", err)
	}
	if len(f.paths) != 1 {
		t.Errorf("walked back: %v", f.paths)
	}
	// Once today's table exists it is used.
	f.mu.Lock()
	f.byDay["2026-11-20"] = "eur_200.json"
	f.mu.Unlock()
	if _, err := c.MidBefore(context.Background(), "EUR", day(2026, 11, 21)); err != nil {
		t.Fatalf("published: %v", err)
	}
	// An older day that is missing (holiday) still walks back.
	if _, err := c.MidBefore(context.Background(), "EUR", day(2026, 11, 20)); err != nil {
		t.Fatalf("tax point today: %v", err)
	}
}

func TestMidBefore_HTTPSOnly(t *testing.T) {
	c := New("http://api.nbp.pl", nil, func() time.Time { return day(2026, 11, 20) })
	if _, err := c.MidBefore(context.Background(), "EUR", day(2026, 11, 12)); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v", err)
	}
}

func TestTaxPoint(t *testing.T) {
	warsaw := time.FixedZone("CET", 3600)
	issue := time.Date(2026, 11, 12, 23, 30, 0, 0, warsaw)
	cases := []struct {
		name        string
		issue, sale time.Time
		want        string
	}{
		{"no sale date", issue, time.Time{}, "2026-11-12"},
		{"sale before issue", issue, day(2026, 11, 5), "2026-11-05"},
		{"sale same day", issue, day(2026, 11, 12), "2026-11-12"},
		{"issued before sale", issue, day(2026, 11, 20), "2026-11-12"},
	}
	for _, tc := range cases {
		if got := TaxPoint(tc.issue, tc.sale).Format(time.DateOnly); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
}
