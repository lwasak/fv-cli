//go:build kseflive

// Package live holds a manual check of fv against the real KSeF TEST or DEMO application.
// It is compiled only with the build tag kseflive, so go test ./... never runs it.
//
// What it does, in order:
//  1. points FV_CONFIG and FV_DATA_DIR into t.TempDir (your real fv config, archive and
//     keyring entries are not touched: the keyring is replaced by an empty in-memory one);
//  2. gives fv the key passphrase only through the variable FV_SECRET_KSEF_<ENV>_<NIP> of the
//     in-process environment (never an argument, never written to a file);
//  3. fv init --import-cert [--import-key]  (writes the config, imports the certificate);
//  4. fv config validate, fv ksef check;
//  5. fv issue 1 PLN net to the buyer, fv send --yes, fv status, fv list;
//  6. checks the invoice is accepted with a valid KSeF number and a stored UPO, then scans
//     every file under the temporary folders and everything fv printed for the passphrase.
//
// Environment variables:
//
//	FV_LIVE_ENV         demo or test (prod and anything else is refused)
//	FV_LIVE_CERT        certificate: .p12/.pfx, or .pem/.crt (then FV_LIVE_KEY)
//	FV_LIVE_KEY         encrypted private key (.pem/.key); optional for a .p12/.pfx
//	FV_LIVE_NIP         seller NIP; the certificate must carry it
//	FV_LIVE_BUYER_NIP   buyer NIP, default 1111111111 (fictional; name and address come
//	                    from the config, because MF asks not to send real company data)
//	FV_SECRET_KSEF_<ENV>_<NIP>  passphrase of the key (the only way to pass it)
//
// Run: go test -tags kseflive -run TestLive -v ./internal/live
package live

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/cli"
	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/secrets"
	"github.com/lwasak/fv-cli/internal/store"
)

// memBackend is an empty in-memory keyring, so the user's real keyring is never touched.
type memBackend struct {
	mu sync.Mutex
	m  map[string]string
}

func (b *memBackend) Get(service, user string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.m[service+"|"+user]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}

func (b *memBackend) Set(service, user, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string]string{}
	}
	b.m[service+"|"+user] = value
	return nil
}

var nipRe = regexp.MustCompile(`^\d{10}$`)

