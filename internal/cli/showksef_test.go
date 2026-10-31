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
