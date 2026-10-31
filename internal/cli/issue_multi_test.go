package cli

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
)

const multiConfig = issueConfig + `
[items.dev]
name = "Programming {month_name} {year}"
unit = "h"
unit_price = "120,00"
quantity = "10"
index = "DEV-01"

[items.support]
name = "Support {month_name_en} {year}"
unit_price = "50"
vat_rate = "8"
pkwiu = "62.01.12.0"

[items.book]
name = "Handbook"
unit_price = "25,00"
vat_rate = "5"
gtin = "5901234123457"

[items.eurdev]
name = "Consulting"
unit_price = "100"
currency = "EUR"

[parties.monthly]
nip = "5252248481"
items = ["dev", "support"]

[parties.exempt]
nip = "5252248481"
items = ["dev"]
vat_rate = "zw"
`

func (fx *issueFx) mustIssue(args ...string) string {
	fx.t.Helper()
	code, out, errs := fx.run(append([]string{"issue"}, args...)...)
	if code != 0 {
		fx.t.Fatalf("issue %v: exit %d: %s", args, code, errs)
	}
	return out
}

func (fx *issueFx) mustFail(wantErr string, args ...string) {
	fx.t.Helper()
	code, _, errs := fx.run(append([]string{"issue"}, args...)...)
	if code != 1 || !strings.Contains(errs, wantErr) {
		fx.t.Errorf("issue %v: exit %d, stderr %q; want exit 1 and %q", args, code, errs, wantErr)
	}
	if _, out, _ := fx.run("list"); strings.TrimSpace(out) != "no invoices" {
		fx.t.Errorf("issue %v: a number was reserved: %q", args, out)
	}
}

func TestIssueMulti_ItemsAndLinesInOrder(t *testing.T) {
	fx := newIssueFx(t, multiConfig)
	out := fx.mustIssue("--to", "acme", "--item", "support=3", "--line", "desc=Hosting {month_name_en} {year}; price=19,99; unit=mies.; index=H-1", "--item", "dev=1,5")
	// command-line order: support, hosting, dev
	iSup, iHost, iDev := strings.Index(out, "Support October 2026"), strings.Index(out, "Hosting October 2026"), strings.Index(out, "Programming październik 2026")
	if iSup < 0 || iHost < iSup || iDev < iHost {
		t.Errorf("lines out of order (%d %d %d):\n%s", iSup, iHost, iDev, out)
	}
	for _, want := range []string{"Index", "DEV-01", "H-1", "150,00", "180,00", "19,99", "8%", "23%"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
	m, err := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Lines) != 3 || m.Lines[0].Item != "support" || m.Lines[0].PKWiU != "62.01.12.0" || m.Lines[1].Item != "" ||
		m.Lines[1].Index != "H-1" || m.Lines[2].Item != "dev" || m.Lines[2].Index != "DEV-01" {
		t.Errorf("snapshot lines: %+v", m.Lines)
	}
	if len(m.Totals) != 2 {
		t.Errorf("totals: %+v", m.Totals)
	}
	x := string(fx.xmlOf(config.EnvTest, m.Number))
	for _, want := range []string{"<Indeks>DEV-01</Indeks>", "<PKWiU>62.01.12.0</PKWiU>", "<NrWierszaFa>3</NrWierszaFa>"} {
		if !strings.Contains(x, want) {
			t.Errorf("xml lacks %s", want)
		}
	}
	// group net: 1,5 x 120 + 19,99 = 199,99 at 23%; 3 x 50 = 150 at 8%
	if !strings.Contains(x, "<P_13_1>199.99</P_13_1>") || !strings.Contains(x, "<P_13_2>150.00</P_13_2>") {
		t.Errorf("group nets:\n%s", x)
	}
}

func TestIssueMulti_ModesAreExclusive(t *testing.T) {
	fx := newIssueFx(t, multiConfig)
	const want = "use --net/--qty/--unit-price/--desc for a single line, or --item/--line for several lines, not both"
	fx.mustFail(want, "--to", "acme", "--net", "100", "--item", "dev")
	fx.mustFail(want, "--to", "acme", "--qty", "1", "--unit-price", "5", "--line", "desc=x; net=1")
	fx.mustFail(want, "--to", "acme", "--desc", "x", "--item", "dev")
}

func TestIssueMulti_PartyItemsAndFlagsReplaceThem(t *testing.T) {
	fx := newIssueFx(t, multiConfig)
	out := fx.mustIssue("--to", "monthly")
	for _, want := range []string{"Programming październik 2026", "Support October 2026", "1200,00", "50,00", "Total:     1530,00 PLN"} {
		if !strings.Contains(out, want) {
			t.Errorf("party items: preview lacks %q:\n%s", want, out)
		}
	}
	// 10 x 120 = 1200 at 23% -> VAT 276; 50 at 8% -> VAT 4; gross 1530
	out = fx.mustIssue("--to", "monthly", "--net", "100")
	if strings.Contains(out, "Support") || strings.Contains(out, "Programming") {
		t.Errorf("flags must replace the party items:\n%s", out)
	}
	out = fx.mustIssue("--to", "monthly", "--item", "book")
	if strings.Contains(out, "Programming") || !strings.Contains(out, "Handbook") {
		t.Errorf("--item must replace the party items:\n%s", out)
	}
}

