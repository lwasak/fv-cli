package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newShowCmd(s *session) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <number>",
		Short: "Show an archived invoice, or print its exact XML with --xml",
		Long: "Prints the invoice stored in the archive in readable form: environment, number and dates, " +
			"seller and buyer with their VAT status and when it was checked, the lines, net, VAT and gross per rate, " +
			"the total, the currency, the bank account with its White List status, the state, the KSeF number and " +
			"whether a UPO is stored. With --xml it prints the stored invoice.xml byte for byte, after checking " +
			"it against the hash in meta.json, so the output can be piped to a file or a validator. " +
			"The number is looked up in the selected environment first (--env or ksef.environment); without an explicit --env the others are searched too, and with one a number from another environment is refused. " +
			"Nothing is sent to KSeF.",
		Example: "  fv show FV/2026/10/001\n  fv show FV/2026/10/001 --xml > invoice.xml\n  fv --env demo show FV/2026/10/001",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			xml, _ := cmd.Flags().GetBool("xml")
			return s.runShow(cmd, args[0], xml)
		},
	}
	cmd.Flags().Bool("xml", false, "print the stored invoice.xml exactly as stored, and nothing else (default: the readable invoice)")
	return cmd
}

func (s *session) runShow(cmd *cobra.Command, number string, asXML bool) error {
	rc, m, err := s.locateStored(cmd, number)
	if err != nil {
		return err
	}
	st := s.store(rc)
	out := cmd.OutOrStdout()
	if asXML {
		b, err := st.XML(m.Env, m.Number)
		if err != nil {
			return err
		}
		_, err = out.Write(b)
		return err
	}
	renderInvoice(out, m, previewOpts{Color: colorOK(out), Detail: true})
	if m.Env != rc.Env {
		_, _ = fmt.Fprintf(out, "\nnote: %s was found in %s, not in the selected environment %s\n", m.Number, m.Env, rc.Env)
	}
	return nil
}
