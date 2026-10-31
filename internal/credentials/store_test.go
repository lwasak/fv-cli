package credentials_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/fsafe"
)

const (
	nip  = "5833281095"
	pass = "correct horse battery"
)

func walk(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestImport_P12_StoresEncryptedCopyUserOnly(t *testing.T) {
	dir := t.TempDir()
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip, RSA: true})
	p12 := credtest.P12(t, c, pass)
	if err := credentials.Import(credentials.ImportSource{P12: p12}, dir, config.EnvTest, nip, pass); err != nil {
		t.Fatal(err)
	}
	stored := filepath.Join(credentials.Dir(dir, config.EnvTest, nip), "credential.p12")
	got, err := os.ReadFile(stored)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(p12) {
		t.Fatal("stored file is not the original encrypted bytes")
	}
	if err := fsafe.CheckPrivate(stored); err != nil {
		t.Fatalf("stored file not private: %v", err)
	}
	cred, err := credentials.Load(dir, config.EnvTest, nip)
	if err != nil {
		t.Fatal(err)
	}
	if !cred.Certificate().Equal(c.Cert) {
		t.Fatal("loaded certificate differs")
	}
}

func TestImport_PEMKeyAndCert(t *testing.T) {
	dir := t.TempDir()
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip})
	src := credentials.ImportSource{CertPEM: credtest.CertPEM(c), KeyPEM: credtest.EncryptedKeyPEM(t, c, pass)}
	if err := credentials.Import(src, dir, config.EnvTest, nip, pass); err != nil {
		t.Fatal(err)
	}
	d := credentials.Dir(dir, config.EnvTest, nip)
	for _, f := range []string{"key.pem", "cert.pem"} {
		if err := fsafe.CheckPrivate(filepath.Join(d, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	key, _ := os.ReadFile(filepath.Join(d, "key.pem"))
	if !strings.Contains(string(key), "ENCRYPTED PRIVATE KEY") {
		t.Fatal("stored key is not encrypted")
	}
}

func TestImport_EncryptedPKCS8ECP256WithSeparateCert(t *testing.T) {
	dir := t.TempDir()
	// The shape of the DEMO credential: PESEL-only, EC P-256, .crt + encrypted PKCS#8 .key.
	c := credtest.New(t, credtest.Spec{SerialNumber: "PNOPL-44051401359"})
	src := credentials.ImportSource{CertPEM: credtest.CertPEM(c), KeyPEM: credtest.EncryptedKeyPEM(t, c, pass)}
	if err := credentials.Import(src, dir, config.EnvDemo, nip, pass); err != nil {
		t.Fatal(err)
	}
	cred, err := credentials.Load(dir, config.EnvDemo, nip)
	if err != nil {
		t.Fatal(err)
	}
	info, err := cred.Info()
	if err != nil || info.IdentifierType != credentials.IdentifierPESEL {
		t.Fatalf("info = %+v, %v", info, err)
	}
}

func TestImport_UnencryptedPEMRefused(t *testing.T) {
	dir := t.TempDir()
	c := credtest.New(t, credtest.Spec{})
	src := credentials.ImportSource{CertPEM: credtest.CertPEM(c), KeyPEM: credtest.PlainKeyPEM(t, c)}
	err := credentials.Import(src, dir, config.EnvTest, nip, pass)
	if err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Fatalf("err = %v", err)
	}
	if got := walk(t, dir); len(got) != 0 {
		t.Fatalf("files written: %v", got)
	}
}

func TestImport_P12EmptyPasswordRefused(t *testing.T) {
	dir := t.TempDir()
	c := credtest.New(t, credtest.Spec{})
	err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, c, "")}, dir, config.EnvTest, nip, pass)
	if err == nil || !strings.Contains(err.Error(), "empty password") {
		t.Fatalf("err = %v", err)
	}
	if got := walk(t, dir); len(got) != 0 {
		t.Fatalf("files written: %v", got)
	}
}

func TestImport_LegacyProcTypePEMRefused(t *testing.T) {
	dir := t.TempDir()
	c := credtest.New(t, credtest.Spec{})
	src := credentials.ImportSource{CertPEM: credtest.CertPEM(c), KeyPEM: credtest.LegacyEncryptedKeyPEM(t, c, pass)}
	err := credentials.Import(src, dir, config.EnvTest, nip, pass)
	if err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("err = %v", err)
	}
	if got := walk(t, dir); len(got) != 0 {
		t.Fatalf("files written: %v", got)
	}
}

func TestImport_WrongPassphraseWritesNothing(t *testing.T) {
	c := credtest.New(t, credtest.Spec{})
	for name, src := range map[string]credentials.ImportSource{
		"p12": {P12: credtest.P12(t, c, pass)},
		"pem": {CertPEM: credtest.CertPEM(c), KeyPEM: credtest.EncryptedKeyPEM(t, c, pass)},
	} {
		dir := t.TempDir()
		err := credentials.Import(src, dir, config.EnvTest, nip, "wrong")
		if err == nil {
			t.Fatalf("%s: wrong passphrase accepted", name)
		}
		if strings.Contains(err.Error(), "wrong") && strings.Contains(err.Error(), pass) {
			t.Fatalf("%s: error leaks passphrase: %v", name, err)
		}
		if got := walk(t, dir); len(got) != 0 {
			t.Fatalf("%s: files written: %v", name, got)
		}
	}
}

