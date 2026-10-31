package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

func (fx *listFx) cachedXMLPath(n string) string {
	return filepath.Join(fx.dataDir, "ksef-cache", "test", ksefNIP, "xml", n+".xml")
}

func TestShow_PurchaseByKSeFNumberDownloadsOnce(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("show", fx.purchaseNo)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"ENVIRONMENT: TEST", "ZAK/09/15", "purchase, KSeF", "Materiały biurowe", "615,00", "KSeF, downloaded", "just now", "SHA-256 checked", fx.purchaseNo, "PL61 1090 1014 0000 0712 1981 2874"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if n := fx.fake.Count("GET", "/invoices/ksef/"+fx.purchaseNo); n != 1 {
		t.Errorf("%d downloads", n)
	}
	if fx.metadataCalls() != 0 {
		t.Errorf("show ran %d metadata queries", fx.metadataCalls())
	}
	before := len(fx.fake.Requests())
	code, out, errs = fx.run("show", fx.purchaseNo)
	if code != 0 || !strings.Contains(out, "from the local cache") {
		t.Fatalf("second show: %d %s%s", code, out, errs)
	}
	if len(fx.fake.Requests()) != before {
		t.Errorf("the second show sent %d requests", len(fx.fake.Requests())-before)
	}
}

func TestShow_SaleOutsideFvIsASale(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("show", fx.saleNo)
	if code != 0 || !strings.Contains(out, "EXT/7   [sale, KSeF]") {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
}

func TestShow_PurchaseByPlainNumberViaTheCache(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("list: %s", errs)
	}
	calls := fx.metadataCalls()
	code, out, errs := fx.run("show", "ZAK/09/15")
	if code != 0 || !strings.Contains(out, "Materiały biurowe") {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
	if fx.metadataCalls() != calls {
		t.Error("show queried the metadata")
	}
	if code, _, errs := fx.run("show", "NOPE/1"); code != 1 || !strings.Contains(errs, "not found in KSeF between 2026-08-01 and 2026-10-31; widen with --from/--to") {
		t.Errorf("unknown: %d %s", code, errs)
	}
}

func TestShow_AmbiguousNumberListsKSeFNumbers(t *testing.T) {
	fx := newListFx(t)
	other := fx.q.Add(ksefake.QMeta{InvoiceNumber: "ZAK/09/15", IssueDate: "2026-09-16", SellerNIP: "5260300804", SellerName: "DRUGI SPRZEDAWCA",
		BuyerType: "Nip", BuyerID: ksefNIP, BuyerName: "MOJA FIRMA", Net: "10.00", VAT: "2.30", Gross: "12.30"},
		otherInvoiceXML(t, "ZAK/09/15", "2026-09-16", 1000, "5260300804", "DRUGI SPRZEDAWCA", ksefNIP, "MOJA FIRMA"))
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("list: %s", errs)
	}
	code, out, errs := fx.run("show", "ZAK/09/15")
	if code != 1 || out != "" {
		t.Fatalf("exit %d out %q", code, out)
	}
	for _, want := range []string{"matches 2 invoices", fx.purchaseNo, other, "DRUGI SPRZEDAWCA"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errs)
		}
	}
}

