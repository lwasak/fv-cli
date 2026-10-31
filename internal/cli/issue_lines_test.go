package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/lwasak/fv-cli/internal/money"
)

func TestLineArgs_KeepOrderAndCommas(t *testing.T) {
	var got []lineArg
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	fs.Var(&lineArgs{dst: &got, kind: kindItem}, "item", "u")
	fs.Var(&lineArgs{dst: &got, kind: kindSpec}, "line", "u")
	err := fs.Parse([]string{"--item", "dev=1,5", "--line", "desc=A; price=1,5", "--item", "support", "--line=desc=B; net=2"})
	if err != nil {
		t.Fatal(err)
	}
	want := []lineArg{
		{kindItem, "dev=1,5"}, {kindSpec, "desc=A; price=1,5"}, {kindItem, "support"}, {kindSpec, "desc=B; net=2"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %v, want %v", i, got[i], want[i])
		}
	}
	if (&lineArgs{kind: kindItem}).Type() != "item" || (&lineArgs{kind: kindSpec}).Type() != "spec" {
		t.Error("Type()")
	}
}

func TestParseItemArg(t *testing.T) {
	q := func(s string) *money.Quantity {
		n, err := money.ParseQuantity(s)
		if err != nil {
			t.Fatal(err)
		}
		return &n
	}
	tests := []struct {
		raw     string
		key     string
		qty     *money.Quantity
		wantErr string
	}{
		{"dev", "dev", nil, ""},
		{"dev=160", "dev", q("160"), ""},
		{"dev=1,5", "dev", q("1,5"), ""},
		{"dev=1.5", "dev", q("1.5"), ""},
		{" dev = 2 ", "dev", q("2"), ""},
		{"dev=", "", nil, `--item "dev=": the quantity after "=" is empty`},
		{"=5", "", nil, "the item key is empty"},
		{"", "", nil, "the item key is empty"},
		{"dev=abc", "", nil, `--item "dev=abc": invalid quantity "abc"`},
		{"dev=0", "", nil, "invalid quantity"},
		{"dev=-1", "", nil, "invalid quantity"},
	}
	for _, tc := range tests {
		key, qty, err := parseItemArg(tc.raw)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: err = %v, want %q", tc.raw, err, tc.wantErr)
			}
			continue
		}
		if err != nil || key != tc.key || (qty == nil) != (tc.qty == nil) || (qty != nil && *qty != *tc.qty) {
			t.Errorf("%q = %q %v %v", tc.raw, key, qty, err)
		}
	}
}

func TestParseLineArg_OK(t *testing.T) {
	f, err := parseLineArg(`desc=Hosting {month_name_en} {year}; price=19,99; qty=2,5; unit=mies.; rate=8; index=H-1; pkwiu=62.01; gtin=5901234123457`)
	if err != nil {
		t.Fatal(err)
	}
	if f.Desc != "Hosting {month_name_en} {year}" || f.Unit != "mies." || f.Rate != "8" || f.Index != "H-1" || f.PKWiU != "62.01" || f.GTIN != "5901234123457" {
		t.Errorf("fields = %+v", f)
	}
	if f.Price == nil || *f.Price != 1999 || f.Qty == nil || *f.Qty != 2500000 || f.Net != nil {
		t.Errorf("amounts = %+v", f)
	}
	// spaces around fields and '=' are trimmed; a trailing ';' is tolerated; '=' inside a value is kept.
	f, err = parseLineArg(`  desc = a=b ;  net = 10 ; `)
	if err != nil || f.Desc != "a=b" || f.Net == nil || *f.Net != 1000 {
		t.Errorf("trim: %+v %v", f, err)
	}
}

func TestParseLineArg_Errors(t *testing.T) {
	tests := []struct {
		raw, want string
	}{
		{`desc=X; prce=1`, `--line "desc=X; prce=1": unknown field "prce"; use desc, qty, unit, price, net, rate, index, pkwiu, gtin`},
		{`price=1`, `field "desc" is missing`},
		{`desc=X`, `give price or net`},
		{`desc=X; price=1; net=1`, `give price or net, not both`},
		{`desc=X; net=1; qty=2`, `"qty" cannot be used with "net"`},
		{`desc=X; price=1; qty=1; qty=2`, `field "qty" appears twice`},
		{`desc=X; Price=1`, `unknown field "Price"`},
		{`desc=Hosting; 19,99`, `"19,99" is not field=value`},
		{`desc=Hosting; extra words; price=1`, `a ";" cannot be part of a value`},
		{`desc=X; price=abc`, `field "price": invalid amount "abc"`},
		{`desc=X; price=1; qty=abc`, `field "qty": invalid quantity "abc"`},
		{`desc=X; price=`, `field "price" is empty`},
		{`desc={nope}; price=1`, `unknown token {nope}`},
		{`desc=X; price=1; gtin=123`, `field "gtin"`},
		{`desc=X; price=1; unit=`, `field "unit" is empty`},
		{`desc=X; price=0`, `field "price"`},
		{``, `field "desc" is missing`},
	}
	for _, tc := range tests {
		_, err := parseLineArg(tc.raw)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.raw, err, tc.want)
		}
	}
	// the money error is wrapped, not flattened
	_, err := parseLineArg(`desc=X; price=abc`)
	var pe *money.ParseError
	if !errors.As(err, &pe) {
		t.Errorf("amount error is not wrapped: %v", err)
	}
}

func TestFlagText(t *testing.T) {
	if got := flagText("--item", "dev=1"); got != `--item "dev=1"` {
		t.Errorf("got %s", got)
	}
	long := strings.Repeat("x", 100)
	if got := flagText("--line", long); !strings.HasSuffix(got, `..."`) || len(got) > 80 {
		t.Errorf("long: %s", got)
	}
}
