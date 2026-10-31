package cli

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fa3"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
	"github.com/lwasak/fv-cli/internal/ksefcache"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/store"
)

const (
	supplierNIP  = "5252248481"
	supplierName = "Zakład Łączności Źdźbło Spółka z ograniczoną odpowiedzialnością"
)

// otherInvoiceXML builds an FA(3) invoice of another issuer with one 23% line: net in
// grosze, the seller and buyer given by NIP and name.
func otherInvoiceXML(t *testing.T, number, issue string, net money.Amount, seller, sellerName, buyer, buyerName string) []byte {
	t.Helper()
	d, err := time.Parse("2006-01-02", issue)
	if err != nil {
		t.Fatal(err)
	}
	rate, err := money.ParseRate("23")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := money.Summarize([]money.Line{{Rate: rate, Net: net}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := fa3.Build(fa3.Input{
		Seller: fa3.Party{NIP: seller, Name: sellerName, AddressL1: "ul. Fabryczna 1", AddressL2: "00-001 Warszawa"},
		Buyer:  fa3.Party{NIP: buyer, Name: buyerName, AddressL1: "ul. Kupiecka 2", AddressL2: "80-001 Gdańsk"},
		Number: number, BankAccount: "PL61 1090 1014 0000 0712 1981 2874",
		IssueDate: d, DueDate: d.AddDate(0, 0, 14), CreatedAt: d.Add(9 * time.Hour),
		Lines:   []fa3.Line{{No: 1, Description: "Materiały biurowe", Unit: "szt.", Qty: 1000000, UnitPrice: net, Net: net, Rate: rate}},
		Summary: sum,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type listFx struct {
	*sendFx
	purchaseNo, saleNo string // KSeF numbers of the two invoices that are not fv's
}

// newListFx issues and sends one invoice with fv (FV/2026/10/001, accepted), and puts a sale
// made outside fv and a purchase on the fake.
func newListFx(t *testing.T) *listFx {
	t.Helper()
	fx := &listFx{sendFx: newSendFx(t, nil)}
	fx.noConfirm = true
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "100", "--send", "--yes"); code != 0 {
		t.Fatalf("issue --send: exit %d: %s", code, errs)
	}
	fx.noConfirm = false
	fx.saleNo = fx.q.Add(ksefake.QMeta{InvoiceNumber: "EXT/7", IssueDate: "2026-10-20", SellerNIP: ksefNIP, SellerName: "MOJA FIRMA",
		BuyerType: "Nip", BuyerID: supplierNIP, BuyerName: "ACME", Net: "200.00", VAT: "46.00", Gross: "246.00"},
		otherInvoiceXML(t, "EXT/7", "2026-10-20", 20000, ksefNIP, "MOJA FIRMA", supplierNIP, "ACME"))
	fx.purchaseNo = fx.q.Add(ksefake.QMeta{InvoiceNumber: "ZAK/09/15", IssueDate: "2026-09-15", SellerNIP: supplierNIP, SellerName: supplierName,
		BuyerType: "Nip", BuyerID: ksefNIP, BuyerName: "MOJA FIRMA", Net: "500.00", VAT: "115.00", Gross: "615.00"},
		otherInvoiceXML(t, "ZAK/09/15", "2026-09-15", 50000, supplierNIP, supplierName, ksefNIP, "MOJA FIRMA"))
	return fx
}

func (fx *listFx) metadataCalls() int { return fx.fake.Count("POST", "/invoices/query/metadata") }

func rowsOf(out string) [][]string {
	var rows [][]string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		rows = append(rows, strings.Fields(l))
	}
	return rows
}

func TestList_ShowsFvSaleOnceAndKSeFSalesAndPurchases(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %q", lines)
	}
	if h := strings.Join(strings.Fields(lines[0]), " "); h != "ISSUED NUMBER DIR SELLER BUYER GROSS CUR STATE" {
		t.Errorf("header %q", h)
	}
	checks := []struct{ line, dir, number, state string }{
		{lines[1], "purchase", "ZAK/09/15", "ksef"},
		{lines[2], "sale", "EXT/7", "ksef"},
		{lines[3], "sale", "FV/2026/10/001", "accepted"},
	}
	for _, c := range checks {
		f := strings.Fields(c.line)
		if f[1] != c.number || f[2] != c.dir || f[len(f)-1] != c.state {
			t.Errorf("row %q, want %s %s %s", c.line, c.number, c.dir, c.state)
		}
	}
	if strings.Count(out, "FV/2026/10/001") != 1 {
		t.Errorf("the fv invoice must show once:\n%s", out)
	}
	if !strings.Contains(lines[1], "615,00") || !strings.Contains(lines[2], "246,00") {
		t.Errorf("amounts: %q %q", lines[1], lines[2])
	}
	for _, want := range []string{"TEST: issue dates 2026-08-01 to 2026-10-31", "KSeF test: 3 invoices"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q: %q", want, errs)
		}
	}
	if fx.metadataCalls() != 2 {
		t.Errorf("%d metadata calls, want 2 (sales and purchases)", fx.metadataCalls())
	}
	// the archive is unchanged by a list
	if m := fx.meta(config.EnvTest, sendNum); m.State != store.StateAccepted {
		t.Errorf("state %s", m.State)
	}
	c, err := ksefcache.Open(fx.dataDir, config.EnvTest, ksefNIP, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.InRange("", ""); err != nil || len(got) != 3 {
		t.Errorf("cache holds %d rows (%v)", len(got), err)
	}
}

func TestList_QueryRequestIsByIssueDateWithTheRequestedRange(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list", "--month", "2026-09", "--as", "seller"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var bodies []string
	for _, r := range fx.fake.Requests() {
		if r.Path == "/invoices/query/metadata" {
			bodies = append(bodies, string(r.Body))
		}
	}
	// the month, widened by a day on each side; the rows outside it are dropped locally
	want := `{"dateRange":{"dateType":"Issue","from":"2026-08-31T00:00:00Z","to":"2026-10-01T23:59:59Z"},"subjectType":"Subject1"}`
	if len(bodies) != 1 || bodies[0] != want {
		t.Errorf("bodies = %q", bodies)
	}
}

func TestList_AsFiltersAndSavesQueries(t *testing.T) {
	fx := newListFx(t)
	_, out, _ := fx.run("list", "--as", "buyer")
	if rows := rowsOf(out); len(rows) != 2 || rows[1][1] != "ZAK/09/15" {
		t.Errorf("buyer: %q", out)
	}
	if fx.metadataCalls() != 1 {
		t.Errorf("%d calls for --as buyer", fx.metadataCalls())
	}
	_, out, _ = fx.run("list", "--as", "seller")
	if rows := rowsOf(out); len(rows) != 3 || strings.Contains(out, "ZAK/09/15") {
		t.Errorf("seller: %q", out)
	}
	if fx.metadataCalls() != 2 {
		t.Errorf("%d calls", fx.metadataCalls())
	}
}

func TestList_OfflineUsesTheCacheAndSendsNothing(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	before := len(fx.fake.Requests())
	code, out, errs := fx.run("list", "--offline")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if len(fx.fake.Requests()) != before {
		t.Errorf("--offline sent %d requests", len(fx.fake.Requests())-before)
	}
	if rows := rowsOf(out); len(rows) != 4 || !strings.Contains(out, "ZAK/09/15") || !strings.Contains(out, "EXT/7") {
		t.Errorf("out = %q", out)
	}
	if !strings.Contains(errs, "offline: archive and KSeF rows cached on") {
		t.Errorf("stderr = %q", errs)
	}
}

func TestList_OfflineWithoutCacheShowsTheArchive(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("list", "--offline")
	if code != 0 || len(rowsOf(out)) != 2 || !strings.Contains(errs, "no KSeF rows have been cached yet") {
		t.Fatalf("exit %d out %q err %q", code, out, errs)
	}
	if fx.metadataCalls() != 0 {
		t.Errorf("%d calls", fx.metadataCalls())
	}
}

func TestList_OfflineAllowsAnyRange(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list", "--offline", "--from", "2019-01-01"); code != 0 {
		t.Errorf("offline: exit %d: %s", code, errs)
	}
	code, _, errs := fx.run("list", "--from", "2019-01-01")
	if code != 1 || !strings.Contains(errs, "366 days") {
		t.Errorf("online: exit %d: %s", code, errs)
	}
	if fx.metadataCalls() != 0 {
		t.Error("a refused range must not reach KSeF")
	}
}

func TestList_NoCertificateIsNotAnError(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("--env", "demo", "list")
	if code != 0 || strings.TrimSpace(out) != "no invoices" || !strings.Contains(errs, "no certificate for demo") || !strings.Contains(errs, "fv init") {
		t.Errorf("exit %d out %q err %q", code, out, errs)
	}
	if fx.metadataCalls() != 0 {
		t.Errorf("%d calls", fx.metadataCalls())
	}
}

func TestList_RateLimitedKeepsRowsExit1AndNeverRetries(t *testing.T) {
	fx := newListFx(t)
	fx.fake.Inject("POST", "/invoices/query/metadata", 10, ksefake.Response{Status: 429, Header: map[string]string{"Retry-After": "120"},
		Body: `{"status":{"code":429,"description":"Too Many Requests","details":["limit 20/h"]}}`})
	code, out, errs := fx.run("list")
	if code != 1 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if rows := rowsOf(out); len(rows) != 2 || !strings.Contains(out, "FV/2026/10/001") {
		t.Errorf("the archive rows must still print: %q", out)
	}
	for _, want := range []string{"too many requests", "in 120 s", "20 metadata queries an hour", "--offline"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q: %q", want, errs)
		}
	}
	if fx.metadataCalls() != 1 || len(fx.sleeps) != 0 {
		t.Errorf("%d metadata calls, slept %v: a refused query must not be repeated", fx.metadataCalls(), fx.sleeps)
	}
}

