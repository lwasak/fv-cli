package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/ksefcache"
	"github.com/lwasak/fv-cli/internal/listing"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/store"
)

// nameWidth is the longest party name in the default table; --wide shows full names.
const nameWidth = 22

type listOpts struct {
	from, to, month, as string
	offline, wide       bool
}

func newListCmd(s *session) *cobra.Command {
	var o listOpts
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your sales and purchase invoices from the archive and KSeF",
		Long: "Lists invoices of the environment in use (--env, else ksef.environment), oldest first: the invoices " +
			"in the local archive and the sales and purchases KSeF reports for your NIP. Without flags the range " +
			"is the current month and the two before it, by issue date.\n\n" +
			"Columns: ISSUED, NUMBER, DIR (sale or purchase), SELLER, BUYER, GROSS, CUR and STATE. STATE is the " +
			"archive state (reserved, draft, sending, accepted, rejected, unknown, void-draft) or ksef for an " +
			"invoice that only KSeF has: a purchase, or a sale made outside fv. --wide adds the full names, NIPs, " +
			"the invoice KIND and the KSEF-NUMBER, which fv show accepts for any listed invoice.\n\n" +
			"The list logs in to KSeF (certificate and passphrase as for fv ksef check) and asks it for the " +
			"invoice metadata, then keeps what it learned in a local cache (ksef-cache in the data folder), " +
			"which fv show reads. KSeF limits the metadata query to 20 requests an hour, so fv makes at most 10 " +
			"per run and never repeats a refused one; use --as to query one side only and --offline to list " +
			"the archive and the cached rows without any request. The range may span at most 366 days when KSeF " +
			"is asked.\n\n" +
			"Only the table goes to stdout (or `no invoices`); the range, counts and warnings go to stderr. " +
			"The archive rows are always shown, also when KSeF cannot be queried. Exit codes: 0 ok (also when no " +
			"certificate has been imported yet), 1 usage, config or a failed query (rate limit, network, missing " +
			"InvoiceRead permission), 3 the login or the certificate failed.\n\n" +
			"Without a config file the archive of every environment is listed and KSeF is not asked.",
		Example: "  fv list\n  fv list --month 2026-09\n  fv list --from 2026-07-01 --to 2026-09-30 --as buyer\n" +
			"  fv list --offline --wide\n  fv --env prod list --as seller",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return s.runList(cmd, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.from, "from", "", "first issue date, as YYYY-MM-DD (default: first day of the month two months back)")
	f.StringVar(&o.to, "to", "", "last issue date, as YYYY-MM-DD (default: last day of the current month)")
	f.StringVar(&o.month, "month", "", "one month, as YYYY-MM (instead of --from and --to)")
	f.StringVar(&o.as, "as", "all", "which invoices: seller (sales), buyer (purchases) or all")
	f.BoolVar(&o.offline, "offline", false, "do not ask KSeF: show the archive and the rows cached by earlier lists")
	f.BoolVar(&o.wide, "wide", false, "add full names, NIPs, the invoice kind and the KSeF number")
	return cmd
}

func (s *session) runList(cmd *cobra.Command, o listOpts) error {
	side, err := listing.ParseSide(o.as)
	if err != nil {
		return err
	}
	paths, err := s.resolvePaths(cmd)
	if err != nil {
		return err
	}
	if _, serr := os.Stat(paths.ConfigFile); errors.Is(serr, fs.ErrNotExist) {
		return s.listArchiveOnly(cmd, paths, o, side)
	}
	rc, err := s.requireConfig(cmd)
	if err != nil {
		return err
	}
	r, err := listing.ParseRange(o.from, o.to, o.month, s.deps.now(), o.offline)
	if err != nil {
		return err
	}
	errw := s.deps.Stderr
	_, _ = fmt.Fprintf(errw, "%s: issue dates %s to %s\n", strings.ToUpper(string(rc.Env)), r.From, r.To)

	env := rc.Env
	local, skipped, err := s.store(rc).ListWithWarnings(store.Filter{Env: &env, From: r.From, To: r.To})
	if err != nil {
		return err
	}
	for _, w := range skipped {
		_, _ = fmt.Fprintf(errw, "warning: %s\n", w)
	}

	nip := rc.Config.Seller.NIP
	var cached []ksef.InvoiceMeta
	var cache *ksefcache.Cache
	if c, cerr := ksefcache.Open(rc.Paths.DataDir, rc.Env, nip, s.deps.now); cerr != nil {
		_, _ = fmt.Fprintf(errw, "warning: KSeF rows not available: %v\n", cerr)
	} else {
		cache = c
		entries, err := cache.InRange(r.From, r.To)
		if err != nil {
			_, _ = fmt.Fprintf(errw, "warning: the KSeF cache is not used: %v\n", err)
		}
		for _, e := range entries {
			if e.IssueDate != "" {
				cached = append(cached, e.InvoiceMeta)
			}
		}
	}

	var fetched []ksef.InvoiceMeta
	var failure error
	switch {
	case o.offline:
		s.noteLastSync(cache)
	case cache == nil:
		failure = fmt.Errorf("KSeF not queried: the seller NIP %q in the config is not a 10 digit NIP", nip)
	default:
		fetched, failure = s.syncKSeF(cmd, rc, cache, side, r)
	}

	rows := listing.Merge(local, append(fetched, cached...), nip, side, r)
	if err := s.printRows(cmd, rows, o.wide, false); err != nil {
		return err
	}
	s.printRowNotes(rows)
	return failure
}

