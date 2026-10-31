package ksefake_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

func queryBody(subject, from, to string) map[string]any {
	return map[string]any{"subjectType": subject,
		"dateRange": map[string]any{"dateType": "Issue", "from": from, "to": to}}
}

type qpage struct {
	HasMore     bool
	IsTruncated bool
	Invoices    []struct {
		KSeFNumber string
		IssueDate  string
	}
}

func queryPage(t *testing.T, f *ksefake.Fake, off, size int, body map[string]any) (int, qpage, string) {
	t.Helper()
	code, b := post(t, f, fmt.Sprintf("/invoices/query/metadata?sortOrder=Asc&pageOffset=%d&pageSize=%d", off, size), body)
	var p qpage
	_ = json.Unmarshal(b, &p)
	return code, p, string(b)
}

func fillQuery(q *ksefake.Query, n int, nip string) {
	for i := 0; i < n; i++ {
		q.Add(ksefake.QMeta{InvoiceNumber: fmt.Sprintf("N/%d", i), IssueDate: fmt.Sprintf("2026-09-%02d", 1+i%28),
			SellerNIP: nip, BuyerID: "5260250274", Gross: "1.23", Net: "1.00", VAT: "0.23"}, []byte("<x/>"))
	}
}

func TestQueryFake_PageIndexAndHasMore(t *testing.T) {
	f := ksefake.New(t)
	q := f.EnableQuery(ksefake.QueryOptions{NIP: "5833281095"})
	fillQuery(q, 25, "5833281095")
	b := queryBody("Subject1", "2026-09-01T00:00:00Z", "2026-09-30T23:59:59Z")
	var all []string
	for off, want := range []struct {
		n    int
		more bool
	}{{10, true}, {10, true}, {5, false}} {
		code, p, raw := queryPage(t, f, off, 10, b)
		if code != 200 || len(p.Invoices) != want.n || p.HasMore != want.more || p.IsTruncated {
			t.Fatalf("page %d: %d %s", off, code, raw)
		}
		for _, i := range p.Invoices {
			all = append(all, i.KSeFNumber)
		}
	}
	if len(all) != 25 {
		t.Errorf("got %d records", len(all))
	}
	if v := q.Violations(); len(v) != 0 {
		t.Errorf("violations %v", v)
	}
	// the buyer sees the same invoices as Subject2
	if _, p, _ := queryPage(t, f, 0, 250, queryBody("Subject2", "2026-09-01T00:00:00Z", "2026-09-30T23:59:59Z")); len(p.Invoices) != 0 {
		t.Errorf("Subject2 of the seller's NIP returned %d", len(p.Invoices))
	}
}

func TestQueryFake_TruncatesHonestly(t *testing.T) {
	f := ksefake.New(t)
	q := f.EnableQuery(ksefake.QueryOptions{NIP: "5833281095", TruncateAt: 15})
	fillQuery(q, 25, "5833281095")
	b := queryBody("Subject1", "2026-09-01T00:00:00Z", "2026-09-30T23:59:59Z")
	_, p0, _ := queryPage(t, f, 0, 10, b)
	_, p1, _ := queryPage(t, f, 1, 10, b)
	if !p0.HasMore || p0.IsTruncated || len(p1.Invoices) != 5 || !p1.HasMore || !p1.IsTruncated {
		t.Errorf("p0 %+v p1 %+v", p0, p1)
	}
}

func TestQueryFake_RecordsEveryViolation(t *testing.T) {
	f := ksefake.New(t)
	q := f.EnableQuery(ksefake.QueryOptions{})
	cases := map[string]struct {
		off, size int
		body      map[string]any
	}{
		"101 days":      {0, 10, queryBody("Subject1", "2026-01-01T00:00:00Z", "2026-04-12T00:00:00Z")},
		"small page":    {0, 9, queryBody("Subject1", "2026-09-01T00:00:00Z", "2026-09-30T00:00:00Z")},
		"large page":    {0, 251, queryBody("Subject1", "2026-09-01T00:00:00Z", "2026-09-30T00:00:00Z")},
		"subject3":      {0, 10, queryBody("Subject3", "2026-09-01T00:00:00Z", "2026-09-30T00:00:00Z")},
		"bad date":      {0, 10, queryBody("Subject1", "2026-09-01", "2026-09-30T00:00:00Z")},
		"negative page": {-1, 10, queryBody("Subject1", "2026-09-01T00:00:00Z", "2026-09-30T00:00:00Z")},
		"date type": {0, 10, map[string]any{"subjectType": "Subject1",
			"dateRange": map[string]any{"dateType": "PermanentStorage", "from": "2026-09-01T00:00:00Z", "to": "2026-09-30T00:00:00Z"}}},
	}
	n := 0
	for name, c := range cases {
		code, _, raw := queryPage(t, f, c.off, c.size, c.body)
		if code != 400 || !strings.Contains(raw, "21405") && name != "subject3" && name != "date type" {
			t.Errorf("%s: %d %s", name, code, raw)
		}
		n++
		if len(q.Violations()) != n {
			t.Errorf("%s: violations %v", name, q.Violations())
		}
	}
}

func TestQueryFake_100DaysExactlyIsAccepted(t *testing.T) {
	f := ksefake.New(t)
	q := f.EnableQuery(ksefake.QueryOptions{})
	code, _, raw := queryPage(t, f, 0, 10, queryBody("Subject1", "2026-01-01T00:00:00Z", "2026-04-11T00:00:00Z"))
	if code != 200 || len(q.Violations()) != 0 {
		t.Errorf("%d %s %v", code, raw, q.Violations())
	}
}

func TestQueryFake_DownloadAndScriptedErrors(t *testing.T) {
	f := ksefake.New(t)
	q := f.EnableQuery(ksefake.QueryOptions{})
	n := q.Add(ksefake.QMeta{InvoiceNumber: "A/1", IssueDate: "2026-09-05", SellerNIP: "5833281095"}, []byte("<Faktura/>"))
	q.SetNotYetAvailable(n, 1)
	get := func(path string) (int, string, http.Header) {
		req, _ := http.NewRequest("GET", f.URL+path, nil)
		req.Header.Set("Authorization", "Bearer t")
		resp, err := f.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	if code, body, _ := get("/invoices/ksef/" + n); code != 400 || !strings.Contains(body, "21165") {
		t.Errorf("first: %d %s", code, body)
	}
	code, body, h := get("/invoices/ksef/" + n)
	if code != 200 || body != "<Faktura/>" || h.Get("x-ms-meta-hash") == "" {
		t.Errorf("second: %d %s %v", code, body, h)
	}
	if code, body, _ := get("/invoices/ksef/5833281095-20260905-AAAAAAAAAAAA-00"); code != 400 || !strings.Contains(body, "21164") {
		t.Errorf("unknown: %d %s", code, body)
	}
}

func TestQueryFake_BearerChecked(t *testing.T) {
	f := ksefake.New(t)
	q := f.EnableQuery(ksefake.QueryOptions{Bearer: "good"})
	code, _, _ := queryPage(t, f, 0, 10, queryBody("Subject1", "2026-09-01T00:00:00Z", "2026-09-30T00:00:00Z"))
	if code != 401 || len(q.Violations()) != 1 {
		t.Errorf("%d %v", code, q.Violations())
	}
}
