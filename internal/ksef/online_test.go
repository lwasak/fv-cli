package ksef

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

const (
	onlineAccess = "ACCESS-online-4444"
	testKeyID    = "KEYID-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
)

type onlineFixture struct {
	f   *ksefake.Fake
	on  *ksefake.Online
	c   *Client
	clk *clock
	s   *Session
}

func newOnlineFixture(t *testing.T) *onlineFixture {
	t.Helper()
	cert := credtest.New(t, credtest.Spec{RSA: true})
	f := ksefake.New(t)
	on := f.EnableOnline(ksefake.OnlineOptions{
		Key: cert.Key.(*rsa.PrivateKey), CertDER: cert.DER, KeyID: testKeyID, Bearer: onlineAccess,
	})
	fx := &onlineFixture{f: f, on: on, clk: &clock{t: t0}}
	fx.c = newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	fx.c.now = fx.clk.now
	fx.c.sleep = fx.clk.sleep
	fx.s = &Session{env: config.EnvTest, accessToken: onlineAccess}
	t.Cleanup(func() {
		if v := on.Violations(); len(v) > 0 {
			t.Errorf("the client made protocol errors: %v", v)
		}
	})
	return fx
}

func (fx *onlineFixture) open(t *testing.T) *OnlineSession {
	t.Helper()
	o, err := OpenOnlineSession(context.Background(), fx.c, fx.s, seqReader(48, 1))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

var invXML = []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Faktura><P_2>FV/2026/10/001</P_2></Faktura>\n")

func TestOnline_OpenSendPollUPOClose(t *testing.T) {
	fx := newOnlineFixture(t)
	ctx := context.Background()
	o := fx.open(t)
	if o.Ref == "" || o.ValidUntil.IsZero() {
		t.Fatalf("session = %+v", o)
	}
	ref, err := o.SendInvoice(ctx, invXML)
	if err != nil {
		t.Fatal(err)
	}
	st, err := fx.c.WaitInvoice(ctx, fx.s, o.Ref, ref, DefaultInvoiceBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Accepted() || ValidateKSeFNumber(st.KSeFNumber) != nil || st.InvoiceRef != ref || st.AcquisitionDate == "" {
		t.Fatalf("status = %+v", st)
	}
	upo, err := fx.c.InvoiceUPO(ctx, fx.s, o.Ref, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(upo, fx.on.UPOFor(ref)) || !strings.Contains(string(upo), st.KSeFNumber) {
		t.Errorf("UPO = %s", upo)
	}
	if err := o.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !fx.on.Closed(o.Ref) {
		t.Error("session not closed on the server")
	}
	got := fx.on.Invoices()
	if len(got) != 1 || !bytes.Equal(got[0].XML, invXML) {
		t.Fatalf("server stored %+v", got)
	}
	wantHash, wantSize := InvoiceHash(invXML)
	if got[0].Hash != wantHash || got[0].Size != wantSize {
		t.Errorf("hash/size = %s/%d, want %s/%d", got[0].Hash, got[0].Size, wantHash, wantSize)
	}
	// The fake unwrapped the very key the client generated.
	key, iv := fx.on.SessionKey(o.Ref)
	if !bytes.Equal(key, seqBytes(32, 1)) || !bytes.Equal(iv, seqBytes(16, 33)) {
		t.Errorf("server key %x iv %x", key, iv)
	}
}

func TestOnline_OpenRequestShape(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.open(t)
	var open ksefake.Request
	for _, r := range fx.f.Requests() {
		if r.Method == "POST" && r.Path == "/sessions/online" {
			open = r
		}
	}
	if got := open.Header.Get("Authorization"); got != "Bearer "+onlineAccess {
		t.Errorf("Authorization = %q", got)
	}
	var body map[string]map[string]string
	if err := json.Unmarshal(open.Body, &body); err != nil {
		t.Fatal(err)
	}
	if fc := body["formCode"]; fc["systemCode"] != "FA (3)" || fc["schemaVersion"] != "1-0E" || fc["value"] != "FA" {
		t.Errorf("formCode = %v", fc)
	}
	enc := body["encryption"]
	if enc["publicKeyId"] != testKeyID {
		t.Errorf("publicKeyId = %q", enc["publicKeyId"])
	}
	if iv, _ := base64.StdEncoding.DecodeString(enc["initializationVector"]); len(iv) != 16 {
		t.Errorf("iv = %d bytes", len(iv))
	}
	if k, _ := base64.StdEncoding.DecodeString(enc["encryptedSymmetricKey"]); len(k) != 256 {
		t.Errorf("wrapped key = %d bytes, want 256 (RSA-2048)", len(k))
	}
}

func TestOnline_SendRequestCarriesBothHashesAndSizes(t *testing.T) {
	fx := newOnlineFixture(t)
	o := fx.open(t)
	if _, err := o.SendInvoice(context.Background(), invXML); err != nil {
		t.Fatal(err)
	}
	var r ksefake.Request
	for _, x := range fx.f.Requests() {
		if strings.HasSuffix(x.Path, "/invoices") && x.Method == "POST" {
			r = x
		}
	}
	var b map[string]any
	if err := json.Unmarshal(r.Body, &b); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"invoiceHash", "invoiceSize", "encryptedInvoiceHash", "encryptedInvoiceSize", "encryptedInvoiceContent"} {
		if _, ok := b[k]; !ok {
			t.Errorf("request lacks %s", k)
		}
	}
	if b["offlineMode"] != false {
		t.Errorf("offlineMode = %v", b["offlineMode"])
	}
	if b["invoiceSize"].(float64) != float64(len(invXML)) {
		t.Errorf("invoiceSize = %v", b["invoiceSize"])
	}
	if enc := b["encryptedInvoiceSize"].(float64); int(enc)%16 != 0 || int(enc) <= len(invXML) {
		t.Errorf("encryptedInvoiceSize = %v", enc)
	}
	if strings.Contains(string(r.Body), "Faktura") {
		t.Error("the request body holds the plain XML")
	}
}

func TestOnline_PublicKeyIDOmittedWhenCertListHasNone(t *testing.T) {
	cert := credtest.New(t, credtest.Spec{RSA: true})
	f := ksefake.New(t)
	on := f.EnableOnline(ksefake.OnlineOptions{Key: cert.Key.(*rsa.PrivateKey), CertDER: cert.DER})
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	if _, err := OpenOnlineSession(context.Background(), c, &Session{accessToken: "x"}, seqReader(48, 1)); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.Requests() {
		if r.Path == "/sessions/online" && strings.Contains(string(r.Body), "publicKeyId") {
			t.Errorf("publicKeyId sent without one in the list: %s", r.Body)
		}
	}
	_ = on
}

func TestOnline_SymmetricKeyCertReturnsIDAndCachesIt(t *testing.T) {
	fx := newOnlineFixture(t)
	cert, id, err := fx.c.SymmetricKeyCert(context.Background())
	if err != nil || cert == nil || id != testKeyID {
		t.Fatalf("SymmetricKeyCert = %v, %q, %v", cert != nil, id, err)
	}
	_, id2, err := fx.c.SymmetricKeyCert(context.Background())
	if err != nil || id2 != testKeyID {
		t.Fatalf("cached: %q, %v", id2, err)
	}
	if n := fx.f.Count("GET", "/security/public-key-certificates"); n != 1 {
		t.Errorf("certificate list fetched %d times", n)
	}
}

func TestPublicKeys_OldCacheWithoutKeyIDStillParses(t *testing.T) {
	// A cache written before publicKeyId existed.
	var now = t0
	dir := t.TempDir()
	f := ksefake.New(t)
	e, _ := entry(t, t0.Add(-time.Hour), t0.Add(time.Hour), UsageSymmetricKeyEncryption)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, e))
	c := clientAt(t, f, config.EnvTest, dir, &now)
	if _, id, err := c.SymmetricKeyCert(context.Background()); err != nil || id != "" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
}

