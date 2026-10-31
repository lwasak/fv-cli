package ksef

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

func sendOne(t *testing.T, fx *onlineFixture, xml []byte) (*OnlineSession, string) {
	t.Helper()
	o := fx.open(t)
	ref, err := o.SendInvoice(context.Background(), xml)
	if err != nil {
		t.Fatal(err)
	}
	return o, ref
}

func TestWait_BackoffScheduleAndPendingThenAccepted(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.Accept().AfterPolls(5))
	o, ref := sendOne(t, fx, invXML)
	st, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, DefaultInvoiceBudget)
	if err != nil || !st.Accepted() {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second}
	if fmt.Sprint(fx.clk.sleeps) != fmt.Sprint(want) {
		t.Errorf("sleeps = %v, want %v", fx.clk.sleeps, want)
	}
	if n := fx.f.Count("GET", "/sessions/"+o.Ref+"/invoices/"+ref); n != 6 {
		t.Errorf("polls = %d, want 6", n)
	}
}

func TestWait_BudgetExhaustedReturnsStillProcessing(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.Accept().AfterPolls(1000))
	o, ref := sendOne(t, fx, invXML)
	st, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, 20*time.Second)
	if !errors.Is(err, ErrStillProcessing) {
		t.Fatalf("err = %v", err)
	}
	if !st.Pending() {
		t.Errorf("last status = %+v, want pending", st)
	}
	var total time.Duration
	for _, d := range fx.clk.sleeps {
		total += d
	}
	if total < 20*time.Second || total > 24*time.Second {
		t.Errorf("slept %v for a 20 s budget", total)
	}
}

func TestWait_BudgetCountsInjectedSleepsEvenIfClockStands(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.Accept().AfterPolls(1000))
	o, ref := sendOne(t, fx, invXML)
	var slept time.Duration
	fx.c.now = func() time.Time { return t0 } // a frozen clock
	fx.c.sleep = func(_ context.Context, d time.Duration) error { slept += d; return nil }
	_, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, 10*time.Second)
	if !errors.Is(err, ErrStillProcessing) || slept < 10*time.Second {
		t.Fatalf("err = %v, slept %v", err, slept)
	}
}

func TestWait_RejectedIsFinalWithDetails(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.Reject(450, "", "P_1 jest z przyszłości", "brak NIP"))
	o, ref := sendOne(t, fx, invXML)
	st, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, DefaultInvoiceBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Rejected() || st.Code != 450 || st.Description == "" || len(st.Details) != 2 || st.KSeFNumber != "" {
		t.Fatalf("st = %+v", st)
	}
}

func TestWait_DuplicateCarriesOriginalNumber(t *testing.T) {
	fx := newOnlineFixture(t)
	orig := "5833281095-20260101-0100001AF629-AF"
	fx.on.Script(ksefake.Duplicate(orig))
	o, ref := sendOne(t, fx, invXML)
	st, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, DefaultInvoiceBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Duplicate() || !st.Rejected() || st.OriginalKSeFNumber != orig || st.OriginalSessionRef == "" {
		t.Fatalf("st = %+v", st)
	}
}

func TestWait_FakeDetectsRealDuplicateByHash(t *testing.T) {
	fx := newOnlineFixture(t)
	o, ref := sendOne(t, fx, invXML)
	first, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, DefaultInvoiceBudget)
	if err != nil || !first.Accepted() {
		t.Fatal(first, err)
	}
	ref2, err := o.SendInvoice(context.Background(), invXML)
	if err != nil {
		t.Fatal(err)
	}
	st, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref2, DefaultInvoiceBudget)
	if err != nil || !st.Duplicate() || st.OriginalKSeFNumber != first.KSeFNumber {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
}