func TestList_RateLimitedFallsBackToCachedRows(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	fx.fake.Inject("POST", "/invoices/query/metadata", 10, ksefake.Response{Status: 429, Header: map[string]string{"Retry-After": "5"}, Body: `{}`})
	code, out, _ := fx.run("list")
	if code != 1 || len(rowsOf(out)) != 4 || !strings.Contains(out, "ZAK/09/15") {
		t.Errorf("exit %d out %q", code, out)
	}
}

func TestList_ForbiddenSaysInvoiceRead(t *testing.T) {
	fx := newListFx(t)
	fx.fake.Handle("POST", "/invoices/query/metadata", func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Status: 403, ContentType: "application/problem+json", Body: `{"title":"Forbidden","status":403,"detail":"no"}`}
	})
	code, out, errs := fx.run("list")
	if code != 1 || !strings.Contains(errs, "InvoiceRead") || len(rowsOf(out)) != 2 {
		t.Errorf("exit %d out %q err %q", code, out, errs)
	}
}

func TestList_ServerErrorExit1(t *testing.T) {
	fx := newListFx(t)
	fx.fake.Respond("POST", "/invoices/query/metadata", 503, "down")
	code, out, errs := fx.run("list")
	if code != 1 || !strings.Contains(errs, "KSeF query failed") || len(rowsOf(out)) != 2 {
		t.Errorf("exit %d out %q err %q", code, out, errs)
	}
}

