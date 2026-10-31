package ksef

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Error is a failure reported by KSeF (or an HTTP failure without a KSeF body).
type Error struct {
	HTTPStatus  int
	Code        int
	Description string
	Details     []string
	// TraceID is KSeF's trace identifier of a problem+json answer, for support requests.
	TraceID string
	// Body is the (bounded) response body. It is for the redacted log only; Error() never
	// prints it.
	Body string

	retryAfter time.Duration
}

// Error renders "KSeF error <code>: <description>", or "KSeF HTTP <status>" when the
// response carried no KSeF error.
func (e *Error) Error() string {
	if e.Code == 0 && e.Description == "" {
		return fmt.Sprintf("KSeF HTTP %d", e.HTTPStatus)
	}
	return fmt.Sprintf("KSeF error %d: %s", e.Code, e.Description)
}

// RetryAfter is the delay the Retry-After header of a 429 asked for, or 0 when there was none.
func (e *Error) RetryAfter() time.Duration { return e.retryAfter }

func asError(err error) *Error {
	var ke *Error
	if errors.As(err, &ke) {
		return ke
	}
	return nil
}

// IsRateLimited reports a 429 answer.
func IsRateLimited(err error) bool {
	ke := asError(err)
	return ke != nil && ke.HTTPStatus == 429
}

// IsForbidden reports a 403 answer: the credential lacks a permission (for the invoice
// endpoints, InvoiceRead).
func IsForbidden(err error) bool {
	ke := asError(err)
	return ke != nil && ke.HTTPStatus == 403
}

// IsNoSuchInvoice reports KSeF error 21164: there is no invoice with that KSeF number.
func IsNoSuchInvoice(err error) bool {
	ke := asError(err)
	return ke != nil && ke.Code == 21164
}

// IsNotYetAvailable reports KSeF error 21165: the invoice was processed but cannot be
// downloaded yet; trying again later works.
func IsNotYetAvailable(err error) bool {
	ke := asError(err)
	return ke != nil && ke.Code == 21165
}

// maxErrorBody bounds how much of an error body is parsed.
const maxErrorBody = 1 << 20

type exceptionBody struct {
	Exception struct {
		List []struct {
			Code        int      `json:"exceptionCode"`
			Description string   `json:"exceptionDescription"`
			Details     []string `json:"details"`
		} `json:"exceptionDetailList"`
	} `json:"exception"`
	// Status is an object for auth-status and 429 bodies and an integer for problem+json.
	Status json.RawMessage `json:"status"`
}

// statusBody is the "status" object of the auth-status, 429 and session-status answers.
type statusBody struct {
	Code        int      `json:"code"`
	Description string   `json:"description"`
	Details     []string `json:"details"`
}

// problemBody is application/problem+json as the OpenAPI defines it (BadRequest,
// Unauthorized, Forbidden, TooManyRequests).
type problemBody struct {
	Title      string          `json:"title"`
	Status     json.RawMessage `json:"status"`
	Detail     string          `json:"detail"`
	ReasonCode string          `json:"reasonCode"`
	TraceID    string          `json:"traceId"`
	Errors     []struct {
		Code        int      `json:"code"`
		Description string   `json:"description"`
		Details     []string `json:"details"`
	} `json:"errors"`
}

// maxLoggedBody bounds Error.Body.
const maxLoggedBody = 16 << 10

// decodeError builds the *Error for a non-2xx response. It understands the KSeF 2.0
// "exception" body, the "status" body (auth status, 429) and application/problem+json;
// anything else gives the HTTP fallback.
//
// A problem+json body with an "errors" list reports the first error's code and description
// (the other errors join the details); without one, the code is the HTTP status and the
// description is "<title>: <detail>" (plus the reasonCode when there is one).
func decodeError(httpStatus int, body []byte) *Error {
	e := &Error{HTTPStatus: httpStatus}
	if len(body) > maxErrorBody {
		body = body[:maxErrorBody]
	}
	e.Body = string(body[:min(len(body), maxLoggedBody)])
	var b exceptionBody
	var p problemBody
	if json.Unmarshal(body, &b) != nil {
		return e
	}
	_ = json.Unmarshal(body, &p)
	var st *statusBody
	if t := bytes.TrimSpace(b.Status); len(t) > 0 && t[0] == '{' {
		st = new(statusBody)
		if json.Unmarshal(t, st) != nil {
			st = nil
		}
	}
	switch {
	case len(b.Exception.List) > 0:
		d := b.Exception.List[0]
		e.Code, e.Description, e.Details = d.Code, d.Description, d.Details
	case st != nil && (st.Code != 0 || st.Description != ""):
		e.Code, e.Description, e.Details = st.Code, st.Description, st.Details
	case len(p.Errors) > 0:
		first := p.Errors[0]
		e.Code, e.Description = first.Code, first.Description
		e.Details = append(e.Details, first.Details...)
		for _, x := range p.Errors[1:] {
			e.Details = append(e.Details, fmt.Sprintf("%d: %s", x.Code, x.Description))
			e.Details = append(e.Details, x.Details...)
		}
	case p.Title != "" || p.Detail != "":
		e.Code, _ = strconv.Atoi(string(p.Status))
		if e.Code == 0 {
			e.Code = httpStatus
		}
		e.Description = problemText(p)
	}
	e.TraceID = p.TraceID
	return e
}

func problemText(p problemBody) string {
	var parts []string
	if p.Title != "" {
		parts = append(parts, p.Title)
	}
	if p.Detail != "" {
		parts = append(parts, p.Detail)
	}
	s := strings.Join(parts, ": ")
	if p.ReasonCode != "" {
		s += " (" + p.ReasonCode + ")"
	}
	return s
}