func TestWait_500And550KeepPolling(t *testing.T) {
	fx := newOnlineFixture(t)
	o, ref := sendOne(t, fx, invXML)
	path := "/sessions/" + o.Ref + "/invoices/" + ref
	fx.f.Inject("GET", path, 2, ksefake.Response{Status: 200,
		Body: `{"ordinalNumber":1,"referenceNumber":"` + ref + `","invoiceHash":"x","invoicingDate":"2026-10-30T12:00:00Z","status":{"code":550,"description":"anulowana"}}`})
	st, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, DefaultInvoiceBudget)
	if err != nil || !st.Accepted() {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
	if n := fx.f.Count("GET", path); n != 3 {
		t.Errorf("polls = %d, want 3", n)
	}
}

func TestWait_TransportErrorAndServerErrorCountAsAttempts(t *testing.T) {
	fx := newOnlineFixture(t)
	o, ref := sendOne(t, fx, invXML)
	path := "/sessions/" + o.Ref + "/invoices/" + ref
	fx.f.Inject("GET", path, 1, ksefake.Response{Drop: true})
	fx.f.Inject("GET", path, 1, ksefake.Response{Status: 503, Body: "busy", ContentType: "text/plain"})
	st, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, DefaultInvoiceBudget)
	if err != nil || !st.Accepted() {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
}

func TestWait_4xxEndsWithError(t *testing.T) {
	fx := newOnlineFixture(t)
	o, _ := sendOne(t, fx, invXML)
	_, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, "no-such-invoice", DefaultInvoiceBudget)
	var ke *Error
	if !errors.As(err, &ke) || ke.HTTPStatus != 400 {
		t.Fatalf("err = %v", err)
	}
}

func TestWait_ContextCancelStops(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.Accept().AfterPolls(1000))
	o, ref := sendOne(t, fx, invXML)
	ctx, cancel := context.WithCancel(context.Background())
	fx.c.sleep = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	_, err := fx.c.WaitInvoice(ctx, fx.s, o.Ref, ref, DefaultInvoiceBudget)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvoiceStatus_BadKSeFNumberIsRejectedBeforeUse(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.Outcome{KSeFNumber: "5833281095-20260101-0100001AF629-00"}) // wrong CRC
	o, ref := sendOne(t, fx, invXML)
	st, err := fx.c.InvoiceStatus(context.Background(), fx.s, o.Ref, ref)
	if !errors.Is(err, ErrBadKSeFNumber) || st.KSeFNumber != "" {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
	if _, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, DefaultInvoiceBudget); !errors.Is(err, ErrBadKSeFNumber) {
		t.Fatalf("WaitInvoice err = %v", err)
	}
}

func TestInvoiceStatus_RejectionBodyGoesToRedactedLog(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.Reject(450, "", "szczegół-do-logu"))
	var buf strings.Builder
	fx.c.log = redactedLogger(&buf)
	o, ref := sendOne(t, fx, invXML)
	if _, err := fx.c.InvoiceStatus(context.Background(), fx.s, o.Ref, ref); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "szczegół-do-logu") {
		t.Errorf("the rejection body is not in the log: %s", buf.String())
	}
}

func TestFind_ByHashAcrossContinuationPages(t *testing.T) {
	fx := newOnlineFixture(t)
	o := fx.open(t)
	var wantRef, wantHash string
	for i := range 105 {
		xml := []byte(fmt.Sprintf("<Faktura><N>%d</N></Faktura>", i))
		ref, err := o.SendInvoice(context.Background(), xml)
		if err != nil {
			t.Fatal(err)
		}
		if i == 103 {
			wantRef = ref
			wantHash, _ = InvoiceHash(xml)
		}
	}
	st, err := fx.c.FindInvoiceByHash(context.Background(), fx.s, o.Ref, wantHash)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || st.InvoiceRef != wantRef || !st.Accepted() || ValidateKSeFNumber(st.KSeFNumber) != nil {
		t.Fatalf("found %+v, want ref %s", st, wantRef)
	}
	var lists int
	for _, r := range fx.f.Requests() {
		if r.Method == "GET" && r.Path == "/sessions/"+o.Ref+"/invoices" {
			lists++
			if !strings.Contains(r.Query, "pageSize=100") {
				t.Errorf("query = %q", r.Query)
			}
		}
	}
	if lists != 2 {
		t.Errorf("list requests = %d, want 2 (continuation token followed)", lists)
	}
}

