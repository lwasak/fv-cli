package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fa3"
	"github.com/lwasak/fv-cli/internal/store"
)

const issueConfig = `[ksef]
environment = "test"

[seller]
nip = "5260250274"
place_of_issue = "Gdynia"

[defaults]
vat_rate = "23"
bank_account = "PL61 1090 1014 0000 0712 1981 2874"

[parties.acme]
nip = "5252248481"
`

type issueFx struct {
	*partyFx
	nbp      *httptest.Server // optional fake NBP; the "nbp" endpoint uses it when set
	ksef     *httptest.Server
	ksefHits atomic.Int64
}

func newIssueFx(t *testing.T, cfg string) *issueFx {
	t.Helper()
	fx := &issueFx{partyFx: newPartyFx(t, cfg)}
	fx.ksef = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.ksefHits.Add(1)
		http.Error(w, "unexpected", http.StatusTeapot)
	}))
	t.Cleanup(fx.ksef.Close)
	return fx
}

func (fx *issueFx) run(args ...string) (code int, stdout, stderr string) {
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
		Now: func() time.Time { return fx.now },
		Endpoints: func(svc string) (string, *http.Client) {
			if svc == "nbp" && fx.nbp != nil {
				return fx.nbp.URL, fx.nbp.Client()
			}
			return fx.srv.URL, fx.srv.Client()
		},
		KSeFEndpoint: func(config.Env) (string, *http.Client) { return fx.ksef.URL, fx.ksef.Client() },
	}
	code = execute(deps, NewRootCmd(deps), append([]string{"--config", fx.cfgPath}, args...))
	return code, out.String(), errb.String()
}

func (fx *issueFx) store() *store.Store {
	return store.Open(fx.dataDir, func() time.Time { return fx.now })
}

func (fx *issueFx) xmlOf(env config.Env, number string) []byte {
	fx.t.Helper()
	b, err := fx.store().XML(env, number)
	if err != nil {
		fx.t.Fatal(err)
	}
	return b
}

func TestIssue_DryRunStoresDraftNoKSeF(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	code, out, errs := fx.run("issue", "--to", "acme", "--net", "18500,43")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.HasSuffix(out, "Nothing was sent to KSeF. To send: fv send FV/2026/10/001\n") {
		t.Errorf("output must end with the send hint:\n%s", out)
	}
	m, err := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if err != nil {
		t.Fatal(err)
	}
	if m.State != store.StateDraft || m.XMLSHA256 == "" || m.Buyer.RequestID != "a1b2c-d3e4f5g" || m.Seller.RequestID != "s3ll3-r000001" {
		t.Errorf("meta: %+v", m)
	}
	if m.Gross != 2275553 { // 18500,43 + VAT 4255,10
		t.Errorf("gross = %d", m.Gross)
	}
	if got := fx.ksefHits.Load(); got != 0 {
		t.Errorf("KSeF received %d requests, want 0", got)
	}
	if !bytes.Contains(fx.xmlOf(config.EnvTest, m.Number), []byte("<P_2>FV/2026/10/001</P_2>")) {
		t.Error("xml does not carry the number")
	}
}

func TestIssue_Deterministic(t *testing.T) {
	var first []byte
	for i := 0; i < 2; i++ {
		fx := newIssueFx(t, issueConfig)
		if code, _, errs := fx.run("issue", "--to", "acme", "--net", "1000"); code != 0 {
			t.Fatalf("exit %d: %s", code, errs)
		}
		b := fx.xmlOf(config.EnvTest, "FV/2026/10/001")
		if first == nil {
			first = b
		} else if !bytes.Equal(first, b) {
			t.Errorf("XML differs between runs:\n%s\n---\n%s", first, b)
		}
	}
	if !bytes.Contains(first, []byte("<DataWytworzeniaFa>2026-10-31T09:00:00Z</DataWytworzeniaFa>")) {
		t.Errorf("creation time must come from the injected clock:\n%s", first)
	}
}

