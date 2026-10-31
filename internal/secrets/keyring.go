package secrets

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

// KeyringBackend reads from the OS keyring (Windows Credential Manager, macOS Keychain,
// Secret Service on Linux).
type KeyringBackend struct{}

// Get returns the keyring secret, mapping a missing entry to ErrNotFound.
func (KeyringBackend) Get(service, user string) (string, error) {
	v, err := keyring.Get(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, classify(err)
}

// Set stores the secret in the OS keyring.
func (KeyringBackend) Set(service, user, value string) error {
	return classify(keyring.Set(service, user, value))
}

// classify wraps errors that mean the keyring service itself is unavailable (no platform
// support, no D-Bus or Secret Service) as ErrUnavailable; other errors pass through.
func classify(err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	if errors.Is(err, keyring.ErrUnsupportedPlatform) || strings.Contains(low, "dbus") ||
		strings.Contains(low, "org.freedesktop.secrets") {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}