func TestFind_NoMatchIsNil(t *testing.T) {
	fx := newOnlineFixture(t)
	o, _ := sendOne(t, fx, invXML)
	st, err := fx.c.FindInvoiceByHash(context.Background(), fx.s, o.Ref, "nomatch")
	if err != nil || st != nil {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
}

func TestFind_LostResponseInvoiceIsFound(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.DropAfterReceive())
	o := fx.open(t)
	if _, err := o.SendInvoice(context.Background(), invXML); err == nil {
		t.Fatal("expected a transport error")
	}
	h, _ := InvoiceHash(invXML)
	st, err := fx.c.FindInvoiceByHash(context.Background(), fx.s, o.Ref, h)
	if err != nil || st == nil || !st.Accepted() {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
}

func TestFind_LostRequestIsNotFound(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.DropBeforeReceive())
	o := fx.open(t)
	if _, err := o.SendInvoice(context.Background(), invXML); err == nil {
		t.Fatal("expected a transport error")
	}
	h, _ := InvoiceHash(invXML)
	st, err := fx.c.FindInvoiceByHash(context.Background(), fx.s, o.Ref, h)
	if err != nil || st != nil {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
}

func TestSessionStatus_OpenClosedAndEmpty(t *testing.T) {
	fx := newOnlineFixture(t)
	ctx := context.Background()
	o, _ := sendOne(t, fx, invXML)
	st, err := fx.c.SessionStatus(ctx, fx.s, o.Ref)
	if err != nil || st.Code != 100 || st.Closed() || st.InvoiceCount != 1 || st.ValidUntil.IsZero() {
		t.Fatalf("open: %+v, %v", st, err)
	}
	if err := o.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = fx.c.SessionStatus(ctx, fx.s, o.Ref)
	if st.Code != 200 || !st.Closed() || st.SuccessfulInvoiceCount != 1 || st.FailedInvoiceCount != 0 {
		t.Errorf("closed: %+v", st)
	}
	empty := fx.open(t)
	_ = empty.Close(ctx)
	st, _ = fx.c.SessionStatus(ctx, fx.s, empty.Ref)
	if st.Code != 440 || st.InvoiceCount != 0 || !st.Closed() {
		t.Errorf("empty closed: %+v", st)
	}
}

func TestUPO_SendsAcceptXMLAndNeverFollowsDownloadURL(t *testing.T) {
	fx := newOnlineFixture(t)
	o, ref := sendOne(t, fx, invXML)
	if _, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, DefaultInvoiceBudget); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.c.InvoiceUPO(context.Background(), fx.s, o.Ref, ref); err != nil {
		t.Fatal(err)
	}
	for _, r := range fx.f.Requests() {
		if strings.HasSuffix(r.Path, "/upo") {
			if got := r.Header.Get("Accept"); got != "application/xml" {
				t.Errorf("Accept = %q", got)
			}
			if r.Header.Get("Authorization") != "Bearer "+onlineAccess {
				t.Error("UPO request without the bearer token")
			}
		}
		if r.Path == "/"+ref || strings.Contains(r.Path, "upo.invalid") {
			t.Errorf("followed the download URL: %s", r.Path)
		}
	}
}

func TestUPO_ErrorsAndEmptyBody(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.Reject(450, ""))
	o, ref := sendOne(t, fx, invXML)
	if _, err := fx.c.InvoiceUPO(context.Background(), fx.s, o.Ref, ref); err == nil {
		t.Error("UPO of a rejected invoice succeeded")
	}
	fx.on.SetUPO(ksefake.Response{Status: 200, ContentType: "application/xml"})
	if _, err := fx.c.InvoiceUPO(context.Background(), fx.s, o.Ref, ref); err == nil {
		t.Error("an empty UPO was accepted")
	}
}

