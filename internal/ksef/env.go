package ksef

import "github.com/lwasak/fv-cli/internal/config"

// BaseURL returns the fixed KSeF API base URL of env, or "" for an unknown environment.
// The URLs are constants on purpose: configuration must not be able to redirect KSeF.
func BaseURL(env config.Env) string {
	switch env {
	case config.EnvTest:
		return "https://api-test.ksef.mf.gov.pl/v2"
	case config.EnvDemo:
		return "https://api-demo.ksef.mf.gov.pl/v2"
	case config.EnvProd:
		return "https://api.ksef.mf.gov.pl/v2"
	}
	return ""
}
