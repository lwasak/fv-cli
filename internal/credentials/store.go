package credentials

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fsafe"
)

// ErrNoCredential means no certificate has been imported for the environment and NIP.
var ErrNoCredential = errors.New("no KSeF credential")

const (
	fileP12  = "credential.p12"
	fileKey  = "key.pem"
	fileCert = "cert.pem"
)

var nipRe = regexp.MustCompile(`^[0-9]{10}$`)

// ImportSource is the raw content of the files the user hands to fv init. Set P12, or
// CertPEM and KeyPEM (either may hold both the certificate and the key).
type ImportSource struct {
	P12     []byte
	CertPEM []byte
	KeyPEM  []byte
}

// Dir is where the credential for env and nip lives: <dataDir>/ksef/<env>/<nip>.
func Dir(dataDir string, env config.Env, nip string) string {
	return filepath.Join(dataDir, "ksef", string(env), nip)
}

func validate(env config.Env, nip string) error {
	if _, err := config.ParseEnv(string(env)); err != nil {
		return err
	}
	if !nipRe.MatchString(nip) {
		return errors.New("invalid NIP: must be 10 digits")
	}
	return nil
}

// Import checks the key (encrypted, opens with passphrase, matches the certificate) and then
// stores the still-encrypted key and the public certificate for env and nip, user-only.
// Nothing is written when any check fails, and a previous credential is replaced as a whole.
// Callers that must check more before the old credential goes (fv init checks the NIP and
// stores the passphrase) use Stage instead.
func Import(src ImportSource, dataDir string, env config.Env, nip, passphrase string) error {
	st, err := Stage(src, dataDir, env, nip, passphrase)
	if err != nil {
		return err
	}
	if err := st.Commit(); err != nil {
		st.Rollback()
		return err
	}
	st.Finish()
	return nil
}

// Staged is an import that passed the key checks and is written to a sibling folder, not yet
// swapped in. The previous credential is untouched until Commit and is kept (as ".old-*")
// until Finish, so Rollback can put it back when a later step fails.
type Staged struct {
	cert              *x509.Certificate
	dir, stage, old   string
	committed, hadOld bool
}

// Info inspects the staged certificate (identifier, type, validity).
func (s *Staged) Info() (Info, error) { return Inspect(s.cert) }

// Stage validates the import like Import and writes it to a staging folder next to the
// credential folder of env and nip. Call Commit to swap it in, then Finish; or Rollback.
func Stage(src ImportSource, dataDir string, env config.Env, nip, passphrase string) (*Staged, error) {
	files, cert, err := checkImport(src, env, nip, passphrase)
	if err != nil {
		return nil, err
	}
	dir := Dir(dataDir, env, nip)
	suffix, err := randomSuffix()
	if err != nil {
		return nil, err
	}
	st := &Staged{cert: cert, dir: dir, stage: dir + ".new-" + suffix, old: dir + ".old-" + suffix}
	if err := fsafe.MkdirAll(st.stage); err != nil {
		return nil, fmt.Errorf("create %s: %w", st.stage, err)
	}
	for name, data := range files {
		if err := fsafe.WriteFile(filepath.Join(st.stage, name), data); err != nil {
			_ = os.RemoveAll(st.stage)
			return nil, fmt.Errorf("store %s: %w", name, err)
		}
	}
	return st, nil
}

