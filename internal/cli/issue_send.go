package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/exitcode"
	"github.com/lwasak/fv-cli/internal/invoice"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/store"
)

// closeTimeout bounds the best-effort session close and the UPO fetch after a verdict.
const closeTimeout = 15 * time.Second

// sendStored sends the stored draft number to KSeF. It is the one send path of fv send and
// fv issue --send. The caller has already shown the preview.
//
// Order: refuse what cannot be sent (already in KSeF, unknown outcome, wrong state, wrong
// environment, issue date in the future), check the XML against its hash, confirm, log in,
// open the session, persist state sending with the session reference BEFORE the POST, post
// the invoice once, poll, and persist the verdict. The session is closed afterwards, best
// effort.
func (s *session) sendStored(cmd *cobra.Command, rc *runContext, number string, yes bool) error {
	st := s.store(rc)
	m, err := st.Locate(number, rc.Env)
	if err != nil {
		return err
	}
	if err := s.checkSendable(rc, m); err != nil {
		return err
	}
	xml, err := st.XML(m.Env, m.Number)
	if err != nil {
		return err
	}
	if err := s.confirmSend(m.Env, m.Number, yes); err != nil {
		return err
	}

	l, closeLog := s.logger(rc.Paths)
	defer closeLog()
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	client, sess, err := s.ksefLogin(ctx, rc, l)
	if err != nil {
		return err
	}
	on, err := ksef.OpenOnlineSession(ctx, client, sess, s.rand())
	if err != nil {
		l.Info("ksef open session failed", "env", string(m.Env), "number", m.Number)
		return fmt.Errorf("opening the KSeF session failed, nothing was sent: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		if cerr := on.Close(cctx); cerr != nil {
			l.Warn("ksef session close failed", "session", on.Ref, "error", cerr.Error())
			_, _ = fmt.Fprintf(s.deps.Stderr, "warning: closing the KSeF session %s failed (KSeF closes it by itself): %v\n", on.Ref, cerr)
		}
	}()

	return s.postAndWait(ctx, cmd, st, l, client, sess, on, m, xml)
}

// checkSendable refuses an invoice that must not be sent right now.
func (s *session) checkSendable(rc *runContext, m *store.Meta) error {
	if m.KSeF != nil && m.KSeF.KSeFNumber != "" {
		return fmt.Errorf("%s is already in KSeF as %s and is never sent again", m.Number, m.KSeF.KSeFNumber)
	}
	switch m.State {
	case store.StateDraft:
	case store.StateSending, store.StateUnknown:
		return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf(
			"%s may already be at KSeF (state %s); it is not sent again until `fv status %s` has resolved it",
			m.Number, m.State, m.Number))
	case store.StateRejected:
		return fmt.Errorf("%s was rejected by KSeF and cannot be sent again; discard it and issue a new invoice", m.Number)
	default:
		return fmt.Errorf("%s is in state %s and cannot be sent", m.Number, m.State)
	}
	if m.Env != rc.Env {
		return fmt.Errorf("%s belongs to the %s environment; it cannot be sent to %s", m.Number, strings.ToUpper(string(m.Env)), strings.ToUpper(string(rc.Env)))
	}
	issue, err := invoice.ParseDate(m.IssueDate)
	if err != nil {
		return fmt.Errorf("%s: bad issue date %q in meta.json: %w", m.Number, m.IssueDate, err)
	}
	if today := invoice.Today(s.deps.now()); issue.After(today) {
		return fmt.Errorf("%s is dated %s, which is after today (%s): KSeF rejects invoices dated in the future; "+
			"discard it and issue it on or after that date", m.Number, m.IssueDate, today.Format("2006-01-02"))
	}
	return nil
}

