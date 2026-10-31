package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
	"github.com/lwasak/fv-cli/internal/secrets"
)

const (
	ksefNIP       = "5260250274"
	ksefPass      = "correct-horse-battery-staple"
	ksefAuthTok   = "AUTHTOKEN-cli-secret-1111"
	ksefAccessTok = "ACCESSTOKEN-cli-secret-2222"
	ksefRefresh   = "REFRESHTOKEN-cli-secret-3333"
	testPESEL     = "44051401359"
)

type memBackend struct {
	mu sync.Mutex
	m  map[string]string
	// down makes every call fail like a keyring service that cannot be reached.
	down bool
	// setErr makes Set fail with this error (the keyring refuses the write).
	setErr error
}

func (b *memBackend) Get(service, user string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.down {
		return "", fmt.Errorf("%w: no D-Bus session", secrets.ErrUnavailable)
	}
	v, ok := b.m[service+"|"+user]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}

func (b *memBackend) Set(service, user, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.down {
		return fmt.Errorf("%w: no D-Bus session", secrets.ErrUnavailable)
	}
	if b.setErr != nil {
		return b.setErr
	}
	if b.m == nil {
		b.m = map[string]string{}
	}
	b.m[service+"|"+user] = value
	return nil
}

type ksefFx struct {
	t       *testing.T
	fake    *ksefake.Fake
	dataDir string
	cfgPath string
	backend *memBackend
	env     map[string]string
}

type pkJSON struct {
	Certificate string   `json:"certificate"`
	ValidFrom   string   `json:"validFrom"`
	ValidTo     string   `json:"validTo"`
	Usage       []string `json:"usage"`
}

// newKsefFx sets up a data dir, a config for env and the fake KSeF. When spec is non-nil a
// certificate for env/ksefNIP is imported and its passphrase stored in the fake keyring.
func newKsefFx(t *testing.T, env config.Env, spec *credtest.Spec) *ksefFx {
	t.Helper()
	fx := &ksefFx{t: t, fake: ksefake.New(t), dataDir: t.TempDir(), backend: &memBackend{}}
	fx.cfgPath = filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(fx.cfgPath, config.MinimalTOML(ksefNIP, env), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.env = map[string]string{"FV_DATA_DIR": fx.dataDir}

	now := time.Now()
	pk := credtest.New(t, credtest.Spec{NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour)})
	pkBody, _ := json.Marshal([]pkJSON{{
		Certificate: base64.StdEncoding.EncodeToString(pk.DER),
		ValidFrom:   now.Add(-time.Hour).Format(time.RFC3339),
		ValidTo:     now.Add(48 * time.Hour).Format(time.RFC3339),
		Usage:       []string{"KsefTokenEncryption", "SymmetricKeyEncryption"},
	}})
	fx.fake.Respond("GET", "/security/public-key-certificates", 200, string(pkBody))
	fx.fake.Respond("POST", "/auth/challenge", 200, `{"challenge":"20261030-CR-CLI","timestamp":"2026-10-30T12:00:00Z","timestampMs":1}`)
	fx.fake.Respond("POST", "/auth/xades-signature", 202, `{"referenceNumber":"REF-1","authenticationToken":{"token":"`+ksefAuthTok+`","validUntil":"2026-10-30T12:10:00Z"}}`)
	fx.fake.Respond("GET", "/auth/REF-1", 200, `{"status":{"code":200,"description":"ok"}}`)
	fx.fake.Respond("POST", "/auth/token/redeem", 200, `{"accessToken":{"token":"`+ksefAccessTok+`","validUntil":"2026-10-30T12:15:00Z"},"refreshToken":{"token":"`+ksefRefresh+`","validUntil":"2026-11-30T12:00:00Z"}}`)

	if spec != nil {
		fx.importCert(env, *spec)
	}
	return fx
}

func (fx *ksefFx) importCert(env config.Env, spec credtest.Spec) *credtest.Cert {
	fx.t.Helper()
	c := credtest.New(fx.t, spec)
	src := credentials.ImportSource{P12: credtest.P12(fx.t, c, ksefPass)}
	if err := credentials.Import(src, fx.dataDir, env, ksefNIP, ksefPass); err != nil {
		fx.t.Fatal(err)
	}
	if err := fx.backend.Set("fv/ksef/"+string(env)+"/"+ksefNIP, "fv", ksefPass); err != nil {
		fx.t.Fatal(err)
	}
	return c
}

