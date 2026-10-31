// Package httpx is the hardened HTTPS client for fv's non-KSeF services (White List, VIES,
// NBP). It applies the same policy as ksef.NewHTTPClient.
package httpx

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/lwasak/fv-cli/internal/useragent"
)

const (
	requestTimeout = 30 * time.Second
	// MaxBody caps how much of a response body is read.
	MaxBody = 4 << 20
)

// NewClient returns a client with TLS 1.2 or newer, the system trust store, a 30 s timeout,
// proxies only from the standard environment variables and no redirects.
func NewClient() *http.Client {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
	}
	return &http.Client{
		Transport:     tr,
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// GetJSON sends GET rawURL with Accept application/json. It refuses non-https URLs. The
// body is capped at 4 MiB. When out is non-nil and the status is 2xx the body is decoded
// into it with UseNumber. A redirect response is returned as is (status 3xx), never followed.
func GetJSON(ctx context.Context, hc *http.Client, rawURL string, out any) (int, []byte, error) {
	return do(ctx, hc, http.MethodGet, rawURL, nil, out)
}

// PostJSON is GetJSON for a POST whose body is in marshalled as JSON.
func PostJSON(ctx context.Context, hc *http.Client, rawURL string, in, out any) (int, []byte, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return 0, nil, fmt.Errorf("httpx: encode request: %w", err)
	}
	return do(ctx, hc, http.MethodPost, rawURL, b, out)
}

func do(ctx context.Context, hc *http.Client, method, rawURL string, body []byte, out any) (int, []byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, fmt.Errorf("httpx: bad URL: %w", err)
	}
	if u.Scheme != "https" {
		return 0, nil, fmt.Errorf("httpx: refusing non-https URL %q", u.Scheme+"://"+u.Host)
	}
	if hc == nil {
		hc = NewClient()
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return 0, nil, fmt.Errorf("httpx: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", useragent.String())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("httpx: %s %s: %w", method, u.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("httpx: read %s: %w", u.Host, err)
	}
	if len(b) > MaxBody {
		return resp.StatusCode, nil, fmt.Errorf("httpx: response from %s exceeds %d bytes", u.Host, MaxBody)
	}
	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
			return resp.StatusCode, b, fmt.Errorf("httpx: decode response from %s: %w", u.Host, err)
		}
	}
	return resp.StatusCode, b, nil
}
