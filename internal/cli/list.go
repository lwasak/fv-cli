package cli

import (
	"fmt"
	"regexp"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/store"
)

var monthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func newListCmd(s *session) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List archived invoices with their state and KSeF number",
		Long: "Lists the invoices in the local archive, sorted by environment, issue date and number. " +
			"Columns: NUMBER, ENV, STATE, ISSUED, GROSS, CUR and KSEF-NUMBER (- until KSeF accepts the invoice). " +
			"Without --env every environment is listed (the ksef.environment default is not used here); " +
			"with the global --env flag only that environment is. --month YYYY-MM keeps the invoices " +
			"issued in that month. Nothing is sent to KSeF. States: reserved, draft, sending, accepted, " +
			"rejected, unknown and void-draft.",
		Example: "  fv list\n  fv list --month 2026-10\n  fv --env prod list --month 2026-10",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			month, _ := cmd.Flags().GetString("month")
			return s.runList(cmd, month)
		},
	}
	cmd.Flags().String("month", "", "only invoices issued in this month, as YYYY-MM (default: all months)")
	return cmd
}

func (s *session) runList(cmd *cobra.Command, month string) error {
	if month != "" && !monthRe.MatchString(month) {
		return fmt.Errorf("--month %q: use the form YYYY-MM, for example 2026-10", month)
	}
	paths, err := s.resolvePaths(cmd)
	if err != nil {
		return err
	}
	f := store.Filter{Month: month}
	if s.envFlag != nil {
		env, err := config.ParseEnv(*s.envFlag)
		if err != nil {
			return fmt.Errorf("--env: %w", err)
		}
		f.Env = &env
	}
	st := store.Open(paths.DataDir, s.deps.now)
	if s.deps.Ctx != nil {
		st = st.WithContext(s.deps.Ctx)
	}
	metas, skipped, err := st.ListWithWarnings(f)
	if err != nil {
		return err
	}
	for _, w := range skipped {
		_, _ = fmt.Fprintf(s.deps.Stderr, "warning: %s\n", w)
	}
	out := cmd.OutOrStdout()
	if len(metas) == 0 {
		_, err := fmt.Fprintln(out, "no invoices")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NUMBER\tENV\tSTATE\tISSUED\tGROSS\tCUR\tKSEF-NUMBER")
	for _, m := range metas {
		ksefNo := "-"
		if m.KSeF != nil && m.KSeF.KSeFNumber != "" {
			ksefNo = m.KSeF.KSeFNumber
		}
		cur := m.Currency
		if cur == "" {
			cur = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Number, m.Env, m.State, m.IssueDate, money.FormatPL(m.Gross), cur, ksefNo)
	}
	return tw.Flush()
}