func TestList_LoginFailureExit3KeepsRows(t *testing.T) {
	fx := newListFx(t)
	if err := fx.backend.Set("fv/ksef/test/"+ksefNIP, "fv", "the wrong passphrase"); err != nil {
		t.Fatal(err)
	}
	code, out, errs := fx.run("list")
	if code != 3 || len(rowsOf(out)) != 2 || !strings.Contains(errs, "KSeF not queried") {
		t.Errorf("exit %d out %q err %q", code, out, errs)
	}
	if fx.metadataCalls() != 0 {
		t.Errorf("%d calls", fx.metadataCalls())
	}
}

func TestList_QueryBudgetStopsTheRun(t *testing.T) {
	fx := newListFx(t)
	// more than 10 pages of 250: 2600 sales in one window need 11 calls
	for i := 0; i < 2600; i++ {
		fx.q.Add(ksefake.QMeta{InvoiceNumber: "B/" + strconv.Itoa(i), IssueDate: "2026-10-10", SellerNIP: ksefNIP, BuyerID: supplierNIP, Gross: "1.00"}, nil)
	}
	code, _, errs := fx.run("list", "--as", "seller")
	if code != 1 || !strings.Contains(errs, "used its 10 calls") {
		t.Errorf("exit %d: %s", code, errs)
	}
	if fx.metadataCalls() != 10 {
		t.Errorf("%d calls, want exactly the budget of 10", fx.metadataCalls())
	}
}