func TestIssueMulti_ItemRateBeatsRateFlag(t *testing.T) {
	fx := newIssueFx(t, multiConfig)
	out := fx.mustIssue("--to", "acme", "--item", "support", "--item", "dev", "--rate", "5")
	m, err := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if err != nil {
		t.Fatal(err)
	}
	rates := map[string]string{}
	for _, l := range m.Lines {
		rates[l.Item] = l.Rate
	}
	if rates["support"] != "8" || rates["dev"] != "5" {
		t.Errorf("rates = %v (item vat_rate wins over --rate; --rate fills the rest)\n%s", rates, out)
	}
}

func TestIssueMulti_LineRateAndNoRate(t *testing.T) {
	cfg := strings.Replace(multiConfig, "vat_rate = \"23\"\nbank_account", "bank_account", 1)
	fx := newIssueFx(t, cfg)
	fx.mustFail(`line 2 (--line "desc=Hosting; price=1"): no VAT rate: add rate=…, pass --rate, or set vat_rate`,
		"--to", "acme", "--line", "desc=Cons; price=1; rate=23", "--line", "desc=Hosting; price=1")
	fx.mustFail("no VAT rate", "--to", "acme", "--net", "100")
	fx.mustFail("not supported in v1", "--to", "acme", "--line", "desc=X; price=1; rate=np")
	fx2 := newIssueFx(t, cfg)
	fx2.mustIssue("--to", "acme", "--line", "desc=Cons; price=1; rate=23", "--item", "support")
}

func TestIssueMulti_ZwLineNeedsTheInvoiceBasis(t *testing.T) {
	fx := newIssueFx(t, multiConfig)
	fx.mustFail("exemption_basis", "--to", "acme", "--item", "dev", "--line", "desc=Training; net=100; rate=zw")
	cfg := multiConfig + "\n[parties.ok]\nnip = \"5252248481\"\nexemption_basis = \"art. 43 ust. 1 pkt 37 ustawy o VAT\"\n"
	fx2 := newIssueFx(t, cfg)
	fx2.mustIssue("--to", "ok", "--item", "dev", "--line", "desc=Training; net=100; rate=zw")
	x := string(fx2.xmlOf(config.EnvTest, "FV/2026/10/001"))
	if !strings.Contains(x, "<P_19A>art. 43 ust. 1 pkt 37 ustawy o VAT</P_19A>") || !strings.Contains(x, "<P_13_7>100.00</P_13_7>") {
		t.Errorf("zw invoice:\n%s", x)
	}
	// a party whose default rate is zw gets the check through its items too
	fx3 := newIssueFx(t, multiConfig)
	fx3.mustFail("exemption_basis", "--to", "exempt")
}

func TestIssueMulti_ItemErrors(t *testing.T) {
	fx := newIssueFx(t, multiConfig)
	fx.mustFail(`--item "x": unknown item "x"; defined: book, dev, eurdev, support`, "--to", "acme", "--item", "x")
	fx.mustFail(`--item "dev=": the quantity after "=" is empty`, "--to", "acme", "--item", "dev=")
	fx.mustFail(`item "eurdev" is priced in EUR, but this invoice is in PLN; use a --line or an item in PLN`, "--to", "acme", "--item", "eurdev")
	fx.mustFail(`--line "desc=X": give price or net`, "--to", "acme", "--line", "desc=X")
	fx.mustFail("--desc describes a single line", "--to", "monthly", "--desc", "x")
	fx.mustFail("give a net amount, or quantity with unit price, or --item/--line, or set items in [parties.acme]", "--to", "acme")
	fx.mustFail("quantity and unit price must be given together", "--to", "acme", "--unit-price", "5")
	if len(fx.wlHits) != 0 {
		t.Errorf("registry was called before the inputs were checked: %v", fx.wlHits)
	}
}

func TestIssueMulti_ItemMatchingCurrencyEUR(t *testing.T) {
	fx := newIssueFx(t, multiConfig)
	nbpFake := &fakeNBP{byDay: map[string]string{"2026-10-30": "eur_200.json"}}
	nbpFake.attach(t, fx)
	out := fx.mustIssue("--to", "acme", "--currency", "EUR", "--item", "eurdev=2", "--line", "desc=Printed manual; price=20; qty=3; rate=8")
	m, err := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if err != nil {
		t.Fatal(err)
	}
	// 2 x 100 at 23% -> VAT 46,00; 3 x 20 at 8% -> VAT 4,80; both converted at 4.2512
	var plns []string
	for _, tot := range m.Totals {
		if tot.VATPLN == nil {
			t.Errorf("rate %s has no VAT in PLN", tot.Rate)
			continue
		}
		plns = append(plns, tot.Rate)
	}
	if len(plns) != 2 {
		t.Fatalf("totals: %+v\n%s", m.Totals, out)
	}
	x := string(fx.xmlOf(config.EnvTest, m.Number))
	if !strings.Contains(x, "<P_14_1W>195.56</P_14_1W>") || !strings.Contains(x, "<P_14_2W>20.41</P_14_2W>") {
		t.Errorf("VAT in PLN per group:\n%s", x)
	}
	if strings.Count(x, "<KursWaluty>4.2512</KursWaluty>") != 2 {
		t.Errorf("KursWaluty must be on both lines:\n%s", x)
	}
}

