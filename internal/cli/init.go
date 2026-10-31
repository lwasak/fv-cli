package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/fsafe"
	"github.com/lwasak/fv-cli/internal/logging"
	"github.com/lwasak/fv-cli/internal/secrets"
	"github.com/lwasak/fv-cli/internal/validate"
)

var errInputEnded = errors.New("input ended before all questions were answered; nothing was written")

func newInitCmd(s *session) *cobra.Command {
	var impCert, impKey string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the config file by answering a few questions",
		Long: "Asks for the seller NIP and the KSeF environment (test, demo or prod; empty means test) " +
			"and writes a minimal config.toml at the resolved config path. " +
			"If the file already exists, asks for confirmation before replacing it; answer N to keep it.\n\n" +
			"Then it offers to import a KSeF certificate for that environment and NIP: give a .p12/.pfx file, " +
			"or a .pem/.crt certificate and its encrypted key file. The private key must be encrypted. " +
			"The passphrase is asked without echo on the terminal, so run fv init in a terminal; it is checked " +
			"against the key, stored in the OS keyring and never written to a file. fv keeps its own encrypted copy " +
			"of the key and certificate in the data folder, and the original files can then be deleted. " +
			"To import the certificate of another environment, run fv --env demo init, answer N to keeping the " +
			"config and y to the import. See the user guide, docs/setup.md.\n\n" +
			"For a scripted run (the live test in internal/live), --import-cert (and --import-key for a .pem/.crt) " +
			"imports without the questions. The passphrase is then read from the environment variable " +
			"FV_SECRET_KSEF_<ENV>_<NIP> (or asked on the terminal) and, when it came from the variable, " +
			"is not written to the keyring; it is never a flag.",
		Example: "  fv init\n  fv --config ./config.toml init\n  fv --env demo init\n  fv init --import-cert cert.pem --import-key key.pem",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if impCert == "" && impKey != "" {
				return errors.New("--import-key needs --import-cert")
			}
			imp := importFiles{cert: impCert, key: impKey}
			paths, err := s.resolvePaths(cmd)
			if err != nil {
				return err
			}
			in := bufio.NewReader(s.deps.Stdin)
			out := s.deps.Stdout

			if _, err := os.Stat(paths.ConfigFile); err == nil {
				_, _ = fmt.Fprintf(out, "Overwrite %s? [y/N] ", paths.ConfigFile)
				line, ok := readLine(in)
				if a := strings.ToLower(line); !ok || (a != "y" && a != "yes") {
					_, _ = fmt.Fprintln(out, "config unchanged")
					// The kept config decides the environment and NIP of the import.
					cfg, err := config.Load(paths.ConfigFile)
					if err != nil || cfg.Seller.NIP == "" {
						return nil
					}
					env, err := config.ResolveEnv(s.envFlag, cfg.Ksef.Environment)
					if err != nil {
						return nil
					}
					return s.offerImport(paths, in, env, cfg.Seller.NIP, imp)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}

			var nip string
			for {
				_, _ = fmt.Fprint(out, "Seller NIP (10 digits): ")
				line, ok := readLine(in)
				if !ok {
					return errInputEnded
				}
				if err := validate.NIP(line); err != nil {
					_, _ = fmt.Fprintf(out, "invalid NIP: %v\n", err)
					continue
				}
				nip = line
				break
			}
			env := config.EnvTest
			for {
				_, _ = fmt.Fprint(out, "KSeF environment (test/demo/prod) [test]: ")
				line, ok := readLine(in)
				if !ok {
					return errInputEnded
				}
				if line == "" {
					break
				}
				e, err := config.ParseEnv(line)
				if err != nil {
					_, _ = fmt.Fprintf(out, "%v\n", err)
					continue
				}
				env = e
				break
			}

			if err := fsafe.MkdirAll(filepath.Dir(paths.ConfigFile)); err != nil {
				return err
			}
			if err := fsafe.WriteFile(paths.ConfigFile, config.MinimalTOML(nip, env)); err != nil {
				return err
			}
			l, closeLog := s.logger(paths)
			defer closeLog()
			l.Info("config written", "path", paths.ConfigFile, "env", string(env))
			_, _ = fmt.Fprintf(out, "config written: %s\n", paths.ConfigFile)
			return s.offerImport(paths, in, env, nip, imp)
		},
	}
	cmd.Flags().StringVar(&impCert, "import-cert", "", "import this certificate (.p12/.pfx or .pem/.crt) without the questions")
	cmd.Flags().StringVar(&impKey, "import-key", "", "encrypted private key (.pem/.key) for a .pem/.crt given with --import-cert")
	return cmd
}

// importFiles are the certificate files given by flags; empty means ask.
type importFiles struct{ cert, key string }