func (fx *ksefFx) run(args ...string) (code int, stdout, stderr string) {
	fx.t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{
		Stdin:   strings.NewReader(""),
		Stdout:  &out,
		Stderr:  &errb,
		Getenv:  func(k string) string { return fx.env[k] },
		GOOS:    "linux",
		Home:    func() (string, error) { return fx.t.TempDir(), nil },
		Secrets: fx.backend,
		KSeFEndpoint: func(config.Env) (string, *http.Client) {
			return fx.fake.URL, fx.fake.Client()
		},
	}
	code = Run(deps, append([]string{"--config", fx.cfgPath}, args...))
	return code, out.String(), errb.String()
}

func (fx *ksefFx) log() string {
	b, _ := os.ReadFile(filepath.Join(fx.dataDir, "fv.log"))
	return string(b)
}

func nipSpec() *credtest.Spec {
	return &credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP}
}

func peselSpec() *credtest.Spec {
	return &credtest.Spec{CommonName: "Jan Kowalski (uwierzytelnienie)", SerialNumber: "PNOPL-" + testPESEL}
}

func TestKsefCheck_PrintsCertificateDetails(t *testing.T) {
	fx := newKsefFx(t, config.EnvTest, nipSpec())
	code, stdout, stderr := fx.run("ksef", "check")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"environment: test", "identifier: NIP", "nip: " + ksefNIP, "type: KSeF Authentication", "serial:", "validity:", "authenticated: OK"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	for _, tok := range []string{ksefAuthTok, ksefAccessTok, ksefRefresh, ksefPass} {
		if strings.Contains(stdout+stderr, tok) {
			t.Errorf("output leaks a secret")
		}
	}
}

func TestKsefCheck_PESELCertificateAcceptedShowsIdentifierType(t *testing.T) {
	fx := newKsefFx(t, config.EnvDemo, peselSpec())
	code, stdout, stderr := fx.run("ksef", "check")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"environment: demo", "identifier: PESEL", ksefNIP + " (context; none in certificate)", "authenticated: OK"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	// The context NIP sent to KSeF is seller.nip.
	var signed []byte
	for _, r := range fx.fake.Requests() {
		if r.Path == "/auth/xades-signature" {
			signed = r.Body
		}
	}
	if !strings.Contains(string(signed), "<Nip>"+ksefNIP+"</Nip>") {
		t.Errorf("signed request lacks the seller NIP: %s", signed)
	}
}

func TestKsefCheck_LogHasNoPESEL(t *testing.T) {
	fx := newKsefFx(t, config.EnvDemo, peselSpec())
	if code, _, stderr := fx.run("ksef", "check"); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if lg := fx.log(); lg == "" || strings.Contains(lg, testPESEL) {
		t.Errorf("log empty or holds the PESEL: %q", lg)
	}
}

