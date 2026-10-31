package ksef

import (
	"time"

	"github.com/lwasak/fv-cli/internal/config"
)

// Session is an authenticated KSeF session. The tokens live only in memory: they are
// unexported, and the type prints no token with any fmt verb.
type Session struct {
	env          config.Env
	accessToken  string
	refreshToken string
	validUntil   time.Time
}

// Env is the environment the session belongs to.
func (s *Session) Env() config.Env { return s.env }

// AccessToken is the bearer token for KSeF calls.
func (s *Session) AccessToken() string { return s.accessToken }

// RefreshToken is the token that renews the access token.
func (s *Session) RefreshToken() string { return s.refreshToken }

// ValidUntil is when the access token expires.
func (s *Session) ValidUntil() time.Time { return s.validUntil }

// String hides the tokens.
func (s *Session) String() string {
	return "ksef.Session{env=" + string(s.env) + " tokens=[hidden]}"
}

// GoString hides the tokens for %#v.
func (s *Session) GoString() string { return s.String() }

// MarshalJSON refuses to serialise tokens.
func (s *Session) MarshalJSON() ([]byte, error) {
	return []byte(`{"env":"` + string(s.env) + `"}`), nil
}
