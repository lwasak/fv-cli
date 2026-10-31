package config

import "fmt"

// MinimalTOML renders the minimal config.toml written by fv init. The caller must pass a
// validated NIP (10 digits) and environment, so no TOML escaping is needed.
func MinimalTOML(nip string, env Env) []byte {
	return []byte(fmt.Sprintf("[ksef]\nenvironment = %q\n\n[seller]\nnip = %q\n", string(env), nip))
}
