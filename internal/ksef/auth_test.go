package ksef

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
	"github.com/lwasak/fv-cli/internal/logging"
)

const testNIP = "5833281095"

// testCred signs with a generated P-256 key; it satisfies Credential.
type testCred struct {
	c *credtest.Cert
}

func (t testCred) Certificate() *x509.Certificate { return t.c.Cert }

func (t testCred) Sign(digest []byte, _ crypto.Hash) ([]byte, error) {
	k := t.c.Key.(*ecdsa.PrivateKey)
	r, s, err := ecdsa.Sign(rand.Reader, k, digest)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 64)
	r.FillBytes(out[:32])
	s.FillBytes(out[32:])
	return out, nil
}

func bigFrom(b []byte) *big.Int { return new(big.Int).SetBytes(b) }

type clock struct {
	mu     sync.Mutex
	t      time.Time
	sleeps []time.Duration
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.t = c.t.Add(d)
	return nil
}

type authFixture struct {
	f      *ksefake.Fake
	c      *Client
	clk    *clock
	cred   testCred
	dir    string
	signed []byte
	polls  int
}

const (
	authTok    = "AUTHTOKEN-secret-1111"
	accessTok  = "ACCESSTOKEN-secret-2222"
	refreshTok = "REFRESHTOKEN-secret-3333"
)

// statuses are the poll answers in order; the last one repeats.
func newAuthFixture(t *testing.T, spec credtest.Spec, statuses ...string) *authFixture {
	t.Helper()
	fx := &authFixture{f: ksefake.New(t), clk: &clock{t: t0}, dir: t.TempDir()}
	fx.cred = testCred{credtest.New(t, spec)}
	fx.f.Respond("GET", "/security/public-key-certificates", 200, body(t, goodEntry(t)))
	fx.f.Respond("POST", "/auth/challenge", 200, `{"challenge":"20261030-CR-CHALLENGE","timestamp":"2026-10-30T12:00:00Z","timestampMs":1}`)
	fx.f.Handle("POST", "/auth/xades-signature", func(r ksefake.Request) ksefake.Response {
		fx.signed = r.Body
		return ksefake.Response{Status: 202, Body: `{"referenceNumber":"REF-1","authenticationToken":{"token":"` + authTok + `","validUntil":"2026-10-30T12:10:00Z"}}`}
	})
	fx.f.Handle("GET", "/auth/REF-1", func(ksefake.Request) ksefake.Response {
		i := fx.polls
		fx.polls++
		if i >= len(statuses) {
			i = len(statuses) - 1
		}
		return ksefake.Response{Status: 200, Body: statuses[i]}
	})
	fx.f.Respond("POST", "/auth/token/redeem", 200, `{"accessToken":{"token":"`+accessTok+`","validUntil":"2026-10-30T12:15:00Z"},"refreshToken":{"token":"`+refreshTok+`","validUntil":"2026-11-30T12:00:00Z"}}`)
	fx.c = newTestClient(t, fx.f, config.EnvDemo, fx.dir, nil)
	fx.c.now = fx.clk.now
	fx.c.sleep = fx.clk.sleep
	return fx
}

func status(code int, desc string) string {
	return fmt.Sprintf(`{"status":{"code":%d,"description":%q}}`, code, desc)
}

func TestAuthenticate_FullFlowHappyPath(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{}, status(100, "w toku"), status(200, "ok"))
	s, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP)
	if err != nil {
		t.Fatal(err)
	}
	if s.AccessToken() != accessTok || s.RefreshToken() != refreshTok {
		t.Errorf("tokens not returned")
	}
	if !s.ValidUntil().Equal(time.Date(2026, 10, 30, 12, 15, 0, 0, time.UTC)) {
		t.Errorf("validUntil = %v", s.ValidUntil())
	}
	var doc struct {
		Challenge string `xml:"Challenge"`
		Nip       string `xml:"ContextIdentifier>Nip"`
	}
	if err := xml.Unmarshal(fx.signed, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Challenge != "20261030-CR-CHALLENGE" || doc.Nip != testNIP {
		t.Errorf("signed body = %+v", doc)
	}
	// The signature over SignedInfo verifies with the certificate's key.
	str := string(fx.signed)
	i, j := strings.Index(str, "<ds:SignedInfo"), strings.Index(str, "</ds:SignedInfo>")
	vi, vj := strings.Index(str, "<ds:SignatureValue>"), strings.Index(str, "</ds:SignatureValue>")
	if i < 0 || j < 0 || vi < 0 || vj < 0 {
		t.Fatalf("signature elements missing in %s", str)
	}
	sig, err := base64.StdEncoding.DecodeString(str[vi+len("<ds:SignatureValue>") : vj])
	if err != nil || len(sig) != 64 {
		t.Fatalf("signature value: %v len %d", err, len(sig))
	}
	h := sha256.Sum256([]byte(str[i : j+len("</ds:SignedInfo>")]))
	pub := fx.cred.c.Cert.PublicKey.(*ecdsa.PublicKey)
	if !ecdsa.Verify(pub, h[:], bigFrom(sig[:32]), bigFrom(sig[32:])) {
		t.Error("XAdES signature does not verify")
	}
	// Polling and redeem use the authentication token; no other call carries a token.
	for _, r := range fx.f.Requests() {
		got := r.Header.Get("Authorization")
		switch r.Path {
		case "/auth/REF-1", "/auth/token/redeem":
			if got != "Bearer "+authTok {
				t.Errorf("%s Authorization = %q", r.Path, got)
			}
		default:
			if got != "" {
				t.Errorf("%s carries Authorization", r.Path)
			}
		}
	}
	if n := fx.f.Count("POST", "/auth/token/redeem"); n != 1 {
		t.Errorf("redeem count = %d", n)
	}
}