func TestIssue_PreviewSections(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	code, out, errs := fx.run("issue", "--to", "acme", "--net", "18500,43")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.HasPrefix(out, "ENVIRONMENT: TEST\n") {
		t.Errorf("environment must come first:\n%s", out)
	}
	for _, want := range []string{
		"FV/2026/10/001", "Issued:    2026-10-31", "Sale date: 2026-10-31", "Due date:  2026-11-14",
		"Seller:    MOJA FIRMA JANUSZ NOWAK", "ul. Świętojańska 10/3, 81-368 Gdynia", "NIP 5260250274",
		"Buyer:     ACME SPÓŁKA Z OGRANICZONĄ ODPOWIEDZIALNOŚCIĄ", "NIP 5252248481",
		"VAT: Czynny (checked 2026-10-31 10:00, White List requestId a1b2c-d3e4f5g)",
		"Usługi za październik 2026", "18 500,43", "23%", "Total:     22 755,53 PLN",
		"PL61 1090 1014 0000 0712 1981 2874  (on White List: yes)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("no colour outside a terminal")
	}
}

func TestIssue_RegistryDownNoCacheExit2NoReservation(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	fx.wlCode = http.StatusInternalServerError
	code, _, errs := fx.run("issue", "--to", "acme", "--net", "100")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errs)
	}
	if _, out, _ := fx.run("list"); strings.TrimSpace(out) != "no invoices" {
		t.Errorf("a number was reserved: %q", out)
	}
	fx.wlCode = 200
	if code, out, errs := fx.run("issue", "--to", "acme", "--net", "100"); code != 0 || !strings.Contains(out, "FV/2026/10/001") {
		t.Errorf("first number must still be 001: %d %s %s", code, out, errs)
	}
}

func TestIssue_RateAndCurrencyRejected(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--rate", "0"}, "not supported in v1"},
		{[]string{"--rate", "np"}, "not supported in v1"},
		{[]string{"--rate", "7"}, "unknown rate"},
		{[]string{"--currency", "EUR", "--rate", "np"}, "reverse charge"},
		{[]string{"--currency", "EUR", "--rate", "0"}, "reverse charge"},
	} {
		fx := newIssueFx(t, issueConfig)
		code, _, errs := fx.run(append([]string{"issue", "--to", "acme", "--net", "100"}, tc.args...)...)
		if code != 1 || !strings.Contains(errs, tc.want) {
			t.Errorf("%v: exit %d, stderr %q (want 1 and %q)", tc.args, code, errs, tc.want)
		}
		if _, out, _ := fx.run("list"); strings.TrimSpace(out) != "no invoices" {
			t.Errorf("%v: a number was reserved: %q", tc.args, out)
		}
		if len(fx.wlHits) != 0 {
			t.Errorf("%v: registry was called before the inputs were checked", tc.args)
		}
	}
}

func TestIssue_ZwNeedsBasis(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	code, _, errs := fx.run("issue", "--to", "acme", "--net", "100", "--rate", "zw")
	if code != 1 || !strings.Contains(errs, "exemption_basis") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	fx2 := newIssueFx(t, issueConfig+"\n[parties.acme_zw]\nnip = \"5252248481\"\nexemption_basis = \"art. 43 ust. 1 pkt 37 ustawy o VAT\"\n")
	code, out, errs := fx2.run("issue", "--to", "acme_zw", "--net", "100", "--rate", "zw")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(string(fx2.xmlOf(config.EnvTest, "FV/2026/10/001")), "<P_19A>art. 43 ust. 1 pkt 37 ustawy o VAT</P_19A>") {
		t.Error("exemption basis missing from XML")
	}
	if !strings.Contains(out, "zw") {
		t.Errorf("preview:\n%s", out)
	}
}

func TestIssue_QtyUnitPrice(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	code, out, errs := fx.run("issue", "--to", "acme", "--qty", "160", "--unit-price", "115,50")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"160", "115,50", "18 480,00", "Total:     22 730,40 PLN"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "1", "--qty", "2", "--unit-price", "3"); code != 1 || !strings.Contains(errs, "not both") {
		t.Errorf("net and qty together: %d %s", code, errs)
	}
	if code, _, errs := fx.run("issue", "--to", "acme", "--qty", "2"); code != 1 || !strings.Contains(errs, "together") {
		t.Errorf("qty alone: %d %s", code, errs)
	}
}