func TestShow_XMLIsExactAndTamperedCacheRefused(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("show", fx.purchaseNo, "--xml")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := otherInvoiceXML(t, "ZAK/09/15", "2026-09-15", 50000, supplierNIP, supplierName, ksefNIP, "MOJA FIRMA")
	if out != string(want) {
		t.Errorf("xml differs from what KSeF served")
	}
	if code, out2, _ := fx.run("show", fx.purchaseNo, "--xml"); code != 0 || out2 != out {
		t.Errorf("cached xml differs")
	}
	p := fx.cachedXMLPath(fx.purchaseNo)
	if err := os.WriteFile(p, []byte(strings.Replace(out, "Materiały", "Materialy", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"show", fx.purchaseNo}, {"show", fx.purchaseNo, "--xml"}} {
		code, out, errs := fx.run(args...)
		if code != 1 || out != "" || !strings.Contains(errs, "hash") {
			t.Errorf("%v: exit %d out %q err %q", args, code, out, errs)
		}
	}
}

func TestShow_LocalInvoiceByItsKSeFNumber(t *testing.T) {
	fx := newListFx(t)
	m := fx.meta("test", sendNum)
	if m.KSeF == nil || m.KSeF.KSeFNumber == "" {
		t.Fatal("the sent invoice has no KSeF number")
	}
	num := m.KSeF.KSeFNumber
	code, out, errs := fx.run("show", num)
	if code != 0 || !strings.Contains(out, sendNum) || strings.Contains(out, "KSeF, downloaded") {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
	if fx.fake.Count("GET", "/invoices/ksef/"+num) != 0 {
		t.Error("a local invoice was downloaded")
	}
}

func TestShow_NotYetAvailableAndUnknown(t *testing.T) {
	fx := newListFx(t)
	fx.q.SetNotYetAvailable(fx.purchaseNo, 1)
	code, _, errs := fx.run("show", fx.purchaseNo)
	if code != 1 || !strings.Contains(errs, "not made this invoice downloadable yet; retry in a minute") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if code, out, errs := fx.run("show", fx.purchaseNo); code != 0 || !strings.Contains(out, "ZAK/09/15") {
		t.Errorf("retry: %d %s%s", code, out, errs)
	}
	code, _, errs = fx.run("show", "5260250274-20260101-AAAAAAAAAAAA-AA")
	if code != 1 {
		t.Errorf("unknown KSeF number: exit %d %s", code, errs)
	}
}

func TestShow_DownloadWithoutHashHeaderIsRefused(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("list: %s", errs)
	}
	forged := otherInvoiceXML(t, "ZAK/09/15", "2026-09-15", 9999900, supplierNIP, supplierName, ksefNIP, "MOJA FIRMA")
	fx.fake.Inject("GET", "/invoices/ksef/", 1, ksefake.Response{Status: 200, Body: string(forged), ContentType: "application/xml"})
	code, out, errs := fx.run("show", fx.purchaseNo)
	if code != 1 || out != "" || !strings.Contains(errs, "x-ms-meta-hash") || !strings.Contains(errs, "nothing was cached") {
		t.Fatalf("exit %d out %q err %q", code, out, errs)
	}
	if _, err := os.Stat(fx.cachedXMLPath(fx.purchaseNo)); !os.IsNotExist(err) {
		t.Errorf("a forged body was cached: %v", err)
	}
}

func TestShow_DownloadThatDiffersFromTheListedHashIsRefused(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("list: %s", errs)
	}
	forged := otherInvoiceXML(t, "ZAK/09/15", "2026-09-15", 9999900, supplierNIP, supplierName, ksefNIP, "MOJA FIRMA")
	sum := sha256.Sum256(forged)
	fx.fake.Inject("GET", "/invoices/ksef/", 1, ksefake.Response{Status: 200, Body: string(forged), ContentType: "application/xml",
		Header: map[string]string{"x-ms-meta-hash": base64.StdEncoding.EncodeToString(sum[:])}})
	code, out, errs := fx.run("show", fx.purchaseNo)
	if code != 1 || out != "" || !strings.Contains(errs, "invoiceHash") {
		t.Fatalf("exit %d out %q err %q", code, out, errs)
	}
	if _, err := os.Stat(fx.cachedXMLPath(fx.purchaseNo)); !os.IsNotExist(err) {
		t.Errorf("a forged body was cached: %v", err)
	}
}

func TestShow_KSeFNumberWithABadChecksumIsSaidDirectly(t *testing.T) {
	fx := newListFx(t)
	before := len(fx.fake.Requests())
	// the number of the purchase with its last check digit changed
	bad := fx.purchaseNo[:len(fx.purchaseNo)-1] + "0"
	if bad == fx.purchaseNo {
		bad = fx.purchaseNo[:len(fx.purchaseNo)-1] + "1"
	}
	code, out, errs := fx.run("show", bad)
	if code != 1 || out != "" || !strings.Contains(errs, "wrong checksum") || !strings.Contains(errs, "typo") || strings.Contains(errs, "not in the archive") {
		t.Fatalf("exit %d out %q err %q", code, out, errs)
	}
	if len(fx.fake.Requests()) != before {
		t.Errorf("show sent %d requests", len(fx.fake.Requests())-before)
	}
}

