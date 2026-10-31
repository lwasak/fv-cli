package ksef

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

func TestBaseURL_FixedPerEnvironment(t *testing.T) {
	tests := []struct {
		env  config.Env
		want string
	}{
		{config.EnvTest, "https://api-test.ksef.mf.gov.pl/v2"},
		{config.EnvDemo, "https://api-demo.ksef.mf.gov.pl/v2"},
		{config.EnvProd, "https://api.ksef.mf.gov.pl/v2"},
	}
	for _, tc := range tests {
		got := BaseURL(tc.env)
		if got != tc.want {
			t.Errorf("BaseURL(%s) = %q, want %q", tc.env, got, tc.want)
		}
		if !strings.HasPrefix(got, "https://") {
			t.Errorf("BaseURL(%s) = %q is not https", tc.env, got)
		}
	}
	if got := BaseURL("other"); got != "" {
		t.Errorf("BaseURL(other) = %q, want empty", got)
	}
}

func TestHTTPClient_MinTLS12AndSystemRoots(t *testing.T) {
	hc := NewHTTPClient()
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", hc.Transport)
	}
	cfg := tr.TLSClientConfig
	if cfg == nil {
		t.Fatal("no TLS client config")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %#x, want TLS 1.2", cfg.MinVersion)
	}
	if cfg.RootCAs != nil {
		t.Error("RootCAs set; the system store must be used")
	}
	if cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify is set")
	}
}

func TestHTTPClient_UntrustedServerFails(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/ping", 200, "{}")
	_, err := NewHTTPClient().Get(f.URL + "/ping")
	if err == nil {
		t.Fatal("request to an untrusted server succeeded")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Errorf("error %q does not mention the certificate", err)
	}
	if n := len(f.Requests()); n != 0 {
		t.Errorf("server saw %d requests", n)
	}
}

func TestHTTPClient_TLS11ServerRefused(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}
	srv.StartTLS()
	defer srv.Close()

	hc := NewHTTPClient()
	tr := hc.Transport.(*http.Transport).Clone()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tr.TLSClientConfig.RootCAs = pool // trust the test server so only the protocol version can fail
	hc.Transport = tr
	_, err := hc.Get(srv.URL)
	if err == nil {
		t.Fatal("TLS 1.1 server was accepted")
	}
	if !strings.Contains(err.Error(), "protocol version") && !strings.Contains(err.Error(), "tls:") {
		t.Errorf("unexpected error: %v", err)
	}
}

func newTestClient(t *testing.T, f *ksefake.Fake, env config.Env, dataDir string, log *slog.Logger) *Client {
	t.Helper()
	if log == nil {
		log = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	}
	return NewClientAt(env, f.URL, f.Client(), dataDir, log)
}

func TestClient_RefusesPlainHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewClientAt(config.EnvTest, srv.URL, srv.Client(), t.TempDir(), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	_, err := c.do(context.Background(), "GET", "/x", "", nil, "")
	if err == nil {
		t.Fatal("plain http request was sent")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error %q does not mention https", err)
	}
}

func TestClient_RefusesRedirect(t *testing.T) {
	f := ksefake.New(t)
	f.Handle("GET", "/r", func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Status: 302, Header: map[string]string{"Location": "http://example.invalid/"}}
	})
	hc := f.Client()
	hc.CheckRedirect = nil // the client under test installs its own policy via NewClientAt
	c := NewClientAt(config.EnvTest, f.URL, hc, t.TempDir(), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	_, err := c.do(context.Background(), "GET", "/r", "", nil, "")
	if err == nil {
		t.Fatal("redirect was followed or treated as success")
	}
}

func TestClient_LogHasNoAuthorizationOrBody(t *testing.T) {
	f := ksefake.New(t)
	f.Handle("POST", "/auth/x", func(ksefake.Request) ksefake.Response {
		return ksefake.Response{Status: 200, Body: `{"secretResponse":"RESPONSE-SECRET"}`, Header: map[string]string{"X-Reference-Number": "REF-123"}}
	})
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), log)
	_, err := c.do(context.Background(), "POST", "/auth/x", "application/xml", []byte("<secret>REQUEST-SECRET</secret>"), "TOKEN-SECRET")
	if err != nil {
		t.Fatal(err)
	}
	got := f.Requests()
	if len(got) != 1 || got[0].Header.Get("Authorization") != "Bearer TOKEN-SECRET" {
		t.Fatalf("fake did not receive the Authorization header: %+v", got)
	}
	out := buf.String()
	for _, bad := range []string{"Authorization", "Bearer", "TOKEN-SECRET", "REQUEST-SECRET", "RESPONSE-SECRET"} {
		if strings.Contains(out, bad) {
			t.Errorf("log contains %q: %s", bad, out)
		}
	}
	for _, want := range []string{"POST", "/auth/x", "200", "REF-123"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q: %s", want, out)
		}
	}
}

func TestClient_SendsUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewClientAt(config.EnvTest, srv.URL, srv.Client(), t.TempDir(), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if _, err := c.do(context.Background(), "GET", "/x", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "fv/") {
		t.Errorf("User-Agent = %q, want fv/<version>", got)
	}
}
