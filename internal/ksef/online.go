package ksef

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"
)

// FormCode is the invoice schema every send uses: FA (3), version 1-0E.
const (
	formSystemCode    = "FA (3)"
	formSchemaVersion = "1-0E"
	formValue         = "FA"
)

// OnlineSession is an open interactive session. The AES key and IV live only here; Close
// wipes them. It is safe for sequential use by one goroutine.
type OnlineSession struct {
	// Ref is the session reference number; persist it before sending.
	Ref string
	// ValidUntil is when KSeF closes the session on its own.
	ValidUntil time.Time

	c      *Client
	access string
	mu     sync.Mutex
	key    *sessionKey
}

type openRequest struct {
	FormCode   formCode   `json:"formCode"`
	Encryption encryption `json:"encryption"`
}

type formCode struct {
	SystemCode    string `json:"systemCode"`
	SchemaVersion string `json:"schemaVersion"`
	Value         string `json:"value"`
}

type encryption struct {
	EncryptedSymmetricKey string `json:"encryptedSymmetricKey"`
	InitializationVector  string `json:"initializationVector"`
	PublicKeyID           string `json:"publicKeyId,omitempty"`
}

type sendRequest struct {
	InvoiceHash             string `json:"invoiceHash"`
	InvoiceSize             int    `json:"invoiceSize"`
	EncryptedInvoiceHash    string `json:"encryptedInvoiceHash"`
	EncryptedInvoiceSize    int    `json:"encryptedInvoiceSize"`
	EncryptedInvoiceContent string `json:"encryptedInvoiceContent"`
	OfflineMode             bool   `json:"offlineMode"`
}

// OpenOnlineSession generates a new AES-256 key and IV from rnd (nil means crypto/rand),
// registers them with the log redactor, wraps the key for the Ministry's
// SymmetricKeyEncryption certificate and opens a session with POST /sessions/online. The
// request is sent once and never retried. On any failure the key material is wiped.
func OpenOnlineSession(ctx context.Context, c *Client, s *Session, rnd io.Reader) (*OnlineSession, error) {
	if s == nil {
		return nil, errors.New("ksef: no authenticated session")
	}
	key, err := newSessionKey(rnd)
	if err != nil {
		return nil, err
	}
	o, err := openWith(ctx, c, s, key)
	if err != nil {
		key.zero()
		return nil, err
	}
	return o, nil
}

func openWith(ctx context.Context, c *Client, s *Session, key *sessionKey) (*OnlineSession, error) {
	cert, keyID, err := c.SymmetricKeyCert(ctx)
	if err != nil {
		return nil, err
	}
	wrapped, err := key.wrap(cert)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(openRequest{
		FormCode: formCode{SystemCode: formSystemCode, SchemaVersion: formSchemaVersion, Value: formValue},
		Encryption: encryption{
			EncryptedSymmetricKey: base64.StdEncoding.EncodeToString(wrapped),
			InitializationVector:  base64.StdEncoding.EncodeToString(key.iv),
			PublicKeyID:           keyID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ksef: encode session request: %w", err)
	}
	resp, err := c.do(ctx, "POST", "/sessions/online", "application/json", body, s.AccessToken())
	if err != nil {
		return nil, err
	}
	var out struct {
		ReferenceNumber string    `json:"referenceNumber"`
		ValidUntil      time.Time `json:"validUntil"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil || out.ReferenceNumber == "" {
		return nil, errors.New("ksef: the open-session response has no reference number")
	}
	return &OnlineSession{Ref: out.ReferenceNumber, ValidUntil: out.ValidUntil, c: c, access: s.AccessToken(), key: key}, nil
}

// SendInvoice encrypts xml and posts it; it returns the invoice reference number. The
// request carries SHA-256 (Base64) and size of both the plain and the encrypted XML. It is
// sent exactly once, never retried. Classify the error with NotAccepted.
func (o *OnlineSession) SendInvoice(ctx context.Context, xml []byte) (invoiceRef string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.key == nil || !o.key.live() {
		return "", errors.New("ksef: the online session is closed")
	}
	ct, err := o.key.encrypt(xml)
	if err != nil {
		return "", err
	}
	plainHash, plainSize := InvoiceHash(xml)
	encHash, encSize := InvoiceHash(ct)
	body, err := json.Marshal(sendRequest{
		InvoiceHash: plainHash, InvoiceSize: plainSize,
		EncryptedInvoiceHash: encHash, EncryptedInvoiceSize: encSize,
		EncryptedInvoiceContent: base64.StdEncoding.EncodeToString(ct),
	})
	if err != nil {
		return "", fmt.Errorf("ksef: encode invoice request: %w", err)
	}
	resp, err := o.c.do(ctx, "POST", "/sessions/online/"+url.PathEscape(o.Ref)+"/invoices", "application/json", body, o.access)
	if err != nil {
		return "", err
	}
	var out struct {
		ReferenceNumber string `json:"referenceNumber"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil || out.ReferenceNumber == "" {
		// KSeF answered 2xx, so the invoice was received; the caller must not treat this as
		// a rejection.
		return "", errors.New("ksef: the send response has no invoice reference number")
	}
	return out.ReferenceNumber, nil
}

// Close closes the session (POST /sessions/online/{ref}/close) and wipes the AES key and
// IV, even when the request fails. Calling it again only repeats the wipe.
func (o *OnlineSession) Close(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.key == nil {
		return nil
	}
	defer func() { o.key.zero() }()
	if !o.key.live() {
		return nil
	}
	_, err := o.c.do(ctx, "POST", "/sessions/online/"+url.PathEscape(o.Ref)+"/close", "", nil, o.access)
	return err
}

// NotAccepted reports whether err proves KSeF did not accept the request: an HTTP 4xx
// answer that carries a KSeF error body. A transport error, a timeout or a 5xx answer
// proves nothing (the invoice may have been received), so it returns false.
func NotAccepted(err error) bool {
	var ke *Error
	return errors.As(err, &ke) && ke.HTTPStatus >= 400 && ke.HTTPStatus < 500 && ke.Code != 0
}

// sessionLevelCodes are the KSeF exception codes of POST /sessions/online/{ref}/invoices
// that describe the session, not the invoice (open-api.json, 400 table): 21155 invoice limit
// of the session reached, 21173 no such session, 21180 session status does not allow sending,
// 21184 session temporarily unavailable (maintenance). KSeF did not process the invoice, and
// another session would accept the same bytes.
var sessionLevelCodes = map[int]bool{21155: true, 21173: true, 21180: true, 21184: true}

// RefusedForSession reports whether err is a 4xx answer to the invoice POST that blames the
// session, the caller or the rate limit rather than the invoice itself: HTTP 401, 403 and
// 429, or one of the session-level exception codes. KSeF stored nothing, so the draft stays
// sendable. Only an invoice-level refusal (21001 unreadable, 21402 size, 21403 hash, 21405
// validation ...) is a rejection of the invoice that fv records as rejected.
func RefusedForSession(err error) bool {
	var ke *Error
	if !errors.As(err, &ke) {
		return false
	}
	switch ke.HTTPStatus {
	case 401, 403, 429:
		return true
	}
	return ke.HTTPStatus >= 400 && ke.HTTPStatus < 500 && sessionLevelCodes[ke.Code]
}
