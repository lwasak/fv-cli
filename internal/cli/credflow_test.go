package cli

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
)

// initFx runs fv init / config validate with a fake keyring, a scripted passphrase prompt and
// a data dir under the test's control.
type initFx struct {
	t       *testing.T
	dataDir string
	cfgPath string
	backend *memBackend
	files   string
	asked   []string
}

func newInitFx(t *testing.T) *initFx {
	return &initFx{t: t, dataDir: t.TempDir(), cfgPath: filepath.Join(t.TempDir(), "config.toml"), backend: &memBackend{}, files: t.TempDir()}
}

func (fx *initFx) write(name string, data []byte) string {
	fx.t.Helper()
	p := filepath.Join(fx.files, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		fx.t.Fatal(err)
	}
	return p
}

func (fx *initFx) run(stdin string, args ...string) (int, string, string) {
	fx.t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb,
		Getenv:  func(k string) string { return map[string]string{"FV_DATA_DIR": fx.dataDir}[k] },
		GOOS:    "linux",
		Home:    func() (string, error) { return fx.t.TempDir(), nil },
		Secrets: fx.backend,
		Prompt:  func(q string) (string, error) { fx.asked = append(fx.asked, q); return ksefPass, nil },
		KSeFEndpoint: func(config.Env) (string, *http.Client) {
			fx.t.Fatal("init must not call KSeF")
			return "", nil
		},
	}
	if len(args) == 0 {
		args = []string{"init"} // no args means: answer the fv init questions from stdin
	}
	code := Run(deps, append([]string{"--config", fx.cfgPath}, args...))
	return code, out.String(), errb.String()
}

func (fx *initFx) storedFile(env config.Env, name string) string {
	return filepath.Join(credentials.Dir(fx.dataDir, env, ksefNIP), name)
}