// postAndWait runs the session part of a send: persist sending, POST once, poll, persist.
func (s *session) postAndWait(ctx context.Context, cmd *cobra.Command, st *store.Store, l *slog.Logger,
	client *ksef.Client, sess *ksef.Session, on *ksef.OnlineSession, m *store.Meta, xml []byte) error {
	env, number := m.Env, m.Number
	now := s.deps.now()
	// The state and the session reference are on disk before the invoice leaves the machine.
	if _, err := st.Transition(env, number, store.StateSending, func(x *store.Meta) error {
		x.KSeF = &store.KSeFRef{SessionRef: on.Ref, SessionValidUntil: on.ValidUntil.UTC().Format(time.RFC3339), SentAt: &now}
		x.SentSessions = appendUnique(x.SentSessions, on.Ref)
		return nil
	}); err != nil {
		return fmt.Errorf("could not record the send in the archive, nothing was sent: %w", err)
	}
	l.Info("ksef send", "env", string(env), "number", number, "session", on.Ref)

	invRef, err := on.SendInvoice(ctx, xml)
	if err != nil {
		if ksef.NotAccepted(err) {
			if ksef.RefusedForSession(err) {
				// A refusal of the caller or the session (401, 403, 429, 21155, 21173, 21180,
				// 21184), not of the invoice: KSeF stored nothing, so the draft stays sendable.
				// Only invoice-level 4xx answers become "rejected".
				if _, terr := st.Transition(env, number, store.StateDraft, func(x *store.Meta) error { x.KSeF = nil; return nil }); terr != nil {
					return fmt.Errorf("could not return %s to draft: %w", number, terr)
				}
				return fmt.Errorf("KSeF refused the request, not the invoice; nothing was stored and %s is still a draft, retry `fv send %s`: %w", number, number, err)
			}
			return s.recordRejected(st, l, env, number, err)
		}
		l.Info("ksef send outcome unknown", "number", number, "error", err.Error())
		if terr := s.markUnknown(st, env, number); terr != nil {
			return terr
		}
		return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf(
			"sending %s failed (%v): KSeF may or may not have received it; run `fv status %s`", number, err, number))
	}
	if _, err := st.UpdateKSeF(env, number, func(r *store.KSeFRef) error { r.InvoiceRef = invRef; return nil }); err != nil {
		l.Warn("could not persist the invoice reference", "number", number, "error", err.Error())
	}

	status, err := client.WaitInvoice(ctx, sess, on.Ref, invRef, ksef.DefaultInvoiceBudget)
	if err != nil {
		l.Info("ksef status wait ended", "number", number, "error", err.Error())
		if terr := s.markUnknown(st, env, number); terr != nil {
			return terr
		}
		return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf(
			"%s was sent but KSeF has not given a final answer yet (%v); run `fv status %s`", number, err, number))
	}
	switch {
	case status.Accepted():
		return s.recordAccepted(ctx, cmd, st, l, client, sess, on.Ref, invRef, m, status,
			fmt.Sprintf("Sent %s to KSeF %s.", number, strings.ToUpper(string(env))))
	case status.Rejected():
		if done, err := s.selfDuplicate(ctx, cmd, st, l, client, sess, m, status); done {
			return err
		}
		return s.recordStatusRejected(st, l, env, number, status)
	}
	if terr := s.markUnknown(st, env, number); terr != nil {
		return terr
	}
	return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("%s: KSeF answered %d %s; run `fv status %s`", number, status.Code, status.Description, number))
}

func (s *session) markUnknown(st *store.Store, env config.Env, number string) error {
	if _, err := st.Transition(env, number, store.StateUnknown, nil); err != nil {
		return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("could not record the unknown outcome of %s (KSeF may hold it; run `fv status %s`): %w", number, number, err))
	}
	return nil
}

// refOf returns a private copy of x's KSeF references, installed on x, for a mutate func.
func refOf(x *store.Meta) *store.KSeFRef {
	r := store.KSeFRef{}
	if x.KSeF != nil {
		r = *x.KSeF
	}
	x.KSeF = &r
	return &r
}

// recordRejected stores a refusal KSeF gave to the POST itself (the invoice is not in KSeF).
func (s *session) recordRejected(st *store.Store, l *slog.Logger, env config.Env, number string, cause error) error {
	var ke *ksef.Error
	errors.As(cause, &ke)
	l.Info("ksef rejected the invoice", "number", number, "code", ke.Code)
	if _, err := st.Transition(env, number, store.StateRejected, func(x *store.Meta) error {
		r := refOf(x)
		r.StatusCode, r.StatusDescription, r.Details = ke.Code, ke.Description, ke.Details
		return nil
	}); err != nil {
		return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("KSeF rejected %s but the archive could not record it; run `fv status %s`: %w", number, number, err))
	}
	return exitcode.Wrap(exitcode.ErrRejected, cause)
}

// recordStatusRejected stores a final refusal from the status poll.
func (s *session) recordStatusRejected(st *store.Store, l *slog.Logger, env config.Env, number string, status ksef.InvoiceStatus) error {
	details := append([]string(nil), status.Details...)
	if status.Duplicate() && status.OriginalKSeFNumber != "" {
		details = append(details, "the invoice is already in KSeF as "+status.OriginalKSeFNumber)
	}
	l.Info("ksef rejected the invoice", "number", number, "code", status.Code)
	if _, err := st.Transition(env, number, store.StateRejected, func(x *store.Meta) error {
		r := refOf(x)
		r.StatusCode, r.StatusDescription, r.Details = status.Code, status.Description, details
		if status.Duplicate() {
			r.Duplicate, r.OriginalKSeFNumber, r.OriginalSessionRef = true, status.OriginalKSeFNumber, status.OriginalSessionRef
		}
		return nil
	}); err != nil {
		return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("KSeF rejected %s but the archive could not record it; run `fv status %s`: %w", number, number, err))
	}
	return exitcode.Wrap(exitcode.ErrRejected, &ksef.Error{Code: status.Code, Description: status.Description, Details: details})
}

