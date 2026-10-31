package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/registry"
)

const (
	pSeller = "5260250274"
	pBuyer  = "5252248481"
	pSole   = "7740001454"
)

var pNow = time.Date(2026, 10, 31, 10, 0, 0, 0, registry.Warsaw())

const partyBaseConfig = `# my fv config - keep this comment
[ksef]
environment = "test"   # trailing comment stays

[seller]
nip = "5260250274"
`

func regData(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "registry", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type partyCounter struct {
	mu   sync.Mutex
	days map[string]int
}

func (c *partyCounter) WhiteListCall(day string, essential bool) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.days == nil {
		c.days = map[string]int{}
	}
	if !essential && c.days[day] >= registry.DailyLimit {
		return c.days[day], fmt.Errorf("limit: %w", registry.ErrQuota)
	}
	c.days[day]++
	return c.days[day], nil
}

type partyFx struct {
	t       *testing.T
	srv     *httptest.Server
	cfgPath string
	dataDir string
	counter *partyCounter
	now     time.Time

	mu      sync.Mutex
	wlHits  []string
	viesHit []string
	wlFile  map[string]string
	wlCode  int
	vies    string
}

func newPartyFx(t *testing.T, cfg string) *partyFx {
	t.Helper()
	fx := &partyFx{t: t, dataDir: t.TempDir(), counter: &partyCounter{}, now: pNow, wlCode: 200, vies: "vies_de.json",
		wlFile: map[string]string{pBuyer: "wl_working.json", pSole: "wl_residence.json", pSeller: "wl_seller.json"}}
	fx.cfgPath = filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(fx.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/search/nip/"):
			nip := strings.TrimPrefix(r.URL.Path, "/api/search/nip/")
			fx.wlHits = append(fx.wlHits, nip)
			if fx.wlCode != 200 {
				w.WriteHeader(fx.wlCode)
				return
			}
			_, _ = io.WriteString(w, regData(t, fx.wlFile[nip]))
		case r.URL.Path == "/check-vat-number":
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			fx.viesHit = append(fx.viesHit, req["countryCode"]+req["vatNumber"]+" by "+req["requesterNumber"])
			_, _ = io.WriteString(w, regData(t, fx.vies))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fx.srv.Close)
	old := whiteListCounter
	whiteListCounter = func(*session, *runContext) registry.Counter { return fx.counter }
	t.Cleanup(func() { whiteListCounter = old })
	return fx
}

func (fx *partyFx) run(args ...string) (code int, stdout, stderr string) {
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
		Endpoints: func(string) (string, *http.Client) {
			return fx.srv.URL, fx.srv.Client()
		},
	}
	root := partyRoot(deps)
	code = execute(deps, root, append([]string{"--config", fx.cfgPath}, args...))
	return code, out.String(), errb.String()
}

// partyRoot builds a root with the party command; it adds the command itself only when
// the root does not register it already.
func partyRoot(deps Deps) *cobra.Command {
	root, s := newRoot(deps)
	if c, _, _ := root.Find([]string{"party"}); c == root {
		root.AddCommand(newPartyCmd(s))
	}
	return root
}

func (fx *partyFx) config() string {
	b, err := os.ReadFile(fx.cfgPath)
	if err != nil {
		fx.t.Fatal(err)
	}
	return string(b)
}

