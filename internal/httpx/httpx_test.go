package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func tlsClient(srv *httptest.Server) *http.Client {
	c := srv.Client()
	c.CheckRedirect = NewClient().CheckRedirect
	return c
}

func TestHttpx_RefusesHTTPAndRedirect(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer target.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redir" {
			http.Redirect(w, r, target.URL, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{"n":12345678901234567890}`))
	}))
	defer srv.Close()
	hc := tlsClient(srv)
	ctx := context.Background()

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer plain.Close()
	if _, _, err := GetJSON(ctx, hc, plain.URL, nil); err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Errorf("GetJSON http: err = %v", err)
	}
	if _, _, err := PostJSON(ctx, hc, plain.URL, map[string]int{"a": 1}, nil); err == nil {
		t.Error("PostJSON http: want error")
	}
	if hits.Load() != 0 {
		t.Fatalf("plain http server was contacted")
	}

	status, _, err := GetJSON(ctx, hc, srv.URL+"/redir", nil)
	if err != nil || status != http.StatusFound {
		t.Errorf("redirect: status=%d err=%v, want 302 not followed", status, err)
	}
	if hits.Load() != 0 {
		t.Error("redirect target was contacted")
	}

	var out map[string]any
	status, _, err = GetJSON(ctx, hc, srv.URL+"/ok", &out)
	if err != nil || status != 200 {
		t.Fatalf("GetJSON: %d %v", status, err)
	}
	if _, ok := out["n"].(json.Number); !ok {
		t.Errorf("want json.Number, got %T", out["n"])
	}
}

func TestPostJSON_SendsBodyAndCapsResponse(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			_, _ = w.Write([]byte(strings.Repeat("x", MaxBody+10)))
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"` + in["k"] + `"}`))
	}))
	defer srv.Close()
	hc := tlsClient(srv)
	var out map[string]any
	status, body, err := PostJSON(context.Background(), hc, srv.URL, map[string]string{"k": "v"}, &out)
	if err != nil || status != 400 || !strings.Contains(string(body), `"v"`) || out != nil {
		t.Errorf("post: %d %q %v out=%v", status, body, err, out)
	}
	if _, _, err := GetJSON(context.Background(), hc, srv.URL+"/big", nil); err == nil {
		t.Error("oversized body: want error")
	}
}

func TestHttpx_SendsUserAgent(t *testing.T) {
	var got []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.UserAgent())
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	hc := tlsClient(srv)
	ctx := context.Background()
	if _, _, err := GetJSON(ctx, hc, srv.URL, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PostJSON(ctx, hc, srv.URL, map[string]int{"a": 1}, nil); err != nil {
		t.Fatal(err)
	}
	for _, ua := range got {
		if !strings.HasPrefix(ua, "fv/") {
			t.Errorf("User-Agent = %q, want fv/<version>", ua)
		}
	}
	if len(got) != 2 {
		t.Fatalf("requests = %d", len(got))
	}
}