func TestStatus_RateLimitedGETRetriesWithRetryAfter(t *testing.T) {
	fx := newOnlineFixture(t)
	o, ref := sendOne(t, fx, invXML)
	path := "/sessions/" + o.Ref + "/invoices/" + ref
	fx.f.Inject("GET", path, 2, ksefake.Response{Status: 429, Header: map[string]string{"Retry-After": "7"},
		Body: `{"status":{"code":429,"description":"Too Many Requests","details":["Przekroczono limit"]}}`})
	st, err := fx.c.InvoiceStatus(context.Background(), fx.s, o.Ref, ref)
	if err != nil || !st.Accepted() {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
	if fmt.Sprint(fx.clk.sleeps) != fmt.Sprint([]time.Duration{7 * time.Second, 7 * time.Second}) {
		t.Errorf("sleeps = %v", fx.clk.sleeps)
	}
}

func TestSessionStatus_NotFoundIs4xx(t *testing.T) {
	fx := newOnlineFixture(t)
	_, err := fx.c.SessionStatus(context.Background(), fx.s, "nope")
	var ke *Error
	if !errors.As(err, &ke) || ke.HTTPStatus != 400 {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateKSeFNumber(t *testing.T) {
	good := []string{
		"5265877635-20250826-0100001AF629-AF", // example from faktury/numer-ksef.md
		"5833281095-20261030-0100001AF62A-" + fmt.Sprintf("%02X", crc8("5833281095-20261030-0100001AF62A")),
	}
	for _, s := range good {
		if err := ValidateKSeFNumber(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	bad := []string{
		"", "x",
		"5265877635-20250826-0100001AF629-AE",    // wrong CRC
		"5265877635-20250826-0100001af629-AF",    // lower case
		"0265877635-20250826-0100001AF629-AF",    // NIP starts with 0
		"5265877635-20251326-0100001AF629-AF",    // month 13
		"5265877635-20250826-0100001AF629-AF ",   // trailing space
		"5265877635-20250826-0100001AF629AF",     // no dash
		"5265877635-20250826-0100001AF629-AFF",   // too long
		"5265877635-20250826-0100001AF62G-AF",    // non-hex
		"../5265877635-20250826-0100001AF629-AF", // path traversal
		"5265877635-19990826-0100001AF629-AF",    // year before 2020
		"526587763-20250826-0100001AF629-AF",     // 9 digit NIP
		"5265877635-20250826-0100001AF629-AF\n",  // newline
	}
	for _, s := range bad {
		err := ValidateKSeFNumber(s)
		if err == nil || !errors.Is(err, ErrBadKSeFNumber) {
			t.Errorf("%q: err = %v, want ErrBadKSeFNumber", s, err)
		}
	}
	// The 36 character KSeF 1.0 form passes the pattern (no CRC is defined for it).
	if err := ValidateKSeFNumber("5265877635-20250826-010000-1AF629-AF"); err != nil {
		t.Errorf("36 character form: %v", err)
	}
}

func TestSessionStatus_MissingFieldsAreNotClosedNorEmpty(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.f.Inject("GET", "/sessions/", 1, ksefake.Response{Status: 200, Body: `{}`})
	st, err := fx.c.SessionStatus(context.Background(), fx.s, "any")
	if err != nil || st.Closed() || st.CountKnown {
		t.Fatalf("%+v %v", st, err)
	}
	for code, want := range map[int]bool{100: false, 0: false, 170: true, 200: true, 415: true, 440: true, 445: true, 999: false} {
		if got := (SessionStatus{Code: code}).Closed(); got != want {
			t.Errorf("Closed(%d) = %v", code, got)
		}
	}
}