func TestList_WideAddsNamesIDsKindAndKSeFNumber(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("list", "--wide")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if h := strings.Join(strings.Fields(lines[0]), " "); h != "ISSUED NUMBER DIR KIND SELLER SELLER-ID BUYER BUYER-ID GROSS CUR STATE KSEF-NUMBER" {
		t.Errorf("header %q", h)
	}
	if !strings.Contains(lines[1], supplierName) || !strings.Contains(lines[1], supplierNIP) || !strings.Contains(lines[1], fx.purchaseNo) || !strings.Contains(lines[1], " VAT ") {
		t.Errorf("purchase row %q", lines[1])
	}
	if !strings.Contains(lines[3], "accepted") || !strings.Contains(lines[3], "5260250274-") {
		t.Errorf("fv row %q", lines[3])
	}
}

func TestList_DefaultTableFits120ColumnsAndCutsByRunes(t *testing.T) {
	fx := newListFx(t)
	_, out, _ := fx.run("list")
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if n := utf8.RuneCountInString(l); n > 120 {
			t.Errorf("%d columns: %q", n, l)
		}
	}
	if !strings.Contains(out, "Zakład Łączności Źdźb…") {
		t.Errorf("the Polish name must be cut at 21 runes plus an ellipsis:\n%s", out)
	}
	if !utf8.ValidString(out) {
		t.Error("invalid UTF-8")
	}
	// the columns line up although the names have non-ASCII letters
	idx := -1
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n")[1:] {
		i := strings.Index(l, " PLN")
		if idx >= 0 && utf8.RuneCountInString(l[:i]) != idx {
			t.Errorf("columns are misaligned:\n%s", out)
		}
		idx = utf8.RuneCountInString(l[:i])
	}
}

func TestList_StripsControlCharactersFromKSeFText(t *testing.T) {
	fx := newListFx(t)
	fx.q.Add(ksefake.QMeta{InvoiceNumber: "EVIL\x1b[2J/1", IssueDate: "2026-10-21", SellerNIP: supplierNIP, SellerName: "Dostawca\x1b]0;pwned\x07\nINNY",
		BuyerType: "Nip", BuyerID: ksefNIP, Gross: "1.00"}, nil)
	_, out, errs := fx.run("list", "--wide")
	if strings.ContainsAny(out+errs, "\x1b\x07") {
		t.Errorf("control characters reached the terminal: %q", out)
	}
	if !strings.Contains(out, "Dostawca ]0;pwned INNY") {
		t.Errorf("out = %q", out)
	}
}

func TestList_StripsBidiControlsFromKSeFText(t *testing.T) {
	fx := newListFx(t)
	fx.q.Add(ksefake.QMeta{InvoiceNumber: "BIDI/1", IssueDate: "2026-10-21", SellerNIP: supplierNIP, SellerName: "ACME\u202eSp.\u2066x\u200f\u061c",
		BuyerType: "Nip", BuyerID: ksefNIP, Gross: "1476.00"}, nil)
	_, out, errs := fx.run("list", "--wide")
	if strings.ContainsAny(out+errs, "\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069\u200e\u200f\u061c") {
		t.Errorf("bidi controls reached the terminal: %q", out)
	}
	if !strings.Contains(out, "ACMESp.x") {
		t.Errorf("out = %q", out)
	}
	if got := clean("ACME\u202eSp.\u2066x\u200f"); got != "ACMESp.x" {
		t.Errorf("clean = %q", got)
	}
}