func TestImport_KeyDoesNotMatchCertRefused(t *testing.T) {
	dir := t.TempDir()
	a := credtest.New(t, credtest.Spec{})
	b := credtest.New(t, credtest.Spec{})
	src := credentials.ImportSource{CertPEM: credtest.CertPEM(a), KeyPEM: credtest.EncryptedKeyPEM(t, b, pass)}
	err := credentials.Import(src, dir, config.EnvTest, nip, pass)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v", err)
	}
	if got := walk(t, dir); len(got) != 0 {
		t.Fatalf("files written: %v", got)
	}
}

func TestLoad_MissingCredentialNamesEnvNIPAndInit(t *testing.T) {
	_, err := credentials.Load(t.TempDir(), config.EnvProd, nip)
	if !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"prod/" + nip, "fv init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestLoad_EnvironmentsAreSeparate(t *testing.T) {
	dir := t.TempDir()
	c := credtest.New(t, credtest.Spec{})
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, c, pass)}, dir, config.EnvTest, nip, pass); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Load(dir, config.EnvDemo, nip); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("demo err = %v", err)
	}
	if _, err := credentials.Load(dir, config.EnvProd, nip); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("prod err = %v", err)
	}
}

func TestLoad_UnencryptedStoredKeyRefused(t *testing.T) {
	dir := t.TempDir()
	c := credtest.New(t, credtest.Spec{})
	d := credentials.Dir(dir, config.EnvTest, nip)
	if err := fsafe.MkdirAll(d); err != nil {
		t.Fatal(err)
	}
	_ = fsafe.WriteFile(filepath.Join(d, "key.pem"), credtest.PlainKeyPEM(t, c))
	_ = fsafe.WriteFile(filepath.Join(d, "cert.pem"), credtest.CertPEM(c))
	if _, err := credentials.Load(dir, config.EnvTest, nip); err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Fatalf("err = %v", err)
	}
}

// Renewing with a different file format must not leave the old credential.p12 behind: Load
// prefers it over key.pem and would sign with the expired certificate's key.
func TestImport_ReplacesPreviousFilesWhenFormatChanges(t *testing.T) {
	dir := t.TempDir()
	oldC := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip})
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, oldC, pass)}, dir, config.EnvTest, nip, pass); err != nil {
		t.Fatal(err)
	}
	newC := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip})
	src := credentials.ImportSource{CertPEM: credtest.CertPEM(newC), KeyPEM: credtest.EncryptedKeyPEM(t, newC, pass)}
	if err := credentials.Import(src, dir, config.EnvTest, nip, pass); err != nil {
		t.Fatal(err)
	}
	d := credentials.Dir(dir, config.EnvTest, nip)
	if _, err := os.Stat(filepath.Join(d, "credential.p12")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale credential.p12 survived: %v", err)
	}
	cred, err := credentials.Load(dir, config.EnvTest, nip)
	if err != nil {
		t.Fatal(err)
	}
	if !cred.Certificate().Equal(newC.Cert) {
		t.Error("Load did not return the newly imported certificate")
	}
	// No staging or backup folders are left next to it.
	entries, _ := os.ReadDir(filepath.Dir(d))
	if len(entries) != 1 {
		t.Errorf("leftovers in %s: %v", filepath.Dir(d), entries)
	}
}

// A failed re-import (wrong passphrase) leaves the previous credential intact.
func TestImport_FailedReimportKeepsPreviousCredential(t *testing.T) {
	dir := t.TempDir()
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip})
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, c, pass)}, dir, config.EnvTest, nip, pass); err != nil {
		t.Fatal(err)
	}
	other := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip})
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, other, "different")}, dir, config.EnvTest, nip, pass); err == nil {
		t.Fatal("expected an error")
	}
	cred, err := credentials.Load(dir, config.EnvTest, nip)
	if err != nil || !cred.Certificate().Equal(c.Cert) {
		t.Fatalf("previous credential damaged: %v", err)
	}
}

func TestStage_RollbackRestoresPreviousAfterCommit(t *testing.T) {
	dir := t.TempDir()
	oldC := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip})
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, oldC, pass)}, dir, config.EnvTest, nip, pass); err != nil {
		t.Fatal(err)
	}
	newC := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip})
	st, err := credentials.Stage(credentials.ImportSource{P12: credtest.P12(t, newC, pass)}, dir, config.EnvTest, nip, pass)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := credentials.Load(dir, config.EnvTest, nip); !c.Certificate().Equal(oldC.Cert) {
		t.Fatal("staging must not touch the old credential")
	}
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}
	if c, _ := credentials.Load(dir, config.EnvTest, nip); !c.Certificate().Equal(newC.Cert) {
		t.Fatal("commit did not swap")
	}
	st.Rollback()
	if c, err := credentials.Load(dir, config.EnvTest, nip); err != nil || !c.Certificate().Equal(oldC.Cert) {
		t.Fatalf("rollback did not restore: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(credentials.Dir(dir, config.EnvTest, nip))); len(entries) != 1 {
		t.Errorf("leftovers: %v", entries)
	}
}

func TestStage_RollbackOfFirstImportRemovesFolder(t *testing.T) {
	dir := t.TempDir()
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + nip})
	st, err := credentials.Stage(credentials.ImportSource{P12: credtest.P12(t, c, pass)}, dir, config.EnvTest, nip, pass)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}
	st.Rollback()
	if _, err := credentials.Load(dir, config.EnvTest, nip); !errors.Is(err, credentials.ErrNoCredential) {
		t.Errorf("err = %v", err)
	}
}