// metadataBodies returns the JSON bodies of the metadata queries sent so far.
func (fx *listFx) metadataBodies(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range fx.fake.Requests() {
		if r.Path != "/invoices/query/metadata" {
			continue
		}
		var b map[string]any
		if err := json.Unmarshal(r.Body, &b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

func TestShow_InvoiceNumberNotCachedIsFoundByATargetedQuery(t *testing.T) {
	fx := newListFx(t)
	fx.sleeps = nil
	code, out, errs := fx.run("show", "ZAK/09/15")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"ZAK/09/15", "purchase, KSeF", "Materiały biurowe", "615,00", "just now", "SHA-256 checked", fx.purchaseNo} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errs, `asking KSeF for invoice number "ZAK/09/15", issue dates 2026-08-01 to 2026-10-31`) {
		t.Errorf("stderr = %q", errs)
	}
	bodies := fx.metadataBodies(t)
	if len(bodies) != 2 || bodies[0]["subjectType"] != "Subject1" || bodies[1]["subjectType"] != "Subject2" {
		t.Fatalf("queries %v", bodies)
	}
	for _, b := range bodies {
		dr := b["dateRange"].(map[string]any)
		to, err := time.Parse(time.RFC3339, dr["to"].(string))
		if b["invoiceNumber"] != "ZAK/09/15" || dr["dateType"] != "Issue" || dr["from"] != "2026-07-31T00:00:00Z" || err != nil || to.After(time.Now().Add(time.Second)) {
			t.Errorf("body %v (to must not be in the future)", b)
		}
	}
	if n := fx.fake.Count("POST", "/auth/challenge"); n != 2 { // one login of the send fixture, one of this show
		t.Errorf("%d logins", n)
	}
	if n := fx.fake.Count("GET", "/invoices/ksef/"+fx.purchaseNo); n != 1 {
		t.Errorf("%d downloads", n)
	}
	paced := 0
	for _, d := range fx.sleeps {
		if d > 0 && d <= ksef.MetadataSpacing {
			paced++
		}
	}
	if paced != 1 {
		t.Errorf("the two queries were not spaced: %v", fx.sleeps)
	}
	// the result is cached: the second show sends nothing, and so does --offline
	before := len(fx.fake.Requests())
	for _, args := range [][]string{{"show", "ZAK/09/15"}, {"show", "ZAK/09/15", "--offline"}, {"show", fx.purchaseNo, "--offline"}} {
		if code, out, errs := fx.run(args...); code != 0 || !strings.Contains(out, "from the local cache") {
			t.Errorf("%v: %d %s%s", args, code, out, errs)
		}
	}
	if len(fx.fake.Requests()) != before {
		t.Errorf("cached shows sent %d requests", len(fx.fake.Requests())-before)
	}
}

func TestShow_QueryRangeDefaultsAndFromTo(t *testing.T) {
	fx := newListFx(t)
	old := fx.q.Add(ksefake.QMeta{InvoiceNumber: "OLD/1", IssueDate: "2026-03-10", SellerNIP: supplierNIP, SellerName: supplierName,
		BuyerType: "Nip", BuyerID: ksefNIP, BuyerName: "MOJA FIRMA", Net: "10.00", VAT: "2.30", Gross: "12.30"},
		otherInvoiceXML(t, "OLD/1", "2026-03-10", 1000, supplierNIP, supplierName, ksefNIP, "MOJA FIRMA"))
	code, out, errs := fx.run("show", "OLD/1")
	if code != 1 || out != "" || !strings.Contains(errs, `"OLD/1" not found in KSeF between 2026-08-01 and 2026-10-31; widen with --from/--to`) {
		t.Fatalf("default range: exit %d out %q err %q", code, out, errs)
	}
	if fx.fake.Count("GET", "/invoices/ksef/"+old) != 0 {
		t.Error("something was downloaded")
	}
	code, out, errs = fx.run("show", "OLD/1", "--from", "2026-03-01", "--to", "2026-03-31")
	if code != 0 || !strings.Contains(out, "OLD/1") || !strings.Contains(out, old) {
		t.Fatalf("with --from/--to: exit %d: %s%s", code, out, errs)
	}
}

func TestShow_LongRangeIsSplitIntoWindowsOfAtMost100Days(t *testing.T) {
	fx := newListFx(t)
	before := fx.metadataCalls()
	code, _, errs := fx.run("show", "NOPE/1", "--from", "2025-10-01", "--to", "2026-09-30")
	if code != 1 || !strings.Contains(errs, "not found in KSeF between 2025-10-01 and 2026-09-30") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	bodies := fx.metadataBodies(t)
	// 365 days: four windows, both subjects
	if n := fx.metadataCalls() - before; n != 8 {
		t.Errorf("%d metadata calls, want 8", n)
	}
	for _, b := range bodies {
		dr := b["dateRange"].(map[string]any)
		from, _ := time.Parse(time.RFC3339, dr["from"].(string))
		to, _ := time.Parse(time.RFC3339, dr["to"].(string))
		if to.Sub(from) > 100*24*time.Hour {
			t.Errorf("window %v is longer than 100 days", dr)
		}
	}
	if code, _, errs := fx.run("show", "NOPE/1", "--from", "2020-01-01", "--to", "2026-09-30"); code != 1 || !strings.Contains(errs, "366") {
		t.Errorf("a range over 366 days: %d %s", code, errs)
	}
}

func TestShow_SeveralMatchesAreListedAndNothingIsDownloaded(t *testing.T) {
	fx := newListFx(t)
	other := fx.q.Add(ksefake.QMeta{InvoiceNumber: "ZAK/09/15", IssueDate: "2026-09-16", SellerNIP: "5260300804", SellerName: "DRUGI SPRZEDAWCA",
		BuyerType: "Nip", BuyerID: ksefNIP, BuyerName: "MOJA FIRMA", Net: "10.00", VAT: "2.30", Gross: "12.30"},
		otherInvoiceXML(t, "ZAK/09/15", "2026-09-16", 1000, "5260300804", "DRUGI SPRZEDAWCA", ksefNIP, "MOJA FIRMA"))
	code, out, errs := fx.run("show", "ZAK/09/15")
	if code != 1 || out != "" {
		t.Fatalf("exit %d out %q", code, out)
	}
	for _, want := range []string{"matches 2 invoices in KSeF between 2026-08-01 and 2026-10-31", "ISSUED", "SELLER", "NIP", "KSEF-NUMBER",
		fx.purchaseNo, other, "DRUGI SPRZEDAWCA", "5260300804", supplierNIP} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errs)
		}
	}
	if fx.fake.Count("GET", "/invoices/ksef/"+fx.purchaseNo)+fx.fake.Count("GET", "/invoices/ksef/"+other) != 0 {
		t.Error("an invoice was downloaded")
	}
	// the matches were cached, so the next try needs no query
	calls := fx.metadataCalls()
	if code, _, errs := fx.run("show", "ZAK/09/15"); code != 1 || !strings.Contains(errs, "matches 2 invoices") || fx.metadataCalls() != calls {
		t.Errorf("second try: %d %s (%d calls)", code, errs, fx.metadataCalls()-calls)
	}
}