func TestLive(t *testing.T) {
	envName := strings.ToLower(strings.TrimSpace(os.Getenv("FV_LIVE_ENV")))
	certPath := os.Getenv("FV_LIVE_CERT")
	keyPath := os.Getenv("FV_LIVE_KEY")
	nip := strings.TrimSpace(os.Getenv("FV_LIVE_NIP"))
	buyerNip := strings.TrimSpace(os.Getenv("FV_LIVE_BUYER_NIP"))
	if buyerNip == "" {
		buyerNip = "1111111111"
	}

	if envName == "" || certPath == "" || nip == "" {
		t.Skip("live test not configured: set FV_LIVE_ENV (demo|test), FV_LIVE_CERT, FV_LIVE_NIP " +
			"(and FV_LIVE_KEY for a .pem/.crt) and the passphrase in FV_SECRET_KSEF_<ENV>_<NIP>")
	}
	switch envName {
	case "demo", "test":
	case "prod":
		t.Fatal("refusing FV_LIVE_ENV=prod: this test sends a real test invoice; use demo or test")
	default:
		t.Fatalf("FV_LIVE_ENV must be demo or test, not %q", envName)
	}
	if !nipRe.MatchString(nip) || !nipRe.MatchString(buyerNip) {
		t.Fatal("FV_LIVE_NIP and FV_LIVE_BUYER_NIP must be 10 digits")
	}
	secretVar := "FV_SECRET_KSEF_" + strings.ToUpper(envName) + "_" + nip
	pass := os.Getenv(secretVar)
	if pass == "" {
		t.Skipf("live test not configured: the passphrase must be in the environment variable %s", secretVar)
	}
	if st, err := os.Stat(certPath); err != nil || st.IsDir() {
		t.Fatalf("certificate not found: %s", certPath)
	}
	ext := strings.ToLower(filepath.Ext(certPath))
	if keyPath == "" && ext != ".p12" && ext != ".pfx" {
		t.Skip("FV_LIVE_KEY is required for a .pem/.crt certificate")
	}
	if keyPath != "" {
		if st, err := os.Stat(keyPath); err != nil || st.IsDir() {
			t.Fatalf("key not found: %s", keyPath)
		}
	}

	work := t.TempDir()
	cfgPath := filepath.Join(work, "config.toml")
	dataDir := filepath.Join(work, "data")
	home := t.TempDir()
	var outAll, errAll bytes.Buffer

	// Registered after the TempDir calls, so it runs before the folders are removed.
	t.Cleanup(func() {
		check := func(where string, data []byte) {
			if bytes.Contains(data, []byte(pass)) {
				t.Errorf("the passphrase leaked into %s", where)
			}
		}
		check("stdout", outAll.Bytes())
		check("stderr", errAll.Bytes())
		files := 0
		for _, root := range []string{work, home} {
			err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				b, rerr := os.ReadFile(p) // #nosec G304 G122 -- path comes from walking our own temp folder
				if rerr != nil {
					return rerr
				}
				files++
				check(p, b)
				return nil
			})
			if err != nil {
				t.Errorf("scanning %s: %v", root, err)
			}
		}
		t.Logf("secret scan: %d files, stdout and stderr checked", files)
	})

	backend := &memBackend{}
	env := map[string]string{
		"FV_CONFIG":   cfgPath,
		"FV_DATA_DIR": dataDir,
		secretVar:     pass,
	}
	// run drives the real CLI in-process: non-terminal stdin, in-memory keyring, real endpoints
	// (nil KSeFEndpoint and Endpoints), the passphrase only through Getenv.
	run := func(stdin string, args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		deps := cli.Deps{
			Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb,
			Getenv:  func(k string) string { return env[k] },
			GOOS:    runtime.GOOS,
			Home:    func() (string, error) { return home, nil },
			Secrets: backend,
		}
		code := cli.Run(deps, args)
		outAll.Write(out.Bytes())
		errAll.Write(errb.Bytes())
		t.Logf("> fv %s (exit %d)\n%s%s", strings.Join(args, " "), code, out.String(), errb.String())
		return code, out.String()
	}
	step := func(name, stdin string, args ...string) string {
		t.Helper()
		code, out := run(stdin, args...)
		if code != 0 {
			t.Fatalf("%s: exit %d", name, code)
		}
		return out
	}

	initArgs := []string{"--env", envName, "init", "--import-cert", certPath}
	if keyPath != "" {
		initArgs = append(initArgs, "--import-key", keyPath)
	}
	step("init + import certificate", nip+"\n"+envName+"\n", initArgs...)

	// UTF-8 without BOM: the extra config for the fictional buyer and invoice defaults.
	cur, err := os.ReadFile(cfgPath) // #nosec G304 -- path is inside t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	extra := "\n[defaults]\nvat_rate = \"23\"\nbank_account = \"PL61 1090 1014 0000 0712 1981 2874\"\n\n" +
		"[parties.livebuyer]\nnip = \"" + buyerNip + "\"\nname = \"fv live test buyer\"\n" +
		"address = \"ul. Próbna 2, 00-001 Warszawa\"\n"
	if err := os.WriteFile(cfgPath, append(cur, extra...), 0o600); err != nil {
		t.Fatal(err)
	}

	step("config validate", "", "config", "validate")
	step("ksef check", "", "--env", envName, "ksef", "check")
	issueOut := step("issue 1 PLN", "", "--env", envName, "issue", "--to", "livebuyer", "--net", "1", "--desc", "fv live check")
	m := regexp.MustCompile(`(?m)^Invoice:\s+(\S+)`).FindStringSubmatch(issueOut)
	if m == nil {
		t.Fatal("could not find the invoice number in the issue output")
	}
	number := m[1]
	step("send --yes", "", "--env", envName, "send", number, "--yes")
	step("status", "", "--env", envName, "status", number)
	step("list", "", "--env", envName, "list")

	meta, err := store.Open(dataDir, time.Now).Load(config.Env(envName), number)
	if err != nil {
		t.Fatalf("loading %s from the archive: %v", number, err)
	}
	if meta.State != store.StateAccepted {
		t.Fatalf("%s: state %s, want accepted", number, meta.State)
	}
	if meta.KSeF == nil || ksef.ValidateKSeFNumber(meta.KSeF.KSeFNumber) != nil {
		t.Fatalf("%s: missing or invalid KSeF number", number)
	}
	upos := 0
	_ = filepath.WalkDir(dataDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "upo.xml" {
			upos++
		}
		return nil
	})
	if upos == 0 {
		t.Fatal("upo.xml not found in the archive")
	}
	t.Logf("PASS: %s (%s, seller %s) accepted as KSeF number %s, UPO stored", number, envName, nip, meta.KSeF.KSeFNumber)
}
