package money

import (
	"math"
	"testing"
)

func mustRate(t *testing.T, code string) Rate {
	t.Helper()
	r, err := ParseRate(code)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func summarizeOne(t *testing.T, code string, net Amount) Group {
	t.Helper()
	s, err := Summarize([]Line{{Rate: mustRate(t, code), Net: net}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Groups) != 1 {
		t.Fatalf("groups = %d", len(s.Groups))
	}
	return s.Groups[0]
}

func TestVAT_SmallNets(t *testing.T) {
	want := map[string][]Amount{
		"23": {0, 0, 1},
		"8":  {0, 0, 0},
		"5":  {0, 0, 0},
		"zw": {0, 0, 0},
	}
	nets := []Amount{1, 2, 5}
	for code, vats := range want {
		for i, net := range nets {
			g := summarizeOne(t, code, net)
			if g.VAT != vats[i] || g.Gross != net+vats[i] {
				t.Errorf("%s net %d: vat %d gross %d, want vat %d", code, net, g.VAT, g.Gross, vats[i])
			}
		}
	}
}

func TestVAT_HalfUp(t *testing.T) {
	cases := []struct {
		code string
		net  Amount
		vat  Amount
	}{
		{"23", 50, 12},
		{"5", 10, 1},
		{"5", 30, 2},
		{"23", 2, 0},
	}
	for _, c := range cases {
		if g := summarizeOne(t, c.code, c.net); g.VAT != c.vat {
			t.Errorf("%s net %d: vat %d, want %d", c.code, c.net, g.VAT, c.vat)
		}
	}
}

func TestSummarize_RoundsOnGroupSum(t *testing.T) {
	r := mustRate(t, "23")
	s, err := Summarize([]Line{{r, 2}, {r, 2}, {r, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Groups[0].Net != 6 || s.Groups[0].VAT != 1 {
		t.Errorf("group = %+v", s.Groups[0])
	}
}

func TestSummarize_Large(t *testing.T) {
	const n = Amount(1_000_000_000_000)
	var lines []Line
	for _, r := range Rates() {
		lines = append(lines, Line{r, n})
	}
	s, err := Summarize(lines)
	if err != nil {
		t.Fatal(err)
	}
	wantVAT := []Amount{230_000_000_000, 80_000_000_000, 50_000_000_000, 0}
	var total Amount
	for i, g := range s.Groups {
		if g.VAT != wantVAT[i] {
			t.Errorf("group %s vat %d, want %d", g.Rate.Code, g.VAT, wantVAT[i])
		}
		total += n + wantVAT[i]
	}
	if s.Gross != total {
		t.Errorf("gross %d, want %d", s.Gross, total)
	}
}

func TestSummarize_OverflowIsError(t *testing.T) {
	r := mustRate(t, "23")
	if _, err := Summarize([]Line{{r, Amount(math.MaxInt64)}}); err == nil {
		t.Error("vat overflow: want error")
	}
	z := mustRate(t, "zw")
	if _, err := Summarize([]Line{{z, Amount(math.MaxInt64)}, {z, 1}}); err == nil {
		t.Error("sum overflow: want error")
	}
	if _, err := Summarize([]Line{{z, Amount(math.MaxInt64)}, {r, Amount(math.MaxInt64 / 200)}}); err == nil {
		t.Error("gross overflow: want error")
	}
}

func TestSummarize_GroupOrderAndP15(t *testing.T) {
	lines := []Line{
		{mustRate(t, "zw"), 100},
		{mustRate(t, "5"), 100},
		{mustRate(t, "23"), 100},
		{mustRate(t, "5"), 100},
	}
	s, err := Summarize(lines)
	if err != nil {
		t.Fatal(err)
	}
	var codes string
	for _, g := range s.Groups {
		codes += g.Rate.Code + " "
	}
	if codes != "23 5 zw " {
		t.Errorf("order = %q", codes)
	}
	// 23: 100+23, 5: 200+10, zw: 100
	if s.Gross != 123+210+100 {
		t.Errorf("gross = %d", s.Gross)
	}
}

func TestSummarize_RejectsUnknownRateCode(t *testing.T) {
	_, err := Summarize([]Line{{Rate: mustRate(t, "23"), Net: 100}, {Rate: Rate{Code: "X"}, Net: 100}})
	if err == nil || err.Error() != `unknown rate code "X"` {
		t.Fatalf("err = %v", err)
	}
}

func TestSummarize_RejectsNonPositiveNet(t *testing.T) {
	for _, net := range []Amount{0, -1, -100} {
		_, err := Summarize([]Line{{Rate: mustRate(t, "23"), Net: net}})
		if err == nil || err.Error() != "net amount must be greater than zero" {
			t.Errorf("net %d: err = %v", net, err)
		}
	}
}