func TestOnline_NonRSACertificateFailsAndWipesKey(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, goodEntry(t))) // P-256
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	_, err := OpenOnlineSession(context.Background(), c, &Session{accessToken: "x"}, seqReader(48, 1))
	if err == nil || !strings.Contains(err.Error(), "RSA") {
		t.Fatalf("err = %v", err)
	}
	if n := f.Count("POST", "/sessions/online"); n != 0 {
		t.Errorf("a session was opened with a non-RSA certificate (%d requests)", n)
	}
}

func TestOnline_CloseWipesKeyAndBlocksFurtherSends(t *testing.T) {
	fx := newOnlineFixture(t)
	o := fx.open(t)
	key := o.key
	if err := o.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key.key, make([]byte, 32)) || !bytes.Equal(key.iv, make([]byte, 16)) {
		t.Error("key material not wiped by Close")
	}
	if _, err := o.SendInvoice(context.Background(), invXML); err == nil {
		t.Error("SendInvoice succeeded on a closed session")
	}
	before := fx.f.Count("POST", "/sessions/online/"+o.Ref+"/close")
	if err := o.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if fx.f.Count("POST", "/sessions/online/"+o.Ref+"/close") != before {
		t.Error("second Close sent another request")
	}
}

func TestOnline_CloseWipesKeyEvenWhenRequestFails(t *testing.T) {
	fx := newOnlineFixture(t)
	o := fx.open(t)
	fx.f.Inject("POST", "/sessions/online/"+o.Ref+"/close", 1, ksefake.Response{Drop: true})
	if err := o.Close(context.Background()); err == nil {
		t.Fatal("Close did not report the transport error")
	}
	if !bytes.Equal(o.key.key, make([]byte, 32)) {
		t.Error("key not wiped after a failed close")
	}
}

func TestOnline_OpenFailureWipesKeyAndIsNotRetried(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.f.Inject("POST", "/sessions/online", 5, ksefake.Response{Status: 500, Body: "boom"})
	_, err := OpenOnlineSession(context.Background(), fx.c, fx.s, seqReader(48, 1))
	if err == nil {
		t.Fatal("open succeeded")
	}
	if n := fx.f.Count("POST", "/sessions/online"); n != 1 {
		t.Errorf("POST /sessions/online sent %d times, want 1", n)
	}
}

