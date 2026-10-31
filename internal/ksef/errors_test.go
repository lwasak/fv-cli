package ksef

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
	"github.com/lwasak/fv-cli/internal/logging"
)

func TestError_DecodesExceptionCodeAndDescription(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("POST", "/auth/challenge", 400, `{"exception":{"serviceCtx":"srvABC","serviceCode":"00-xx","serviceName":"auth","timestamp":"2026-10-30T10:00:00Z","referenceNumber":"ref-1","exceptionDetailList":[{"exceptionCode":21401,"exceptionDescription":"Brak uprawnien","details":["detail one","detail two"]}]}}`)
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	_, err := c.do(context.Background(), "POST", "/auth/challenge", "", nil, "")
	var ke *Error
	if !errors.As(err, &ke) {
		t.Fatalf("error %T %v is not *Error", err, err)
	}
	if ke.HTTPStatus != 400 || ke.Code != 21401 || ke.Description != "Brak uprawnien" {
		t.Errorf("decoded %+v", ke)
	}
	if len(ke.Details) != 2 || ke.Details[0] != "detail one" {
		t.Errorf("details = %v", ke.Details)
	}
	if got, want := ke.Error(), "KSeF error 21401: Brak uprawnien"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestError_DecodesStatusBody(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/auth/ref", 400, `{"status":{"code":450,"description":"Bledny token","details":["d"]}}`)
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	_, err := c.do(context.Background(), "GET", "/auth/ref", "", nil, "")
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != 450 || ke.Description != "Bledny token" {
		t.Fatalf("got %v", err)
	}
}

func TestError_FallbackToHTTPStatus(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/boom", 502, `<html>bad gateway</html>`)
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	_, err := c.do(context.Background(), "GET", "/boom", "", nil, "")
	var ke *Error
	if !errors.As(err, &ke) {
		t.Fatalf("error %T is not *Error", err)
	}
	if ke.HTTPStatus != 502 || ke.Code != 0 {
		t.Errorf("decoded %+v", ke)
	}
	if got, want := ke.Error(), "KSeF HTTP 502"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func decodeCase(t *testing.T, status int, body string) *Error {
	t.Helper()
	f := ksefake.New(t)
	f.Respond("POST", "/x", status, body)
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := c.do(context.Background(), "POST", "/x", "", nil, "")
	var ke *Error
	if !errors.As(err, &ke) {
		t.Fatalf("error %T %v is not *Error", err, err)
	}
	return ke
}

func TestError_ProblemJSONWithErrorsList(t *testing.T) {
	ke := decodeCase(t, 400, `{"title":"Bad Request","status":400,"detail":"Żądanie jest nieprawidłowe.","errors":[{"code":21405,"description":"Błąd walidacji danych wejściowych.","details":["Wskazany kod formularza nie jest wspierany."]},{"code":21157,"description":"Nieprawidłowy rozmiar części pakietu.","details":["x"]}],"timestamp":"2025-07-11T12:23:56Z","traceId":"trace-1"}`)
	if ke.HTTPStatus != 400 || ke.Code != 21405 || ke.Description != "Błąd walidacji danych wejściowych." {
		t.Errorf("decoded %+v", ke)
	}
	if len(ke.Details) != 3 || ke.Details[1] != "21157: Nieprawidłowy rozmiar części pakietu." {
		t.Errorf("details = %q", ke.Details)
	}
	if ke.TraceID != "trace-1" || !strings.Contains(ke.Body, "21405") {
		t.Errorf("trace/body: %q %q", ke.TraceID, ke.Body)
	}
}

func TestError_ProblemJSONWithoutErrorsUsesTitleAndDetail(t *testing.T) {
	ke := decodeCase(t, 403, `{"title":"Forbidden","status":403,"detail":"Brak uprawnień.","reasonCode":"missing-permissions","traceId":"t"}`)
	if ke.Code != 403 || ke.Description != "Forbidden: Brak uprawnień. (missing-permissions)" {
		t.Errorf("decoded %+v", ke)
	}
	if got := ke.Error(); got != "KSeF error 403: Forbidden: Brak uprawnień. (missing-permissions)" {
		t.Errorf("Error() = %q", got)
	}
}

func TestError_ProblemJSONWithoutStatusFieldUsesHTTPStatus(t *testing.T) {
	ke := decodeCase(t, 401, `{"title":"Unauthorized","detail":"Wymagane jest uwierzytelnienie."}`)
	if ke.Code != 401 || ke.Description != "Unauthorized: Wymagane jest uwierzytelnienie." {
		t.Errorf("decoded %+v", ke)
	}
}

func TestError_429StatusObject(t *testing.T) {
	ke := decodeCase(t, 429, `{"status":{"code":429,"description":"Too Many Requests","details":["Przekroczono limit 20 żądań na minutę."]}}`)
	if ke.Code != 429 || ke.Description != "Too Many Requests" || len(ke.Details) != 1 {
		t.Errorf("decoded %+v", ke)
	}
}

func TestError_NonJSONBodyIsHTTPFallback(t *testing.T) {
	ke := decodeCase(t, 502, `<html>bad gateway</html>`)
	if ke.Code != 0 || ke.Error() != "KSeF HTTP 502" || ke.Body == "" {
		t.Errorf("decoded %+v", ke)
	}
}

func TestError_BodyLoggedThroughRedactor(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("POST", "/x", 400, `{"title":"Bad","status":400,"detail":"token SECRETVALUE-123 rejected"}`)
	var buf strings.Builder
	logging.Default.Register("SECRETVALUE-123")
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), redactedLogger(&buf))
	if _, err := c.do(context.Background(), "POST", "/x", "", nil, ""); err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(buf.String(), "token [REDACTED] rejected") || strings.Contains(buf.String(), "SECRETVALUE-123") {
		t.Errorf("log = %s", buf.String())
	}
}

