package ksef

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

const queryNIP = "5833281095"
const queryBearer = "ACCESS-query-5555"

type queryFixture struct {
	f *ksefake.Fake
	q *ksefake.Query
	c *Client
	s *Session
	// wantViolations is set by a test that makes the client's mistake on purpose.
	wantViolations bool
}

func newQueryFixture(t *testing.T, truncateAt int) *queryFixture {
	t.Helper()
	f := ksefake.New(t)
	q := f.EnableQuery(ksefake.QueryOptions{NIP: queryNIP, Bearer: queryBearer, TruncateAt: truncateAt})
	c := newTestClient(t, f, config.EnvDemo, t.TempDir(), nil)
	c.now = func() time.Time { return t0 }
	fx := &queryFixture{f: f, q: q, c: c, s: &Session{env: config.EnvDemo, accessToken: queryBearer}}
	t.Cleanup(func() {
		if v := q.Violations(); len(v) > 0 && !fx.wantViolations {
			t.Errorf("the client made protocol errors: %v", v)
		}
	})
	return fx
}

// add stores n invoices, issued one per day from start (YYYY-MM-DD), cycling over days.
func (fx *queryFixture) add(t *testing.T, n int, start string, days int) {
	t.Helper()
	d, err := time.Parse("2006-01-02", start)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		fx.q.Add(ksefake.QMeta{InvoiceNumber: fmt.Sprintf("FV/%d", i), IssueDate: d.AddDate(0, 0, i%days).Format("2006-01-02"),
			SellerNIP: queryNIP, BuyerID: "5260250274", Gross: "123.00", Net: "100.00", VAT: "23.00"}, []byte("<x/>"))
	}
}

func (fx *queryFixture) queries() []struct {
	Query url.Values
	Body  map[string]any
} {
	var out []struct {
		Query url.Values
		Body  map[string]any
	}
	for _, r := range fx.f.Requests() {
		if r.Path != "/invoices/query/metadata" {
			continue
		}
		v, _ := url.ParseQuery(r.Query)
		var b map[string]any
		_ = json.Unmarshal(r.Body, &b)
		out = append(out, struct {
			Query url.Values
			Body  map[string]any
		}{v, b})
	}
	return out
}

func TestQueryMetadata_RequestShape(t *testing.T) {
	fx := newQueryFixture(t, 0)
	fx.add(t, 3, "2026-08-10", 3)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC)
	page, err := fx.c.QueryMetadata(context.Background(), fx.s, Subject1, from, to, 0, 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Invoices) != 3 || page.HasMore || page.IsTruncated {
		t.Errorf("page = %+v", page)
	}
	reqs := fx.f.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	r := reqs[0]
	if r.Method != "POST" || r.Query != "pageOffset=0&pageSize=250&sortOrder=Asc" {
		t.Errorf("%s ?%s", r.Method, r.Query)
	}
	if r.Header.Get("Authorization") != "Bearer "+queryBearer || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
		t.Errorf("headers %v", r.Header)
	}
	want := `{"dateRange":{"dateType":"Issue","from":"2026-08-01T00:00:00Z","to":"2026-10-31T23:59:59Z"},"subjectType":"Subject1"}`
	if string(r.Body) != want {
		t.Errorf("body %s\nwant %s", r.Body, want)
	}
	m := page.Invoices[0]
	if m.SellerNIP != queryNIP || m.BuyerIDType != "Nip" || m.BuyerID != "5260250274" || m.Gross != "123.00" ||
		m.Net != "100.00" || m.VAT != "23.00" || m.Currency != "PLN" || m.InvoiceType != "Vat" || m.FormCode != "FA (3)" ||
		m.InvoiceHash == "" || ValidateKSeFNumber(m.KSeFNumber) != nil {
		t.Errorf("meta = %+v", m)
	}
}