func TestOnline_KeyMaterialNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	fx := newOnlineFixture(t)
	// A logger whose output goes through the process-wide redactor, as in production.
	fx.c.log = redactedLogger(&buf)
	o := fx.open(t)
	ref, err := o.SendInvoice(context.Background(), invXML)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.c.WaitInvoice(context.Background(), fx.s, o.Ref, ref, DefaultInvoiceBudget); err != nil {
		t.Fatal(err)
	}
	_ = o.Close(context.Background())
	out := buf.String()
	if out == "" {
		t.Fatal("nothing logged")
	}
	for _, b := range [][]byte{seqBytes(32, 1), seqBytes(16, 33)} {
		if strings.Contains(out, base64.StdEncoding.EncodeToString(b)) {
			t.Error("base64 key material in the log")
		}
	}
}

func TestSend_DropAfterPostIsTransportErrorNotRejection(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.DropAfterReceive())
	o := fx.open(t)
	_, err := o.SendInvoice(context.Background(), invXML)
	if err == nil {
		t.Fatal("no error for a dropped connection")
	}
	if NotAccepted(err) {
		t.Errorf("a dropped connection was classified as a definite rejection: %v", err)
	}
	if n := fx.f.Count("POST", "/sessions/online/"+o.Ref+"/invoices"); n != 1 {
		t.Errorf("POST /invoices sent %d times, want 1 (never retried)", n)
	}
	if len(fx.on.Invoices()) != 1 {
		t.Error("the fake should hold the invoice it received")
	}
}

func TestSend_5xxIsNotAProvenRejection(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.PostError(503))
	o := fx.open(t)
	_, err := o.SendInvoice(context.Background(), invXML)
	var ke *Error
	if !errors.As(err, &ke) || ke.HTTPStatus != 503 || NotAccepted(err) {
		t.Fatalf("err = %v (NotAccepted=%v)", err, NotAccepted(err))
	}
	if n := fx.f.Count("POST", "/sessions/online/"+o.Ref+"/invoices"); n != 1 {
		t.Errorf("POST sent %d times", n)
	}
}

func TestSend_4xxWithKSeFBodyIsNotAccepted(t *testing.T) {
	fx := newOnlineFixture(t)
	fx.on.Script(ksefake.PostError(400))
	o := fx.open(t)
	_, err := o.SendInvoice(context.Background(), invXML)
	if !NotAccepted(err) {
		t.Fatalf("err = %v, want NotAccepted", err)
	}
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != 400 || ke.Description != "scripted error" {
		t.Errorf("decoded = %+v", ke)
	}
	if len(fx.on.Invoices()) != 0 {
		t.Error("a rejected POST stored an invoice")
	}
}

func TestSend_RateLimitedPostIsNeverRetried(t *testing.T) {
	fx := newOnlineFixture(t)
	o := fx.open(t)
	fx.f.Inject("POST", "/sessions/online/"+o.Ref+"/invoices", 1,
		ksefake.Response{Status: 429, Body: `{"status":{"code":429,"description":"Too Many Requests","details":["wait"]}}`, Header: map[string]string{"Retry-After": "1"}})
	_, err := o.SendInvoice(context.Background(), invXML)
	var ke *Error
	if !errors.As(err, &ke) || ke.HTTPStatus != 429 {
		t.Fatalf("err = %v", err)
	}
	if n := fx.f.Count("POST", "/sessions/online/"+o.Ref+"/invoices"); n != 1 || len(fx.clk.sleeps) != 0 {
		t.Errorf("POSTs = %d, sleeps = %v", n, fx.clk.sleeps)
	}
}

func TestOnline_NoTokenNoSession(t *testing.T) {
	fx := newOnlineFixture(t)
	if _, err := OpenOnlineSession(context.Background(), fx.c, nil, seqReader(48, 1)); err == nil {
		t.Error("opened a session without authentication")
	}
	if n := fx.f.Count("POST", "/sessions/online"); n != 0 {
		t.Errorf("%d requests", n)
	}
}

func TestOnline_AlignedXMLGetsFullPaddingBlock(t *testing.T) {
	fx := newOnlineFixture(t)
	o := fx.open(t)
	aligned := bytes.Repeat([]byte("x"), 64)
	if _, err := o.SendInvoice(context.Background(), aligned); err != nil {
		t.Fatal(err)
	}
	if got := fx.on.Invoices(); len(got) != 1 || !bytes.Equal(got[0].XML, aligned) {
		t.Fatalf("stored %+v", got)
	}
	for _, r := range fx.f.Requests() {
		if strings.HasSuffix(r.Path, "/invoices") && r.Method == "POST" {
			var b struct{ EncryptedInvoiceSize int }
			_ = json.Unmarshal(r.Body, &b)
			if b.EncryptedInvoiceSize != 80 {
				t.Errorf("encrypted size = %d, want 80", b.EncryptedInvoiceSize)
			}
		}
	}
}

func ExampleInvoiceHash() {
	h, n := InvoiceHash([]byte("abc"))
	fmt.Println(h, n)
	// Output: ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0= 3
}