func TestKsefCheck_ExpiresIn10DaysWarns(t *testing.T) {
	spec := nipSpec()
	spec.NotAfter = time.Now().Add(10*24*time.Hour + time.Hour)
	fx := newKsefFx(t, config.EnvTest, spec)
	code, _, stderr := fx.run("ksef", "check")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "expires in 10 days") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestKsefCheck_ExpiredRefusedNoRequestSent(t *testing.T) {
	spec := nipSpec()
	spec.NotBefore, spec.NotAfter = time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour)
	fx := newKsefFx(t, config.EnvTest, spec)
	code, _, stderr := fx.run("ksef", "check")
	if code != 3 || !strings.Contains(stderr, "expired") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	if n := len(fx.fake.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestKsefCheck_NotYetValidRefused(t *testing.T) {
	spec := nipSpec()
	spec.NotBefore, spec.NotAfter = time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour)
	fx := newKsefFx(t, config.EnvTest, spec)
	code, _, stderr := fx.run("ksef", "check")
	if code != 3 || !strings.Contains(stderr, "not valid before") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	if n := len(fx.fake.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestKsefCheck_NIPMismatchRefused(t *testing.T) {
	fx := newKsefFx(t, config.EnvTest, &credtest.Spec{OrganizationIdentifier: "VATPL-5833281095"})
	code, _, stderr := fx.run("ksef", "check")
	if code != 3 || !strings.Contains(stderr, "does not match seller.nip") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	if n := len(fx.fake.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestKsefCheck_OtherEnvironmentCredentialNotUsed(t *testing.T) {
	fx := newKsefFx(t, config.EnvTest, nipSpec())
	code, _, stderr := fx.run("--env", "demo", "ksef", "check")
	if code != 3 || !strings.Contains(stderr, "demo/"+ksefNIP) {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	if n := len(fx.fake.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestKsefCheck_ProdHasNoTokenLogin(t *testing.T) {
	fx := newKsefFx(t, config.EnvProd, nil)
	fx.env["FV_SECRET_TOKEN_PROD_"+ksefNIP] = "some-ksef-token"
	code, _, stderr := fx.run("ksef", "check")
	if code != 3 || !strings.Contains(stderr, "certificate") || !strings.Contains(stderr, "fv init") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	if n := len(fx.fake.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestKsefCheck_KSeFErrorPrintsCodeAndDescriptionExit3(t *testing.T) {
	fx := newKsefFx(t, config.EnvTest, nipSpec())
	fx.fake.Respond("GET", "/auth/REF-1", 200, `{"status":{"code":450,"description":"Uwierzytelnianie zakonczone niepowodzeniem"}}`)
	code, _, stderr := fx.run("ksef", "check")
	if code != 3 {
		t.Errorf("exit = %d, want 3", code)
	}
	if !strings.Contains(stderr, "fv: KSeF error 450: Uwierzytelnianie zakonczone niepowodzeniem") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestKsefCheck_LogHasNoSecrets(t *testing.T) {
	fx := newKsefFx(t, config.EnvTest, nipSpec())
	if code, _, stderr := fx.run("ksef", "check"); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	sawAuth := false
	for _, r := range fx.fake.Requests() {
		if r.Path == "/auth/token/redeem" && strings.Contains(r.Header.Get("Authorization"), ksefAuthTok) {
			sawAuth = true
		}
	}
	if !sawAuth {
		t.Fatal("the fake never received the Authorization header")
	}
	lg := fx.log()
	if lg == "" {
		t.Fatal("log is empty")
	}
	for _, bad := range []string{"Authorization", "Bearer", ksefAuthTok, ksefAccessTok, ksefRefresh, ksefPass,
		"PRIVATE KEY", "<AuthTokenRequest", "ds:Signature", "20261030-CR-CLI"} {
		if strings.Contains(lg, bad) {
			t.Errorf("fv.log contains %q", bad)
		}
	}
}

func TestKsefCheck_PassphraseFlagRejected(t *testing.T) {
	fx := newKsefFx(t, config.EnvTest, nipSpec())
	code, _, stderr := fx.run("ksef", "check", "--passphrase", "hunter2-secret")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if strings.Contains(stderr, "hunter2-secret") {
		t.Errorf("stderr echoes the value: %q", stderr)
	}
	if n := len(fx.fake.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestConfig_PassphraseKeyRejected(t *testing.T) {
	p := writeConfig(t, "[ksef]\nenvironment = \"test\"\npassphrase = \"x\"\n[seller]\nnip = \""+ksefNIP+"\"\n")
	code, _, stderr := runCLI(t, nil, "--config", p, "config", "validate")
	if code != 1 || !strings.Contains(stderr, "passphrase") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestKsefCheck_NoInsecureFlagAnywhere(t *testing.T) {
	root := NewRootCmd(DefaultDeps())
	walk(root, func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			n := strings.ToLower(f.Name + " " + f.Usage)
			if strings.Contains(n, "insecure") || strings.Contains(n, "skip-verify") || strings.Contains(n, "passphrase") {
				t.Errorf("%s: flag --%s", c.CommandPath(), f.Name)
			}
		})
	})
	ref := strings.ToLower(config.FormatReference())
	if strings.Contains(ref, "insecure") || strings.Contains(ref, "skip-verify") || strings.Contains(ref, "passphrase") {
		t.Error("config reference mentions an insecure or passphrase key")
	}
}
