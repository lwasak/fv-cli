package cli

import (
	"fmt"
	"strings"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/exitcode"
)

// prodWord is what the user types to confirm a send to the production KSeF.
const prodWord = "PRODUCTION"

// sendPrompt is the confirmation question. Test and demo ask [y/N]; prod asks for the word
// PRODUCTION, in red when color is set. Both name the environment in capitals.
func sendPrompt(env config.Env, number string, color bool) string {
	name := strings.ToUpper(string(env))
	if env != config.EnvProd {
		return fmt.Sprintf("Send invoice %s to KSeF %s? [y/N] ", number, name)
	}
	q := fmt.Sprintf("Send invoice %s to the PRODUCTION KSeF? This is real and legally binding. Type %s to continue: ", number, prodWord)
	if color {
		return "\x1b[1;31m" + q + "\x1b[0m"
	}
	return q
}

// confirmSend asks the user to confirm sending number to env. yes skips the question on test
// and demo; it never skips the PRODUCTION confirmation. Without a terminal and without
// Deps.Confirm there is nobody to ask: a prod send aborts, and so does a test or demo send
// without --yes. Every refusal is exitcode.ErrAborted (exit 10) and nothing was sent.
func (s *session) confirmSend(env config.Env, number string, yes bool) error {
	prod := env == config.EnvProd
	if yes {
		if !prod {
			return nil
		}
		_, _ = fmt.Fprintf(s.deps.Stderr, "notice: --yes does not skip the %s confirmation for the production KSeF\n", prodWord)
	}
	ask := s.confirmer()
	if ask == nil {
		if prod {
			_, _ = fmt.Fprintf(s.deps.Stderr, "no terminal to type %s on, so the invoice was not sent to the production KSeF\n", prodWord)
		} else {
			_, _ = fmt.Fprintf(s.deps.Stderr, "no terminal to ask on; run it in a terminal or add --yes to send to %s\n", strings.ToUpper(string(env)))
		}
		return exitcode.ErrAborted
	}
	ans, err := ask(sendPrompt(env, number, colorOK(s.deps.Stderr)))
	if err != nil {
		return fmt.Errorf("%w: %v", exitcode.ErrAborted, err)
	}
	ans = strings.TrimSpace(ans)
	if prod {
		if ans == prodWord {
			return nil
		}
		return exitcode.ErrAborted
	}
	if strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes") {
		return nil
	}
	return exitcode.ErrAborted
}