func TestQueryMetadata_UTCBoundsFromOtherZone(t *testing.T) {
	fx := newQueryFixture(t, 0)
	warsaw := time.FixedZone("CET", 3600)
	_, err := fx.c.QueryMetadata(context.Background(), fx.s, Subject2, time.Date(2026, 8, 1, 1, 0, 0, 0, warsaw), time.Date(2026, 8, 2, 1, 0, 0, 0, warsaw), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if b := string(fx.f.Requests()[0].Body); !strings.Contains(b, `"from":"2026-08-01T00:00:00Z"`) || !strings.Contains(b, `"subjectType":"Subject2"`) {
		t.Errorf("body %s", b)
	}
}

func TestQueryMetadata_RejectsBadArguments(t *testing.T) {
	fx := newQueryFixture(t, 0)
	for _, c := range []struct {
		subj      SubjectType
		off, size int
	}{{"Subject3", 0, 10}, {Subject1, 0, 9}, {Subject1, 0, 251}, {Subject1, -1, 10}} {
		if _, err := fx.c.QueryMetadata(context.Background(), fx.s, c.subj, t0, t0, c.off, c.size); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	if n := len(fx.f.Requests()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestQueryAll_SplitsIntoWindowsOf92Days(t *testing.T) {
	fx := newQueryFixture(t, 0)
	fx.add(t, 10, "2026-01-01", 10)
	c := fx.c
	c.now = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }
	got, err := c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-01-01", "2026-07-19", nil) // 200 days
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Errorf("got %d invoices", len(got))
	}
	qs := fx.queries()
	if len(qs) != 3 {
		t.Fatalf("%d queries, want 3", len(qs))
	}
	wantRanges := [][2]string{
		{"2026-01-01T00:00:00Z", "2026-04-02T23:59:59Z"}, // 92 days
		{"2026-04-03T00:00:00Z", "2026-07-03T23:59:59Z"},
		{"2026-07-04T00:00:00Z", "2026-07-19T23:59:59Z"},
	}
	for i, q := range qs {
		dr := q.Body["dateRange"].(map[string]any)
		if dr["from"] != wantRanges[i][0] || dr["to"] != wantRanges[i][1] || dr["dateType"] != "Issue" {
			t.Errorf("window %d = %v, want %v", i, dr, wantRanges[i])
		}
	}
}

func TestQueryAll_DefaultThreeMonthsIsOneWindow(t *testing.T) {
	fx := newQueryFixture(t, 0)
	if _, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-08-01", "2026-10-30", nil); err != nil {
		t.Fatal(err)
	}
	if n := len(fx.queries()); n != 1 {
		t.Errorf("%d queries", n)
	}
}

func TestQueryAll_PagesThroughThreePages(t *testing.T) {
	fx := newQueryFixture(t, 0)
	fx.add(t, 600, "2026-09-01", 28)
	got, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-09-01", "2026-09-30", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 600 {
		t.Errorf("got %d", len(got))
	}
	qs := fx.queries()
	if len(qs) != 3 {
		t.Fatalf("%d queries", len(qs))
	}
	for i, q := range qs {
		if q.Query.Get("pageOffset") != fmt.Sprint(i) || q.Query.Get("pageSize") != "250" || q.Query.Get("sortOrder") != "Asc" {
			t.Errorf("query %d = %v", i, q.Query)
		}
	}
}

func TestQueryAll_TruncationRestartsFromLastDateAndDedupes(t *testing.T) {
	fx := newQueryFixture(t, 300)
	fx.add(t, 600, "2026-09-01", 28)
	got, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-09-01", "2026-09-30", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range got {
		if seen[m.KSeFNumber] {
			t.Fatalf("duplicate %s", m.KSeFNumber)
		}
		seen[m.KSeFNumber] = true
	}
	if len(got) != 600 {
		t.Errorf("got %d of 600", len(got))
	}
	qs := fx.queries()
	if len(qs) < 3 {
		t.Fatalf("%d queries", len(qs))
	}
	// after a truncated page the next query starts at the last date and at page 0
	restart := 0
	for i, q := range qs {
		dr := q.Body["dateRange"].(map[string]any)
		if i > 0 && q.Query.Get("pageOffset") == "0" {
			restart++
			if dr["from"] == "2026-09-01T00:00:00Z" {
				t.Errorf("query %d restarted from the old date", i)
			}
		}
	}
	if restart == 0 {
		t.Error("no restart seen")
	}
}

func TestQueryAll_TruncatedOnOneDayIsReportedStuck(t *testing.T) {
	fx := newQueryFixture(t, 260)
	fx.add(t, 300, "2026-09-10", 1)
	got, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-09-01", "2026-09-30", nil)
	if !errors.Is(err, ErrQueryStuck) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "10 000") || len(got) != 260 {
		t.Errorf("err %q, %d collected", err, len(got))
	}
	// the first restart moves to the day of the invoices, the second finds no progress
	if n := len(fx.queries()); n != 4 {
		t.Errorf("%d queries", n)
	}
}

