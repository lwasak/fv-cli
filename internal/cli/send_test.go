package cli

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/exitcode"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
	"github.com/lwasak/fv-cli/internal/store"
)

const (
	sendNum     = "FV/2026/10/001"
	sendNum2    = "FV/2026/10/002"
	origKSeFNum = "5260250274-20261001-AAAAAA-BBBBBB-1C"
)

// seqRand is a deterministic randomness source: byte p of the stream is byte(p*7+3), so a
// test knows every session key (48 bytes per session: 32 key, 16 IV).
type seqRand struct {
	mu  sync.Mutex
	pos int
}

func streamByte(p int) byte { return byte(p*7 + 3) }

func (r *seqRand) Read(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range b {
		b[i] = streamByte(r.pos)
		r.pos++
	}
	return len(b), nil
}

// sessionSecrets returns every textual form of the key and IV of the first n sessions.
func sessionSecrets(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		for _, part := range [][2]int{{i * 48, 32}, {i*48 + 32, 16}} {
			raw := make([]byte, part[1])
			for j := range raw {
				raw[j] = streamByte(part[0] + j)
			}
			h := hex.EncodeToString(raw)
			out = append(out, base64.StdEncoding.EncodeToString(raw), h, strings.ToUpper(h))
		}
	}
	return out
}

type sendFx struct {
	*partyFx
	fake    *ksefake.Fake
	on      *ksefake.Online
	q       *ksefake.Query
	backend *memBackend
	rnd     *seqRand

	// answers are the replies the confirmation prompt gives, in order; prompts records the questions.
	answers []string
	prompts []string
	// noConfirm leaves Deps.Confirm nil (no terminal).
	noConfirm bool
	sleeps    []time.Duration
	// stdout and stderr of every run, for the secret scan.
	outAll, errAll bytes.Buffer
}

// newSendFx starts the party fakes, a fake KSeF with the online routes and imports a test
// credential. pre runs before the online routes are registered.
func newSendFx(t *testing.T, pre func(*ksefake.Fake)) *sendFx {
	t.Helper()
	fx := &sendFx{partyFx: newPartyFx(t, issueConfig), fake: ksefake.New(t), backend: &memBackend{}, rnd: &seqRand{}}
	if pre != nil {
		pre(fx.fake)
	}
	sym := credtest.New(t, credtest.Spec{RSA: true})
	fx.on = fx.fake.EnableOnline(ksefake.OnlineOptions{
		Key: sym.Key.(*rsa.PrivateKey), CertDER: sym.DER, Bearer: ksefAccessTok, NIP: ksefNIP,
	})
	fx.q = fx.fake.EnableQuery(ksefake.QueryOptions{NIP: ksefNIP, Bearer: ksefAccessTok, Online: fx.on})
	fx.fake.Respond("POST", "/auth/challenge", 200, `{"challenge":"20261030-CR-SEND","timestamp":"2026-10-30T12:00:00Z","timestampMs":1}`)
	fx.fake.Respond("POST", "/auth/xades-signature", 202, `{"referenceNumber":"REF-1","authenticationToken":{"token":"`+ksefAuthTok+`","validUntil":"2027-10-30T12:10:00Z"}}`)
	fx.fake.Respond("GET", "/auth/REF-1", 200, `{"status":{"code":200,"description":"ok"}}`)
	fx.fake.Respond("POST", "/auth/token/redeem", 200, `{"accessToken":{"token":"`+ksefAccessTok+`","validUntil":"2027-10-30T12:15:00Z"},"refreshToken":{"token":"`+ksefRefresh+`","validUntil":"2027-11-30T12:00:00Z"}}`)
	fx.importCred(config.EnvTest)
	t.Cleanup(func() {
		if v := append(fx.on.Violations(), fx.q.Violations()...); len(v) > 0 {
			t.Errorf("fv made protocol errors against KSeF: %v", v)
		}
	})
	return fx
}

func (fx *sendFx) importCred(env config.Env) {
	fx.t.Helper()
	now := time.Now()
	c := credtest.New(fx.t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP, NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(30 * 24 * time.Hour)})
	src := credentials.ImportSource{P12: credtest.P12(fx.t, c, ksefPass)}
	if err := credentials.Import(src, fx.dataDir, env, ksefNIP, ksefPass); err != nil {
		fx.t.Fatal(err)
	}
	if err := fx.backend.Set("fv/ksef/"+string(env)+"/"+ksefNIP, "fv", ksefPass); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *sendFx) run(args ...string) (code int, stdout, stderr string) {
	fx.t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string {
			if k == "FV_DATA_DIR" {
				return fx.dataDir
			}
			return ""
		},
		GOOS: "linux", Home: func() (string, error) { return fx.t.TempDir(), nil },
		Now:     func() time.Time { return fx.now },
		Secrets: fx.backend,
		Rand:    fx.rnd,
		Endpoints: func(string) (string, *http.Client) {
			return fx.srv.URL, fx.srv.Client()
		},
		KSeFEndpoint: func(config.Env) (string, *http.Client) { return fx.fake.URL, fx.fake.Client() },
		Sleep: func(_ context.Context, d time.Duration) error {
			fx.sleeps = append(fx.sleeps, d)
			return nil
		},
	}
	if !fx.noConfirm {
		deps.Confirm = func(prompt string) (string, error) {
			fx.prompts = append(fx.prompts, prompt)
			if len(fx.answers) == 0 {
				return "", errors.New("unexpected prompt: " + prompt)
			}
			a := fx.answers[0]
			fx.answers = fx.answers[1:]
			return a, nil
		}
	}
	code = execute(deps, NewRootCmd(deps), append([]string{"--config", fx.cfgPath}, args...))
	fx.outAll.WriteString(out.String())
	fx.errAll.WriteString(errb.String())
	return code, out.String(), errb.String()
}

