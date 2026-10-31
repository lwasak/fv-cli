// Package cli holds the Cobra command tree of fv.
package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/httpx"
	"github.com/lwasak/fv-cli/internal/logging"
	"github.com/lwasak/fv-cli/internal/secrets"
	"github.com/lwasak/fv-cli/internal/useragent"
)

// Deps are the process-level collaborators commands use, injected so tests
// can run commands in-process.
type Deps struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// GOOS is the operating system name used to resolve default paths.
	GOOS string
	// Home returns the user's home directory.
	Home func() (string, error)
	// Secrets is the keyring backend; nil means the OS keyring.
	Secrets secrets.Backend
	// Prompt asks the user a question without echo (passphrases); nil means a no-echo
	// terminal read when stdin is a terminal, and no prompt otherwise.
	Prompt func(question string) (string, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// KSeFEndpoint is a Go-only test seam that points the KSeF client at a stand-in server.
	// Nil (as in DefaultDeps) means the fixed per-environment URL and the hardened client.
	// No flag, config key or environment variable can set it.
	KSeFEndpoint func(config.Env) (baseURL string, client *http.Client)
	// Endpoints is a Go-only test seam for the public registries and NBP, like KSeFEndpoint.
	// service is "whitelist", "vies" or "nbp". Nil (as in DefaultDeps) means the fixed URL
	// and httpx.NewClient(). No flag, config key or environment variable can set it.
	Endpoints func(service string) (baseURL string, client *http.Client)
	// Confirm asks the user to confirm a send and returns the typed answer; nil means a line
	// read from the terminal on stdin, and unavailable when stdin is not a terminal.
	Confirm func(prompt string) (string, error)
	// Ctx, when set, is the context of the whole run: Execute cancels it on Ctrl-C, which
	// ends a wait for the archive lock and every request made with the command's context.
	Ctx context.Context
	// Sleep replaces the wait between KSeF status polls; nil means a real, cancellable sleep.
	// It is a Go-only test seam; no flag, config key or environment variable can set it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Rand is the source of randomness for session keys; nil means crypto/rand.Reader.
	Rand io.Reader
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// prompter returns the passphrase prompt: Deps.Prompt, else a no-echo read from the
// terminal on stdin, else nil (no prompt available).
func (d Deps) prompter() func(string) (string, error) {
	if d.Prompt != nil {
		p := d.Prompt
		return func(q string) (string, error) {
			defer beginPrompt(nil)()
			return p(q)
		}
	}
	f, ok := d.Stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	return func(q string) (string, error) {
		_, _ = fmt.Fprint(d.Stderr, q)
		// If Ctrl-C stops fv while the terminal is in no-echo mode, put the echo back first.
		var restore func()
		if old, err := term.GetState(int(f.Fd())); err == nil {
			restore = func() { _ = term.Restore(int(f.Fd()), old) }
		}
		defer beginPrompt(restore)()
		b, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(d.Stderr)
		return string(b), err
	}
}

// endpoint returns the base URL and client for a public service: the Deps.Endpoints seam
// when set, else defURL with the hardened client.
func (s *session) endpoint(service, defURL string) (string, *http.Client) {
	if s.deps.Endpoints != nil {
		base, hc := s.deps.Endpoints(service)
		if hc == nil {
			hc = httpx.NewClient()
		}
		return base, hc
	}
	return defURL, httpx.NewClient()
}

// confirmer returns the confirmation prompt: Deps.Confirm, else a line read from the terminal
// on stdin, else nil (no prompt available).
func (s *session) confirmer() func(string) (string, error) {
	if s.deps.Confirm != nil {
		c := s.deps.Confirm
		return func(q string) (string, error) {
			defer beginPrompt(nil)()
			return c(q)
		}
	}
	f, ok := s.deps.Stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	rd := bufio.NewReader(f)
	return func(prompt string) (string, error) {
		_, _ = fmt.Fprint(s.deps.Stderr, prompt)
		defer beginPrompt(nil)()
		line, err := rd.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
}

// rand returns Deps.Rand, else crypto/rand.Reader.
func (s *session) rand() io.Reader {
	if s.deps.Rand != nil {
		return s.deps.Rand
	}
	return rand.Reader
}

// DefaultDeps returns Deps wired to the real process.
func DefaultDeps() Deps {
	return Deps{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		GOOS: runtime.GOOS, Home: os.UserHomeDir,
	}
}

// runContext is what config-needing commands receive: resolved paths, the loaded
// config and the resolved environment.
type runContext struct {
	Paths  config.Paths
	Config *config.Config
	Env    config.Env
}

// session holds the root's per-run state shared with subcommands.
type session struct {
	deps Deps
	// envFlag is nil when --env was not given.
	envFlag *string
}

// resolvePaths resolves the config and data locations for this run.
func (s *session) resolvePaths(cmd *cobra.Command) (config.Paths, error) {
	home := ""
	if s.deps.Home != nil {
		// A missing home is only an error when a default path needs it; ResolvePaths decides.
		if h, err := s.deps.Home(); err == nil {
			home = h
		}
	}
	flagConfig, _ := cmd.Flags().GetString("config")
	return config.ResolvePaths(s.deps.GOOS, s.deps.Getenv, home, flagConfig)
}

// requireConfig resolves paths, loads the config file and resolves the environment.
// A missing file is an error naming the path and suggesting fv init.
func (s *session) requireConfig(cmd *cobra.Command) (*runContext, error) {
	paths, err := s.resolvePaths(cmd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("config file %s not found; run `fv init` to create it", paths.ConfigFile)
		}
		return nil, err
	}
	env, err := config.ResolveEnv(s.envFlag, cfg.Ksef.Environment)
	if err != nil {
		return nil, err
	}
	return &runContext{Paths: paths, Config: cfg, Env: env}, nil
}

// NewRootCmd builds the fv command tree.
func NewRootCmd(deps Deps) *cobra.Command {
	root, _ := newRoot(deps)
	return root
}

// logger returns the fv.log logger for paths.DataDir; the file is created on first write.
func (s *session) logger(paths config.Paths) (*slog.Logger, func()) {
	return logging.New(paths.DataDir)
}

func newRoot(deps Deps) (*cobra.Command, *session) {
	useragent.SetVersion(Version)
	root := &cobra.Command{
		Use:   "fv",
		Short: "Issue Polish VAT invoices and send them to KSeF",
		Long: "fv is a command-line tool for issuing Polish VAT invoices and sending " +
			"them to KSeF, the national e-invoicing system.\n\n" +
			"The usual path: fv init (config and certificate), fv party add (a buyer), fv issue (a local draft; " +
			"nothing is sent), fv show (check it), fv send (to KSeF, after a confirmation), fv status (when the " +
			"answer is missing). Nothing reaches KSeF without fv send or --send. Exit codes: 0 ok, 1 usage or " +
			"config, 2 registry, 3 login or credential, 4 rejected by KSeF, 5 outcome unknown, 10 aborted. " +
			"The user guide is in docs/README.md.",
		Example:       "  fv version\n  fv issue --to acme --net \"18500,43\"\n  fv --env demo version",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	s := &session{deps: deps}
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("env") {
			v, _ := cmd.Flags().GetString("env")
			if _, err := config.ParseEnv(v); err != nil {
				return fmt.Errorf("--env: %w", err)
			}
			s.envFlag = &v
		}
		return nil
	}
	root.SetIn(deps.Stdin)
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)
	root.PersistentFlags().String("config", "", "path to the config.toml file (overrides FV_CONFIG and the default location)")
	root.PersistentFlags().String("env", "", "KSeF environment: test, demo or prod (default: ksef.environment, else test)")
	root.AddCommand(newVersionCmd(deps))
	root.AddCommand(newConfigCmd(s))
	root.AddCommand(newInitCmd(s))
	root.AddCommand(newKsefCmd(s))
	root.AddCommand(newHelpAmountsCmd())
	root.AddCommand(newPartyCmd(s))
	root.AddCommand(newListCmd(s))
	root.AddCommand(newDiscardCmd(s))
	root.AddCommand(newIssueCmd(s))
	root.AddCommand(newShowCmd(s))
	root.AddCommand(newSendCmd(s))
	root.AddCommand(newStatusCmd(s))
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	decorateBuiltins(root)
	return root, s
}
