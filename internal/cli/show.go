package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/ksefcache"
	"github.com/lwasak/fv-cli/internal/store"
)

func newShowCmd(s *session) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <number>",
		Short: "Show an invoice from the archive or from KSeF, or print its exact XML with --xml",
		Long: "Prints the invoice stored in the archive in readable form: environment, number and dates, " +
			"seller and buyer with their VAT status and when it was checked, the lines, net, VAT and gross per rate, " +
			"the total, the currency, the bank account with its White List status, the state, the KSeF number and " +
			"whether a UPO is stored. With --xml it prints the stored invoice.xml byte for byte, after checking " +
			"it against the hash in meta.json, so the output can be piped to a file or a validator. " +
			"The number is looked up in the selected environment first (--env or ksef.environment); without an explicit --env the others are searched too, and with one a number from another environment is refused. " +
			"Any invoice listed by fv list can be shown, including purchases: give its KSeF number, or its invoice number when only one listed invoice has it. " +
			"A KSeF invoice that is not stored yet is downloaded once (one login, one request) and kept in a local cache, hash-checked on every read. " +
			"An invoice number that is neither in the archive nor in the cache is looked up in KSeF with one targeted metadata query for that exact number, as seller and as buyer, over the issue dates " +
			"of the default range of fv list (the current month and the two before it) or --from and --to (at most 366 days; KSeF is asked for at most 100 days per request); what it finds is cached. " +
			"One match is shown; several are listed with their KSeF numbers (exit 1); none ends with exit 1 and a hint to widen the range. " +
			"--offline contacts KSeF for nothing: only the archive and the cache are used. " +
			"An archived invoice is not sent anywhere.",
		Example: "  fv show FV/2026/10/001\n  fv show FV/2026/10/001 --xml > invoice.xml\n  fv show 5252248481-20261012-0200002AF62A-20\n  fv show ZAK/09/15 --from 2026-06-01 --to 2026-09-30\n  fv show ZAK/09/15 --offline\n  fv --env demo show FV/2026/10/001",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var o showOpts
			o.xml, _ = cmd.Flags().GetBool("xml")
			o.offline, _ = cmd.Flags().GetBool("offline")
			o.from, _ = cmd.Flags().GetString("from")
			o.to, _ = cmd.Flags().GetString("to")
			return s.runShow(cmd, args[0], o)
		},
	}
	f := cmd.Flags()
	f.Bool("xml", false, "print the stored invoice.xml exactly as stored, and nothing else (default: the readable invoice)")
	f.Bool("offline", false, "do not contact KSeF: use only the archive and the local cache")
	f.String("from", "", "first issue date of the KSeF lookup by invoice number, as YYYY-MM-DD (default: first day of the month two months back)")
	f.String("to", "", "last issue date of the KSeF lookup by invoice number, as YYYY-MM-DD (default: last day of the current month)")
	return cmd
}

// showOpts are the flags of fv show.
type showOpts struct {
	xml, offline bool
	from, to     string
}

func (s *session) runShow(cmd *cobra.Command, arg string, o showOpts) error {
	asXML := o.xml
	rc, err := s.requireConfig(cmd)
	if err != nil {
		return err
	}
	st := s.store(rc)
	ksefNo := ""
	if verr := ksef.ValidateKSeFNumber(arg); verr != nil && ksef.HasKSeFNumberShape(arg) {
		// it has the shape of a KSeF number, so it is one with a typo, not an invoice number
		return fmt.Errorf("%w; check the number for a typo", verr)
	} else if verr == nil {
		ksefNo = arg
		m, err := st.FindByKSeFNumber(rc.Env, arg)
		switch {
		case err == nil:
			return s.showArchived(cmd, rc, m, asXML)
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
	} else {
		m, err := st.Locate(arg, rc.Env)
		switch {
		case err == nil:
			if s.envFlag != nil && rc.Env != m.Env {
				return fmt.Errorf("%s belongs to the %s environment, but --env %s was given; use --env %s or leave --env out",
					m.Number, m.Env, rc.Env, m.Env)
			}
			return s.showArchived(cmd, rc, m, asXML)
		case !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrInvalidNumber):
			return err
		}
	}

	nip := rc.Config.Seller.NIP
	cache, err := ksefcache.Open(rc.Paths.DataDir, rc.Env, nip, s.deps.now)
	if err != nil {
		if ksefNo == "" {
			return notInCache(arg)
		}
		return err
	}
	if ksefNo == "" {
		es, err := cache.ByInvoiceNumber(arg)
		if err != nil {
			return err
		}
		switch len(es) {
		case 0:
			if o.offline {
				return notInCache(arg)
			}
			return s.showByQuery(cmd, rc, cache, arg, o)
		case 1:
			ksefNo = es[0].KSeFNumber
		default:
			return ambiguousNumber(arg, es, "in the KSeF rows of your last fv list")
		}
	}
	return s.showFromKSeF(cmd, rc, cache, ksefNo, o, nil)
}

func (s *session) showArchived(cmd *cobra.Command, rc *runContext, m *store.Meta, asXML bool) error {
	out := cmd.OutOrStdout()
	if asXML {
		b, err := s.store(rc).XML(m.Env, m.Number)
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