func TestAuthenticate_PollBackoffSchedule(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{},
		status(100, "w"), status(100, "w"), status(100, "w"), status(100, "w"), status(200, "ok"))
	if _, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	if fmt.Sprint(fx.clk.sleeps) != fmt.Sprint(want) {
		t.Errorf("sleeps = %v, want %v", fx.clk.sleeps, want)
	}
	if fx.polls != 5 {
		t.Errorf("polls = %d", fx.polls)
	}
}

func TestAuthenticate_PollTimeoutAt90s(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{}, status(100, "w"))
	_, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v", err)
	}
	var total time.Duration
	for _, d := range fx.clk.sleeps {
		total += d
	}
	if total < 90*time.Second || total > 94*time.Second {
		t.Errorf("slept %v in total", total)
	}
	if fx.f.Count("POST", "/auth/token/redeem") != 0 {
		t.Error("redeem called after timeout")
	}
}

func TestAuthenticate_PollStatus450IsAuthError(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{}, status(450, "Uwierzytelnienie zakończone niepowodzeniem"))
	_, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != 450 || !strings.Contains(ke.Description, "niepowodzeniem") {
		t.Errorf("err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "KSeF error 450:") {
		t.Errorf("message = %q", err.Error())
	}
}

func TestAuthenticate_RedeemNeverRetriedOn500(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{}, status(200, "ok"))
	fx.f.Respond("POST", "/auth/token/redeem", 500, `{"exception":{"exceptionDetailList":[{"exceptionCode":500,"exceptionDescription":"boom"}]}}`)
	_, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	if n := fx.f.Count("POST", "/auth/token/redeem"); n != 1 {
		t.Errorf("redeem requests = %d, want 1", n)
	}
}

func TestAuthenticate_RedeemNeverRetriedOnConnectionReset(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{}, status(200, "ok"))
	fx.f.Handle("POST", "/auth/token/redeem", func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Drop: true}
	})
	_, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	if n := fx.f.Count("POST", "/auth/token/redeem"); n != 1 {
		t.Errorf("redeem requests = %d, want 1", n)
	}
}

func TestAuthenticate_PESELCertificateSendsSellerNIPAsContext(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{CommonName: "Jan (uwierzytelnienie)", SerialNumber: "PNOPL-44051401359"}, status(200, "ok"))
	if _, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Nip string `xml:"ContextIdentifier>Nip"`
	}
	if err := xml.Unmarshal(fx.signed, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Nip != testNIP {
		t.Errorf("context NIP = %q, want %q", doc.Nip, testNIP)
	}
	if strings.Contains(string(fx.signed), "44051401359") {
		t.Error("PESEL leaked into the signed body")
	}
}

func TestAuthenticate_PESELCertificateRefusedByKSeFIsAuthError(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{SerialNumber: "PNOPL-44051401359"}, status(100, "w"), status(415, "Brak uprawnień do kontekstu"))
	_, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != 415 || ke.Description != "Brak uprawnień do kontekstu" {
		t.Errorf("err = %v", err)
	}
}

func TestAuthenticate_TokensRegisteredWithRedactor(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{}, status(200, "ok"))
	if _, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{authTok, accessTok, refreshTok} {
		if got := logging.Default.Redact("x " + tok); strings.Contains(got, tok) {
			t.Errorf("token %q not registered with the redactor", tok[:6])
		}
	}
}

func TestSession_DoesNotPrintTokens(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{}, status(200, "ok"))
	s, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP)
	if err != nil {
		t.Fatal(err)
	}
	out := fmt.Sprintf("%v %+v %#v %s", s, s, s, s)
	for _, tok := range []string{accessTok, refreshTok, authTok} {
		if strings.Contains(out, tok) {
			t.Errorf("formatted session contains a token: %s", out)
		}
	}
}

func TestAuthenticate_WritesNoTokenToDisk(t *testing.T) {
	fx := newAuthFixture(t, credtest.Spec{}, status(200, "ok"))
	if _, err := Authenticate(context.Background(), fx.c, fx.cred, testNIP); err != nil {
		t.Fatal(err)
	}
	var files []string
	err := filepath.WalkDir(fx.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files = append(files, filepath.Base(p))
		data, _ := os.ReadFile(p)
		for _, tok := range []string{authTok, accessTok, refreshTok} {
			if strings.Contains(string(data), tok) {
				t.Errorf("%s contains a token", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(files) != "[public-key-certificates.json]" {
		t.Errorf("data dir files = %v", files)
	}
}