func TestInit_ImportsP12AndStoresPassphrase(t *testing.T) {
	fx := newInitFx(t)
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	p := fx.write("c.p12", credtest.P12(t, c, ksefPass))
	code, stdout, stderr := fx.run(ksefNIP + "\ntest\n\ny\n" + p + "\n")
	if code != 0 {
		t.Fatalf("exit %d stderr %q stdout %q", code, stderr, stdout)
	}
	if v, err := fx.backend.Get("fv/ksef/test/"+ksefNIP, "fv"); err != nil || v != ksefPass {
		t.Errorf("keyring = %q, %v", v, err)
	}
	if _, err := os.Stat(fx.storedFile(config.EnvTest, "credential.p12")); err != nil {
		t.Error(err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(fx.storedFile(config.EnvTest, "credential.p12"))
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("mode %v", st.Mode())
		}
	}
	if !strings.Contains(stdout, "identifier: NIP") || !strings.Contains(stdout, "can be deleted") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestInit_ImportsPEMKeyAndCert(t *testing.T) {
	fx := newInitFx(t)
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	cp := fx.write("c.crt", credtest.CertPEM(c))
	kp := fx.write("c.key", credtest.EncryptedKeyPEM(t, c, ksefPass))
	code, stdout, stderr := fx.run(ksefNIP + "\ntest\n\nyes\n" + cp + "\n" + kp + "\n")
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if _, err := os.Stat(fx.storedFile(config.EnvTest, "key.pem")); err != nil {
		t.Error(err)
	}
	if !strings.Contains(stdout, "identifier: NIP") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestInit_ImportsPESELOnlyEncryptedPKCS8OnDemo(t *testing.T) {
	fx := newInitFx(t)
	c := credtest.New(t, *peselSpec())
	cp := fx.write("cli-tool.crt", credtest.CertPEM(c))
	kp := fx.write("cli-tool.key", credtest.EncryptedKeyPEM(t, c, ksefPass))
	code, stdout, stderr := fx.run(ksefNIP + "\ndemo\n\ny\n" + cp + "\n" + kp + "\n")
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if v, err := fx.backend.Get("fv/ksef/demo/"+ksefNIP, "fv"); err != nil || v != ksefPass {
		t.Errorf("keyring = %q, %v", v, err)
	}
	if !strings.Contains(stdout, "identifier: PESEL") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestInit_ImportDeclinedOrEOFSkips(t *testing.T) {
	for _, tail := range []string{"", "n\n", "\n"} {
		fx := newInitFx(t)
		code, _, stderr := fx.run(ksefNIP + "\ntest\n\n" + tail)
		if code != 0 {
			t.Fatalf("tail %q: exit %d stderr %q", tail, code, stderr)
		}
		if _, err := os.Stat(filepath.Join(fx.dataDir, "ksef")); err == nil {
			t.Errorf("tail %q: credentials directory created", tail)
		}
		if len(fx.asked) != 0 {
			t.Errorf("tail %q: prompted", tail)
		}
	}
}

func TestInit_ImportUnencryptedKeyRefusedWritesNothing(t *testing.T) {
	fx := newInitFx(t)
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	cp := fx.write("c.crt", credtest.CertPEM(c))
	kp := fx.write("c.key", credtest.PlainKeyPEM(t, c))
	code, _, stderr := fx.run(ksefNIP + "\ntest\n\ny\n" + cp + "\n" + kp + "\n")
	if code == 0 || stderr == "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(fx.dataDir, "ksef")); err == nil {
		t.Error("credentials directory created")
	}
	if len(fx.backend.m) != 0 {
		t.Errorf("keyring written: %v", fx.backend.m)
	}
}

func TestInit_ImportWithExistingConfigUsesItsNIPAndEnv(t *testing.T) {
	fx := newInitFx(t)
	if err := os.WriteFile(fx.cfgPath, config.MinimalTOML(ksefNIP, config.EnvDemo), 0o600); err != nil {
		t.Fatal(err)
	}
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	p := fx.write("c.p12", credtest.P12(t, c, ksefPass))
	// "n" keeps the existing config, then the import question follows.
	code, stdout, stderr := fx.run("n\ny\n" + p + "\n")
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "demo/"+ksefNIP) {
		t.Errorf("stdout = %q", stdout)
	}
	if _, err := os.Stat(fx.storedFile(config.EnvDemo, "credential.p12")); err != nil {
		t.Error(err)
	}
}

func TestInit_ImportNIPMismatchRefused(t *testing.T) {
	fx := newInitFx(t)
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-5260250275"})
	p := fx.write("c.p12", credtest.P12(t, c, ksefPass))
	code, _, stderr := fx.run(ksefNIP + "\ntest\n\ny\n" + p + "\n")
	if code == 0 || !strings.Contains(stderr, "5260250275") {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if _, err := os.Stat(credentials.Dir(fx.dataDir, config.EnvTest, ksefNIP)); err == nil {
		t.Error("credentials directory created")
	}
	if len(fx.backend.m) != 0 {
		t.Errorf("keyring written")
	}
}

func TestInit_PassphraseNeverEchoed(t *testing.T) {
	fx := newInitFx(t)
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	p := fx.write("c.p12", credtest.P12(t, c, ksefPass))
	_, stdout, stderr := fx.run(ksefNIP + "\ntest\n\ny\n" + p + "\n")
	if strings.Contains(stdout+stderr, ksefPass) {
		t.Error("passphrase echoed")
	}
	if len(fx.asked) != 1 {
		t.Errorf("asked = %v", fx.asked)
	}
}

// --- fv config validate ---

func (fx *initFx) validate() (int, string, string) {
	fx.t.Helper()
	return fx.run("", "config", "validate")
}

func (fx *initFx) setup(env config.Env, spec credtest.Spec) {
	fx.t.Helper()
	body := strings.Replace(validConfigBody, `environment = "demo"`, `environment = "`+string(env)+`"`, 1)
	if err := os.WriteFile(fx.cfgPath, []byte(body), 0o600); err != nil {
		fx.t.Fatal(err)
	}
	c := credtest.New(fx.t, spec)
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(fx.t, c, ksefPass)}, fx.dataDir, env, ksefNIP, ksefPass); err != nil {
		fx.t.Fatal(err)
	}
}

func okSpec() credtest.Spec {
	now := time.Now()
	return credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour)}
}

