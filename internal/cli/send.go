package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/store"
)

func newSendCmd(s *session) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <number>",
		Short: "Send a stored draft to KSeF after a confirmation",
		Long: "Shows the stored draft, asks for confirmation and sends it to KSeF: it logs in with the imported " +
			"certificate, opens an online session, encrypts the invoice (AES-256-CBC, the key wrapped for the " +
			"Ministry's certificate), posts it once, polls for the verdict for up to 120 seconds, stores the KSeF " +
			"number and the UPO (upo.xml) in the archive and closes the session.\n\n" +
			"The invoice is sent to the environment it was issued for. A different --env is refused. " +
			"The question names the environment: on test and demo answer y, or use --yes to skip it. " +
			"On prod fv asks you to type PRODUCTION; --yes never skips that, and without a terminal the send is aborted.\n\n" +
			"An invoice that is already in KSeF is never sent again. A draft in state sending or unknown " +
			"(the answer was lost or is still pending) is refused until fv status has resolved it. " +
			"fv also refuses an invoice dated after today in Warsaw, because KSeF rejects it. When KSeF refuses the " +
			"request itself (401, 403 or 429, not the invoice) nothing was stored there and the draft stays sendable.\n\n" +
			"Exit codes: 0 sent and accepted, 1 usage, config or refusal, 3 login or credential failed, " +
			"4 rejected by KSeF (code, description and details are printed), 5 outcome unknown (run fv status), " +
			"10 aborted at the question (nothing was sent).",
		Example: "  fv send FV/2026/10/001\n  fv send FV/2026/10/001 --yes\n  fv --env demo send FV/2026/10/001",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			return s.runSend(cmd, args[0], yes)
		},
	}
	cmd.Flags().Bool("yes", false, "skip the y/N question on test and demo; never skips the PRODUCTION confirmation (default: off)")
	return cmd
}

func (s *session) runSend(cmd *cobra.Command, number string, yes bool) error {
	rc, m, err := s.resolveStored(cmd, number)
	if err != nil {
		return err
	}
	if err := s.checkSendable(rc, m); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	renderInvoice(out, m, previewOpts{Color: colorOK(out)})
	_, _ = fmt.Fprintln(out)
	return s.sendStored(cmd, rc, m.Number, yes)
}

// resolveStored loads the config and finds number. The invoice's own environment is the one
// used from here on; an explicit --env that differs from it is a usage error.
func (s *session) resolveStored(cmd *cobra.Command, number string) (*runContext, *store.Meta, error) {
	rc, m, err := s.locateStored(cmd, number)
	if err != nil {
		return nil, nil, err
	}
	rc2 := *rc
	rc2.Env = m.Env
	return &rc2, m, nil
}

// locateStored loads the config and finds number: in the selected environment first and, when
// --env was not given, in the others. The returned runContext keeps the SELECTED environment;
// an explicit --env that differs from the invoice's is a usage error, the one rule shared by
// send, status, discard and show.
func (s *session) locateStored(cmd *cobra.Command, number string) (*runContext, *store.Meta, error) {
	rc, err := s.requireConfig(cmd)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.store(rc).Locate(number, rc.Env)
	if err != nil {
		return nil, nil, err
	}
	if s.envFlag != nil && rc.Env != m.Env {
		return nil, nil, fmt.Errorf("%s belongs to the %s environment, but --env %s was given; use --env %s or leave --env out",
			m.Number, m.Env, rc.Env, m.Env)
	}
	return rc, m, nil
}