func TestPartyAdd_WritesConfigKeepsComments(t *testing.T) {
	fx := newPartyFx(t, partyBaseConfig)
	code, out, errs := fx.run("party", "add", "acme", "--nip", pBuyer)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	got := fx.config()
	if !strings.HasPrefix(got, partyBaseConfig) {
		t.Errorf("existing config text changed:\n%s", got)
	}
	cfg, err := config.Load(fx.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Parties["acme"].NIP != pBuyer || cfg.Parties["acme"].Name != "" {
		t.Errorf("parties = %+v", cfg.Parties)
	}
	for _, want := range []string{"Added [parties.acme]", "ACME SPÓŁKA", "[whitelist]", "a1b2c-d3e4f5g", "Czynny"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(fx.wlHits) != 1 || fx.counter.days["2026-10-31"] != 1 {
		t.Errorf("hits=%v counter=%v", fx.wlHits, fx.counter.days)
	}
	if _, err := os.Stat(filepath.Join(fx.dataDir, "parties.json")); err != nil {
		t.Errorf("no cache written: %v", err)
	}
}

func TestPartyAdd_VatIDUsesSellerAsRequester(t *testing.T) {
	fx := newPartyFx(t, partyBaseConfig)
	code, out, errs := fx.run("party", "add", "berlin", "--vat-id", "de123456789")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if fmt.Sprint(fx.viesHit) != "[DE123456789 by "+pSeller+"]" {
		t.Errorf("VIES hits = %v", fx.viesHit)
	}
	if !strings.Contains(fx.config(), `vat_id = "DE123456789"`) || !strings.Contains(out, "WAPIAAAAZ1b2c3d4") || !strings.Contains(out, "[vies]") {
		t.Errorf("config:\n%s\nout:\n%s", fx.config(), out)
	}
	if fx.counter.days["2026-10-31"] != 0 {
		t.Error("VIES counted as White List call")
	}
}

func TestPartyAdd_ExistingAliasRefused(t *testing.T) {
	cfg := partyBaseConfig + "\n[parties.acme]\nnip = \"" + pBuyer + "\"\n"
	fx := newPartyFx(t, cfg)
	code, _, errs := fx.run("party", "add", "acme", "--nip", pSole)
	if code != 1 || !strings.Contains(errs, "already exists") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if fx.config() != cfg || len(fx.wlHits) != 0 {
		t.Error("config changed or registry called")
	}
}

func TestPartyAdd_FetchFailWritesNothing(t *testing.T) {
	fx := newPartyFx(t, partyBaseConfig)
	fx.wlCode = 503
	code, _, errs := fx.run("party", "add", "acme", "--nip", pBuyer)
	if code != 2 || !strings.Contains(errs, "fv: ") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if fx.config() != partyBaseConfig {
		t.Errorf("config changed:\n%s", fx.config())
	}
	fx.wlCode = 200
	fx.wlFile[pBuyer] = "wl_subject_null.json"
	if code, _, errs := fx.run("party", "add", "acme", "--nip", pBuyer); code != 2 || !strings.Contains(errs, "not registered") {
		t.Fatalf("not registered: exit %d: %s", code, errs)
	}
	if fx.config() != partyBaseConfig {
		t.Error("config changed on not-registered")
	}
}

func TestPartyAdd_UsageErrors(t *testing.T) {
	fx := newPartyFx(t, partyBaseConfig)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"acme"}, "exactly one of nip and vat_id"},
		{[]string{"acme", "--nip", pBuyer, "--vat-id", "DE1"}, "not both"},
		{[]string{"acme", "--nip", "5252248480"}, "checksum"},
		{[]string{"Acme!", "--nip", pBuyer}, "alias"},
		{[]string{"gr", "--vat-id", "GR123456783"}, "EL"},
		{[]string{"pl", "--vat-id", "PL5252248481"}, "--nip"},
	}
	for _, c := range cases {
		code, _, errs := fx.run(append([]string{"party", "add"}, c.args...)...)
		if code != 1 || !strings.Contains(errs, c.want) {
			t.Errorf("%v: exit %d, stderr %q (want %q)", c.args, code, errs, c.want)
		}
	}
	if len(fx.wlHits)+len(fx.viesHit) != 0 || fx.config() != partyBaseConfig {
		t.Error("registry called or config changed by a usage error")
	}
}