func TestConfigValidate_CredentialOK(t *testing.T) {
	fx := newInitFx(t)
	fx.setup(config.EnvDemo, okSpec())
	code, stdout, stderr := fx.validate()
	if code != 0 || stderr != "" || !strings.Contains(stdout, "config OK") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestConfigValidate_NoCredentialWarnsExit0(t *testing.T) {
	fx := newInitFx(t)
	if err := os.WriteFile(fx.cfgPath, []byte(validConfigBody), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := fx.validate()
	if code != 0 || !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "fv init") {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
}

func TestConfigValidate_ExpiredCredentialExit1(t *testing.T) {
	fx := newInitFx(t)
	now := time.Now()
	fx.setup(config.EnvDemo, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP, NotBefore: now.Add(-48 * time.Hour), NotAfter: now.Add(-time.Hour)})
	code, _, stderr := fx.validate()
	if code != 1 || !strings.Contains(stderr, "expired") {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
}

func TestConfigValidate_KeyReadableByOthersExit1(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode test; the DACL case is covered in fsafe")
	}
	fx := newInitFx(t)
	fx.setup(config.EnvDemo, okSpec())
	if err := os.Chmod(fx.storedFile(config.EnvDemo, "credential.p12"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := fx.validate()
	if code != 1 || stderr == "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
}

func TestConfigValidate_CredentialNIPMismatchExit1(t *testing.T) {
	fx := newInitFx(t)
	spec := okSpec()
	spec.OrganizationIdentifier = "VATPL-5260250275"
	fx.setup(config.EnvDemo, spec)
	code, _, stderr := fx.validate()
	if code != 1 || !strings.Contains(stderr, "5260250275") {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
}

func TestConfigValidate_PESELCredentialOK(t *testing.T) {
	fx := newInitFx(t)
	spec := *peselSpec()
	now := time.Now()
	spec.NotBefore, spec.NotAfter = now.Add(-time.Hour), now.Add(365*24*time.Hour)
	fx.setup(config.EnvDemo, spec)
	code, stdout, stderr := fx.validate()
	if code != 0 || stderr != "" || !strings.Contains(stdout, "config OK") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// Headless machine without a keyring service: a clear message that names the FV_SECRET_*
// variable, nothing imported, and the passphrase is not written anywhere.
func TestInit_KeyringUnavailableFailsEarlyWithClearMessage(t *testing.T) {
	fx := newInitFx(t)
	fx.backend.down = true
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	p := fx.write("c.p12", credtest.P12(t, c, ksefPass))
	code, _, stderr := fx.run(ksefNIP + "\ntest\n\ny\n" + p + "\n")
	if code == 0 {
		t.Fatal("expected a failure")
	}
	for _, want := range []string{"keyring is not available", "never writes it to a file", "FV_SECRET_KSEF_TEST_" + ksefNIP, "nothing was imported"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, ksefPass) {
		t.Error("passphrase in stderr")
	}
	if len(fx.asked) != 0 {
		t.Errorf("asked for the passphrase although it cannot be kept: %q", fx.asked)
	}
	if _, err := os.Stat(credentials.Dir(fx.dataDir, config.EnvTest, ksefNIP)); err == nil {
		t.Error("credentials directory created")
	}
}

// R-B1: a refused renewal must leave the old credential, and its passphrase, untouched.
func renewalFx(t *testing.T) (*initFx, *credtest.Cert) {
	t.Helper()
	fx := newInitFx(t)
	if err := os.WriteFile(fx.cfgPath, config.MinimalTOML(ksefNIP, config.EnvDemo), 0o600); err != nil {
		t.Fatal(err)
	}
	old := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, old, "old-pass")}, fx.dataDir, config.EnvDemo, ksefNIP, "old-pass"); err != nil {
		t.Fatal(err)
	}
	if err := fx.backend.Set("fv/ksef/demo/"+ksefNIP, "fv", "old-pass"); err != nil {
		t.Fatal(err)
	}
	return fx, old
}

func assertOldCredential(t *testing.T, fx *initFx, old *credtest.Cert) {
	t.Helper()
	cred, err := credentials.Load(fx.dataDir, config.EnvDemo, ksefNIP)
	if err != nil {
		t.Fatalf("old credential gone: %v", err)
	}
	if !cred.Certificate().Equal(old.Cert) {
		t.Fatal("old credential was replaced")
	}
	if v, _ := fx.backend.Get("fv/ksef/demo/"+ksefNIP, "fv"); v != "old-pass" {
		t.Errorf("keyring passphrase changed to %q", v)
	}
	entries, _ := os.ReadDir(filepath.Dir(credentials.Dir(fx.dataDir, config.EnvDemo, ksefNIP)))
	if len(entries) != 1 {
		t.Errorf("staging or backup folders left: %v", entries)
	}
}

func TestInit_WrongNIPRenewalKeepsOldCredential(t *testing.T) {
	fx, old := renewalFx(t)
	wrong := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-5260250275"})
	p := fx.write("w.p12", credtest.P12(t, wrong, ksefPass))
	code, _, stderr := fx.run("n\ny\n" + p + "\n")
	if code == 0 || !strings.Contains(stderr, "5260250275") {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	assertOldCredential(t, fx, old)
}

func TestInit_KeyringSetFailureOnRenewalKeepsOldCredential(t *testing.T) {
	fx, old := renewalFx(t)
	fx.backend.setErr = errors.New("keychain access denied")
	renewed := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	p := fx.write("n.p12", credtest.P12(t, renewed, ksefPass))
	code, _, stderr := fx.run("n\ny\n" + p + "\n")
	if code == 0 || !strings.Contains(stderr, "keychain access denied") {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	assertOldCredential(t, fx, old)
}

func TestInit_RenewalSucceedsAndDropsOldCredential(t *testing.T) {
	fx, _ := renewalFx(t)
	renewed := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	p := fx.write("n.p12", credtest.P12(t, renewed, ksefPass))
	if code, _, stderr := fx.run("n\ny\n" + p + "\n"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	cred, err := credentials.Load(fx.dataDir, config.EnvDemo, ksefNIP)
	if err != nil || !cred.Certificate().Equal(renewed.Cert) {
		t.Fatalf("new credential not in place: %v", err)
	}
	if v, _ := fx.backend.Get("fv/ksef/demo/"+ksefNIP, "fv"); v != ksefPass {
		t.Errorf("passphrase = %q", v)
	}
	entries, _ := os.ReadDir(filepath.Dir(credentials.Dir(fx.dataDir, config.EnvDemo, ksefNIP)))
	if len(entries) != 1 {
		t.Errorf("leftovers: %v", entries)
	}
}

// fv init --import-cert/--import-key: no questions about the import, passphrase from the
// FV_SECRET_KSEF_<ENV>_<NIP> variable, keyring untouched.
func TestInit_ScriptedImportUsesEnvPassphraseAndLeavesKeyringAlone(t *testing.T) {
	fx := newInitFx(t)
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP})
	// A PEM saved by Windows PowerShell 5 starts with a UTF-8 byte order mark.
	cp := fx.write("c.crt", append([]byte("\xef\xbb\xbf"), credtest.CertPEM(c)...))
	kp := fx.write("c.key", append([]byte("\xef\xbb\xbf"), credtest.EncryptedKeyPEM(t, c, ksefPass)...))
	var out, errb bytes.Buffer
	deps := Deps{
		Stdin: strings.NewReader(ksefNIP + "\ndemo\n\n"), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string {
			return map[string]string{"FV_DATA_DIR": fx.dataDir, "FV_SECRET_KSEF_DEMO_" + ksefNIP: ksefPass}[k]
		},
		GOOS:    "linux",
		Home:    func() (string, error) { return t.TempDir(), nil },
		Secrets: fx.backend,
		Prompt:  func(string) (string, error) { t.Fatal("passphrase prompted"); return "", nil },
	}
	code := Run(deps, []string{"--config", fx.cfgPath, "init", "--import-cert", cp, "--import-key", kp})
	if code != 0 {
		t.Fatalf("exit %d stderr %q stdout %q", code, errb.String(), out.String())
	}
	if _, err := os.Stat(fx.storedFile(config.EnvDemo, "key.pem")); err != nil {
		t.Error(err)
	}
	if v, err := fx.backend.Get("fv/ksef/demo/"+ksefNIP, "fv"); err == nil && v != "" {
		t.Errorf("keyring was written: %q", v)
	}
	if strings.Contains(out.String()+errb.String(), ksefPass) {
		t.Error("passphrase printed")
	}
}

func TestInit_ImportKeyWithoutCertRefused(t *testing.T) {
	fx := newInitFx(t)
	code, _, stderr := fx.run("", "init", "--import-key", "k.pem")
	if code != 1 || !strings.Contains(stderr, "--import-cert") {
		t.Errorf("exit %d stderr %q", code, stderr)
	}
}