// noteLastSync tells when the cache was last filled.
func (s *session) noteLastSync(c *ksefcache.Cache) {
	if c == nil {
		return
	}
	if t, err := c.LastSync(); err == nil && !t.IsZero() {
		_, _ = fmt.Fprintf(s.deps.Stderr, "offline: archive and KSeF rows cached on %s\n", t.In(time.Local).Format("2006-01-02 15:04"))
	} else {
		_, _ = fmt.Fprintln(s.deps.Stderr, "offline: archive only, no KSeF rows have been cached yet (run fv list without --offline)")
	}
}

// syncKSeF logs in, queries the metadata of the requested sides within the call budget and
// stores it in the cache. The invoices found come back even when a later call failed; the
// returned error is what the command ends with (nil when everything worked or when there is
// simply no certificate yet).
func (s *session) syncKSeF(cmd *cobra.Command, rc *runContext, cache *ksefcache.Cache, side listing.Side, r listing.Range) ([]ksef.InvoiceMeta, error) {
	errw := s.deps.Stderr
	l, closeLog := s.logger(rc.Paths)
	defer closeLog()
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	if ctx == nil {
		ctx = context.Background()
	}
	client, sess, err := s.ksefLogin(ctx, rc, l)
	if err != nil {
		if errors.Is(err, credentials.ErrNoCredential) {
			_, _ = fmt.Fprintf(errw, "KSeF not queried: no certificate for %s, run `fv init` to import one\n", rc.Env)
			return nil, nil
		}
		return nil, fmt.Errorf("KSeF not queried: %w", err)
	}
	var subjects []ksef.SubjectType
	switch side {
	case listing.SideSeller:
		subjects = []ksef.SubjectType{ksef.Subject1}
	case listing.SideBuyer:
		subjects = []ksef.SubjectType{ksef.Subject2}
	default:
		subjects = []ksef.SubjectType{ksef.Subject1, ksef.Subject2}
	}
	budget := ksef.DefaultQueryBudget
	var fetched []ksef.InvoiceMeta
	var failure error
	for _, subj := range subjects {
		got, err := client.QueryAllMetadata(ctx, sess, subj, r.From, r.To, &budget)
		fetched = append(fetched, got...)
		if err != nil {
			failure = queryFailure(err)
			break
		}
	}
	if failure == nil || len(fetched) > 0 {
		if err := cache.Merge(ctx, fetched); err != nil {
			_, _ = fmt.Fprintf(errw, "warning: the KSeF rows could not be cached: %v\n", err)
		}
	}
	if failure == nil {
		_, _ = fmt.Fprintf(errw, "KSeF %s: %s\n", rc.Env, countInvoices(len(fetched)))
	} else if len(fetched) > 0 {
		_, _ = fmt.Fprintf(errw, "KSeF %s: %s before the query stopped\n", rc.Env, countInvoices(len(fetched)))
	}
	return fetched, failure
}

func countInvoices(n int) string {
	if n == 1 {
		return "1 invoice"
	}
	return fmt.Sprintf("%d invoices", n)
}