func TestDo_NeverLogsSuccessBodies(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/x", 200, `{"secret":"BODYMARKER"}`)
	var buf strings.Builder
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), redactedLogger(&buf))
	if _, err := c.do(context.Background(), "GET", "/x", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "BODYMARKER") {
		t.Error("a 2xx body was logged")
	}
}

func TestDoAccept_SetsAcceptHeader(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/x", 200, `<a/>`)
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	if _, err := c.doAccept(context.Background(), "GET", "/x", "application/xml", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.do(context.Background(), "GET", "/x", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	rs := f.Requests()
	if rs[0].Header.Get("Accept") != "application/xml" || rs[1].Header.Get("Accept") != "application/json" {
		t.Errorf("Accept headers: %q, %q", rs[0].Header.Get("Accept"), rs[1].Header.Get("Accept"))
	}
}

func rateLimited(retryAfter string) ksefake.Response {
	h := map[string]string{}
	if retryAfter != "" {
		h["Retry-After"] = retryAfter
	}
	return ksefake.Response{Status: 429, Header: h, Body: `{"status":{"code":429,"description":"Too Many Requests"}}`}
}

func TestDo_GETRetriesOn429HonouringRetryAfterCappedAt30s(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{{"3", 3 * time.Second}, {"120", 30 * time.Second}, {"", 2 * time.Second}, {"soon", 2 * time.Second}} {
		f := ksefake.New(t)
		f.Respond("GET", "/x", 200, `{}`)
		f.Inject("GET", "/x", 1, rateLimited(tc.header))
		var slept []time.Duration
		c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
		c.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
		if _, err := c.do(context.Background(), "GET", "/x", "", nil, ""); err != nil {
			t.Fatalf("Retry-After %q: %v", tc.header, err)
		}
		if len(slept) != 1 || slept[0] != tc.want {
			t.Errorf("Retry-After %q: slept %v, want %v", tc.header, slept, tc.want)
		}
	}
}

func TestDo_GETGivesUpAfterThreeRetries(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/x", 200, `{}`)
	f.Inject("GET", "/x", 10, rateLimited("1"))
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := c.do(context.Background(), "GET", "/x", "", nil, "")
	var ke *Error
	if !errors.As(err, &ke) || ke.HTTPStatus != 429 || ke.Code != 429 {
		t.Fatalf("err = %v", err)
	}
	if n := f.Count("GET", "/x"); n != 4 {
		t.Errorf("requests = %d, want 4 (1 + 3 retries)", n)
	}
}

func TestDo_POSTNeverRetriedOn429(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("POST", "/x", 429, `{"status":{"code":429,"description":"Too Many Requests"}}`)
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	var slept int
	c.sleep = func(context.Context, time.Duration) error { slept++; return nil }
	if _, err := c.do(context.Background(), "POST", "/x", "", nil, ""); err == nil {
		t.Fatal("no error")
	}
	if n := f.Count("POST", "/x"); n != 1 || slept != 0 {
		t.Errorf("requests = %d, sleeps = %d", n, slept)
	}
}

func TestDo_GETRetryStopsWhenSleepIsCancelled(t *testing.T) {
	f := ksefake.New(t)
	f.Inject("GET", "/x", 10, rateLimited("1"))
	f.Respond("GET", "/x", 200, `{}`)
	c := newTestClient(t, f, config.EnvTest, t.TempDir(), nil)
	c.sleep = func(context.Context, time.Duration) error { return context.Canceled }
	if _, err := c.do(context.Background(), "GET", "/x", "", nil, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