func TestIssue_SaleDateRuleAndDefaults(t *testing.T) {
	cfg := issueConfig + "\n[parties.monthly]\nnip = \"5252248481\"\nsale_date = \"last_day_of_month\"\nline_template = \"Consulting {month_name_en} {year}\"\nunit = \"h\"\npayment_days = 0\n"
	fx := newIssueFx(t, cfg)
	code, out, errs := fx.run("issue", "--to", "monthly", "--net", "100", "--issue-date", "2028-02-15")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"Issued:    2028-02-15", "Sale date: 2028-02-29", "Due date:  2028-02-15", "Consulting February 2028"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "FV/2028/02/001") {
		t.Errorf("number from the issue date:\n%s", out)
	}
	code, out, _ = fx.run("issue", "--to", "acme", "--net", "100", "--issue-date", "2026-12-20", "--sale-date", "2026-12-01", "--desc", "Praca {month_name} {year}")
	if code != 0 || !strings.Contains(out, "Praca grudzień 2026") || !strings.Contains(out, "Due date:  2027-01-03") {
		t.Errorf("flag sale date: %d\n%s", code, out)
	}
}

func TestIssue_AccountWarningAndMPP(t *testing.T) {
	cfg := strings.Replace(issueConfig, "PL61 1090 1014 0000 0712 1981 2874", "PL27 1140 2004 0000 3002 0135 5387", 1)
	fx := newIssueFx(t, cfg)
	code, out, errs := fx.run("issue", "--to", "acme", "--net", "13000")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"(on White List: NO)", "warning: bank account 27114020040000300201355387 is not in the White List accounts", "split payment (MPP)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	m, _ := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if m.BankOnWhiteList == nil || *m.BankOnWhiteList {
		t.Errorf("BankOnWhiteList = %v", m.BankOnWhiteList)
	}
}

func TestIssue_BuildFailureReleasesNumber(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	old := buildFA3
	buildFA3 = func(fa3.Input) ([]byte, error) { return nil, errors.New("boom") }
	code, _, errs := fx.run("issue", "--to", "acme", "--net", "100")
	buildFA3 = old
	if code != 1 || !strings.Contains(errs, "boom") || !strings.Contains(errs, "released") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if _, out, _ := fx.run("list"); strings.TrimSpace(out) != "no invoices" {
		t.Errorf("draft left behind: %q", out)
	}
	if code, out, _ := fx.run("issue", "--to", "acme", "--net", "100"); code != 0 || !strings.Contains(out, "FV/2026/10/001") {
		t.Errorf("number was not reused: %s", out)
	}
}

func TestIssue_UnknownPartyAndMissingConfig(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	if code, _, errs := fx.run("issue", "--to", "nobody", "--net", "1"); code != 1 || !strings.Contains(errs, "known: acme") {
		t.Errorf("%d %s", code, errs)
	}
	if code, _, errs := fx.run("issue", "--net", "1"); code != 1 || !strings.Contains(errs, "--to is required") {
		t.Errorf("%d %s", code, errs)
	}
	noBank := strings.Replace(issueConfig, "bank_account = \"PL61 1090 1014 0000 0712 1981 2874\"\n", "", 1)
	fx = newIssueFx(t, noBank)
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "1"); code != 1 || !strings.Contains(errs, "no bank account") {
		t.Errorf("%d %s", code, errs)
	}
}

func TestIssue_SendWithoutCredentialKeepsDraft(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	code, out, errs := fx.run("issue", "--to", "acme", "--net", "100", "--send", "--yes")
	if code != 3 || !strings.Contains(errs, "no KSeF credential") || !strings.Contains(out, "FV/2026/10/001") {
		t.Errorf("exit %d\nout: %s\nerr: %s", code, out, errs)
	}
	if m, err := fx.store().Load(config.EnvTest, "FV/2026/10/001"); err != nil || m.State != store.StateDraft {
		t.Errorf("meta = %+v, err = %v", m, err)
	}
	if fx.ksefHits.Load() != 0 {
		t.Error("KSeF was contacted")
	}
}

