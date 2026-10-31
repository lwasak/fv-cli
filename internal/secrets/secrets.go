// Package secrets reads secrets (KSeF tokens and the like) from the OS keyring, with an
// environment-variable fallback. Every value returned is registered with the redactor.
package secrets

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/logging"
)

// ErrNotFound means no secret exists for the key.
var ErrNotFound = errors.New("secret not found")

// ErrUnavailable means the keyring service itself cannot be reached (for example headless
// Linux without D-Bus), as opposed to a missing entry.
var ErrUnavailable = errors.New("keyring service unavailable")

// account is the keyring account name under every service.
const account = "fv"

// Backend reads secrets from a store such as the OS keyring. Get returns ErrNotFound
// when there is no entry; any other error is a real failure.
type Backend interface {
	Get(service, user string) (string, error)
	Set(service, user, value string) error
}

// Store looks secrets up in Backend, then in environment variables.
type Store struct {
	Backend Backend
	Getenv  func(string) string
	Log     *slog.Logger
	Redact  *logging.Redactor
}

// NewStore returns a Store on the OS keyring, os.Getenv and logging.Default.
func NewStore(log *slog.Logger) *Store {
	return &Store{Backend: KeyringBackend{}, Getenv: os.Getenv, Log: log, Redact: logging.Default}
}

var (
	purposeRe = regexp.MustCompile(`^[a-z0-9-]+$`)
	nipRe     = regexp.MustCompile(`^[0-9]{10}$`)
	nonAlnum  = regexp.MustCompile(`[^A-Z0-9]`)
)

// Get returns the secret for purpose, environment and seller NIP: keyring entry
// fv/<purpose>/<env>/<nip> first, then env var FV_SECRET_<PURPOSE>_<ENV>_<NIP>.
// An unavailable keyring service falls back like a missing entry; other keyring failures
// are returned without falling back.
func (s *Store) Get(purpose string, env config.Env, nip string) (string, error) {
	key, err := secretKey(purpose, env, nip)
	if err != nil {
		return "", err
	}

	v, err := s.Backend.Get(key, account)
	unavailable := errors.Is(err, ErrUnavailable)
	switch {
	case err == nil && v != "":
		return s.register(v), nil
	case err != nil && !errors.Is(err, ErrNotFound) && !unavailable:
		return "", fmt.Errorf("keyring lookup for %s: %w", key, err)
	}

	name := "FV_SECRET_" + nonAlnum.ReplaceAllString(strings.ToUpper(purpose+"_"+string(env)+"_"+nip), "_")
	if v := s.Getenv(name); v != "" {
		if s.Log != nil {
			if unavailable {
				s.Log.Warn("secret read from environment variable", "reason", "keyring unavailable", "key", key, "variable", name)
			} else {
				s.Log.Warn("secret read from environment variable", "key", key, "variable", name)
			}
		}
		return s.register(v), nil
	}
	if unavailable {
		return "", fmt.Errorf("%w: %s (%v; set environment variable %s)", ErrUnavailable, key, err, name)
	}
	return "", fmt.Errorf("%w: %s (keyring entry or environment variable %s)", ErrNotFound, key, name)
}

// secretKey validates the parts and returns the keyring service name fv/<purpose>/<env>/<nip>.
func secretKey(purpose string, env config.Env, nip string) (string, error) {
	if !purposeRe.MatchString(purpose) {
		return "", fmt.Errorf("invalid secret purpose %q: use lower-case letters, digits and '-'", purpose)
	}
	if !nipRe.MatchString(nip) {
		return "", errors.New("invalid NIP for secret lookup: must be 10 digits")
	}
	if _, err := config.ParseEnv(string(env)); err != nil {
		return "", err
	}
	return fmt.Sprintf("fv/%s/%s/%s", purpose, env, nip), nil
}

// Set stores value in the keyring under fv/<purpose>/<env>/<nip> and registers it with the
// redactor. The key is validated like Get.
func (s *Store) Set(purpose string, env config.Env, nip, value string) error {
	key, err := secretKey(purpose, env, nip)
	if err != nil {
		return err
	}
	if value == "" {
		return errors.New("refusing to store an empty secret")
	}
	if err := s.Backend.Set(key, account, value); err != nil {
		return fmt.Errorf("keyring store for %s: %w", key, err)
	}
	s.register(value)
	return nil
}

func (s *Store) register(v string) string {
	if s.Redact != nil {
		s.Redact.Register(v)
	}
	return v
}
