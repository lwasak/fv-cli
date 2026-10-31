package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fa3"
	"github.com/lwasak/fv-cli/internal/ksefcache"
)

func renderTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "fa3", "testdata", "parse", name))
	if err != nil {
		t.Fatal(err)
	}
	d, err := fa3.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	renderKSeFInvoice(&w, d, ksefcache.Entry{}, "5260250274-20260101-AAAAAAAAAAAA-AA", ksefNIP, config.EnvTest, true, previewOpts{})
	return w.String()
}

func TestRenderKSeFInvoice_Fixtures(t *testing.T) {
	for _, name := range []string{"foreign.xml", "kor.xml", "zal.xml", "zw.xml", "fa2.xml"} {
		out := renderTestdata(t, name)
		if !strings.HasPrefix(out, "ENVIRONMENT: TEST\n") || !strings.Contains(out, "KSeF no.:  5260250274-20260101-AAAAAAAAAAAA-AA") {
			t.Errorf("%s:\n%s", name, out)
		}
		if strings.Contains(out, "%!") {
			t.Errorf("%s has a format error:\n%s", name, out)
		}
	}
	if out := renderTestdata(t, "kor.xml"); !strings.Contains(out, "Corrects:") || !strings.Contains(out, "[before correction]") {
		t.Errorf("kor:\n%s", out)
	}
	if out := renderTestdata(t, "zal.xml"); !strings.Contains(out, "order lines (ZamowienieWiersz) not shown") {
		t.Errorf("zal:\n%s", out)
	}
	if out := renderTestdata(t, "zw.xml"); !strings.Contains(out, "Exempt:") {
		t.Errorf("zw:\n%s", out)
	}
}

func TestFmtDec(t *testing.T) {
	for in, want := range map[string]string{
		"500": "500,00", "12.5": "12,50", "0.12345678": "0,12345678", "1234567.10": "1 234 567,10",
		"-3": "-3,00", "": "-", "1.50000000": "1,50",
	} {
		if got := fmtDec(in, 2); got != want {
			t.Errorf("fmtDec(%q) = %q, want %q", in, got, want)
		}
	}
	if got := fmtDec("2.000", 0); got != "2" {
		t.Errorf("qty = %q", got)
	}
}

func TestKRate(t *testing.T) {
	for in, want := range map[string]string{"23": "23%", "np I": "np I", "0 KR": "0 KR", "zw": "zw", "": "-"} {
		if got := kRate(in); got != want {
			t.Errorf("kRate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderKSeFSummary_UnsupportedForm(t *testing.T) {
	var w bytes.Buffer
	renderKSeFSummary(&w, config.EnvTest, ksefcache.Entry{}, "N", "FA_RR (1)", previewOpts{})
	if !strings.Contains(w.String(), "readable view not supported for FA_RR (1); use --xml") {
		t.Errorf("%s", w.String())
	}
}

func TestRenderKSeFInvoice_RoleLabelIsCleanedAndCapitalisedByRune(t *testing.T) {
	d := &fa3.Document{Number: "X", Others: []fa3.DocParty{
		{Name: "N1", Role: "łącznik\nTotal:     0,00 PLN\u0085\x1b[31m"},
		{Name: "N2", Role: "\u202eevil"},
		{Name: "N3", Role: "factor"},
		{Name: "N4", Role: ""},
	}}
	var b bytes.Buffer
	renderKSeFInvoice(&b, d, ksefcache.Entry{}, "k", "", config.EnvTest, false, previewOpts{})
	out := b.String()
	if strings.ContainsAny(out, "\x1b\u0085\u202e\ufffd") {
		t.Errorf("unsafe or broken characters in %q", out)
	}
	for _, want := range []string{"Łącznik Total: 0,00 PLN [31m: ", "Evil:", "Factor:", "Other:"} {
		if !strings.Contains(strings.Join(strings.Fields(out), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// the injected text stays on one line with the label: no line of its own starting with "Total:"
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Total:     0,00") {
			t.Errorf("forged line %q", l)
		}
	}
}

func TestRenderKSeFInvoice_FactorAccountMarginAndRates(t *testing.T) {
	d, err := fa3.Parse([]byte(`<Faktura xmlns="` + fa3.Namespace + `"><Fa><P_2>X/1</P_2><P_15>123.00</P_15>` +
		`<P_13_1>100.00</P_13_1><P_14_1>23.00</P_14_1><P_13_11>50.00</P_13_11>` +
		`<FaWiersz><NrWierszaFa>1</NrWierszaFa><P_11>100.00</P_11><P_12>23</P_12></FaWiersz>` +
		`<Platnosc><RachunekBankowy><NrRB>61109010140000071219812874</NrRB></RachunekBankowy>` +
		`<RachunekBankowyFaktora><NrRB>27114020040000300201355387</NrRB></RachunekBankowyFaktora></Platnosc></Fa></Faktura>`))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	renderKSeFInvoice(&b, d, ksefcache.Entry{}, "k", "", config.EnvTest, false, previewOpts{})
	out := b.String()
	if !strings.Contains(out, "PL27 1140 2004 0000 3002 0135 5387  (FACTOR account") {
		t.Errorf("factor account missing or unlabelled:\n%s", out)
	}
	if strings.Contains(out, "23% / 22%") || !strings.Contains(out, "23%  100,00  23,00  123,00") {
		t.Errorf("rate label:\n%s", out)
	}
	if !strings.Contains(out, "margin       -        -   50,00") && !strings.Contains(strings.Join(strings.Fields(out), " "), "margin - - 50,00") {
		t.Errorf("margin row:\n%s", out)
	}
}

func renderGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "fa3", "testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	d, err := fa3.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	renderKSeFInvoice(&w, d, ksefcache.Entry{}, "5260250274-20260101-AAAAAAAAAAAA-AA", ksefNIP, config.EnvTest, true, previewOpts{})
	return w.String()
}

func TestRenderKSeFInvoice_IndexColumnOnlyWhenPresent(t *testing.T) {
	out := renderGolden(t, "multi.xml")
	for _, want := range []string{"Index", "DEV-01", "BOOK-7", "mBank S.A.", "SWIFT BREXPLPW"} {
		if !strings.Contains(out, want) {
			t.Errorf("multi lacks %q:\n%s", want, out)
		}
	}
	if out := renderGolden(t, "rate23.xml"); strings.Contains(out, "Index") {
		t.Errorf("no index in the document, no column:\n%s", out)
	}
}
