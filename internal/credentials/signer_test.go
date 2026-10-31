package credentials_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/logging"
	"github.com/lwasak/fv-cli/internal/secrets"
)

type memBackend struct{ data map[string]string }

func (m *memBackend) Get(service, user string) (string, error) {
	v, ok := m.data[service]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}

func (m *memBackend) Set(service, user, value string) error {
	m.data[service] = value
	return nil
}

func store(data map[string]string, env map[string]string) (*secrets.Store, *logging.Redactor) {
	r := &logging.Redactor{}
	return &secrets.Store{
		Backend: &memBackend{data: data},
		Getenv:  func(k string) string { return env[k] },
		Redact:  r,
	}, r
}

// imported imports c (as PKCS#12 when p12, else PEM) for env test and returns the data dir
// and the loaded credential.
func imported(t *testing.T, c *credtest.Cert, p12 bool) (string, *credentials.Credential) {
	t.Helper()
	dir := t.TempDir()
	src := credentials.ImportSource{CertPEM: credtest.CertPEM(c), KeyPEM: credtest.EncryptedKeyPEM(t, c, pass)}
	if p12 {
		src = credentials.ImportSource{P12: credtest.P12(t, c, pass)}
	}
	if err := credentials.Import(src, dir, config.EnvTest, nip, pass); err != nil {
		t.Fatal(err)
	}
	cred, err := credentials.Load(dir, config.EnvTest, nip)
	if err != nil {
		t.Fatal(err)
	}
	return dir, cred
}

func digestOf(msg string) []byte {
	d := sha256.Sum256([]byte(msg))
	return d[:]
}

func TestSign_PassphraseFromKeyring(t *testing.T) {
	_, cred := imported(t, credtest.New(t, credtest.Spec{}), false)
	st, _ := store(map[string]string{"fv/ksef/test/" + nip: pass}, nil)
	cred.Source = credentials.PassphraseSource{Secrets: st}
	if _, err := cred.Sign(digestOf("x"), crypto.SHA256); err != nil {
		t.Fatal(err)
	}
}

func TestSign_PassphraseFromEnvVar(t *testing.T) {
	_, cred := imported(t, credtest.New(t, credtest.Spec{}), true)
	st, _ := store(map[string]string{}, map[string]string{"FV_SECRET_KSEF_TEST_" + nip: pass})
	cred.Source = credentials.PassphraseSource{Secrets: st}
	if _, err := cred.Sign(digestOf("x"), crypto.SHA256); err != nil {
		t.Fatal(err)
	}
}

func TestSign_PassphraseFromPrompt(t *testing.T) {
	_, cred := imported(t, credtest.New(t, credtest.Spec{}), false)
	st, r := store(map[string]string{}, nil)
	var asked string
	cred.Source = credentials.PassphraseSource{Secrets: st, Prompt: func(q string) (string, error) {
		asked = q
		return pass, nil
	}}
	if _, err := cred.Sign(digestOf("x"), crypto.SHA256); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "test/"+nip) {
		t.Fatalf("prompt = %q", asked)
	}
	if got := r.Redact(pass); got != logging.Placeholder {
		t.Fatalf("prompted passphrase not registered: %q", got)
	}
}

func TestSign_NoSourceErrorNamesKeyringAndVariable(t *testing.T) {
	_, cred := imported(t, credtest.New(t, credtest.Spec{}), false)
	st, _ := store(map[string]string{}, nil)
	cred.Source = credentials.PassphraseSource{Secrets: st}
	_, err := cred.Sign(digestOf("x"), crypto.SHA256)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"fv/ksef/test/" + nip, "FV_SECRET_KSEF_TEST_" + nip} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestSign_WrongPassphraseIsError(t *testing.T) {
	_, cred := imported(t, credtest.New(t, credtest.Spec{}), false)
	st, _ := store(map[string]string{"fv/ksef/test/" + nip: "nope"}, nil)
	cred.Source = credentials.PassphraseSource{Secrets: st}
	if _, err := cred.Sign(digestOf("x"), crypto.SHA256); err == nil {
		t.Fatal("expected error")
	}
}

func TestSign_KeyFileReadableByOthersRefused(t *testing.T) {
	for name, p12 := range map[string]bool{"p12": true, "pem": false} {
		dir, cred := imported(t, credtest.New(t, credtest.Spec{}), p12)
		st, _ := store(map[string]string{"fv/ksef/test/" + nip: pass}, nil)
		cred.Source = credentials.PassphraseSource{Secrets: st}
		file := "key.pem"
		if p12 {
			file = "credential.p12"
		}
		makeReadableByOthers(t, filepath.Join(credentials.Dir(dir, config.EnvTest, nip), file))
		_, err := cred.Sign(digestOf("x"), crypto.SHA256)
		if err == nil {
			t.Fatalf("%s: readable key file accepted", name)
		}
		if _, statErr := os.Stat(dir); statErr != nil {
			t.Fatal(statErr)
		}
	}
}

func TestSign_RSAAndECDSAVerify(t *testing.T) {
	d := digestOf("signed info")
	for _, p12 := range []bool{true, false} {
		rc := credtest.New(t, credtest.Spec{RSA: true})
		_, cred := imported(t, rc, p12)
		st, _ := store(map[string]string{"fv/ksef/test/" + nip: pass}, nil)
		cred.Source = credentials.PassphraseSource{Secrets: st}
		sig, err := cred.Sign(d, crypto.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		if err := rsa.VerifyPKCS1v15(rc.Cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, d, sig); err != nil {
			t.Fatalf("rsa verify (p12=%v): %v", p12, err)
		}

		ec := credtest.New(t, credtest.Spec{})
		_, cred = imported(t, ec, p12)
		cred.Source = credentials.PassphraseSource{Secrets: st}
		sig, err = cred.Sign(d, crypto.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		if len(sig) != 64 {
			t.Fatalf("ecdsa signature is %d bytes, want raw r||s of 64", len(sig))
		}
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(ec.Cert.PublicKey.(*ecdsa.PublicKey), d, r, s) {
			t.Fatalf("ecdsa verify failed (p12=%v)", p12)
		}
	}
}

func TestSign_PassphraseRegisteredWithRedactor(t *testing.T) {
	_, cred := imported(t, credtest.New(t, credtest.Spec{}), false)
	st, r := store(map[string]string{"fv/ksef/test/" + nip: pass}, nil)
	cred.Source = credentials.PassphraseSource{Secrets: st}
	if _, err := cred.Sign(digestOf("x"), crypto.SHA256); err != nil {
		t.Fatal(err)
	}
	if got := r.Redact("pw=" + pass); got != "pw="+logging.Placeholder {
		t.Fatalf("redact = %q", got)
	}
}