func TestList_LongInvoiceNumbersAreCutInTheDefaultTable(t *testing.T) {
	fx := newListFx(t)
	long := "FA/KUDYO1a7dddfe-610e-4843-84ba-6b887e35266e"
	fx.q.Add(ksefake.QMeta{InvoiceNumber: long, IssueDate: "2026-10-21", SellerNIP: supplierNIP, SellerName: "X",
		BuyerType: "Nip", BuyerID: ksefNIP, Gross: "1.00"}, nil)
	_, out, _ := fx.run("list")
	if strings.Contains(out, long) || !strings.Contains(out, "FA/KUDYO1a7dddfe-610e-4…") {
		t.Errorf("default table:\n%s", out)
	}
	_, out, _ = fx.run("list", "--wide")
	if !strings.Contains(out, long) {
		t.Errorf("--wide must show the whole number:\n%s", out)
	}
}

func TestList_UnknownSendIsOneRowWithAHintAndNoArchiveChange(t *testing.T) {
	fx := newListFx(t)
	fx.on.Script(ksefake.DropAfterReceive())
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "99"); code != 0 {
		t.Fatalf("issue: %s", errs)
	}
	fx.answers = []string{"y"}
	if code, _, errs := fx.run("send", sendNum2); code != 5 {
		t.Fatalf("send: exit %d: %s", code, errs)
	}
	if st := fx.stateOf(sendNum2); st != store.StateUnknown {
		t.Fatalf("state %s", st)
	}
	_, out, errs := fx.run("list", "--wide")
	if strings.Count(out, sendNum2) != 1 {
		t.Errorf("the invoice must be one row:\n%s", out)
	}
	var row string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sendNum2) {
			row = l
		}
	}
	if !strings.Contains(row, "unknown") || !strings.Contains(row, "5260250274-") {
		t.Errorf("row %q", row)
	}
	if !strings.Contains(errs, "run `fv status "+sendNum2+"`") {
		t.Errorf("stderr = %q", errs)
	}
	if st := fx.stateOf(sendNum2); st != store.StateUnknown {
		t.Errorf("list changed the archive: %s", st)
	}
}

func TestList_UnreadableAmountShowsQuestionMarkAndWarns(t *testing.T) {
	fx := newListFx(t)
	fx.fake.Handle("POST", "/invoices/query/metadata", func(r ksefake.Request) ksefake.Response {
		var body struct{ SubjectType string }
		_ = json.Unmarshal(r.Body, &body)
		if body.SubjectType != "Subject2" {
			return ksefake.Response{Body: `{"hasMore":false,"isTruncated":false,"invoices":[]}`}
		}
		m := map[string]any{"ksefNumber": fx.purchaseNo, "invoiceNumber": "ODD/1", "issueDate": "2026-09-15",
			"seller": map[string]any{"nip": supplierNIP}, "buyer": map[string]any{"identifier": map[string]any{"type": "Nip", "value": ksefNIP}},
			"netAmount": 1, "grossAmount": json.RawMessage("1.5e3"), "vatAmount": 0, "currency": "PLN", "invoiceType": "Vat",
			"formCode": map[string]any{"systemCode": "FA (3)", "schemaVersion": "1-0E", "value": "FA"}, "invoiceHash": "x"}
		b, _ := json.Marshal(map[string]any{"hasMore": false, "isTruncated": false, "invoices": []any{m}})
		return ksefake.Response{Body: string(b)}
	})
	code, out, errs := fx.run("list")
	if code != 0 || !strings.Contains(out, "?") || !strings.Contains(errs, "could not be read exactly") {
		t.Errorf("exit %d out %q err %q", code, out, errs)
	}
}

