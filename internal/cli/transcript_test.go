package cli

import (
	"bytes"
	"context"
	"crypto/rsa"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

// updateTranscripts rewrites the transcript blocks of docs/workflow.md from a fresh run:
// go test ./internal/cli -run TestTranscript_Guide -update
var updateTranscripts = flag.Bool("update", false, "rewrite the transcript blocks in docs/workflow.md")

const workflowDoc = "../../docs/workflow.md"

// transcriptStep is one command of the guide scenario.
type transcriptStep struct {
	name    string   // the <!-- transcript:NAME --> marker
	args    []string // the arguments after "fv"
	answers []string // what the user types at the confirmation prompt
}

var guideSteps = []transcriptStep{
	{name: "issue", args: []string{"issue", "--to", "acme", "--net", "8500,43"}},
	{name: "show", args: []string{"show", "FV/2026/10/001"}},
	{name: "send", args: []string{"send", "FV/2026/10/001"}, answers: []string{"y"}},
	{name: "status", args: []string{"status", "FV/2026/10/001"}},
	{name: "list", args: []string{"list"}},
	{name: "issue-eur", args: []string{"issue", "--to", "acme", "--net", "2000", "--currency", "EUR"}},
}

// shellWord quotes an argument the way the guide writes it: amounts and anything with a
// comma or a space in double quotes.
func shellWord(a string) string {
	if a == "" || strings.ContainsAny(a, " ,\"'$") {
		return `"` + a + `"`
	}
	return a
}

// runGuideScenario runs the guide's scenario in-process against the fakes with a fixed clock
// (2026-10-31 10:00 Warsaw) and returns each step's transcript as a terminal would show it:
// the command line, then stdout and stderr interleaved, then the typed answers.
func runGuideScenario(t *testing.T) map[string]string {
	t.Helper()
	fx := &sendFx{partyFx: newPartyFx(t, issueConfig), fake: ksefake.New(t), backend: &memBackend{}, rnd: &seqRand{}}
	clock := func() time.Time { return fx.now }
	sym := credtest.New(t, credtest.Spec{RSA: true})
	fx.on = fx.fake.EnableOnline(ksefake.OnlineOptions{
		Key: sym.Key.(*rsa.PrivateKey), CertDER: sym.DER, Bearer: ksefAccessTok, NIP: ksefNIP, Now: clock,
	})
	fx.fake.Respond("POST", "/auth/challenge", 200, `{"challenge":"20261031-CR-GUIDE","timestamp":"2026-10-31T09:00:00Z","timestampMs":1}`)
	fx.fake.Respond("POST", "/auth/xades-signature", 202, `{"referenceNumber":"REF-1","authenticationToken":{"token":"`+ksefAuthTok+`","validUntil":"2027-10-31T09:10:00Z"}}`)
	fx.fake.Respond("GET", "/auth/REF-1", 200, `{"status":{"code":200,"description":"ok"}}`)
	fx.fake.Respond("POST", "/auth/token/redeem", 200, `{"accessToken":{"token":"`+ksefAccessTok+`","validUntil":"2027-10-31T09:15:00Z"},"refreshToken":{"token":"`+ksefRefresh+`","validUntil":"2027-11-30T09:00:00Z"}}`)
	// A certificate whose validity is fixed relative to the scenario clock, so no expiry warning appears.
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + ksefNIP,
		NotBefore: fx.now.AddDate(0, -3, 0), NotAfter: fx.now.AddDate(2, 0, 0)})
	if err := credentials.Import(credentials.ImportSource{P12: credtest.P12(t, c, ksefPass)}, fx.dataDir, config.EnvTest, ksefNIP, ksefPass); err != nil {
		t.Fatal(err)
	}
	if err := fx.backend.Set("fv/ksef/test/"+ksefNIP, "fv", ksefPass); err != nil {
		t.Fatal(err)
	}

	nbpSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/2026-10-30") {
			b, err := os.ReadFile(nbpTestdata + "eur_200.json")
			if err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
			return
		}
		nf, err := os.ReadFile(nbpTestdata + "404.txt")
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(nf)
	}))
	t.Cleanup(nbpSrv.Close)

	out := map[string]string{}
	for _, st := range guideSteps {
		var term bytes.Buffer
		answers := st.answers
		deps := Deps{
			Stdin: strings.NewReader(""), Stdout: &term, Stderr: &term,
			Getenv: func(k string) string {
				if k == "FV_DATA_DIR" {
					return fx.dataDir
				}
				return ""
			},
			GOOS: "linux", Home: func() (string, error) { return t.TempDir(), nil },
			Now: clock, Secrets: fx.backend, Rand: fx.rnd,
			Endpoints: func(service string) (string, *http.Client) {
				if service == "nbp" {
					return nbpSrv.URL, nbpSrv.Client()
				}
				return fx.srv.URL, fx.srv.Client()
			},
			KSeFEndpoint: func(config.Env) (string, *http.Client) { return fx.fake.URL, fx.fake.Client() },
			Sleep:        func(context.Context, time.Duration) error { return nil },
			Confirm: func(prompt string) (string, error) {
				if len(answers) == 0 {
					return "", errors.New("unexpected prompt: " + prompt)
				}
				term.WriteString(prompt + answers[0] + "\n")
				a := answers[0]
				answers = answers[1:]
				return a, nil
			},
		}
		code := execute(deps, NewRootCmd(deps), append([]string{"--config", fx.cfgPath}, st.args...))
		if code != 0 {
			t.Fatalf("fv %s: exit %d\n%s", strings.Join(st.args, " "), code, term.String())
		}
		words := make([]string, len(st.args))
		for i, a := range st.args {
			words[i] = shellWord(a)
		}
		out[st.name] = "$ fv " + strings.Join(words, " ") + "\n" + strings.TrimRight(term.String(), "\n") + "\n"
	}
	return out
}

var transcriptRe = regexp.MustCompile("(?s)(<!-- transcript:([a-z0-9-]+) -->\n```text\n)(.*?)(```)")

func TestTranscript_Guide(t *testing.T) {
	got := runGuideScenario(t)
	raw, err := os.ReadFile(workflowDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	seen := map[string]bool{}
	next := transcriptRe.ReplaceAllStringFunc(doc, func(block string) string {
		m := transcriptRe.FindStringSubmatch(block)
		name := m[2]
		want, ok := got[name]
		if !ok {
			t.Errorf("docs/workflow.md has a transcript %q that the scenario does not produce", name)
			return block
		}
		seen[name] = true
		if !*updateTranscripts && m[3] != want {
			t.Errorf("transcript %q in docs/workflow.md differs from the real output (run go test ./internal/cli -run TestTranscript_Guide -update).\n--- guide\n%s--- real\n%s", name, m[3], want)
		}
		return m[1] + want + m[4]
	})
	for _, st := range guideSteps {
		if !seen[st.name] {
			t.Errorf("docs/workflow.md lacks the <!-- transcript:%s --> block", st.name)
		}
	}
	if *updateTranscripts && next != doc {
		if err := os.WriteFile(workflowDoc, []byte(next), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
