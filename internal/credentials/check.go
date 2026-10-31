package credentials

import (
	"errors"
	"fmt"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fsafe"
)

// CheckResult is what Check found. Findings make `fv config validate` fail; warnings do not.
type CheckResult struct {
	Findings []string
	Warnings []string
}

// Check inspects the stored credential for env and sellerNIP without decrypting the key and
// without prompting: the key file must be private, the certificate must parse, carry a
// matching NIP (if any) and be valid now. No credential at all is only a warning.
func Check(dataDir string, env config.Env, sellerNIP string, now time.Time) CheckResult {
	var res CheckResult
	cred, err := Load(dataDir, env, sellerNIP)
	if errors.Is(err, ErrNoCredential) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("no KSeF certificate imported for %s/%s; run `fv init` to import one", env, sellerNIP))
		return res
	}
	if err != nil {
		res.Findings = append(res.Findings, err.Error())
		return res
	}
	if err := fsafe.CheckPrivate(cred.KeyFile()); err != nil {
		res.Findings = append(res.Findings, err.Error())
	}
	info, err := cred.Info()
	if err != nil {
		res.Findings = append(res.Findings, err.Error())
		return res
	}
	if _, err := info.ContextNIP(sellerNIP); err != nil {
		res.Findings = append(res.Findings, err.Error())
	}
	warning, err := info.CheckValidity(now)
	if err != nil {
		res.Findings = append(res.Findings, err.Error())
	}
	if warning != "" {
		res.Warnings = append(res.Warnings, warning)
	}
	return res
}