func TestQueryAll_BudgetCapReturnsWhatWasCollected(t *testing.T) {
	fx := newQueryFixture(t, 0)
	fx.add(t, 600, "2026-09-01", 28)
	budget := 2
	got, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-09-01", "2026-09-30", &budget)
	if !errors.Is(err, ErrQueryBudget) {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 500 || budget != 0 || len(fx.queries()) != 2 {
		t.Errorf("%d collected, budget %d, %d queries", len(got), budget, len(fx.queries()))
	}
}

func TestQueryAll_BudgetIsSharedAcrossCalls(t *testing.T) {
	fx := newQueryFixture(t, 0)
	budget := 2
	for _, subj := range []SubjectType{Subject1, Subject2} {
		if _, err := fx.c.QueryAllMetadata(context.Background(), fx.s, subj, "2026-09-01", "2026-09-30", &budget); err != nil {
			t.Fatal(err)
		}
	}
	if budget != 0 {
		t.Errorf("budget %d", budget)
	}
	if _, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-09-01", "2026-09-30", &budget); !errors.Is(err, ErrQueryBudget) {
		t.Errorf("err = %v", err)
	}
	if n := len(fx.queries()); n != 2 {
		t.Errorf("%d queries", n)
	}
}

func TestQueryAll_ToIsCappedAtTodayInWarsaw(t *testing.T) {
	fx := newQueryFixture(t, 0)
	// 23:30 UTC on 30 Oct is already 31 Oct in Warsaw (CET after the 25 Oct change)
	fx.c.now = func() time.Time { return time.Date(2026, 10, 30, 23, 30, 0, 0, time.UTC) }
	if _, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-10-01", "2026-12-31", nil); err != nil {
		t.Fatal(err)
	}
	dr := fx.queries()[0].Body["dateRange"].(map[string]any)
	if dr["to"] != "2026-10-31T23:59:59Z" {
		t.Errorf("to = %v", dr["to"])
	}
}

func TestQueryAll_ClientSideDateFilter(t *testing.T) {
	fx := newQueryFixture(t, 0)
	fx.f.Handle("POST", "/invoices/query/metadata", func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Body: `{"hasMore":false,"isTruncated":false,"invoices":[` +
			metaJSON("5833281095-20260831-0100001AF629-00", "2026-08-31") + `,` +
			metaJSON("5833281095-20260915-0100001AF62A-00", "2026-09-15") + `]}`}
	})
	got, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-09-01", "2026-09-30", nil)
	if err == nil {
		// the hand-made numbers have wrong check digits, so the answer is refused
		t.Fatalf("got %v", got)
	}
	if !errors.Is(err, ErrBadKSeFNumber) {
		t.Errorf("err = %v", err)
	}
}

func metaJSON(n, issue string) string {
	return fmt.Sprintf(`{"ksefNumber":%q,"invoiceNumber":"X","issueDate":%q,"seller":{"nip":"5833281095"},"buyer":{"identifier":{"type":"None"}},"netAmount":1,"grossAmount":1.5,"vatAmount":0.5,"currency":"PLN","invoiceType":"Vat","formCode":{"systemCode":"FA (3)","schemaVersion":"1-0E","value":"FA"},"isSelfInvoicing":false,"hasAttachment":false,"invoiceHash":"x"}`, n, issue)
}

func TestQueryAll_DropsRecordsOutsideTheRange(t *testing.T) {
	fx := newQueryFixture(t, 0)
	in := fx.q.Add(ksefake.QMeta{InvoiceNumber: "IN", IssueDate: "2026-09-15", SellerNIP: queryNIP}, nil)
	out := fx.q.Add(ksefake.QMeta{InvoiceNumber: "OUT", IssueDate: "2026-08-31", SellerNIP: queryNIP}, nil)
	// a server that ignores the bounds: serve everything
	fx.f.Handle("POST", "/invoices/query/metadata", func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Body: `{"hasMore":false,"isTruncated":false,"invoices":[` + metaJSON(out, "2026-08-31") + `,` + metaJSON(in, "2026-09-15") + `]}`}
	})
	got, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-09-01", "2026-09-30", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].KSeFNumber != in {
		t.Errorf("got %+v", got)
	}
}

func TestQueryMetadata_AmountsStayExactDecimals(t *testing.T) {
	fx := newQueryFixture(t, 0)
	id := fx.q.Add(ksefake.QMeta{InvoiceNumber: "A", IssueDate: "2026-09-15", SellerNIP: queryNIP, Gross: "-1230.50", Net: "-1000.41", VAT: "230.09"}, nil)
	page, err := fx.c.QueryMetadata(context.Background(), fx.s, Subject1, t0.AddDate(0, -2, 0), t0, 0, 10)
	if err != nil || len(page.Invoices) != 1 || page.Invoices[0].KSeFNumber != id {
		t.Fatalf("%v %+v", err, page)
	}
	m := page.Invoices[0]
	if m.Gross.String() != "-1230.50" || m.Net.String() != "-1000.41" || m.VAT.String() != "230.09" {
		t.Errorf("amounts %v %v %v", m.Gross, m.Net, m.VAT)
	}
}