// offerImport asks whether to import a KSeF certificate for env/nip and, on yes, reads the
// file paths, takes the passphrase from the prompt only, imports the credential and stores
// the passphrase in the keyring. Anything but y/yes (or end of input) skips it.
func (s *session) offerImport(paths config.Paths, in *bufio.Reader, env config.Env, nip string, imp importFiles) error {
	out := s.deps.Stdout
	scripted := imp.cert != ""
	if !scripted {
		_, _ = fmt.Fprintf(out, "Import a KSeF certificate for %s/%s? [y/N] ", env, nip)
		line, ok := readLine(in)
		if a := strings.ToLower(line); !ok || (a != "y" && a != "yes") {
			_, _ = fmt.Fprintln(out)
			return nil
		}
	}
	envName := "FV_SECRET_KSEF_" + strings.ToUpper(string(env)) + "_" + nip
	envPass := ""
	if scripted && s.deps.Getenv != nil {
		envPass = s.deps.Getenv(envName)
	}
	var backend secrets.Backend = secrets.KeyringBackend{}
	if s.deps.Secrets != nil {
		backend = s.deps.Secrets
	}
	// The passphrase can only be kept in the OS keyring (fv never writes it to a file), so
	// find out now, before any file is read or copied, whether there is one.
	if _, err := backend.Get(fmt.Sprintf("fv/ksef/%s/%s", env, nip), "fv"); envPass == "" && errors.Is(err, secrets.ErrUnavailable) {
		return keyringUnavailable(env, nip, err)
	}
	ask := func(q string) (string, error) {
		if scripted {
			return "", errInputEnded
		}
		_, _ = fmt.Fprint(out, q)
		l, ok := readLine(in)
		if !ok {
			return "", errInputEnded
		}
		return l, nil
	}
	certPath := imp.cert
	var err error
	if !scripted {
		if certPath, err = ask("Certificate file (.p12/.pfx, or .pem/.crt): "); err != nil {
			return err
		}
	}
	var src credentials.ImportSource
	switch strings.ToLower(filepath.Ext(certPath)) {
	case ".p12", ".pfx":
		if src.P12, err = os.ReadFile(certPath); err != nil { // #nosec G304 -- the user names the certificate file to import
			return err
		}
	default:
		if src.CertPEM, err = os.ReadFile(certPath); err != nil { // #nosec G304 -- the user names the certificate file to import
			return err
		}
		src.CertPEM = trimBOM(src.CertPEM)
		keyPath := imp.key
		if !scripted {
			if keyPath, err = ask("Encrypted private key file (.pem/.key; empty if the certificate file holds it): "); err != nil && !errors.Is(err, errInputEnded) {
				return err
			}
		}
		if keyPath != "" {
			if src.KeyPEM, err = os.ReadFile(keyPath); err != nil { // #nosec G304 -- the user names the key file to import
				return err
			}
			src.KeyPEM = trimBOM(src.KeyPEM)
		}
	}
	pass := envPass
	if pass == "" {
		prompt := s.deps.prompter()
		if prompt == nil {
			return errors.New("no terminal to ask the key passphrase on; run fv init in a terminal")
		}
		if pass, err = prompt("Key passphrase: "); err != nil {
			return fmt.Errorf("read passphrase: %w", err)
		}
	}
	logging.Default.Register(pass)
	// Stage first: the certificate is checked (key, NIP) before anything of the old credential
	// is touched, and the old one stays aside until the passphrase is stored.
	staged, err := credentials.Stage(src, paths.DataDir, env, nip, pass)
	if err != nil {
		return err
	}
	info, err := staged.Info()
	if err == nil {
		_, err = info.ContextNIP(nip)
	}
	if err != nil {
		staged.Rollback()
		return err
	}
	if err := staged.Commit(); err != nil {
		staged.Rollback()
		return err
	}
	l, closeLog := s.logger(paths)
	defer closeLog()
	store := &secrets.Store{Backend: backend, Getenv: s.deps.Getenv, Log: l, Redact: logging.Default}
	// A passphrase that came from the environment variable stays there: nothing is written to
	// the keyring (a scripted run must not touch the user's real keyring entry).
	if envPass != "" {
		staged.Finish()
		l.Info("ksef certificate imported", "env", string(env))
		_, _ = fmt.Fprintf(out, "certificate imported for %s/%s (passphrase stays in %s)\n%s", env, nip, envName, info.Format(nip))
		return nil
	}
	if err := store.Set("ksef", env, nip, pass); err != nil {
		staged.Rollback() // the previous credential and its passphrase stay as they were
		if errors.Is(err, secrets.ErrUnavailable) {
			return keyringUnavailable(env, nip, err)
		}
		return err
	}
	staged.Finish()
	l.Info("ksef certificate imported", "env", string(env))
	_, _ = fmt.Fprintf(out, "certificate imported for %s/%s\n%s", env, nip, info.Format(nip))
	_, _ = fmt.Fprintln(out, "fv keeps its own encrypted copy; the original files can be deleted.")
	return nil
}

// trimBOM drops a UTF-8 byte order mark that Windows PowerShell 5 and Notepad put in front of
// text files, which would hide the PEM header.
func trimBOM(b []byte) []byte { return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) }

// keyringUnavailable explains what to do on a machine whose keyring service cannot be
// reached (typically headless Linux without D-Bus). Nothing has been imported.
func keyringUnavailable(env config.Env, nip string, cause error) error {
	name := "FV_SECRET_KSEF_" + strings.ToUpper(string(env)) + "_" + nip
	return fmt.Errorf("the OS keyring is not available on this machine (%v), so fv init cannot store the key passphrase, "+
		"and fv never writes it to a file; nothing was imported. fv can still use the certificate here: "+
		"import it with fv init on a machine that has a keyring, copy the credential folder for %s/%s from its data directory "+
		"to this machine (keep the files private), and set the environment variable %s to the passphrase "+
		"before running fv (see the setup guide, \"Where the passphrase lives\")", cause, env, nip, name)
}

// readLine returns the next line without its line ending. ok is false when the input
// ended with no data.
func readLine(r *bufio.Reader) (string, bool) {
	defer beginPrompt(nil)() // Ctrl-C while waiting for an answer aborts fv init at once
	line, err := r.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", false
	}
	return strings.TrimSpace(line), true
}
