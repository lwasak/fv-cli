package credentials

import (
	"errors"
	"fmt"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/logging"
	"github.com/lwasak/fv-cli/internal/secrets"
)

// PassphraseSource finds the key passphrase: the keyring entry fv/ksef/<env>/<nip> or its
// environment variable (through Secrets), then Prompt. There is no flag or config source.
type PassphraseSource struct {
	Secrets *secrets.Store
	// Prompt asks the user without echo. Nil when no terminal is available.
	Prompt func(question string) (string, error)
}

// Get returns the passphrase for env and nip and registers it with the redactor.
func (p PassphraseSource) Get(env config.Env, nip string) (string, error) {
	var missing error
	if p.Secrets != nil {
		v, err := p.Secrets.Get("ksef", env, nip)
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, secrets.ErrNotFound) {
			return "", err
		}
		missing = err
	}
	if p.Prompt == nil {
		if missing == nil {
			missing = errors.New("no passphrase source configured")
		}
		return "", fmt.Errorf("no passphrase for the KSeF key and no terminal to ask on: %w", missing)
	}
	v, err := p.Prompt(fmt.Sprintf("Passphrase for the KSeF key (%s/%s): ", env, nip))
	if err != nil {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	if v == "" {
		return "", errors.New("an empty passphrase was entered")
	}
	if p.Secrets != nil && p.Secrets.Redact != nil {
		p.Secrets.Redact.Register(v)
	} else {
		logging.Default.Register(v)
	}
	return v, nil
}
