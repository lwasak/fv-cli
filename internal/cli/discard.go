package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/store"
)

func newDiscardCmd(s *session) *cobra.Command {
	return &cobra.Command{
		Use:   "discard <number>",
		Short: "Discard an unsent invoice draft and release or void its number",
		Long: "Discards a local draft that was never accepted by KSeF. If its number is the latest in its " +
			"counter, the number is released (the folder is deleted and the next invoice reuses the number). " +
			"Otherwise the draft stays in the archive marked void-draft, so every gap in the numbering can be " +
			"explained. A rejected duplicate (KSeF status 440) is always voided, never released: its number is already used in KSeF. Only reserved, draft and rejected invoices can be discarded; sending, unknown and " +
			"accepted invoices are refused. Discarding is destructive, so the number must exist in the selected environment (--env or ksef.environment); one that only exists in another environment is refused and the error names the --env to use. Nothing is sent to KSeF.",
		Example: "  fv discard FV/2026/10/003\n  fv --env demo discard FV/2026/10/003",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.runDiscard(cmd, args[0])
		},
	}
}

func (s *session) runDiscard(cmd *cobra.Command, number string) error {
	// An explicit --env must match the invoice's environment: discarding is destructive, and
	// the same number often exists in test and prod.
	rc, m, err := s.locateStored(cmd, number)
	if err != nil {
		return err
	}
	if m.Env != rc.Env {
		return fmt.Errorf("%s exists in the %s environment, not in the selected %s; run `fv --env %s discard %s` to discard it there",
			m.Number, m.Env, rc.Env, m.Env, m.Number)
	}
	res, err := s.store(rc).Discard(m.Env, m.Number)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	switch res {
	case store.Released:
		_, err = fmt.Fprintf(out, "discarded %s (%s): number released, the next invoice will reuse it\n", m.Number, m.Env)
	default:
		if m.KSeF != nil && m.KSeF.Duplicate {
			_, err = fmt.Fprintf(out, "discarded %s (%s): kept as void-draft, the number is never released because KSeF already holds an invoice with it (%s)\n",
				m.Number, m.Env, orDash(m.KSeF.OriginalKSeFNumber))
			break
		}
		_, err = fmt.Fprintf(out, "discarded %s (%s): newer numbers exist, so it is kept as void-draft\n", m.Number, m.Env)
	}
	return err
}
