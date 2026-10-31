package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/exitcode"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/store"
)

func newStatusCmd(s *session) *cobra.Command {
	return &cobra.Command{
		Use:   "status <number>",
		Short: "Ask KSeF what became of a sent invoice and update the archive",
		Long: "Shows the state of a stored invoice. A draft was never sent. For an invoice in state sending or " +
			"unknown (a send that crashed, lost its answer or ran out of waiting) fv logs in and asks KSeF, using the " +
			"session and invoice references stored before the send: an accepted invoice gets its KSeF number and " +
			"UPO (upo.xml), a rejected one is marked rejected and its details are printed. If the answer to the " +
			"send was lost, fv finds the invoice in the session by its hash; if KSeF provably never received it " +
			"(the session is closed or expired and holds no invoice) the invoice goes back to draft and can be sent again. " +
			"An accepted invoice without a stored UPO gets it fetched. Uses the environment the invoice was issued for; " +
			"a different --env is refused.\n\n" +
			"Exit codes: 0 resolved (accepted, back to draft, or a draft/accepted invoice shown), 3 login or credential failed, " +
			"4 rejected, 5 still unknown or still processing (run it again later).",
		Example: "  fv status FV/2026/10/001\n  fv --env demo status FV/2026/10/001",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.runStatus(cmd, args[0])
		},
	}
}

func (s *session) runStatus(cmd *cobra.Command, number string) error {
	rc, m, err := s.resolveStored(cmd, number)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	env := strings.ToUpper(string(m.Env))
	switch m.State {
	case store.StateReserved, store.StateDraft:
		_, err = fmt.Fprintf(out, "%s (%s): not sent, it is a local draft. To send: fv send %s\n", m.Number, env, m.Number)
		return err
	case store.StateVoid:
		_, err = fmt.Fprintf(out, "%s (%s): discarded draft (void-draft), it was never sent\n", m.Number, env)
		return err
	case store.StateRejected:
		printRejection(out, m)
		return exitcode.Wrap(exitcode.ErrRejected, rejectionError(m))
	case store.StateAccepted:
		if m.KSeF != nil && m.UPOSHA256 != "" {
			printAccepted(out, m, fmt.Sprintf("%s (%s): accepted by KSeF.", m.Number, env))
			_, err = fmt.Fprintln(out, "UPO:         stored (upo.xml)")
			return err
		}
	}

	l, closeLog := s.logger(rc.Paths)
	defer closeLog()
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	if m.KSeF == nil || m.KSeF.SessionRef == "" {
		return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("%s has no KSeF session reference in the archive, so fv cannot ask KSeF about it", m.Number))
	}
	client, sess, err := s.ksefLogin(ctx, rc, l)
	if err != nil {
		return err
	}
	st := s.store(rc)
	if m.State == store.StateAccepted {
		return s.statusFetchUPO(ctx, cmd, st, client, sess, m)
	}
	return s.statusResolve(ctx, cmd, st, l, client, sess, m)
}

// statusFetchUPO fetches the UPO of an accepted invoice that has none stored.
func (s *session) statusFetchUPO(ctx context.Context, cmd *cobra.Command, st *store.Store, client *ksef.Client, sess *ksef.Session, m *store.Meta) error {
	out := cmd.OutOrStdout()
	printAccepted(out, m, fmt.Sprintf("%s (%s): accepted by KSeF.", m.Number, strings.ToUpper(string(m.Env))))
	if m.KSeF.InvoiceRef == "" {
		return errors.New("the invoice reference is missing in the archive, so the UPO cannot be fetched")
	}
	if err := s.fetchUPO(ctx, st, client, sess, m.KSeF.SessionRef, m.KSeF.InvoiceRef, m.Env, m.Number); err != nil {
		return fmt.Errorf("fetching the UPO failed: %w", err)
	}
	_, err := fmt.Fprintln(out, "UPO:         fetched and stored (upo.xml)")
	return err
}

