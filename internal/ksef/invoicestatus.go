package ksef

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// ErrStillProcessing is returned by WaitInvoice when the poll budget ran out before KSeF
// reached a final status.
var ErrStillProcessing = errors.New("ksef: the invoice is still being processed")

// InvoiceStatus is KSeF's view of one invoice of a session.
type InvoiceStatus struct {
	Code        int
	Description string
	Details     []string
	// KSeFNumber is set for status 200 and has been checked with ValidateKSeFNumber.
	KSeFNumber string
	InvoiceRef string
	// InvoiceHash is the SHA-256 (Base64) of the plain XML as KSeF stored it.
	InvoiceHash     string
	AcquisitionDate string
	// OriginalKSeFNumber and OriginalSessionRef are set for status 440 (duplicate).
	OriginalKSeFNumber string
	OriginalSessionRef string
}

// Pending: KSeF accepted the document for processing but has not decided (100, 150).
func (s InvoiceStatus) Pending() bool { return s.Code == 100 || s.Code == 150 }

// Accepted: processed successfully (200); the invoice has a KSeF number.
func (s InvoiceStatus) Accepted() bool { return s.Code == 200 }

// Retry: status 500 or 550. KSeF failed internally; asking again may give another answer.
func (s InvoiceStatus) Retry() bool { return s.Code == 500 || s.Code == 550 }

// Rejected: a final refusal (405, 410, 415, 430, 435, 440, 450 and any other 4xx).
func (s InvoiceStatus) Rejected() bool { return s.Code >= 400 && s.Code < 500 }

// Duplicate: status 440; OriginalKSeFNumber names the stored invoice.
func (s InvoiceStatus) Duplicate() bool { return s.Code == 440 }

type invoiceStatusJSON struct {
	InvoiceNumber   string `json:"invoiceNumber"`
	KSeFNumber      string `json:"ksefNumber"`
	ReferenceNumber string `json:"referenceNumber"`
	InvoiceHash     string `json:"invoiceHash"`
	AcquisitionDate string `json:"acquisitionDate"`
	Status          struct {
		Code        int               `json:"code"`
		Description string            `json:"description"`
		Details     []string          `json:"details"`
		Extensions  map[string]string `json:"extensions"`
	} `json:"status"`
}

func (j invoiceStatusJSON) convert() (InvoiceStatus, error) {
	st := InvoiceStatus{
		Code: j.Status.Code, Description: j.Status.Description, Details: j.Status.Details,
		InvoiceRef: j.ReferenceNumber, InvoiceHash: j.InvoiceHash, AcquisitionDate: j.AcquisitionDate,
		OriginalKSeFNumber: j.Status.Extensions["originalKsefNumber"],
		OriginalSessionRef: j.Status.Extensions["originalSessionReferenceNumber"],
	}
	if st.Code == 200 {
		if err := ValidateKSeFNumber(j.KSeFNumber); err != nil {
			return st, err
		}
		st.KSeFNumber = j.KSeFNumber
	}
	return st, nil
}

// InvoiceStatus reads GET /sessions/{ref}/invoices/{invRef}. A status 200 whose KSeF number
// does not match the TNumerKSeF pattern is an error. The raw answer of a final non-success
// status is logged (redacted).
func (c *Client) InvoiceStatus(ctx context.Context, s *Session, sessionRef, invoiceRef string) (InvoiceStatus, error) {
	resp, err := c.do(ctx, "GET", "/sessions/"+url.PathEscape(sessionRef)+"/invoices/"+url.PathEscape(invoiceRef), "", nil, s.AccessToken())
	if err != nil {
		return InvoiceStatus{}, err
	}
	var j invoiceStatusJSON
	if err := json.Unmarshal(resp.Body, &j); err != nil {
		return InvoiceStatus{}, fmt.Errorf("ksef: decode invoice status: %w", err)
	}
	st, err := j.convert()
	if err != nil {
		return st, err
	}
	if st.InvoiceRef == "" {
		st.InvoiceRef = invoiceRef
	}
	if !st.Pending() && !st.Accepted() {
		c.log.Info("ksef invoice status", "code", st.Code, "body", string(resp.Body))
	}
	return st, nil
}

// Poll schedule of WaitInvoice: at once, then after 1 s, 2 s, 4 s, 4 s ...
const (
	invoicePollFirst = time.Second
	invoicePollMax   = 4 * time.Second
)

// DefaultInvoiceBudget is the time fv waits for a verdict after sending.
const DefaultInvoiceBudget = 120 * time.Second

// WaitInvoice polls InvoiceStatus until the status is final (not pending, not 500/550) or
// budget has passed, sleeping through the client's injectable sleep. A transport error or a
// 5xx answer counts as one more attempt; a 4xx answer (a 429 was already retried by the client) or a cancelled context ends the wait with that error. When the budget runs out
// it returns the last status seen and ErrStillProcessing.
func (c *Client) WaitInvoice(ctx context.Context, s *Session, sessionRef, invoiceRef string, budget time.Duration) (InvoiceStatus, error) {
	start := c.now()
	var slept time.Duration
	delay := invoicePollFirst
	var last InvoiceStatus
	for {
		st, err := c.InvoiceStatus(ctx, s, sessionRef, invoiceRef)
		switch {
		case err == nil:
			last = st
			if !st.Pending() && !st.Retry() {
				return st, nil
			}
		default:
			var ke *Error
			if ctx.Err() != nil || (errors.As(err, &ke) && ke.HTTPStatus < 500) || errors.Is(err, ErrBadKSeFNumber) {
				return last, err
			}
			// transport error or 5xx: one more attempt
		}
		if max(c.now().Sub(start), slept) >= budget {
			return last, ErrStillProcessing
		}
		if err := c.sleep(ctx, delay); err != nil {
			return last, err
		}
		slept += delay
		delay = min(delay*2, invoicePollMax)
	}
}