// recordAccepted stores the KSeF number, then fetches and stores the UPO. A UPO failure is a
// warning: the invoice is in KSeF, and fv status fetches the UPO later.
func (s *session) recordAccepted(ctx context.Context, cmd *cobra.Command, st *store.Store, l *slog.Logger,
	client *ksef.Client, sess *ksef.Session, sessionRef, invRef string, m *store.Meta, status ksef.InvoiceStatus, header string) error {
	env, number := m.Env, m.Number
	if _, err := st.Transition(env, number, store.StateAccepted, func(x *store.Meta) error {
		r := refOf(x)
		r.SessionRef = sessionRef // the session that holds the invoice (an earlier one after a duplicate)
		r.InvoiceRef = invRef
		r.KSeFNumber = status.KSeFNumber
		r.AcquisitionDate = status.AcquisitionDate
		r.StatusCode, r.StatusDescription, r.Details = status.Code, status.Description, nil
		return nil
	}); err != nil {
		return exitcode.Wrap(exitcode.ErrUnknown, fmt.Errorf("%s was accepted by KSeF as %s but the archive could not be updated; run `fv status %s`: %w", number, status.KSeFNumber, number, err))
	}
	l.Info("ksef accepted the invoice", "number", number, "ksef", status.KSeFNumber)
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "%s\nKSeF number: %s\nAcquired:    %s\n", header, status.KSeFNumber, status.AcquisitionDate)
	if err := s.fetchUPO(ctx, st, client, sess, sessionRef, invRef, env, number); err != nil {
		l.Warn("could not fetch the UPO", "number", number, "error", err.Error())
		_, _ = fmt.Fprintf(s.deps.Stderr, "warning: the UPO could not be fetched (%v); run `fv status %s` to get it\n", err, number)
		return nil
	}
	_, _ = fmt.Fprintln(out, "UPO:         stored (upo.xml)")
	return nil
}

// fetchUPO downloads the UPO of an accepted invoice and stores it.
func (s *session) fetchUPO(ctx context.Context, st *store.Store, client *ksef.Client, sess *ksef.Session,
	sessionRef, invRef string, env config.Env, number string) error {
	upo, err := client.InvoiceUPO(ctx, sess, sessionRef, invRef)
	if err != nil {
		return err
	}
	return st.WriteUPO(env, number, upo)
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// selfDuplicate handles a 440 whose original session is one this very invoice was sent in
// earlier (a lost answer followed by a resend after a wrong unknown-to-draft, or a lost
// meta.json write). KSeF then holds THIS invoice: it is accepted, not rejected. fv finds it in
// the original session by its hash, records the KSeF number and fetches the UPO. done is false
// when the duplicate is somebody else's invoice, or the original cannot be confirmed; the
// caller then records the rejection (marked as a duplicate, never released).
func (s *session) selfDuplicate(ctx context.Context, cmd *cobra.Command, st *store.Store, l *slog.Logger,
	client *ksef.Client, sess *ksef.Session, m *store.Meta, status ksef.InvoiceStatus) (bool, error) {
	if !status.Duplicate() || status.OriginalSessionRef == "" {
		return false, nil
	}
	cur, err := st.Load(m.Env, m.Number)
	if err != nil || !slices.Contains(cur.SentSessions, status.OriginalSessionRef) {
		return false, nil
	}
	found, err := client.FindInvoiceByHash(ctx, sess, status.OriginalSessionRef, m.XMLSHA256)
	if err != nil || found == nil || !found.Accepted() || found.InvoiceRef == "" {
		l.Info("ksef duplicate points at an own session but the invoice could not be confirmed there",
			"number", m.Number, "session", status.OriginalSessionRef)
		return false, nil
	}
	l.Info("ksef duplicate is this invoice from an earlier send", "number", m.Number, "ksef", found.KSeFNumber)
	_, _ = fmt.Fprintf(s.deps.Stderr, "note: KSeF answered 440 (duplicate) because %s was already accepted by an earlier send; recording that\n", m.Number)
	return true, s.recordAccepted(ctx, cmd, st, l, client, sess, status.OriginalSessionRef, found.InvoiceRef, m, *found,
		fmt.Sprintf("%s (%s): accepted by KSeF (found from an earlier send).", m.Number, strings.ToUpper(string(m.Env))))
}