func TestPartyAdd_NameAddressOverridesAreWritten(t *testing.T) {
	fx := newPartyFx(t, partyBaseConfig)
	fx.vies = "vies_dash.json"
	if code, _, errs := fx.run("party", "add", "de2", "--vat-id", "DE987654321"); code != 2 || !strings.Contains(errs, "parties.de2.name") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if fx.config() != partyBaseConfig {
		t.Fatal("config changed")
	}
	code, out, errs := fx.run("party", "add", "de2", "--vat-id", "DE987654321", "--name", `Muster "AG"\Co`, "--address", "Weg 2, 80331 München")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	cfg, err := config.Load(fx.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if p := cfg.Parties["de2"]; p.Name != `Muster "AG"\Co` || p.Address != "Weg 2, 80331 München" {
		t.Errorf("party = %+v", p)
	}
	if !strings.Contains(out, "[config]") {
		t.Errorf("output: %s", out)
	}
}

func TestPartyShow_SourcesAndAge(t *testing.T) {
	cfg := partyBaseConfig + "\n[parties.acme]\nnip = \"" + pBuyer + "\"\naddress = \"Inna 1, 00-001 Łódź\"\n"
	fx := newPartyFx(t, cfg)
	// first show has no cache, so it fetches once
	code, _, errs := fx.run("party", "show", "acme")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	fx.now = pNow.Add(3*24*time.Hour + time.Hour)
	code, out, errs := fx.run("party", "show", "acme")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{
		"Name:", "ACME SPÓŁKA Z OGRANICZONĄ ODPOWIEDZIALNOŚCIĄ  [whitelist]",
		"Inna 1, 00-001 Łódź  [config]",
		"Czynny  [whitelist]",
		"61109010140000071219812874  [whitelist]",
		"a1b2c-d3e4f5g",
		"fetched 3 d ago (2026-10-31 10:00)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stale") {
		t.Errorf("fresh entry shown as stale:\n%s", out)
	}
	if len(fx.wlHits) != 1 {
		t.Errorf("show contacted the registry again: %v", fx.wlHits)
	}
	fx.now = pNow.Add(45 * 24 * time.Hour)
	_, out, _ = fx.run("party", "show", "acme")
	if !strings.Contains(out, "stale (older than 30 days; run fv party refresh acme)") || len(fx.wlHits) != 1 {
		t.Errorf("stale output:\n%s hits=%v", out, fx.wlHits)
	}
	if code, _, errs := fx.run("party", "show", "nobody"); code != 1 || !strings.Contains(errs, "unknown party") {
		t.Errorf("unknown alias: exit %d %s", code, errs)
	}
}

func TestPartyShow_VIESAndTTLFromConfig(t *testing.T) {
	cfg := partyBaseConfig + "\n[registry]\ncache_ttl = 10\n\n[parties.berlin]\nvat_id = \"DE123456789\"\n"
	fx := newPartyFx(t, cfg)
	if code, _, errs := fx.run("party", "show", "berlin"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	fx.now = pNow.Add(11 * 24 * time.Hour)
	_, out, _ := fx.run("party", "show", "berlin")
	for _, want := range []string{"VAT id DE123456789", "Beispiel GmbH  [vies]", "WAPIAAAAZ1b2c3d4", "older than 10 days"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestPartyRefresh_All(t *testing.T) {
	cfg := partyBaseConfig + "\n[parties.acme]\nnip = \"" + pBuyer + "\"\n\n[parties.jan]\nnip = \"" + pSole + "\"\n\n[parties.berlin]\nvat_id = \"DE123456789\"\n"
	fx := newPartyFx(t, cfg)
	code, out, errs := fx.run("party", "refresh", "--all")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if fmt.Sprint(fx.wlHits) != "["+pBuyer+" "+pSole+"]" || len(fx.viesHit) != 1 {
		t.Errorf("wl=%v vies=%v", fx.wlHits, fx.viesHit)
	}
	for _, want := range []string{"acme: refreshed (a1b2c-d3e4f5g)", "jan: refreshed", "berlin: refreshed (WAPIAAAAZ1b2c3d4)", "Zwolniony"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// a single alias refreshes only that party
	fx.wlHits = nil
	if code, _, errs := fx.run("party", "refresh", "jan"); code != 0 || fmt.Sprint(fx.wlHits) != "["+pSole+"]" {
		t.Errorf("single: exit %d %s hits=%v", code, errs, fx.wlHits)
	}
	if code, _, errs := fx.run("party", "refresh"); code != 1 || !strings.Contains(errs, "--all") {
		t.Errorf("no args: exit %d %s", code, errs)
	}
	if code, _, _ := fx.run("party", "refresh", "jan", "--all"); code != 1 {
		t.Errorf("both: exit %d", code)
	}
}

func TestPartyRefresh_StopsAtQuota(t *testing.T) {
	cfg := partyBaseConfig + "\n[parties.acme]\nnip = \"" + pBuyer + "\"\n\n[parties.jan]\nnip = \"" + pSole + "\"\n"
	fx := newPartyFx(t, cfg)
	fx.counter.days = map[string]int{"2026-10-31": registry.DailyLimit - 1}
	code, out, errs := fx.run("party", "refresh", "--all")
	if code != 2 || !strings.Contains(errs, "90 White List calls") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "acme: refreshed") || !strings.Contains(out, "jan: not refreshed") || len(fx.wlHits) != 1 {
		t.Errorf("out:\n%s hits=%v", out, fx.wlHits)
	}
	// adding a party is essential and still works at the limit
	if code, _, errs := fx.run("party", "add", "other", "--nip", pSeller); code != 0 {
		t.Errorf("add at limit: exit %d %s", code, errs)
	}
}

func TestPartyRefresh_FailureKeepsOldEntry(t *testing.T) {
	cfg := partyBaseConfig + "\n[parties.acme]\nnip = \"" + pBuyer + "\"\n"
	fx := newPartyFx(t, cfg)
	if code, _, errs := fx.run("party", "refresh", "acme"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	fx.wlCode = 500
	if code, _, _ := fx.run("party", "refresh", "acme"); code != 2 {
		t.Errorf("failed refresh: exit %d", code)
	}
	code, out, _ := fx.run("party", "refresh", "--all")
	if code != 2 || !strings.Contains(out, "acme: FAILED") {
		t.Errorf("--all failure: exit %d out %s", code, out)
	}
	fx.wlCode = 200
	if code, out, _ := fx.run("party", "show", "acme"); code != 0 || !strings.Contains(out, "ACME") {
		t.Errorf("entry lost: %d %s", code, out)
	}
}

func TestParty_HelpHasShortLongExampleAndFlagUsage(t *testing.T) {
	deps := Deps{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
	root := partyRoot(deps)
	pc, _, err := root.Find([]string{"party"})
	if err != nil || pc == root {
		t.Fatalf("party command not found: %v", err)
	}
	var n int
	walk(pc, func(c *cobra.Command) {
		n++
		if strings.TrimSpace(c.Short) == "" || strings.TrimSpace(c.Long) == "" || strings.TrimSpace(c.Example) == "" {
			t.Errorf("%s: Short, Long and Example are all required", c.CommandPath())
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if strings.TrimSpace(f.Usage) == "" {
				t.Errorf("%s: flag --%s has no usage", c.CommandPath(), f.Name)
			}
		})
	})
	if n != 4 {
		t.Errorf("party tree has %d commands, want 4", n)
	}
}

func TestTomlString_RoundTrips(t *testing.T) {
	for _, s := range []string{`a "quoted" name`, `back\slash`, "tab\there", "line\nbreak", "zażółć gęślą jaźń"} {
		doc := appendPartyBlock([]byte("[seller]\nnip = \"1\""), "x", config.Party{NIP: pBuyer, Name: s})
		path := filepath.Join(t.TempDir(), "c.toml")
		if err := os.WriteFile(path, doc, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("%q: %v\n%s", s, err, doc)
		}
		if cfg.Parties["x"].Name != s {
			t.Errorf("round trip %q -> %q", s, cfg.Parties["x"].Name)
		}
	}
}

func TestPartyAdd_NotRegisteredWithNameAndAddress(t *testing.T) {
	fx := newPartyFx(t, partyBaseConfig)
	fx.wlFile[pBuyer] = "wl_subject_null.json"
	code, out, errs := fx.run("party", "add", "fund", "--nip", pBuyer, "--name", "Fundacja X", "--address", "ul. A 1, 00-001 Warszawa")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out+errs, "not on the VAT White List") || !strings.Contains(fx.config(), "Fundacja X") {
		t.Errorf("out %q err %q config:\n%s", out, errs, fx.config())
	}
}

func TestRenderParty_ContinuationLinesHaveNoColon(t *testing.T) {
	var b bytes.Buffer
	p := registry.Party{Alias: "acme", NIP: pBuyer, Name: "ACME", AccountNumbers: []string{"11111111111111111111111111", "22222222222222222222222222"},
		Provenance: map[string]string{"accounts": "whitelist"}, FetchedAt: pNow}
	renderParty(&b, p, pNow, 30)
	if strings.Contains(b.String(), "\n :") || strings.Contains(b.String(), "\n            :") {
		t.Errorf("stray colon:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "Accounts:    1111") || !strings.Contains(b.String(), "\n             2222") {
		t.Errorf("layout:\n%s", b.String())
	}
}

func TestPartyAdd_ConfigWithUTF8BOM(t *testing.T) {
	fx := newPartyFx(t, "\xef\xbb\xbf"+partyBaseConfig)
	code, _, errs := fx.run("party", "add", "acme", "--nip", pBuyer)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	cfg, err := config.Load(fx.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Parties["acme"].NIP != pBuyer {
		t.Errorf("parties = %+v", cfg.Parties)
	}
}