// statusResolve asks KSeF about an invoice in state sending or unknown.
func (s *session) statusResolve(ctx context.Context, cmd *cobra.Command, st *store.Store, l *slog.Logger,
	client *ksef.Client, sess *ksef.Session, m *store.Meta) error {
	out := cmd.OutOrStdout()
	sessionRef, invRef := m.KSeF.SessionRef, m.KSeF.InvoiceRef
	var status ksef.InvoiceStatus
	if invRef != "" {
		var err error
		if status, err = client.InvoiceStatus(ctx, sess, sessionRef, invRef); err != nil {
			return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("asking KSeF about %s failed: %w", m.Number, err))
		}
	} else {
		found, err := client.FindInvoiceByHash(ctx, sess, sessionRef, m.XMLSHA256)
		if err != nil {
			return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("searching the KSeF session for %s failed: %w", m.Number, err))
		}
		if found == nil {
			return s.statusNotFound(ctx, cmd, st, l, client, sess, m)
		}
		status, invRef = *found, found.InvoiceRef
		if invRef != "" {
			if _, err := st.UpdateKSeF(m.Env, m.Number, func(r *store.KSeFRef) error { r.InvoiceRef = invRef; return nil }); err != nil {
				l.Warn("could not persist the invoice reference", "number", m.Number, "error", err.Error())
			}
		}
	}
	l.Info("ksef status", "number", m.Number, "code", status.Code)
	switch {
	case status.Accepted():
		return s.recordAccepted(ctx, cmd, st, l, client, sess, sessionRef, invRef, m, status,
			fmt.Sprintf("%s (%s): accepted by KSeF.", m.Number, strings.ToUpper(string(m.Env))))
	case status.Rejected():
		if done, err := s.selfDuplicate(ctx, cmd, st, l, client, sess, m, status); done {
			return err
		}
		return s.recordStatusRejected(st, l, m.Env, m.Number, status)
	}
	if m.State == store.StateSending {
		if err := s.markUnknown(st, m.Env, m.Number); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(out, "%s: KSeF is still processing it (%d %s)\n", m.Number, status.Code, status.Description)
	return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("%s is still unknown; run `fv status %s` again in a moment", m.Number, m.Number))
}

// statusNotFound handles an invoice KSeF's session does not list: when the session is closed
// or expired and holds no invoice, KSeF never received it and it goes back to draft.
func (s *session) statusNotFound(ctx context.Context, cmd *cobra.Command, st *store.Store, l *slog.Logger,
	client *ksef.Client, sess *ksef.Session, m *store.Meta) error {
	ss, err := client.SessionStatus(ctx, sess, m.KSeF.SessionRef)
	if err != nil {
		return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("asking KSeF about the session of %s failed: %w", m.Number, err))
	}
	validUntil := ss.ValidUntil
	if validUntil.IsZero() {
		validUntil, _ = time.Parse(time.RFC3339, m.KSeF.SessionValidUntil)
	}
	expired := !validUntil.IsZero() && s.deps.now().After(validUntil)
	// Only a session that says it holds no invoice (a present count of 0) and has a known final
	// status, or is past its validity, proves KSeF never received this one. A body that lacks
	// the fields proves nothing.
	if ss.CountKnown && ss.InvoiceCount == 0 && ss.Code != 0 && (ss.Closed() || expired) {
		if _, err := st.Transition(m.Env, m.Number, store.StateDraft, func(x *store.Meta) error { x.KSeF = nil; return nil }); err != nil {
			return fmt.Errorf("could not return %s to draft: %w", m.Number, err)
		}
		l.Info("ksef never received the invoice; back to draft", "number", m.Number)
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: KSeF never received it (its session is closed and empty). It is a draft again; to send: fv send %s\n", m.Number, m.Number)
		return err
	}
	if m.State == store.StateSending {
		if err := s.markUnknown(st, m.Env, m.Number); err != nil {
			return err
		}
	}
	return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf(
		"KSeF does not list %s in its session yet (session open or holding other invoices); run `fv status %s` again later", m.Number, m.Number))
}

func printAccepted(out interface{ Write([]byte) (int, error) }, m *store.Meta, header string) {
	_, _ = fmt.Fprintf(out, "%s\nKSeF number: %s\nAcquired:    %s\n", header, m.KSeF.KSeFNumber, m.KSeF.AcquisitionDate)
}

func printRejection(out interface{ Write([]byte) (int, error) }, m *store.Meta) {
	if m.KSeF != nil && m.KSeF.Duplicate {
		who := "another invoice"
		if m.KSeF.OriginalKSeFNumber != "" {
			who = m.KSeF.OriginalKSeFNumber
		}
		_, _ = fmt.Fprintf(out, "%s (%s): rejected by KSeF as a duplicate: this number is already used in KSeF by %s.\n", m.Number, strings.ToUpper(string(m.Env)), who)
		return
	}
	_, _ = fmt.Fprintf(out, "%s (%s): rejected by KSeF, it is not in KSeF.\n", m.Number, strings.ToUpper(string(m.Env)))
}

func rejectionError(m *store.Meta) error {
	if m.KSeF == nil {
		return errors.New("rejected by KSeF")
	}
	return &ksef.Error{Code: m.KSeF.StatusCode, Description: m.KSeF.StatusDescription, Details: m.KSeF.Details}
}
