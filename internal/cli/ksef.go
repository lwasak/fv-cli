package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/logging"
	"github.com/lwasak/fv-cli/internal/secrets"
)

func newKsefCmd(s *session) *cobra.Command {
	ksefCmd := &cobra.Command{
		Use:     "ksef",
		Short:   "Work with the KSeF connection",
		Long:    "Commands that talk to KSeF, the national e-invoicing system.",
		Example: "  fv ksef check\n  fv --env demo ksef check",
	}
	ksefCmd.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Log in to KSeF with the imported certificate and show it",
		Long: "Logs in to KSeF in the configured environment with the certificate imported by fv init " +
			"(challenge, XAdES-signed request, status polling, one token redeem), then prints the " +
			"environment, the certificate subject, identifier type (NIP or PESEL), NIP, type, serial and " +
			"validity. The key passphrase comes from the keyring entry fv/ksef/<env>/<nip>, the environment " +
			"variable FV_SECRET_KSEF_<ENV>_<NIP> or a prompt; it is never a flag or a config value. " +
			"Expired or not-yet-valid certificates are refused before any request is sent, and a warning " +
			"is printed 30 days before expiry. Exit code 3 means the KSeF login or the credential failed.",
		Example: "  fv ksef check\n  fv --env demo ksef check",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return s.runKsefCheck(cmd)
		},
	})
	return ksefCmd
}

func (s *session) runKsefCheck(cmd *cobra.Command) error {
	rc, err := s.requireConfig(cmd)
	if err != nil {
		return err
	}
	l, closeLog := s.logger(rc.Paths)
	defer closeLog()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	if ctx == nil {
		ctx = context.Background()
	}
	_, _, cert, err := s.ksefAuth(ctx, rc, l)
	if err != nil {
		l.Info("ksef check failed", "env", string(rc.Env))
		return err
	}
	l.Info("ksef check succeeded", "env", string(rc.Env))

	_, err = fmt.Fprintf(s.deps.Stdout, "environment: %s\n%sauthenticated: OK\n", rc.Env, cert.info.Format(cert.ctxNIP))
	return err
}

// certReport is what a login learned about the credential, for fv ksef check to print.
type certReport struct {
	info   credentials.Info
	ctxNIP string
}

// ksefLogin loads the credential of rc.Env, checks it, and authenticates against KSeF. It
// returns the client and the in-memory session (tokens never leave the process). Credential
// problems are *credentialError (exit 3), a failed login wraps ksef.ErrAuth (exit 3).
func (s *session) ksefLogin(ctx context.Context, rc *runContext, l *slog.Logger) (*ksef.Client, *ksef.Session, error) {
	c, sess, _, err := s.ksefAuth(ctx, rc, l)
	return c, sess, err
}

func (s *session) ksefAuth(ctx context.Context, rc *runContext, l *slog.Logger) (*ksef.Client, *ksef.Session, certReport, error) {
	nip := rc.Config.Seller.NIP
	if nip == "" {
		return nil, nil, certReport{}, errors.New("seller.nip is not set in the config; run `fv init` or set it")
	}

	var backend secrets.Backend = secrets.KeyringBackend{}
	if s.deps.Secrets != nil {
		backend = s.deps.Secrets
	}
	store := &secrets.Store{Backend: backend, Getenv: s.deps.Getenv, Log: l, Redact: logging.Default}

	cred, err := credentials.Load(rc.Paths.DataDir, rc.Env, nip)
	if err != nil {
		return nil, nil, certReport{}, &credentialError{err: err}
	}
	cred.Source = credentials.PassphraseSource{Secrets: store, Prompt: s.deps.prompter()}

	info, err := cred.Info()
	if err != nil {
		return nil, nil, certReport{}, &credentialError{err: err}
	}
	ctxNIP, err := info.ContextNIP(nip)
	if err != nil {
		return nil, nil, certReport{}, &credentialError{err: err}
	}
	warning, err := info.CheckValidity(s.deps.now())
	if err != nil {
		return nil, nil, certReport{}, &credentialError{err: err}
	}
	if warning != "" {
		_, _ = fmt.Fprintf(s.deps.Stderr, "warning: %s\n", warning)
	}

	var client *ksef.Client
	if s.deps.KSeFEndpoint != nil {
		base, hc := s.deps.KSeFEndpoint(rc.Env)
		client = ksef.NewClientAt(rc.Env, base, hc, rc.Paths.DataDir, l)
	} else if client, err = ksef.NewClient(rc.Env, rc.Paths.DataDir, l); err != nil {
		return nil, nil, certReport{}, err
	}

	if s.deps.Sleep != nil {
		client.SetSleep(s.deps.Sleep)
	}
	sess, err := ksef.Authenticate(ctx, client, cred, ctxNIP)
	if err != nil {
		return nil, nil, certReport{}, err
	}
	return client, sess, certReport{info: info, ctxNIP: ctxNIP}, nil
}