func TestShow_OfflineNeverContactsKSeF(t *testing.T) {
	fx := newListFx(t)
	before := len(fx.fake.Requests())
	for _, args := range [][]string{{"show", "ZAK/09/15", "--offline"}, {"show", fx.purchaseNo, "--offline"}} {
		code, out, errs := fx.run(args...)
		if code != 1 || out != "" || !strings.Contains(errs, "--offline") {
			t.Errorf("%v: exit %d out %q err %q", args, code, out, errs)
		}
	}
	if len(fx.fake.Requests()) != before {
		t.Errorf("--offline sent %d requests", len(fx.fake.Requests())-before)
	}
}

func TestShow_QueryRateLimitIsNotRetried(t *testing.T) {
	fx := newListFx(t)
	fx.fake.Inject("POST", "/invoices/query/metadata", 5, ksefake.Response{Status: 429,
		Header: map[string]string{"Retry-After": "3600"},
		Body:   `{"status":{"code":429,"description":"Too Many Requests","details":["Przekroczono limit"]}}`})
	code, out, errs := fx.run("show", "ZAK/09/15")
	if code != 1 || out != "" || !strings.Contains(errs, "too many requests") || !strings.Contains(errs, "3600") {
		t.Fatalf("exit %d out %q err %q", code, out, errs)
	}
	if n := fx.metadataCalls(); n != 1 {
		t.Errorf("%d metadata calls after a 429, want 1 (the POST is never retried, the other subject is not tried)", n)
	}
}