func (fx *sendFx) issue(extra ...string) string {
	fx.t.Helper()
	code, out, errs := fx.run(append([]string{"issue", "--to", "acme", "--net", "100"}, extra...)...)
	if code != 0 {
		fx.t.Fatalf("issue: exit %d\n%s\n%s", code, out, errs)
	}
	return sendNum
}

func (fx *sendFx) meta(env config.Env, number string) *store.Meta {
	fx.t.Helper()
	m, err := fx.store().Load(env, number)
	if err != nil {
		fx.t.Fatal(err)
	}
	return m
}

func (fx *sendFx) store() *store.Store {
	return store.Open(fx.dataDir, func() time.Time { return fx.now })
}

// postInvoiceCount counts POSTs of invoices to any online session.
func (fx *sendFx) postInvoiceCount() int {
	n := 0
	for _, r := range fx.fake.Requests() {
		if r.Method == "POST" && strings.HasPrefix(r.Path, "/sessions/online/") && strings.HasSuffix(r.Path, "/invoices") {
			n++
		}
	}
	return n
}

func (fx *sendFx) stateOf(number string) store.State { return fx.meta(config.EnvTest, number).State }

func TestNoSend_ZeroKSeFRequests(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue()
	for _, args := range [][]string{
		{"show", sendNum}, {"show", sendNum, "--xml"}, {"list", "--offline"}, {"list", "--offline", "--env", "test"},
		{"status", sendNum}, {"discard", sendNum},
	} {
		if code, _, errs := fx.run(args...); code != 0 {
			t.Fatalf("fv %v: exit %d: %s", args, code, errs)
		}
	}
	if got := fx.fake.Requests(); len(got) != 0 {
		t.Errorf("KSeF received %d requests without a send: %v", len(got), got[0])
	}
}