// checkImport runs every check of Import and returns the files to store.
func checkImport(src ImportSource, env config.Env, nip, passphrase string) (map[string][]byte, *x509.Certificate, error) {
	if err := validate(env, nip); err != nil {
		return nil, nil, err
	}
	if passphrase == "" {
		return nil, nil, errors.New("an empty passphrase is not accepted")
	}

	var cert *x509.Certificate
	files := map[string][]byte{}
	switch {
	case len(src.P12) > 0:
		if _, _, err := decryptP12(src.P12, ""); err == nil {
			return nil, nil, errors.New("the PKCS#12 file opens with an empty password: fv only accepts an encrypted key")
		}
		key, c, err := decryptP12(src.P12, passphrase)
		if err != nil {
			return nil, nil, err
		}
		if !sameKey(key, c) {
			return nil, nil, errors.New("the private key does not match the certificate")
		}
		cert = c
		files[fileP12] = src.P12
	case len(src.KeyPEM) > 0 || len(src.CertPEM) > 0:
		blk, err := classifyKeyBlock(append(append([]byte{}, src.KeyPEM...), src.CertPEM...))
		if err != nil {
			return nil, nil, err
		}
		c, err := firstCertificate(src.CertPEM, src.KeyPEM)
		if err != nil {
			return nil, nil, err
		}
		key, err := decryptPEMKey(blk, passphrase)
		if err != nil {
			return nil, nil, err
		}
		if !sameKey(key, c) {
			return nil, nil, errors.New("the private key does not match the certificate")
		}
		cert = c
		files[fileKey] = pem.EncodeToMemory(blk)
	default:
		return nil, nil, errors.New("no certificate or key to import")
	}
	files[fileCert] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})

	return files, cert, nil
}

// Commit swaps the staged folder in, keeping the previous one aside.
func (s *Staged) Commit() error {
	s.hadOld = true
	if err := fsafe.Rename(s.dir, s.old); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("replace %s: %w", s.dir, err)
		}
		s.hadOld = false
	}
	if err := fsafe.Rename(s.stage, s.dir); err != nil {
		if s.hadOld {
			if rerr := fsafe.Rename(s.old, s.dir); rerr != nil {
				return fmt.Errorf("replace %s: %w (the previous credential is still in %s)", s.dir, err, s.old)
			}
		}
		return fmt.Errorf("replace %s: %w", s.dir, err)
	}
	s.committed = true
	return nil
}

// Finish drops the previous credential after everything else succeeded.
func (s *Staged) Finish() {
	if s.hadOld {
		_ = os.RemoveAll(s.old)
	}
}

// Rollback undoes Stage or Commit: the previous credential (if any) is back in place and
// nothing of the new import is left.
func (s *Staged) Rollback() {
	_ = os.RemoveAll(s.stage)
	if !s.committed {
		return
	}
	_ = os.RemoveAll(s.dir)
	if s.hadOld {
		if err := fsafe.Rename(s.old, s.dir); err != nil {
			return // the previous credential stays in s.old
		}
	}
	s.committed = false
}

func randomSuffix() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("temp name: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Load reads the stored certificate for env and nip. It does not touch the key.
func Load(dataDir string, env config.Env, nip string) (*Credential, error) {
	if err := validate(env, nip); err != nil {
		return nil, err
	}
	dir := Dir(dataDir, env, nip)
	certPEM, err := os.ReadFile(filepath.Join(dir, fileCert)) // #nosec G304 -- path under the user-only credential dir built by Dir
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w for %s/%s: run fv init to import a certificate", ErrNoCredential, env, nip)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Join(dir, fileCert), err)
	}
	cert, err := firstCertificate(certPEM)
	if err != nil {
		return nil, fmt.Errorf("stored certificate for %s/%s: %w", env, nip, err)
	}

	keyFile := filepath.Join(dir, fileP12)
	isP12 := true
	if _, err := os.Stat(keyFile); err != nil {
		keyFile, isP12 = filepath.Join(dir, fileKey), false
		data, err := os.ReadFile(keyFile) // #nosec G304 -- path under the user-only credential dir built by Dir
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w for %s/%s: the key file is missing; run fv init to import it again", ErrNoCredential, env, nip)
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", keyFile, err)
		}
		if _, err := classifyKeyBlock(data); err != nil {
			return nil, fmt.Errorf("stored key %s: %w", keyFile, err)
		}
	}
	return &Credential{env: env, nip: nip, cert: cert, keyFile: keyFile, isP12: isP12}, nil
}
