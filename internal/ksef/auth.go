package ksef

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/lwasak/fv-cli/internal/logging"
	"github.com/lwasak/fv-cli/internal/xades"
)

// ErrAuth marks every failure of the KSeF login. The command layer maps it to exit code 3.
var ErrAuth = errors.New("KSeF authentication failed")

// authError wraps a login failure: it prints as the cause and matches ErrAuth.
type authError struct{ err error }

func (e *authError) Error() string        { return e.err.Error() }
func (e *authError) Unwrap() error        { return e.err }
func (e *authError) Is(target error) bool { return target == ErrAuth }

func authFail(err error) error { return &authError{err: err} }

// Credential is the login certificate and its one-shot signer; *credentials.Credential
// satisfies it.
type Credential interface {
	Certificate() *x509.Certificate
	Sign(digest []byte, hash crypto.Hash) ([]byte, error)
}

// Polling schedule: first poll at once, then sleeps of 1 s, 2 s, 4 s and 4 s thereafter,
// until pollBudget has passed.
const (
	pollBudget   = 90 * time.Second
	pollFirst    = time.Second
	pollMaxSleep = 4 * time.Second
)

type tokenInfo struct {
	Token      string    `json:"token"`
	ValidUntil time.Time `json:"validUntil"`
}

func (t tokenInfo) register() { logging.Default.Register(t.Token) }

// Authenticate runs the certificate login: MF public key, challenge, XAdES-signed
// AuthTokenRequest, status polling and one redeem. nip is the context NIP. Every failure
// matches ErrAuth. The redeem request is sent once and never retried.
func Authenticate(ctx context.Context, c *Client, cred Credential, nip string) (*Session, error) {
	// The Ministry's SymmetricKeyEncryption certificate is needed by the send flow; fetching
	// (or reading the cache) here proves it is available and valid.
	if _, err := c.PublicKeyFor(ctx, UsageSymmetricKeyEncryption); err != nil {
		return nil, authFail(err)
	}

	resp, err := c.do(ctx, "POST", "/auth/challenge", "application/json", []byte("{}"), "")
	if err != nil {
		return nil, authFail(err)
	}
	var ch struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(resp.Body, &ch); err != nil || ch.Challenge == "" {
		return nil, authFail(errors.New("ksef: the challenge response has no challenge"))
	}

	signed, err := xades.SignAuthTokenRequest(xades.AuthTokenRequest{
		Challenge: ch.Challenge, ContextNIP: nip, SubjectIdentifierType: "certificateSubject",
	}, cred.Certificate(), cred, c.now())
	if err != nil {
		return nil, authFail(err)
	}

	resp, err = c.do(ctx, "POST", "/auth/xades-signature", "application/xml", signed, "")
	if err != nil {
		return nil, authFail(err)
	}
	var sub struct {
		ReferenceNumber     string    `json:"referenceNumber"`
		AuthenticationToken tokenInfo `json:"authenticationToken"`
	}
	if err := json.Unmarshal(resp.Body, &sub); err != nil || sub.ReferenceNumber == "" || sub.AuthenticationToken.Token == "" {
		return nil, authFail(errors.New("ksef: the signature submission response has no reference number or token"))
	}
	sub.AuthenticationToken.register()
	authTok := sub.AuthenticationToken.Token

	if err := c.waitForAuth(ctx, sub.ReferenceNumber, authTok); err != nil {
		return nil, authFail(err)
	}

	// Exactly one redeem request: no helper, no retry, no Idempotency-Key.
	resp, err = c.do(ctx, "POST", "/auth/token/redeem", "", nil, authTok)
	if err != nil {
		return nil, authFail(err)
	}
	var red struct {
		AccessToken  tokenInfo `json:"accessToken"`
		RefreshToken tokenInfo `json:"refreshToken"`
	}
	if err := json.Unmarshal(resp.Body, &red); err != nil || red.AccessToken.Token == "" {
		return nil, authFail(errors.New("ksef: the redeem response has no access token"))
	}
	red.AccessToken.register()
	red.RefreshToken.register()
	return &Session{
		env:          c.env,
		accessToken:  red.AccessToken.Token,
		refreshToken: red.RefreshToken.Token,
		validUntil:   red.AccessToken.ValidUntil,
	}, nil
}

// waitForAuth polls GET /auth/{reference} until KSeF reports 200.
func (c *Client) waitForAuth(ctx context.Context, ref, bearer string) error {
	start := c.now()
	delay := pollFirst
	path := "/auth/" + url.PathEscape(ref)
	for {
		resp, err := c.do(ctx, "GET", path, "", nil, bearer)
		switch {
		case err == nil:
			var st struct {
				Status statusBody `json:"status"`
			}
			if jerr := json.Unmarshal(resp.Body, &st); jerr != nil {
				return fmt.Errorf("ksef: decode authentication status: %w", jerr)
			}
			switch {
			case st.Status.Code == 200:
				return nil
			case st.Status.Code >= 300:
				return &Error{HTTPStatus: resp.Status, Code: st.Status.Code,
					Description: st.Status.Description, Details: st.Status.Details}
			}
		default:
			var ke *Error
			if errors.As(err, &ke) || ctx.Err() != nil {
				return err
			}
			// A transport error counts as one more attempt.
		}
		if c.now().Sub(start) >= pollBudget {
			return errors.New("ksef: timed out waiting for the authentication to complete (90 s)")
		}
		if err := c.sleep(ctx, delay); err != nil {
			return err
		}
		delay = min(delay*2, pollMaxSleep)
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
