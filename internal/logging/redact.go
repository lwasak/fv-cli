// Package logging provides fv's structured log (fv.log) and the process-wide secret
// redactor that keeps registered secret values out of log files and printed errors.
package logging

import (
	"sort"
	"strings"
	"sync"
)

// Placeholder replaces every registered secret value.
const Placeholder = "[REDACTED]"

// Redactor replaces registered secret values with Placeholder.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

// Default is the process-wide redactor.
var Default = &Redactor{}

// Register adds a secret value. Empty values are ignored.
func (r *Redactor) Register(secret string) {
	if secret == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.secrets {
		if s == secret {
			return
		}
	}
	r.secrets = append(r.secrets, secret)
	// Longest first, so a secret that contains another is replaced whole.
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

// Redact returns s with every registered secret replaced.
func (r *Redactor) Redact(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, Placeholder)
	}
	return s
}

// Reset forgets every registered secret. It is for tests that must see only the secrets the
// code under test registers itself.
func (r *Redactor) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secrets = nil
}