func TestQueryMetadata_429IsNotRetriedAndExposesRetryAfter(t *testing.T) {
	fx := newQueryFixture(t, 0)
	fx.f.Inject("POST", "/invoices/query/metadata", 5, ksefake.Response{Status: 429,
		Header: map[string]string{"Retry-After": "3600"},
		Body:   `{"status":{"code":429,"description":"Too Many Requests","details":["Przekroczono limit 20 żądań na godzinę"]}}`})
	var slept []time.Duration
	fx.c.SetSleep(func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil })
	_, err := fx.c.QueryMetadata(context.Background(), fx.s, Subject1, t0.AddDate(0, -1, 0), t0, 0, 10)
	if !IsRateLimited(err) {
		t.Fatalf("err = %v", err)
	}
	var ke *Error
	if !errors.As(err, &ke) || ke.RetryAfter() != time.Hour {
		t.Errorf("RetryAfter = %v", ke)
	}
	if n := len(fx.queries()); n != 1 || len(slept) != 0 {
		t.Errorf("%d requests, slept %v", n, slept)
	}
	// QueryAllMetadata returns the error as is, without a second call
	if _, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-09-01", "2026-09-30", nil); !IsRateLimited(err) {
		t.Errorf("err = %v", err)
	}
	if n := len(fx.queries()); n != 2 {
		t.Errorf("%d requests", n)
	}
}

func TestQueryMetadata_ForbiddenCarriesTheInvoiceReadHint(t *testing.T) {
	fx := newQueryFixture(t, 0)
	fx.f.Handle("POST", "/invoices/query/metadata", func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Status: 403, ContentType: "application/problem+json",
			Body: `{"title":"Forbidden","status":403,"detail":"Brak uprawnień"}`}
	})
	_, err := fx.c.QueryMetadata(context.Background(), fx.s, Subject1, t0.AddDate(0, -1, 0), t0, 0, 10)
	if !IsForbidden(err) || !strings.Contains(err.Error(), "InvoiceRead") {
		t.Errorf("err = %v", err)
	}
	if IsRateLimited(err) || IsNoSuchInvoice(err) || IsNotYetAvailable(err) {
		t.Error("wrong classification")
	}
}

func TestQueryMetadata_UnauthorizedIsNotForbidden(t *testing.T) {
	fx := newQueryFixture(t, 0)
	fx.wantViolations = true
	fx.s = &Session{env: config.EnvDemo, accessToken: "other"}
	_, err := fx.c.QueryMetadata(context.Background(), fx.s, Subject1, t0.AddDate(0, -1, 0), t0, 0, 10)
	if err == nil || IsForbidden(err) {
		t.Errorf("err = %v", err)
	}
}

func TestQueryFake_OnlineAcceptedInvoicesAppearAsSales(t *testing.T) {
	fx := newOnlineFixture(t)
	q := fx.f.EnableQuery(ksefake.QueryOptions{NIP: "5260250274", Bearer: onlineAccess, Online: fx.on})
	xml := []byte(`<?xml version="1.0"?><Faktura xmlns="http://crd.gov.pl/wzor/2025/06/25/13775/"><Podmiot1><DaneIdentyfikacyjne><NIP>5260250274</NIP><Nazwa>Jan Testowy</Nazwa></DaneIdentyfikacyjne></Podmiot1>` +
		`<Podmiot2><DaneIdentyfikacyjne><NIP>5252248481</NIP><Nazwa>Acme</Nazwa></DaneIdentyfikacyjne></Podmiot2>` +
		`<Fa><KodWaluty>PLN</KodWaluty><P_1>2026-10-29</P_1><P_2>FV/2026/10/001</P_2><P_13_1>100.00</P_13_1><P_14_1>23.00</P_14_1><P_15>123.00</P_15><RodzajFaktury>VAT</RodzajFaktury></Fa></Faktura>`)
	_, _ = sendOne(t, fx, xml)
	fx.c.now = func() time.Time { return t0 }
	got, err := fx.c.QueryAllMetadata(context.Background(), fx.s, Subject1, "2026-10-01", "2026-10-30", nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	m := got[0]
	if m.InvoiceNumber != "FV/2026/10/001" || m.IssueDate != "2026-10-29" || m.SellerNIP != "5260250274" || m.BuyerID != "5252248481" ||
		m.Gross != "123.00" || m.Net != "100.00" || m.VAT != "23.00" || m.BuyerName != "Acme" {
		t.Errorf("meta %+v", m)
	}
	if b, _, err := fx.c.DownloadInvoice(context.Background(), fx.s, m.KSeFNumber); err != nil || string(b) != string(xml) {
		t.Errorf("download: %v", err)
	}
	if v := q.Violations(); len(v) != 0 {
		t.Errorf("violations %v", v)
	}
}