func TestIssue_EnvHintAndShowOtherEnv(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	code, out, errs := fx.run("--env", "demo", "issue", "--to", "acme", "--net", "100")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.HasPrefix(out, "ENVIRONMENT: DEMO\n") || !strings.HasSuffix(out, "To send: fv send FV/2026/10/001 --env demo\n") {
		t.Errorf("demo output:\n%s", out)
	}
	// The config default is test: show finds the demo invoice and says so.
	code, out, errs = fx.run("show", "FV/2026/10/001")
	if code != 0 || !strings.Contains(out, "ENVIRONMENT: DEMO") || !strings.Contains(out, "was found in demo, not in the selected environment test") {
		t.Errorf("show: %d\n%s%s", code, out, errs)
	}
	if code, _, errs := fx.run("show", "FV/2026/10/999"); code != 1 || !strings.Contains(errs, "is not in the archive") {
		t.Errorf("missing invoice: %d %s", code, errs)
	}
}

func TestShow_ReadableAndXMLExact(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "18500,43"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	code, out, errs := fx.run("show", "FV/2026/10/001")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"ENVIRONMENT: TEST", "[draft]", "Seller:", "Buyer:", "VAT: Czynny", "Total:     22 755,53 PLN",
		"State:     draft", "KSeF no.:  -", "UPO:       -", "on White List: yes", "XML:       "} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	stored, err := os.ReadFile(filepath.Join(fx.dataDir, "invoices", "test", "2026", "FV_2026_10_001", "invoice.xml"))
	if err != nil {
		t.Fatal(err)
	}
	code, out, errs = fx.run("show", "FV/2026/10/001", "--xml")
	if code != 0 || out != string(stored) || errs != "" {
		t.Errorf("--xml must print the stored bytes exactly: code %d, err %q", code, errs)
	}
	if err := os.WriteFile(filepath.Join(fx.dataDir, "invoices", "test", "2026", "FV_2026_10_001", "invoice.xml"), append(stored, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := fx.run("show", "FV/2026/10/001", "--xml"); code == 0 || !strings.Contains(errs, "hash") {
		t.Errorf("tampered XML must be refused: %d %q", code, errs)
	}
}

func TestIssue_ValidateWithXmllint(t *testing.T) {
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Skip("xmllint not on PATH")
	}
	fx := newIssueFx(t, issueConfig)
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "18500,43", "--validate"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

func TestIssue_HelpMentionsEveryFlag(t *testing.T) {
	var out, errb bytes.Buffer
	deps := Deps{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }}
	if code := execute(deps, NewRootCmd(deps), []string{"issue", "--help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"--to", "--net", "--qty", "--unit-price", "--rate", "--currency", "--issue-date", "--sale-date",
		"--desc", "--item", "--line", "--validate", "--send", "--yes", "--net \"18500,43\"", "--item dev=160", "Examples:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("issue --help lacks %q", want)
		}
	}
}

// D1: a buyer that is not on the White List is invoiceable with name and address in the config.
func TestIssue_NotOnWhiteListWithOverrides(t *testing.T) {
	cfg := issueConfig + "name = \"Fundacja Przyklad\"\naddress = \"ul. Dluga 1, 80-001 Gdansk\"\n"
	fx := newIssueFx(t, cfg)
	fx.wlFile[pBuyer] = "wl_subject_null.json"
	code, out, errs := fx.run("issue", "--to", "acme", "--net", "100")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(errs+out, "not on the VAT White List") || !strings.Contains(out, "Niezarejestrowany") {
		t.Errorf("want warning and status in the output:\n%s\n%s", out, errs)
	}
	m, err := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if err != nil {
		t.Fatal(err)
	}
	if m.Buyer.Name != "Fundacja Przyklad" || m.Buyer.StatusVat != "Niezarejestrowany" || m.Buyer.Source != "config" {
		t.Errorf("buyer = %+v", m.Buyer)
	}
}

func TestIssue_NotOnWhiteListWithoutOverridesExit2(t *testing.T) {
	fx := newIssueFx(t, issueConfig)
	fx.wlFile[pBuyer] = "wl_subject_null.json"
	if code, _, errs := fx.run("issue", "--to", "acme", "--net", "100"); code != 2 || !strings.Contains(errs, "not registered") {
		t.Fatalf("exit %d: %s", code, errs)
	}
}
