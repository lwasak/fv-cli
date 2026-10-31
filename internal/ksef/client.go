package ksef

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/useragent"
)

// requestTimeout bounds one HTTP exchange.
const requestTimeout = 30 * time.Second

// maxResponseBody bounds how much of a response is read.
const maxResponseBody = 16 << 20

// NewHTTPClient returns the hardened client for KSeF: TLS 1.2 or newer, the system trust
// store, a 30 s timeout, proxies only from the standard environment variables and no
// redirects. There is deliberately no option to relax verification.
func NewHTTPClient() *http.Client {
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
		CheckRedirect: noRedirect,
	}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Client talks to one KSeF environment.
type Client struct {
	env     config.Env
	base    string
	http    *http.Client
	dataDir string
	log     *slog.Logger
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error

	// pacing of the metadata queries
	paceMu         sync.Mutex
	lastMetadata   time.Time
	skippedRecords atomic.Int64
}

// NewClient returns a client for env using its fixed base URL and the hardened HTTP
// client. dataDir holds the public-key cache; log receives one line per request.
func NewClient(env config.Env, dataDir string, log *slog.Logger) (*Client, error) {
	base := BaseURL(env)
	if base == "" {
		return nil, fmt.Errorf("ksef: unknown environment %q", env)
	}
	return NewClientAt(env, base, NewHTTPClient(), dataDir, log), nil
}

// NewClientAt is for Go callers that must reach a stand-in server (tests); no flag, config
// key or environment variable leads here. Every request is still refused unless baseURL is
// https, and redirects are never followed.
func NewClientAt(env config.Env, baseURL string, hc *http.Client, dataDir string, log *slog.Logger) *Client {
	cp := *hc
	cp.CheckRedirect = noRedirect
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Client{
		env: env, base: strings.TrimRight(baseURL, "/"), http: &cp,
		dataDir: dataDir, log: log, now: time.Now, sleep: sleepContext,
	}
}

// response is a successful (2xx) answer.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// request describes one KSeF call.
type request struct {
	method      string
	path        string
	query       string
	accept      string // default application/json
	contentType string
	body        []byte
	bearer      string
	header      map[string]string
}

// Limits for the 429 handling of GET requests.
const (
	maxRetryAfter     = 30 * time.Second
	defaultRetryAfter = 2 * time.Second
	maxRateRetries    = 3
)

// SetSleep replaces the function the client waits with between polls and rate-limit retries.
// It is a seam for tests (and the Deps.Sleep seam of the cli package); nil restores the
// real, context-aware sleep.
func (c *Client) SetSleep(f func(context.Context, time.Duration) error) {
	if f == nil {
		f = sleepContext
	}
	c.sleep = f
}

// do sends one JSON request; see doAccept.
func (c *Client) do(ctx context.Context, method, path, contentType string, body []byte, bearer string) (*response, error) {
	return c.doAccept(ctx, method, path, "application/json", contentType, body, bearer)
}

// doAccept sends one request with the given Accept header. bearer, when not empty, becomes
// the Authorization header. A non-2xx answer is returned as *Error. It logs method, path,
// status, duration and the KSeF reference number, never headers or bodies (an error body
// is logged through the redacting logger, see logRejection). A GET answered 429 is retried
// up to three times after the Retry-After delay (default 2 s, capped at 30 s); any other
// method is never retried.
func (c *Client) doAccept(ctx context.Context, method, path, accept, contentType string, body []byte, bearer string) (*response, error) {
	return c.send(ctx, request{method: method, path: path, accept: accept, contentType: contentType, body: body, bearer: bearer})
}

func (c *Client) send(ctx context.Context, rq request) (*response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.sendOnce(ctx, rq)
		var ke *Error
		if err == nil || rq.method != http.MethodGet || !errors.As(err, &ke) || ke.HTTPStatus != http.StatusTooManyRequests || attempt >= maxRateRetries {
			return resp, err
		}
		delay := ke.retryAfter
		if delay <= 0 {
			delay = defaultRetryAfter
		}
		delay = min(delay, maxRetryAfter)
		c.log.Info("ksef rate limited; retrying", "path", rq.path, "after", delay)
		if serr := c.sleep(ctx, delay); serr != nil {
			return nil, serr
		}
	}
}

func (c *Client) sendOnce(ctx context.Context, rq request) (*response, error) {
	u, err := url.Parse(c.base + rq.path)
	if err != nil {
		return nil, fmt.Errorf("ksef: bad URL for %s: %w", rq.path, err)
	}
	if u.Scheme != "https" {
		return nil, errors.New("ksef: refusing to send a request over anything but https")
	}
	if rq.query != "" {
		u.RawQuery = rq.query
	}
	var rdr io.Reader
	if rq.body != nil {
		rdr = bytes.NewReader(rq.body)
	}
	req, err := http.NewRequestWithContext(ctx, rq.method, u.String(), rdr)
	if err != nil {
		return nil, fmt.Errorf("ksef: build request: %w", err)
	}
	accept := rq.accept
	if accept == "" {
		accept = "application/json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", useragent.String())
	if rq.contentType != "" {
		req.Header.Set("Content-Type", rq.contentType)
	}
	if rq.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+rq.bearer)
	}
	for k, v := range rq.header {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Info("ksef request failed", "method", rq.method, "path", rq.path, "duration", time.Since(start))
		return nil, fmt.Errorf("ksef: %s %s: %w", rq.method, rq.path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	ref := resp.Header.Get("X-Reference-Number")
	c.log.Info("ksef request", "method", rq.method, "path", rq.path, "status", resp.StatusCode,
		"duration", time.Since(start), "reference", ref)
	if err != nil {
		return nil, fmt.Errorf("ksef: read response of %s %s: %w", rq.method, rq.path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ke := decodeError(resp.StatusCode, data)
		ke.retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		c.logRejection(rq, ke)
		return nil, ke
	}
	return &response{Status: resp.StatusCode, Header: resp.Header, Body: data}, nil
}

// logRejection records the full error body (bounded) so a support case has it. It goes
// through the redacting logger like every other record.
func (c *Client) logRejection(rq request, ke *Error) {
	c.log.Info("ksef error response", "method", rq.method, "path", rq.path,
		"status", ke.HTTPStatus, "code", ke.Code, "body", ke.Body)
}

// parseRetryAfter reads the Retry-After header (whole seconds, as the OpenAPI defines it).
func parseRetryAfter(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}