func TestIssueMulti_ValidatedAgainstSchema(t *testing.T) {
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Skip("xmllint not on PATH")
	}
	fx := newIssueFx(t, multiConfig)
	fx.mustIssue("--to", "acme", "--validate", "--item", "dev", "--item", "support", "--item", "book=4",
		"--line", "desc=Hosting; price=19,99; qty=2,5; pkwiu=62.01; gtin=96385074")
}

func TestIssueMulti_ShowPrintsTheIndexColumn(t *testing.T) {
	fx := newIssueFx(t, multiConfig)
	fx.mustIssue("--to", "acme", "--item", "dev")
	code, out, errs := fx.run("show", "FV/2026/10/001")
	if code != 0 || !strings.Contains(out, "Index") || !strings.Contains(out, "DEV-01") {
		t.Errorf("show: %d %s\n%s", code, errs, out)
	}
	// without any index the column is absent
	fx2 := newIssueFx(t, multiConfig)
	out2 := fx2.mustIssue("--to", "acme", "--net", "100")
	if strings.Contains(out2, "Index") {
		t.Errorf("no index, no column:\n%s", out2)
	}
}

const bankConfig = `[ksef]
environment = "test"

[seller]
nip = "5260250274"
place_of_issue = "Gdynia"

[defaults]
vat_rate = "23"
bank_account = "PL61 1090 1014 0000 0712 1981 2874"
bank_name = "Default Bank S.A."
swift = "brex plpw"

[parties.acme]
nip = "5252248481"

[parties.own]
nip = "5252248481"
bank_account = "PL27 1140 2004 0000 3002 0135 5387"

[parties.ownnamed]
nip = "5252248481"
bank_account = "PL27 1140 2004 0000 3002 0135 5387"
bank_name = "Own Bank"
swift = "BREXDEFF"
`

func TestIssueBank_NameAndSwiftEndUpEverywhere(t *testing.T) {
	fx := newIssueFx(t, bankConfig)
	out := fx.mustIssue("--to", "acme", "--net", "100")
	if !strings.Contains(out, "Bank:      Default Bank S.A., SWIFT BREXPLPW") {
		t.Errorf("preview:\n%s", out)
	}
	if strings.Contains(out, "warning: the SWIFT code") {
		t.Errorf("matching countries must not warn:\n%s", out)
	}
	x := string(fx.xmlOf(config.EnvTest, "FV/2026/10/001"))
	if !strings.Contains(x, "<SWIFT>BREXPLPW</SWIFT>\n        <NazwaBanku>Default Bank S.A.</NazwaBanku>") {
		t.Errorf("xml:\n%s", x)
	}
	m, _ := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if m.BankName != "Default Bank S.A." || m.BankSWIFT != "BREXPLPW" {
		t.Errorf("meta: %+v", m)
	}
	if _, show, _ := fx.run("show", "FV/2026/10/001"); !strings.Contains(show, "Bank:      Default Bank S.A., SWIFT BREXPLPW") {
		t.Errorf("show:\n%s", show)
	}
}

// A party's own account must not pick up the defaults' bank name or SWIFT.
func TestIssueBank_FollowsTheAccountsLayer(t *testing.T) {
	fx := newIssueFx(t, bankConfig)
	out := fx.mustIssue("--to", "own", "--net", "100")
	if strings.Contains(out, "Bank:") || strings.Contains(out, "Default Bank") {
		t.Errorf("the party's account took the defaults' bank:\n%s", out)
	}
	x := string(fx.xmlOf(config.EnvTest, "FV/2026/10/001"))
	if strings.Contains(x, "<SWIFT>") || strings.Contains(x, "<NazwaBanku>") {
		t.Errorf("xml:\n%s", x)
	}
}

func TestIssueBank_SwiftCountryWarning(t *testing.T) {
	fx := newIssueFx(t, bankConfig)
	out := fx.mustIssue("--to", "ownnamed", "--net", "100")
	if !strings.Contains(out, "Bank:      Own Bank, SWIFT BREXDEFF") ||
		!strings.Contains(out, "warning: the SWIFT code BREXDEFF is for country DE, but the account PL27114020040000300201355387 is from PL") {
		t.Errorf("preview:\n%s", out)
	}
	m, _ := fx.store().Load(config.EnvTest, "FV/2026/10/001")
	if len(m.Warnings) == 0 {
		t.Error("the warning must be stored")
	}
}

func TestIssueBank_InvalidConfigIsRefused(t *testing.T) {
	cfg := strings.Replace(bankConfig, `swift = "brex plpw"`, `swift = "BREXPLPW9"`, 1)
	fx := newIssueFx(t, cfg)
	fx.mustFail("defaults.swift", "--to", "acme", "--net", "100")
}
