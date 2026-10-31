package config

import (
	"fmt"
	"strings"
)

// MinimalTOML renders the minimal config.toml written by fv init. The caller must pass a
// validated NIP (10 digits) and environment, so no TOML escaping is needed.
func MinimalTOML(nip string, env Env) []byte {
	return MinimalTOMLWithBank(nip, env, "")
}

// MinimalTOMLWithBank is MinimalTOML plus a [defaults] table with bank_account when the
// account is not empty. The caller must pass a validated IBAN (letters, digits, spaces).
func MinimalTOMLWithBank(nip string, env Env, bankAccount string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "[ksef]\nenvironment = %q\n\n[seller]\nnip = %q\n", string(env), nip)
	if bankAccount != "" {
		fmt.Fprintf(&b, "\n[defaults]\nbank_account = %q\n", bankAccount)
	}
	return []byte(b.String())
}
