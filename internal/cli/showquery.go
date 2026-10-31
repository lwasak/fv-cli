package cli

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/ksefcache"
	"github.com/lwasak/fv-cli/internal/listing"
)

// showByQuery finds an invoice number that is neither in the archive nor in the cache by one
// targeted metadata query per subject (exact invoiceNumber, as seller then as buyer) over the
// date range of the command, caches what it finds and shows the one match. It keeps to the
// rules of every metadata query: one login, calls spaced out, at most DefaultQueryBudget of
// them, nothing retried.
func (s *session) showByQuery(cmd *cobra.Command, rc *runContext, cache *ksefcache.Cache, number string, o showOpts) error {
	r, err := listing.ParseRange(o.from, o.to, "", s.deps.now(), false)
	if err != nil {
		return err
	}
	if len(number) > ksef.MaxInvoiceNumberLength {
		return fmt.Errorf("%q is not in the archive or the cache, and an invoice number is at most %d characters long", clean(cut(number, 40)), ksef.MaxInvoiceNumberLength)
	}
	conn, done, err := s.connectKSeF(cmd, rc)
	if errors.Is(err, credentials.ErrNoCredential) {
		return fmt.Errorf("%q is not in the archive or the cache, and KSeF cannot be asked: no certificate for %s (run `fv init` to import one)", clean(number), rc.Env)
	}
	if err != nil {
		return err
	}
	defer done()
	errw := s.deps.Stderr
	_, _ = fmt.Fprintf(errw, "%s: asking KSeF for invoice number %q, issue dates %s to %s\n", rc.Env, clean(number), r.From, r.To)

	budget := ksef.DefaultQueryBudget
	var found []ksef.InvoiceMeta
	seen := map[string]bool{}
	var failure error
	for _, subj := range []ksef.SubjectType{ksef.Subject1, ksef.Subject2} {
		got, err := conn.client.QueryAllMetadataFiltered(conn.ctx, conn.sess, subj, r.From, r.To, ksef.MetadataFilter{InvoiceNumber: number}, &budget)
		for _, m := range got {
			// the number is an exact match on KSeF's side; check it here as well
			if m.InvoiceNumber == number && !seen[m.KSeFNumber] {
				seen[m.KSeFNumber] = true
				found = append(found, m)
			}
		}
		if err != nil {
			failure = showQueryFailure(err)
			break
		}
	}
	if n := conn.client.SkippedRecords(); n > 0 {
		_, _ = fmt.Fprintf(errw, "warning: %d KSeF record(s) with an unreadable KSeF number or issue date were skipped\n", n)
	}
	if len(found) > 0 {
		if err := cache.Remember(conn.ctx, found); err != nil {
			_, _ = fmt.Fprintf(errw, "warning: the KSeF rows could not be cached: %v\n", err)
		}
	}
	switch {
	case len(found) == 0 && failure != nil:
		return failure
	case len(found) == 0:
		return fmt.Errorf("%q not found in KSeF between %s and %s; widen with --from/--to", clean(number), r.From, r.To)
	}
	if failure != nil {
		_, _ = fmt.Fprintf(errw, "warning: %v; other invoices with this number may exist\n", failure)
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].IssueDate != found[j].IssueDate {
			return found[i].IssueDate < found[j].IssueDate
		}
		return found[i].KSeFNumber < found[j].KSeFNumber
	})
	if len(found) > 1 {
		es := make([]ksefcache.Entry, len(found))
		for i, m := range found {
			es[i] = ksefcache.Entry{InvoiceMeta: m}
		}
		return ambiguousNumber(number, es, fmt.Sprintf("in KSeF between %s and %s", r.From, r.To))
	}
	return s.showFromKSeF(cmd, rc, cache, found[0].KSeFNumber, o, conn)
}

// showQueryFailure words a failed lookup query; the hints of fv list (--as) do not apply.
func showQueryFailure(err error) error {
	if errors.Is(err, ksef.ErrQueryBudget) {
		return fmt.Errorf("KSeF query stopped: this run used its %d calls; use a shorter --from/--to range", ksef.DefaultQueryBudget)
	}
	return queryFailure(err)
}