// FindInvoiceByHash lists the invoices of a session (GET /sessions/{ref}/invoices, 100 per
// page, following x-continuation-token) and returns the one whose invoiceHash equals
// hashB64, or nil when none matches. When several match, an accepted one wins.
func (c *Client) FindInvoiceByHash(ctx context.Context, s *Session, sessionRef, hashB64 string) (*InvoiceStatus, error) {
	var found *InvoiceStatus
	token := ""
	for page := 0; page < 100; page++ {
		rq := request{method: "GET", path: "/sessions/" + url.PathEscape(sessionRef) + "/invoices",
			query: "pageSize=100", bearer: s.AccessToken()}
		if token != "" {
			rq.header = map[string]string{"x-continuation-token": token}
		}
		resp, err := c.send(ctx, rq)
		if err != nil {
			return nil, err
		}
		var list struct {
			ContinuationToken string              `json:"continuationToken"`
			Invoices          []invoiceStatusJSON `json:"invoices"`
		}
		if err := json.Unmarshal(resp.Body, &list); err != nil {
			return nil, fmt.Errorf("ksef: decode invoice list: %w", err)
		}
		for _, j := range list.Invoices {
			if j.InvoiceHash != hashB64 {
				continue
			}
			st, err := j.convert()
			if err != nil {
				return nil, err
			}
			if found == nil || (st.Accepted() && !found.Accepted()) {
				found = &st
			}
		}
		if list.ContinuationToken == "" {
			return found, nil
		}
		token = list.ContinuationToken
	}
	return nil, errors.New("ksef: the session's invoice list has too many pages")
}

// SessionStatus is GET /sessions/{ref}: for an interactive session 100 open, 170 closed,
// 200 processed, 440 cancelled (no invoices were sent), 445 no valid invoice.
type SessionStatus struct {
	Code        int
	Description string
	// InvoiceCount is meaningful only when CountKnown: the field is nullable in the API, and a
	// missing value must never read as "no invoices".
	InvoiceCount           int
	CountKnown             bool
	SuccessfulInvoiceCount int
	FailedInvoiceCount     int
	DateCreated            time.Time
	ValidUntil             time.Time
}

// closedSessionCodes are the final session statuses: 170 closed, 200 processed, 415 no
// invoice could be processed, 440 cancelled, 445 no valid invoice. A missing status (code 0)
// or an unlisted one is not "closed": the caller must treat it as unknown.
var closedSessionCodes = map[int]bool{170: true, 200: true, 415: true, 440: true, 445: true}

// Closed is true only for a final session status listed in closedSessionCodes.
func (s SessionStatus) Closed() bool { return closedSessionCodes[s.Code] }

// SessionStatus reads GET /sessions/{ref}.
func (c *Client) SessionStatus(ctx context.Context, s *Session, sessionRef string) (SessionStatus, error) {
	resp, err := c.do(ctx, "GET", "/sessions/"+url.PathEscape(sessionRef), "", nil, s.AccessToken())
	if err != nil {
		return SessionStatus{}, err
	}
	var j struct {
		Status struct {
			Code        int    `json:"code"`
			Description string `json:"description"`
		} `json:"status"`
		DateCreated            time.Time `json:"dateCreated"`
		ValidUntil             time.Time `json:"validUntil"`
		InvoiceCount           *int      `json:"invoiceCount"`
		SuccessfulInvoiceCount int       `json:"successfulInvoiceCount"`
		FailedInvoiceCount     int       `json:"failedInvoiceCount"`
	}
	if err := json.Unmarshal(resp.Body, &j); err != nil {
		return SessionStatus{}, fmt.Errorf("ksef: decode session status: %w", err)
	}
	ss := SessionStatus{Code: j.Status.Code, Description: j.Status.Description,
		SuccessfulInvoiceCount: j.SuccessfulInvoiceCount,
		FailedInvoiceCount:     j.FailedInvoiceCount, DateCreated: j.DateCreated, ValidUntil: j.ValidUntil}
	if j.InvoiceCount != nil {
		ss.InvoiceCount, ss.CountKnown = *j.InvoiceCount, true
	}
	return ss, nil
}

// InvoiceUPO downloads the UPO (signed XML) of an accepted invoice from
// GET /sessions/{ref}/invoices/{invRef}/upo with Accept application/xml. It never follows
// the upoDownloadUrl of the status answer.
func (c *Client) InvoiceUPO(ctx context.Context, s *Session, sessionRef, invoiceRef string) ([]byte, error) {
	resp, err := c.doAccept(ctx, "GET", "/sessions/"+url.PathEscape(sessionRef)+"/invoices/"+url.PathEscape(invoiceRef)+"/upo",
		"application/xml", "", nil, s.AccessToken())
	if err != nil {
		return nil, err
	}
	if len(resp.Body) == 0 {
		return nil, errors.New("ksef: the UPO answer is empty")
	}
	if want := resp.Header.Get("x-ms-meta-hash"); want != "" {
		if got, _ := InvoiceHash(resp.Body); got != want {
			c.log.Warn("ksef UPO hash differs from x-ms-meta-hash")
		}
	}
	return resp.Body, nil
}

// InvoiceHash returns the SHA-256 of b in Base64 and its size, as KSeF wants both.
func InvoiceHash(b []byte) (b64 string, size int) {
	sum := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(sum[:]), len(b)
}
