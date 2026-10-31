package ksef

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// ErrDownloadHash is returned when a downloaded invoice does not match the SHA-256 KSeF
// sent with it (x-ms-meta-hash).
var ErrDownloadHash = errors.New("ksef: the downloaded invoice does not match its SHA-256 hash")

// DownloadInvoice reads GET /invoices/ksef/{ksefNumber}: the invoice XML exactly as KSeF
// stores it. The number is validated first. The SHA-256 of the body must equal the
// x-ms-meta-hash header (a mismatch or a missing header is an error, because the content is shown to the user);
// hashB64 is the SHA-256 of the body in Base64. A 429 is retried like any GET. KSeF answers
// 21164 for an unknown number and 21165 for one that is not downloadable yet (see
// IsNoSuchInvoice and IsNotYetAvailable).
func (c *Client) DownloadInvoice(ctx context.Context, s *Session, ksefNumber string) (xml []byte, hashB64 string, err error) {
	if err := ValidateKSeFNumber(ksefNumber); err != nil {
		return nil, "", err
	}
	resp, err := c.doAccept(ctx, "GET", "/invoices/ksef/"+url.PathEscape(ksefNumber), "application/xml", "", nil, s.AccessToken())
	if err != nil {
		return nil, "", fmt.Errorf("ksef: download invoice %s: %w", ksefNumber, withHint(err))
	}
	if len(resp.Body) == 0 {
		return nil, "", fmt.Errorf("ksef: the answer for invoice %s is empty", ksefNumber)
	}
	got, _ := InvoiceHash(resp.Body)
	want := resp.Header.Get("x-ms-meta-hash")
	if want == "" {
		return nil, "", fmt.Errorf("invoice %s: %w (KSeF sent no x-ms-meta-hash header, so the content cannot be verified)", ksefNumber, ErrDownloadHash)
	}
	if want != got {
		return nil, "", fmt.Errorf("invoice %s: %w", ksefNumber, ErrDownloadHash)
	}
	return resp.Body, got, nil
}