// queryFailure words a failed metadata query for the user.
func queryFailure(err error) error {
	var ke *ksef.Error
	switch {
	case errors.Is(err, ksef.ErrQueryBudget):
		return fmt.Errorf("KSeF query stopped: this run used its %d calls, so the list may be incomplete; use a shorter range or --as", ksef.DefaultQueryBudget)
	case errors.Is(err, ksef.ErrQueryStuck):
		return fmt.Errorf("KSeF query stopped: %w", err)
	case ksef.IsRateLimited(err) && errors.As(err, &ke):
		wait := "later"
		if d := ke.RetryAfter(); d > 0 {
			wait = fmt.Sprintf("in %d s", int(d.Round(time.Second)/time.Second))
		}
		return fmt.Errorf("KSeF not queried: too many requests (KSeF allows 20 metadata queries an hour); try again %s, or use --offline", wait)
	}
	return fmt.Errorf("KSeF query failed: %w", err)
}

// listArchiveOnly is the list without a config file: the archive of every environment (or
// the one --env names), no KSeF.
func (s *session) listArchiveOnly(cmd *cobra.Command, paths config.Paths, o listOpts, side listing.Side) error {
	r, err := listing.ParseRange(o.from, o.to, o.month, s.deps.now(), true)
	if err != nil {
		return err
	}
	f := store.Filter{From: r.From, To: r.To}
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
	local, skipped, err := st.ListWithWarnings(f)
	if err != nil {
		return err
	}
	for _, w := range skipped {
		_, _ = fmt.Fprintf(s.deps.Stderr, "warning: %s\n", w)
	}
	_, _ = fmt.Fprintf(s.deps.Stderr, "no config file at %s: listing the local archive only (run `fv init`); issue dates %s to %s\n",
		paths.ConfigFile, r.From, r.To)
	rows := listing.Merge(local, nil, "", side, r)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Local.Env < rows[j].Local.Env })
	if err := s.printRows(cmd, rows, o.wide, true); err != nil {
		return err
	}
	s.printRowNotes(rows)
	return nil
}

// printRowNotes writes the hints and warnings that belong to the rows to stderr.
func (s *session) printRowNotes(rows []listing.Row) {
	for _, r := range rows {
		if r.Hint != "" {
			_, _ = fmt.Fprintf(s.deps.Stderr, "note: %s\n", clean(r.Hint))
		}
		if !r.GrossOK {
			_, _ = fmt.Fprintf(s.deps.Stderr, "warning: the amount of %s (%s) could not be read exactly and is shown as ?\n", clean(r.Number), r.KSeFNumber)
		}
	}
}

// clean makes text from KSeF safe for a terminal: control characters (escape sequences, line
// breaks) become spaces, and runs of spaces collapse into one.
func clean(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 {
			return ' '
		}
		return r
	}, s)), " ")
}

// cut shortens s to n runes, ending in an ellipsis when it was longer.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func (s *session) printRows(cmd *cobra.Command, rows []listing.Row, wide, showEnv bool) error {
	out := cmd.OutOrStdout()
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "no invoices")
		return err
	}
	name := func(n, id string) string {
		n = clean(n)
		if n == "" {
			n = clean(id)
		}
		if n == "" {
			return "-"
		}
		if wide {
			return n
		}
		return cut(n, nameWidth)
	}
	gross := make([]string, len(rows))
	width := 0
	for i, r := range rows {
		gross[i] = "?"
		if r.GrossOK {
			gross[i] = money.FormatPL(r.Gross)
		}
		width = max(width, utf8.RuneCountInString(gross[i]))
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	head := []string{"ISSUED", "NUMBER", "DIR"}
	if showEnv {
		head = append(head, "ENV")
	}
	if wide {
		head = append(head, "KIND", "SELLER", "SELLER-ID", "BUYER", "BUYER-ID")
	} else {
		head = append(head, "SELLER", "BUYER")
	}
	head = append(head, "GROSS", "CUR", "STATE")
	if wide {
		head = append(head, "KSEF-NUMBER")
	}
	fmt.Fprintln(tw, strings.Join(head, "\t"))
	for i, r := range rows {
		cells := []string{r.Issued, orDash(clean(r.Number)), r.Dir}
		if showEnv {
			cells = append(cells, string(r.Local.Env))
		}
		if wide {
			cells = append(cells, orDash(clean(r.Kind)), name(r.SellerName, r.SellerID), orDash(clean(r.SellerID)),
				name(r.BuyerName, r.BuyerID), orDash(clean(r.BuyerID)))
		} else {
			cells = append(cells, name(r.SellerName, r.SellerID), name(r.BuyerName, r.BuyerID))
		}
		cells = append(cells, strings.Repeat(" ", width-utf8.RuneCountInString(gross[i]))+gross[i], orDash(clean(r.Currency)), r.State)
		if wide {
			cells = append(cells, orDash(r.KSeFNumber))
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}
