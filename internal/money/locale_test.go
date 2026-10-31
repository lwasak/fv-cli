package money

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const localeHelperEnv = "FV_MONEY_LOCALE_HELPER"

var localeInputs = []string{"18500", "18500.43", "18500,43", "18_500,43", "12_000_000,01", "18.500,43", "5,123"}

func localeReport() string {
	var b strings.Builder
	for _, in := range localeInputs {
		a, err := ParseAmount(in)
		if err != nil {
			fmt.Fprintf(&b, "%s => error %v\n", in, err)
			continue
		}
		fmt.Fprintf(&b, "%s => %d\n", in, a)
	}
	return b.String()
}

func TestLocaleHelperProcess(t *testing.T) {
	if os.Getenv(localeHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	fmt.Print("REPORT-BEGIN\n" + localeReport() + "REPORT-END\n")
}

func TestParseAmount_SameUnderLocales(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test skipped under -short")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := localeReport()
	if !strings.Contains(want, "18500,43 => 1850043") {
		t.Fatalf("unexpected baseline report:\n%s", want)
	}
	for _, loc := range []string{"C", "en_US.UTF-8", "pl_PL.UTF-8", "de_DE.UTF-8", "fr_FR.UTF-8"} {
		cmd := exec.Command(exe, "-test.run=^TestLocaleHelperProcess$")
		cmd.Env = append(os.Environ(), localeHelperEnv+"=1", "LANG="+loc, "LC_ALL="+loc, "LC_NUMERIC="+loc)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", loc, err, out)
		}
		s := string(out)
		i := strings.Index(s, "REPORT-BEGIN\n")
		j := strings.Index(s, "REPORT-END\n")
		if i < 0 || j < i {
			t.Fatalf("%s: no report in output:\n%s", loc, s)
		}
		got := s[i+len("REPORT-BEGIN\n") : j]
		if got != want {
			t.Errorf("%s: report differs\n got: %s\nwant: %s", loc, got, want)
		}
	}
}
