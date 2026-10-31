package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
	"github.com/lwasak/fv-cli/internal/logging"
	"github.com/lwasak/fv-cli/internal/store"
)

// TestE2E_IssueSendStatusNoSecretLeak runs issue, send and status against the fake KSeF and
// scans every file fv wrote (data dir and config dir) and everything it printed for the
// passphrase, the auth, access and refresh tokens and the AES session key and IV.
func TestE2E_IssueSendStatusNoSecretLeak(t *testing.T) {
	fx := newSendFx(t, nil)
	secrets := append([]string{ksefPass, ksefAuthTok, ksefAccessTok, ksefRefresh}, sessionSecrets(3)...)
	// The redactor starts empty (other tests may have registered values): only what fv
	// registers itself while it runs counts, so a missing Register call in fv fails this test.
	logging.Default.Reset()
	t.Cleanup(logging.Default.Reset)
	// Raw key bytes of the first session, for the binary scan.
	rawKey := make([]byte, 32)
	for i := range rawKey {
		rawKey[i] = streamByte(i)
	}

	fx.noConfirm = true
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "1234,56", "--send", "--yes"); code != 0 {
		t.Fatalf("issue --send: exit %d: %s", code, errs)
	}
	fx.noConfirm = false
	// A second invoice goes through issue, send and status separately, with a lost answer.
	fx.on.Script(ksefake.DropAfterReceive())
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "99"); code != 0 {
		t.Fatalf("issue: exit %d: %s", code, errs)
	}
	fx.answers = []string{"y"}
	if code, _, errs := fx.run("send", sendNum2); code != 5 {
		t.Fatalf("send: exit %d: %s", code, errs)
	}
	for _, args := range [][]string{{"status", sendNum2}, {"status", sendNum}, {"show", sendNum}, {"list"}, {"ksef", "check"}} {
		if code, _, errs := fx.run(args...); code != 0 {
			t.Fatalf("fv %v: exit %d: %s", args, code, errs)
		}
	}
	for _, n := range []string{sendNum, sendNum2} {
		if m := fx.meta(config.EnvTest, n); m.State != store.StateAccepted {
			t.Fatalf("%s: state %s", n, m.State)
		}
	}

	// The scan only means something when the fake really saw the secrets.
	sawBearer := false
	for _, r := range fx.fake.Requests() {
		if strings.Contains(r.Header.Get("Authorization"), ksefAccessTok) {
			sawBearer = true
		}
	}
	if !sawBearer || len(fx.on.Invoices()) != 2 {
		t.Fatalf("the fake did not see the secrets (bearer=%v, invoices=%d)", sawBearer, len(fx.on.Invoices()))
	}

	// fv registered the secrets itself: the redactor knows every one it used (the first two
	// sessions of the stream were opened by issue --send and send).
	used := append([]string{ksefPass, ksefAuthTok, ksefAccessTok, ksefRefresh}, sessionSecrets(2)...)
	for _, sec := range used {
		if got := logging.Default.Redact(sec); got != logging.Placeholder {
			t.Errorf("fv did not register a secret with the redactor (%d chars, starts %q)", len(sec), sec[:min(4, len(sec))])
		}
	}

	check := func(where string, data []byte) {
		for _, sec := range secrets {
			if strings.Contains(string(data), sec) {
				t.Errorf("%s contains a secret (%d chars, starts %q)", where, len(sec), sec[:min(4, len(sec))])
			}
		}
		if strings.Contains(string(data), string(rawKey)) {
			t.Errorf("%s contains the raw session key", where)
		}
	}
	check("stdout", fx.outAll.Bytes())
	check("stderr", fx.errAll.Bytes())
	files := 0
	for _, root := range []string{fx.dataDir, filepath.Dir(fx.cfgPath)} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			files++
			check(p, b)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files < 8 {
		t.Errorf("only %d files scanned", files)
	}
	if lg := fx.logText(); !strings.Contains(lg, "ksef send") {
		t.Errorf("fv.log lacks the send record:\n%s", lg)
	}
}
