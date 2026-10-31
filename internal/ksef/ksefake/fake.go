// Package ksefake is a scripted TLS test server standing in for the KSeF API. It records
// every request and answers from handlers registered by the test. It imports nothing from
// package ksef, so ksef's own tests can use it.
package ksefake

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Request is one request the fake received.
type Request struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
	// Params holds the wildcard segments of a pattern registered with HandlePattern.
	Params []string
}

// Response is what a handler answers.
type Response struct {
	Status      int
	Body        string
	ContentType string // default application/json
	Header      map[string]string
	// Drop closes the connection without answering (a connection reset).
	Drop bool
}

// Handler answers one request.
type Handler func(Request) Response

// Fake is a started TLS server. Its Client() trusts the server certificate.
type Fake struct {
	*httptest.Server

	mu       sync.Mutex
	handlers map[string]Handler
	patterns []pattern
	injects  []*inject
	requests []Request
}

type pattern struct {
	method string
	segs   []string // "*" matches one segment
	h      Handler
}

type inject struct {
	method, prefix string
	left           int
	resp           Response
}

// New starts a fake KSeF server that is closed when the test ends.
func New(tb testing.TB) *Fake {
	tb.Helper()
	f := &Fake{handlers: map[string]Handler{}}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	tb.Cleanup(f.Server.Close)
	return f
}

func key(method, path string) string { return method + " " + path }

// Handle registers h for method and path (the path without the query string).
func (f *Fake) Handle(method, path string, h Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[key(method, path)] = h
}

// HandlePattern registers h for method and a slash-separated path pattern in which a "*"
// segment matches any one segment ("/sessions/*/invoices/*"). The matched segments reach
// the handler as Request.Params. Exact handlers (Handle) win over patterns; patterns are
// tried in registration order.
func (f *Fake) HandlePattern(method, pat string, h Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patterns = append(f.patterns, pattern{method: method, segs: strings.Split(strings.Trim(pat, "/"), "/"), h: h})
}

// Inject makes the next n requests with method whose path starts with prefix answer resp
// instead of reaching their handler (for example 429 with a Retry-After, or a 503). The
// request is still recorded.
func (f *Fake) Inject(method, prefix string, n int, resp Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.injects = append(f.injects, &inject{method: method, prefix: prefix, left: n, resp: resp})
}

// Respond registers a fixed JSON answer.
func (f *Fake) Respond(method, path string, status int, body string) {
	f.Handle(method, path, func(Request) Response { return Response{Status: status, Body: body} })
}

// Requests returns a copy of every request received so far, in order.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Count returns how many requests used method and path.
func (f *Fake) Count(method, path string) int {
	n := 0
	for _, r := range f.Requests() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

// CountPrefix returns how many requests of any method had a path starting with prefix.
func (f *Fake) CountPrefix(prefix string) int {
	n := 0
	for _, r := range f.Requests() {
		if strings.HasPrefix(r.Path, prefix) {
			n++
		}
	}
	return n
}

// Counts returns the number of requests per "METHOD /path".
func (f *Fake) Counts() map[string]int {
	out := map[string]int{}
	for _, r := range f.Requests() {
		out[key(r.Method, r.Path)]++
	}
	return out
}

func matchPattern(segs []string, path string) ([]string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != len(segs) {
		return nil, false
	}
	var params []string
	for i, sg := range segs {
		if sg == "*" {
			params = append(params, parts[i])
		} else if sg != parts[i] {
			return nil, false
		}
	}
	return params, true
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req := Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body}
	f.mu.Lock()
	h := f.handlers[key(r.Method, r.URL.Path)]
	var injected *Response
	for _, in := range f.injects {
		if in.left > 0 && in.method == r.Method && strings.HasPrefix(r.URL.Path, in.prefix) {
			in.left--
			resp := in.resp
			injected = &resp
			break
		}
	}
	if h == nil {
		for _, p := range f.patterns {
			if p.method != r.Method {
				continue
			}
			if params, ok := matchPattern(p.segs, r.URL.Path); ok {
				h, req.Params = p.h, params
				break
			}
		}
	}
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	var resp Response
	switch {
	case injected != nil:
		resp = *injected
	case h == nil:
		http.Error(w, "ksefake: no handler for "+key(r.Method, r.URL.Path), http.StatusNotFound)
		return
	default:
		resp = h(req)
	}
	if resp.Drop {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return
	}
	ct := resp.ContentType
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	for k, v := range resp.Header {
		w.Header().Set(k, v)
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, resp.Body)
}