func TestSend_AcceptedStoresNumberAndUPO(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue()
	fx.answers = []string{"y"}
	code, out, errs := fx.run("send", sendNum)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	m := fx.meta(config.EnvTest, sendNum)
	if m.State != store.StateAccepted || m.KSeF == nil || ksef.ValidateKSeFNumber(m.KSeF.KSeFNumber) != nil ||
		m.KSeF.AcquisitionDate == "" || m.KSeF.SessionRef == "" || m.KSeF.InvoiceRef == "" || m.KSeF.SentAt == nil || m.UPOSHA256 == "" {
		t.Fatalf("meta = %+v ksef = %+v", m, m.KSeF)
	}
	for _, want := range []string{"ENVIRONMENT: TEST", sendNum, "Sent " + sendNum + " to KSeF TEST", "KSeF number: " + m.KSeF.KSeFNumber, "UPO:"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if len(fx.prompts) != 1 || !strings.Contains(fx.prompts[0], "TEST") || !strings.Contains(fx.prompts[0], "[y/N]") {
		t.Errorf("prompts = %q", fx.prompts)
	}
	// What the fake decrypted is the stored XML, and the UPO is stored.
	inv := fx.on.Invoices()
	if len(inv) != 1 || !bytes.Equal(inv[0].XML, mustXML(t, fx, config.EnvTest, sendNum)) {
		t.Fatalf("fake invoices = %d", len(inv))
	}
	if !fx.on.Closed(m.KSeF.SessionRef) {
		t.Error("the session was not closed")
	}
	if upo := fx.on.UPOFor(m.KSeF.InvoiceRef); len(upo) == 0 {
		t.Error("the UPO was never served")
	}
	// A second send is refused, whatever the state.
	fx.answers = []string{"y"}
	code, _, errs = fx.run("send", sendNum)
	if code != 1 || !strings.Contains(errs, "already in KSeF as "+m.KSeF.KSeFNumber) {
		t.Errorf("resend: exit %d, stderr %q", code, errs)
	}
	if fx.postInvoiceCount() != 1 {
		t.Errorf("POSTs = %d", fx.postInvoiceCount())
	}
	// status of an accepted invoice with a UPO prints the number and asks nothing.
	before := len(fx.fake.Requests())
	code, out, _ = fx.run("status", sendNum)
	if code != 0 || !strings.Contains(out, m.KSeF.KSeFNumber) || len(fx.fake.Requests()) != before {
		t.Errorf("status: exit %d, out %q, new requests %d", code, out, len(fx.fake.Requests())-before)
	}
}

func mustXML(t *testing.T, fx *sendFx, env config.Env, number string) []byte {
	t.Helper()
	b, err := fx.store().XML(env, number)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSend_YesSkipsQuestionOnTest(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue()
	fx.noConfirm = true
	if code, _, errs := fx.run("send", sendNum, "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if fx.stateOf(sendNum) != store.StateAccepted {
		t.Errorf("state = %s", fx.stateOf(sendNum))
	}
}

func TestSend_AnsweringNoAbortsExit10(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue()
	fx.answers = []string{"n"}
	code, _, errs := fx.run("send", sendNum)
	if code != 10 || !strings.Contains(errs, "aborted, nothing was sent") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if fx.stateOf(sendNum) != store.StateDraft || len(fx.fake.Requests()) != 0 {
		t.Errorf("state %s, requests %d", fx.stateOf(sendNum), len(fx.fake.Requests()))
	}
	// No terminal and no --yes: nothing to ask, so abort.
	fx.noConfirm = true
	if code, _, errs := fx.run("send", sendNum); code != 10 || !strings.Contains(errs, "--yes") {
		t.Errorf("no terminal: exit %d, stderr %q", code, errs)
	}
	if len(fx.fake.Requests()) != 0 {
		t.Error("KSeF was contacted")
	}
}

func TestSend_IssueSendFlow(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.noConfirm = true
	code, out, errs := fx.run("issue", "--to", "acme", "--net", "100", "--send", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(out, "ENVIRONMENT: TEST") || !strings.Contains(out, "Sent "+sendNum+" to KSeF TEST") {
		t.Errorf("stdout:\n%s", out)
	}
	if fx.stateOf(sendNum) != store.StateAccepted {
		t.Errorf("state = %s", fx.stateOf(sendNum))
	}
}

func TestSend_StatePersistedBeforePost(t *testing.T) {
	var mu sync.Mutex
	var seen *store.Meta
	var fx *sendFx
	fx = newSendFx(t, func(f *ksefake.Fake) {
		f.HandlePattern("POST", "/sessions/online/*/invoices", func(ksefake.Request) ksefake.Response {
			m, err := fx.store().Load(config.EnvTest, sendNum)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				seen = m
			}
			return ksefake.Response{Drop: true}
		})
	})
	fx.issue()
	fx.answers = []string{"y"}
	if code, _, _ := fx.run("send", sendNum); code != 5 {
		t.Fatalf("exit %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen == nil || seen.State != store.StateSending || seen.KSeF == nil || seen.KSeF.SessionRef == "" ||
		seen.KSeF.SentAt == nil || seen.KSeF.SessionValidUntil == "" || seen.XMLSHA256 == "" {
		t.Errorf("meta at the time of the POST: %+v / %+v", seen, seen.KSeF)
	}
}

func TestSend_CrashAfterPostLeavesUnknown(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.DropAfterReceive())
	fx.issue()
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 5 || !strings.Contains(errs, "fv status "+sendNum) {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	m := fx.meta(config.EnvTest, sendNum)
	if m.State != store.StateUnknown || m.KSeF == nil || m.KSeF.SessionRef == "" || m.KSeF.InvoiceRef != "" || m.KSeF.KSeFNumber != "" {
		t.Errorf("meta = %+v / %+v", m, m.KSeF)
	}
	if len(fx.on.Invoices()) != 1 {
		t.Error("the fake should have stored the invoice")
	}
	if !fx.on.Closed(m.KSeF.SessionRef) {
		t.Error("the session should be closed after the outcome was persisted")
	}
}

func TestSend_RefusesUnknownUntilStatus(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.DropAfterReceive())
	fx.issue()
	fx.answers = []string{"y"}
	fx.run("send", sendNum)
	before := len(fx.fake.Requests())
	fx.answers = []string{"y", "y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 5 || !strings.Contains(errs, "fv status "+sendNum) {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if len(fx.fake.Requests()) != before || len(fx.prompts) != 1 {
		t.Errorf("the refusal must come before the prompt and any request: %d new requests, prompts %q",
			len(fx.fake.Requests())-before, fx.prompts)
	}
}

func TestSend_PersistedSendingCountsAsUnknown(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue()
	if _, err := fx.store().Transition(config.EnvTest, sendNum, store.StateSending, func(m *store.Meta) error {
		m.KSeF = &store.KSeFRef{SessionRef: "20261031-SO-0000000000-0000000000-00"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	code, _, errs := fx.run("send", sendNum)
	if code != 5 || !strings.Contains(errs, "fv status "+sendNum) || len(fx.fake.Requests()) != 0 {
		t.Errorf("exit %d, stderr %q, requests %d", code, errs, len(fx.fake.Requests()))
	}
}

func TestStatus_LostResponseFindsByHash(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.DropAfterReceive())
	fx.issue()
	fx.answers = []string{"y"}
	if code, _, _ := fx.run("send", sendNum); code != 5 {
		t.Fatalf("send exit %d", code)
	}
	code, out, errs := fx.run("status", sendNum)
	if code != 0 {
		t.Fatalf("status exit %d\n%s\n%s", code, out, errs)
	}
	m := fx.meta(config.EnvTest, sendNum)
	if m.State != store.StateAccepted || m.KSeF.InvoiceRef == "" || m.UPOSHA256 == "" ||
		!strings.Contains(out, "KSeF number: "+m.KSeF.KSeFNumber) {
		t.Errorf("meta = %+v / %+v\n%s", m, m.KSeF, out)
	}
	if fx.postInvoiceCount() != 1 {
		t.Errorf("POSTs = %d, status must never post", fx.postInvoiceCount())
	}
	// The history shows the whole path.
	var path []string
	for _, h := range m.History {
		path = append(path, string(h.To))
	}
	if got := strings.Join(path, ","); got != "reserved,draft,sending,unknown,accepted" {
		t.Errorf("history = %s", got)
	}
}

// pollTimeoutPending is more pending answers than the 120 s poll budget can ask for.
const pollTimeoutPending = 40

func TestSend_PollTimeoutUnknown(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.Accept().AfterPolls(1000))
	fx.issue()
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 5 || !strings.Contains(errs, "fv status "+sendNum) {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	var total time.Duration
	for _, d := range fx.sleeps {
		total += d
	}
	if total < 120*time.Second || total > 130*time.Second {
		t.Errorf("slept %v in %d waits, want about the 120 s budget", total, len(fx.sleeps))
	}
	if len(fx.sleeps) < 4 || fx.sleeps[0] != time.Second || fx.sleeps[1] != 2*time.Second || fx.sleeps[2] != 4*time.Second {
		t.Errorf("backoff = %v", fx.sleeps)
	}
	m := fx.meta(config.EnvTest, sendNum)
	if m.State != store.StateUnknown || m.KSeF.InvoiceRef == "" || m.KSeF.KSeFNumber != "" {
		t.Errorf("meta = %+v / %+v", m, m.KSeF)
	}
	// While KSeF is still processing, status says so and exits 5.
	code, out, _ := fx.run("status", sendNum)
	if code != 5 || !strings.Contains(out, "still processing") {
		t.Errorf("status: exit %d, out %q", code, out)
	}
}

func TestStatus_ResolvesUnknownToAccepted(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.Accept().AfterPolls(pollTimeoutPending))
	fx.issue()
	fx.answers = []string{"y"}
	if code, _, errs := fx.run("send", sendNum); code != 5 {
		t.Fatalf("send exit %d: %s", code, errs)
	}
	var code int
	var out, errs string
	// KSeF finishes after the poll budget; status asks until it has the verdict.
	for i := 0; i < 20 && fx.stateOf(sendNum) != store.StateAccepted; i++ {
		code, out, errs = fx.run("status", sendNum)
	}
	m := fx.meta(config.EnvTest, sendNum)
	if code != 0 || m.State != store.StateAccepted || m.KSeF.KSeFNumber == "" || m.UPOSHA256 == "" {
		t.Fatalf("exit %d, meta %+v / %+v\n%s\n%s", code, m, m.KSeF, out, errs)
	}
	if fx.postInvoiceCount() != 1 {
		t.Errorf("POSTs = %d", fx.postInvoiceCount())
	}
}

func TestStatus_AcceptedWithoutUPOFetchesIt(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.SetUPO(ksefake.Response{Status: 503})
	fx.issue()
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 0 || !strings.Contains(errs, "UPO could not be fetched") {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	m := fx.meta(config.EnvTest, sendNum)
	if m.State != store.StateAccepted || m.KSeF.KSeFNumber == "" || m.UPOSHA256 != "" {
		t.Fatalf("meta = %+v", m)
	}
	fx.on.SetUPO(ksefake.Response{Status: 200, Body: ksefake.UPOXML(m.KSeF.KSeFNumber), ContentType: "application/xml"})
	code, out, errs := fx.run("status", sendNum)
	if code != 0 || !strings.Contains(out, "UPO:") {
		t.Fatalf("status: exit %d\n%s\n%s", code, out, errs)
	}
	if fx.meta(config.EnvTest, sendNum).UPOSHA256 == "" {
		t.Error("the UPO hash was not stored")
	}
}

// N1: a session answer that lacks status and invoiceCount proves nothing; the invoice must
// stay unknown instead of going back to draft.
func TestStatus_MissingSessionFieldsStayUnknown(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.DropBeforeReceive())
	fx.issue()
	fx.answers = []string{"y"}
	if code, _, _ := fx.run("send", sendNum); code != 5 {
		t.Fatalf("send exit %d", code)
	}
	// The invoice list and the session status both answer with an empty object.
	fx.fake.Inject("GET", "/sessions/", 2, ksefake.Response{Status: 200, Body: `{}`})
	code, out, errs := fx.run("status", sendNum)
	if code != 5 || strings.Contains(out, "never received") {
		t.Fatalf("status: exit %d\n%s\n%s", code, out, errs)
	}
	if m := fx.meta(config.EnvTest, sendNum); m.State != store.StateUnknown || m.KSeF == nil {
		t.Errorf("meta = %+v / %+v", m, m.KSeF)
	}
}

func TestStatus_NotReceivedBackToDraft(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.DropBeforeReceive())
	fx.issue()
	fx.answers = []string{"y"}
	if code, _, _ := fx.run("send", sendNum); code != 5 {
		t.Fatalf("send exit %d", code)
	}
	if fx.stateOf(sendNum) != store.StateUnknown {
		t.Fatalf("state = %s", fx.stateOf(sendNum))
	}
	code, out, errs := fx.run("status", sendNum)
	if code != 0 || !strings.Contains(out, "never received") {
		t.Fatalf("status: exit %d\n%s\n%s", code, out, errs)
	}
	m := fx.meta(config.EnvTest, sendNum)
	if m.State != store.StateDraft || m.KSeF != nil {
		t.Errorf("meta = %+v / %+v", m, m.KSeF)
	}
	// It can be sent again, and this time it goes through.
	fx.answers = []string{"y"}
	if code, _, errs := fx.run("send", sendNum); code != 0 {
		t.Fatalf("resend exit %d: %s", code, errs)
	}
	if fx.stateOf(sendNum) != store.StateAccepted {
		t.Errorf("state = %s", fx.stateOf(sendNum))
	}
}

func TestStatus_DraftIsNotSent(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue()
	code, out, _ := fx.run("status", sendNum)
	if code != 0 || !strings.Contains(out, "not sent") {
		t.Errorf("exit %d, out %q", code, out)
	}
}

func TestSend_Rejected450Exit4PrintsDetails(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.Reject(450, "Błąd weryfikacji semantyki dokumentu faktury", "P_1 jest z przyszłości", "druga uwaga"))
	fx.issue()
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 4 {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	for _, want := range []string{"KSeF error 450: Błąd weryfikacji semantyki dokumentu faktury", "P_1 jest z przyszłości", "druga uwaga"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q: %q", want, errs)
		}
	}
	m := fx.meta(config.EnvTest, sendNum)
	if m.State != store.StateRejected || m.KSeF.StatusCode != 450 || len(m.KSeF.Details) != 2 || m.KSeF.KSeFNumber != "" {
		t.Errorf("meta = %+v / %+v", m, m.KSeF)
	}
	if !fx.on.Closed(m.KSeF.SessionRef) {
		t.Error("the session was not closed after the rejection")
	}
	// status repeats the verdict from the archive without asking KSeF; send refuses.
	before := len(fx.fake.Requests())
	code, _, errs = fx.run("status", sendNum)
	if code != 4 || !strings.Contains(errs, "450") || len(fx.fake.Requests()) != before {
		t.Errorf("status: exit %d, stderr %q", code, errs)
	}
	if code, _, errs := fx.run("send", sendNum); code != 1 || !strings.Contains(errs, "rejected") {
		t.Errorf("resend: exit %d, stderr %q", code, errs)
	}
	// The rejection body reached the log (redacted), and the verdict is in fv.log.
	if lg := fx.logText(); !strings.Contains(lg, "ksef invoice status") {
		t.Errorf("log lacks the rejection record:\n%s", lg)
	}
}

func (fx *sendFx) logText() string {
	b, _ := readFile(fx.dataDir + "/fv.log")
	return string(b)
}

func TestSend_Duplicate440ShowsOriginal(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.Duplicate(origKSeFNum))
	fx.issue()
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 4 || !strings.Contains(errs, "440") || !strings.Contains(errs, origKSeFNum) {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if fx.stateOf(sendNum) != store.StateRejected {
		t.Errorf("state = %s", fx.stateOf(sendNum))
	}
}

// A 440 means KSeF holds an invoice with this number: it is marked, shown, and discard must
// never release the number (the next invoice would be refused as a duplicate again).
func TestSend_DuplicateNumberIsNeverReleased(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.Duplicate(origKSeFNum))
	fx.issue()
	fx.answers = []string{"y"}
	if code, _, errs := fx.run("send", sendNum); code != 4 {
		t.Fatalf("send: exit %d: %s", code, errs)
	}
	if k := fx.meta(config.EnvTest, sendNum).KSeF; !k.Duplicate || k.OriginalKSeFNumber != origKSeFNum {
		t.Fatalf("marker not stored: %+v", k)
	}
	if code, out, _ := fx.run("show", sendNum); code != 0 || !strings.Contains(out, origKSeFNum) {
		t.Errorf("show: exit %d, out lacks the original number:\n%s", code, out)
	}
	if code, out, errs := fx.run("status", sendNum); code != 4 || !strings.Contains(out, origKSeFNum) {
		t.Errorf("status: exit %d, out %q, %s", code, out, errs)
	}
	code, out, errs := fx.run("discard", sendNum)
	if code != 0 || !strings.Contains(out, "never released") || !strings.Contains(out, origKSeFNum) {
		t.Fatalf("discard: exit %d\n%s\n%s", code, out, errs)
	}
	if st := fx.stateOf(sendNum); st != store.StateVoid {
		t.Errorf("state = %s, want void-draft (the folder must stay)", st)
	}
	// The next invoice gets a new number.
	code, out, errs = fx.run("issue", "--to", "acme", "--net", "100")
	if code != 0 || !strings.Contains(out, sendNum2) {
		t.Errorf("next issue: exit %d\n%s\n%s", code, out, errs)
	}
}

// R2: the answer to an earlier send was lost, the invoice was wrongly put back to draft and
// sent again. KSeF answers 440 and names the earlier session: that is this very invoice, so
// it is recorded as accepted with its UPO, not rejected.
func TestSend_DuplicateOfOwnEarlierSendIsAccepted(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.DropAfterReceive())
	fx.issue()
	fx.answers = []string{"y"}
	if code, _, errs := fx.run("send", sendNum); code != 5 {
		t.Fatalf("first send: exit %d: %s", code, errs)
	}
	first := fx.meta(config.EnvTest, sendNum).KSeF.SessionRef
	// Simulate the wrong unknown -> draft (for example a lost meta.json write).
	if _, err := fx.store().Transition(config.EnvTest, sendNum, store.StateDraft, func(m *store.Meta) error { m.KSeF = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	fx.answers = []string{"y"}
	code, out, errs := fx.run("send", sendNum)
	if code != 0 || !strings.Contains(out, "KSeF number:") {
		t.Fatalf("second send: exit %d\n%s\n%s", code, out, errs)
	}
	m := fx.meta(config.EnvTest, sendNum)
	if m.State != store.StateAccepted || m.KSeF.KSeFNumber == "" || m.KSeF.SessionRef != first || m.UPOSHA256 == "" {
		t.Errorf("meta = %+v / %+v", m, m.KSeF)
	}
	if len(m.SentSessions) != 2 || m.SentSessions[0] != first {
		t.Errorf("sent sessions = %v", m.SentSessions)
	}
	// A later status shows it as accepted and does not need the network.
	if code, out, _ := fx.run("status", sendNum); code != 0 || !strings.Contains(out, m.KSeF.KSeFNumber) {
		t.Errorf("status: exit %d\n%s", code, out)
	}
}

func TestSend_PostRefusedByKSeFIsRejected(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.on.Script(ksefake.PostError(400))
	fx.issue()
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 4 || !strings.Contains(errs, "KSeF error") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if fx.stateOf(sendNum) != store.StateRejected {
		t.Errorf("state = %s", fx.stateOf(sendNum))
	}
}

// Session-level answers (maintenance, closed session, invoice limit) say nothing about the
// invoice: it goes back to draft and can be sent again. Invoice-level codes reject it.
func TestSend_SessionUnavailableStaysDraft(t *testing.T) {
	for _, code := range []int{21155, 21173, 21180, 21184} {
		fx := newSendFx(t, nil)
		fx.on.Script(ksefake.PostErrorCode(400, code, "session problem"))
		fx.issue()
		fx.answers = []string{"y"}
		c, _, errs := fx.run("send", sendNum)
		if c != 1 || !strings.Contains(errs, "still a draft") {
			t.Errorf("%d: exit %d, stderr %q", code, c, errs)
		}
		if m := fx.meta(config.EnvTest, sendNum); m.State != store.StateDraft || m.KSeF != nil {
			t.Errorf("%d: meta = %+v", code, m)
		}
		// The next send, in a new session, goes through.
		fx.answers = []string{"y"}
		if c, _, errs := fx.run("send", sendNum); c != 0 {
			t.Errorf("%d: second send exit %d: %s", code, c, errs)
		}
	}
}

func TestSend_InvoiceLevel4xxIsRejected(t *testing.T) {
	for _, code := range []int{21001, 21402, 21403, 21405} {
		fx := newSendFx(t, nil)
		fx.on.Script(ksefake.PostErrorCode(400, code, "invoice problem"))
		fx.issue()
		fx.answers = []string{"y"}
		if c, _, errs := fx.run("send", sendNum); c != 4 {
			t.Errorf("%d: exit %d: %s", code, c, errs)
		}
		if st := fx.stateOf(sendNum); st != store.StateRejected {
			t.Errorf("%d: state = %s", code, st)
		}
	}
}

func TestSend_NeverRetriesPost(t *testing.T) {
	for name, out := range map[string]ksefake.Outcome{
		"5xx": ksefake.PostError(503), "dropped": ksefake.DropAfterReceive(), "lost": ksefake.DropBeforeReceive(),
	} {
		t.Run(name, func(t *testing.T) {
			fx := newSendFx(t, nil)
			fx.on.Script(out)
			fx.issue()
			fx.answers = []string{"y"}
			if code, _, errs := fx.run("send", sendNum); code != 5 {
				t.Fatalf("exit %d: %s", code, errs)
			}
			if n := fx.postInvoiceCount(); n != 1 {
				t.Errorf("POST /invoices was sent %d times, want exactly 1", n)
			}
			if fx.stateOf(sendNum) != store.StateUnknown {
				t.Errorf("state = %s", fx.stateOf(sendNum))
			}
		})
	}
}

func TestSend_RateLimitedPostStaysDraft(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue()
	fx.fake.Inject("POST", "/sessions/online/", 1, ksefake.Response{Status: 429,
		Body: `{"status":{"code":429,"description":"Too Many Requests","details":["slow down"]}}`})
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 1 || !strings.Contains(errs, "still a draft") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if m := fx.meta(config.EnvTest, sendNum); m.State != store.StateDraft || m.KSeF != nil {
		t.Errorf("meta = %+v", m)
	}
}

func TestSend_ProdNeedsTypedPRODUCTION(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.importCred(config.EnvProd)
	fx.issue("--env", "prod")
	// A wrong word aborts.
	fx.answers = []string{"production"}
	code, _, errs := fx.run("send", sendNum, "--env", "prod")
	if code != 10 || !strings.Contains(errs, "aborted, nothing was sent") || len(fx.fake.Requests()) != 0 {
		t.Fatalf("lowercase: exit %d, stderr %q, requests %d", code, errs, len(fx.fake.Requests()))
	}
	// The exact word sends.
	fx.answers = []string{"PRODUCTION"}
	code, out, errs := fx.run("send", sendNum, "--env", "prod")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if len(fx.prompts) != 2 || !strings.Contains(fx.prompts[1], "PRODUCTION") || strings.Contains(fx.prompts[1], "[y/N]") {
		t.Errorf("prompts = %q", fx.prompts)
	}
	if m := fx.meta(config.EnvProd, sendNum); m.State != store.StateAccepted {
		t.Errorf("state = %s", m.State)
	}
	if !strings.Contains(out, "ENVIRONMENT: PROD") || !strings.Contains(out, "to KSeF PROD") {
		t.Errorf("stdout:\n%s", out)
	}
}

// N10: a prod draft with only a TEST credential imported is refused at login (exit 3), and
// KSeF sees no request at all.
func TestSend_ProdNeedsTheProdCertificate(t *testing.T) {
	fx := newSendFx(t, nil) // imports a TEST credential only
	fx.issue("--env", "prod")
	fx.answers = []string{"PRODUCTION"}
	code, _, errs := fx.run("send", sendNum, "--env", "prod")
	if code != 3 {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	if n := len(fx.fake.Requests()); n != 0 {
		t.Errorf("KSeF got %d requests, want 0", n)
	}
	if st := fx.meta(config.EnvProd, sendNum).State; st != store.StateDraft {
		t.Errorf("state = %s", st)
	}
}

func TestSend_ProdYesAloneAborts10(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.importCred(config.EnvProd)
	fx.issue("--env", "prod")
	fx.noConfirm = true
	code, _, errs := fx.run("send", sendNum, "--env", "prod", "--yes")
	if code != 10 || !strings.Contains(errs, "--yes does not skip the PRODUCTION confirmation") || !strings.Contains(errs, "aborted, nothing was sent") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if len(fx.fake.Requests()) != 0 || fx.meta(config.EnvProd, sendNum).State != store.StateDraft {
		t.Error("something was sent or changed")
	}
	// --yes with a terminal still asks for PRODUCTION.
	fx.noConfirm = false
	fx.answers = []string{"PRODUCTION"}
	if code, _, errs := fx.run("send", sendNum, "--env", "prod", "--yes"); code != 0 || len(fx.prompts) != 1 {
		t.Errorf("exit %d, prompts %q, stderr %q", code, fx.prompts, errs)
	}
	// The same through fv issue --send --yes.
	fx2 := newSendFx(t, nil)
	fx2.importCred(config.EnvProd)
	fx2.noConfirm = true
	code, out, errs := fx2.run("--env", "prod", "issue", "--to", "acme", "--net", "100", "--send", "--yes")
	if code != 10 || len(fx2.fake.Requests()) != 0 || !strings.Contains(out, "ENVIRONMENT: PROD") {
		t.Errorf("issue --send --yes on prod: exit %d, requests %d\n%s\n%s", code, len(fx2.fake.Requests()), out, errs)
	}
}

func TestSendPrompt_ProdIsRedOnATerminalOnly(t *testing.T) {
	if p := sendPrompt(config.EnvProd, sendNum, true); !strings.HasPrefix(p, "\x1b[1;31m") || !strings.HasSuffix(p, "\x1b[0m") || !strings.Contains(p, "PRODUCTION") {
		t.Errorf("red prompt = %q", p)
	}
	if p := sendPrompt(config.EnvProd, sendNum, false); strings.Contains(p, "\x1b") {
		t.Errorf("plain prompt = %q", p)
	}
	for _, env := range []config.Env{config.EnvTest, config.EnvDemo} {
		if p := sendPrompt(env, sendNum, true); strings.Contains(p, "\x1b") || !strings.Contains(p, strings.ToUpper(string(env))) || !strings.Contains(p, "[y/N]") {
			t.Errorf("%s prompt = %q", env, p)
		}
	}
}

func TestSend_EnvMismatchRefused(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.importCred(config.EnvDemo)
	fx.issue()
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum, "--env", "demo")
	if code != 1 || !strings.Contains(errs, "belongs to the test environment") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if code, _, errs := fx.run("status", sendNum, "--env", "demo"); code != 1 || !strings.Contains(errs, "belongs to the test environment") {
		t.Errorf("status: exit %d, stderr %q", code, errs)
	}
	if len(fx.fake.Requests()) != 0 || len(fx.prompts) != 0 {
		t.Error("the mismatch must be refused before any prompt or request")
	}
	// A config default of another environment does not matter: the draft's environment is used.
	if err := writeFileForTest(fx.cfgPath, strings.Replace(issueConfig, `environment = "test"`, `environment = "demo"`, 1)); err != nil {
		t.Fatal(err)
	}
	fx.answers = []string{"y"}
	if code, out, errs := fx.run("send", sendNum); code != 0 || !strings.Contains(out, "to KSeF TEST") {
		t.Errorf("default-env send: exit %d\n%s\n%s", code, out, errs)
	}
	// sendStored itself refuses a different environment too.
	s := &session{deps: Deps{}}
	rc := &runContext{Env: config.EnvDemo}
	if err := s.checkSendable(rc, &store.Meta{Number: "X", Env: config.EnvTest, State: store.StateDraft, IssueDate: "2026-10-01"}); err == nil || !strings.Contains(err.Error(), "TEST") {
		t.Errorf("checkSendable err = %v", err)
	}
}

func TestSend_FutureIssueDateRefused(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue("--issue-date", "2026-11-05")
	fx.answers = []string{"y"}
	code, out, errs := fx.run("send", "FV/2026/11/001")
	if code != 1 || !strings.Contains(errs, "2026-11-05") || !strings.Contains(errs, "after today") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if strings.Contains(out, "ENVIRONMENT") || len(fx.prompts) != 0 || len(fx.fake.Requests()) != 0 {
		t.Error("a future-dated invoice must be refused before the preview, the prompt and any request")
	}
	// Today (Warsaw) is fine.
	fx2 := newSendFx(t, nil)
	fx2.issue("--issue-date", "2026-10-31")
	fx2.answers = []string{"y"}
	if code, _, errs := fx2.run("send", sendNum); code != 0 {
		t.Errorf("today: exit %d: %s", code, errs)
	}
}

func TestSend_XMLTamperedRefused(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue()
	if err := appendToInvoiceXML(fx.dataDir, "<!-- edited -->"); err != nil {
		t.Fatal(err)
	}
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 1 || len(fx.fake.Requests()) != 0 || len(fx.prompts) != 0 {
		t.Errorf("exit %d, stderr %q, requests %d", code, errs, len(fx.fake.Requests()))
	}
}

func TestSend_AuthFailureExit3KeepsDraft(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.fake.Respond("GET", "/auth/REF-1", 200, `{"status":{"code":450,"description":"Uwierzytelnianie zakonczone niepowodzeniem"}}`)
	fx.issue()
	fx.answers = []string{"y"}
	code, _, errs := fx.run("send", sendNum)
	if code != 3 || !strings.Contains(errs, "KSeF error 450") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if fx.stateOf(sendNum) != store.StateDraft || fx.postInvoiceCount() != 0 {
		t.Error("a failed login must leave the draft untouched")
	}
}

func TestSend_RequestCarriesBothHashesAndCiphertext(t *testing.T) {
	fx := newSendFx(t, nil)
	fx.issue()
	fx.answers = []string{"y"}
	if code, _, errs := fx.run("send", sendNum); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var body []byte
	for _, r := range fx.fake.Requests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/invoices") {
			body = r.Body
		}
	}
	for _, f := range []string{"invoiceHash", "invoiceSize", "encryptedInvoiceHash", "encryptedInvoiceSize", "encryptedInvoiceContent"} {
		if !bytes.Contains(body, []byte(`"`+f+`"`)) {
			t.Errorf("request lacks %s", f)
		}
	}
	// The key the fake unwrapped is the one Deps.Rand produced.
	key, iv := fx.on.SessionKey(fx.on.SessionRefs()[0])
	for i := range key {
		if key[i] != streamByte(i) {
			t.Fatal("session key is not the Deps.Rand stream")
		}
	}
	if len(iv) != 16 {
		t.Errorf("iv = %d bytes", len(iv))
	}
}

func readFile(p string) ([]byte, error) { return os.ReadFile(p) }

func writeFileForTest(p, content string) error { return os.WriteFile(p, []byte(content), 0o600) }

// appendToInvoiceXML edits every invoice.xml under dataDir behind the archive's back.
func appendToInvoiceXML(dataDir, extra string) error {
	return filepath.WalkDir(dataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "invoice.xml" {
			return err
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(extra)
		return err
	})
}

// N2: when the archive cannot record an outcome KSeF may already hold, the exit code is 5
// (unknown), not 1, so scripts run fv status.
func TestMarkUnknown_ArchiveFailureExits5(t *testing.T) {
	s := &session{}
	st := store.Open(t.TempDir(), nil)
	err := s.markUnknown(st, config.EnvTest, "FV/2026/10/001")
	if err == nil || exitcode.Code(err) != 5 {
		t.Fatalf("err = %v, code %d", err, exitcode.Code(err))
	}
}