func TestList_DoesNotCacheOrQueryOtherEnvironments(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("%s", errs)
	}
	c, _ := ksefcache.Open(fx.dataDir, config.EnvDemo, ksefNIP, nil)
	if got, err := c.InRange("", ""); err != nil || len(got) != 0 {
		t.Errorf("demo cache has %d rows (%v)", len(got), err)
	}
}

func TestList_MetadataCallsAreSpaced(t *testing.T) {
	fx := newListFx(t)
	fx.sleeps = nil
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("list: %s", errs)
	}
	spaced := 0
	for _, d := range fx.sleeps {
		// the real clock runs between the calls, so the wait is a little under the spacing
		if d > 0 && d <= ksef.MetadataSpacing {
			spaced++
		}
	}
	if fx.metadataCalls() != 2 || spaced != 1 {
		t.Errorf("%d waits of %v between the two metadata calls, sleeps %v", spaced, ksef.MetadataSpacing, fx.sleeps)
	}
}

func TestList_OddKSeFRecordIsSkippedWithAWarning(t *testing.T) {
	fx := newListFx(t)
	fx.fake.Handle("POST", "/invoices/query/metadata", func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Body: `{"hasMore":false,"isTruncated":false,"invoices":[` +
			`{"ksefNumber":"not-a-number","invoiceNumber":"X","issueDate":"2026-10-01","seller":{"nip":"5252248481"},"buyer":{"identifier":{"type":"None"}},"grossAmount":1}]}`}
	})
	code, out, errs := fx.run("list")
	if code != 0 || !strings.Contains(errs, "were skipped") || !strings.Contains(out, "FV/2026/10/001") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
}

func TestList_LoginTransportOr5xxFailureIsExit1AndKeepsTheArchiveRows(t *testing.T) {
	for name, set := range map[string]func(*listFx){
		"challenge 503": func(fx *listFx) { fx.fake.Respond("POST", "/auth/challenge", 503, "down") },
		"connection reset": func(fx *listFx) {
			fx.fake.Inject("POST", "/auth/challenge", 100, ksefake.Response{Drop: true})
		},
		"signature 500": func(fx *listFx) { fx.fake.Respond("POST", "/auth/xades-signature", 500, "oops") },
		"challenge 429": func(fx *listFx) { fx.fake.Respond("POST", "/auth/challenge", 429, "slow down") },
	} {
		t.Run(name, func(t *testing.T) {
			fx := newListFx(t)
			set(fx)
			code, out, errs := fx.run("list")
			if code != 1 {
				t.Errorf("exit %d, want 1: %s", code, errs)
			}
			if !strings.Contains(errs, "KSeF not queried") || !(strings.Contains(errs, "unreachable") || strings.Contains(errs, "limiting")) {
				t.Errorf("stderr = %q", errs)
			}
			if !strings.Contains(out, "FV/2026/10/001") {
				t.Errorf("the archive rows are missing:\n%s", out)
			}
		})
	}
}

func TestList_LoginRefusalStaysExit3(t *testing.T) {
	fx := newListFx(t)
	fx.fake.Respond("POST", "/auth/xades-signature", 401, `{"title":"Unauthorized","status":401,"detail":"Brak uprawnień"}`)
	code, out, errs := fx.run("list")
	if code != 3 || !strings.Contains(out, "FV/2026/10/001") {
		t.Errorf("exit %d: %s\n%s", code, errs, out)
	}
}

func TestShow_LoginServerDownIsExit1(t *testing.T) {
	fx := newListFx(t)
	fx.fake.Respond("POST", "/auth/challenge", 503, "down")
	code, out, errs := fx.run("show", fx.purchaseNo)
	if code != 1 || out != "" || !strings.Contains(errs, "KSeF unreachable") {
		t.Errorf("exit %d out %q err %q", code, out, errs)
	}
}
