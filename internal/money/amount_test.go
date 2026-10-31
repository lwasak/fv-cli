package money

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAmount_Accepted(t *testing.T) {
	cases := []struct {
		in   string
		want Amount
	}{
		{"18500.43", 1850043},
		{"18500,43", 1850043},
		{"18_500,43", 1850043},
		{"12_000_000,01", 1200000001},
		{"18500", 1850000},
		{"1_85_00", 1850000},
		{"0,5", 50},
		{"0.01", 1},
		{"007", 700},
		{"999_999_999_999,99", 99999999999999},
	}
	for _, c := range cases {
		got, err := ParseAmount(c.in)
		if err != nil {
			t.Errorf("ParseAmount(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseAmount(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	if MaxAmount != 99999999999999 {
		t.Errorf("MaxAmount = %d", MaxAmount)
	}
}

func TestParseAmount_Rejected(t *testing.T) {
	cases := []struct {
		in     string
		reason string
	}{
		{"18.500,43", "mixed decimal separators"},
		{"18,500.43", "mixed decimal separators"},
		{"1.2.3", "more than one decimal separator"},
		{"18 500", "space"},
		{" 18500", "space"},
		{"+5", "sign"},
		{"-5", "sign"},
		{"-0,01", "sign"},
		{"5zł", "currency symbol"},
		{"PLN5", "currency symbol"},
		{"$5", "currency symbol"},
		{"€5", "currency symbol"},
		{"5,123", "too many decimals"},
		{"0", "zero"},
		{"0,00", "zero"},
		{"0_0", "zero"},
		{"_18", "underscore"},
		{"18_", "underscore"},
		{"1__8", "underscore"},
		{"18_,5", "underscore"},
		{"18,_5", "underscore"},
		{"18,5_0", "underscore"},
		{"18.", "missing decimals"},
		{".5", "missing whole part"},
		{",5", "missing whole part"},
		{"", "empty"},
		{"1e3", "invalid character"},
		{"1000000000000", "too large"},
		{"١٨", "non-ASCII"},
	}
	for _, c := range cases {
		_, err := ParseAmount(c.in)
		if err == nil {
			t.Errorf("ParseAmount(%q) accepted, want rejection", c.in)
			continue
		}
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("ParseAmount(%q) error type %T, want *ParseError", c.in, err)
			continue
		}
		if !strings.Contains(pe.Reason, c.reason) {
			t.Errorf("ParseAmount(%q) reason %q, want it to contain %q", c.in, pe.Reason, c.reason)
		}
		for _, f := range []string{"18500", "18500.43", "18500,43", "18_500,43"} {
			if !strings.Contains(err.Error(), f) {
				t.Errorf("ParseAmount(%q) message %q lacks accepted format %q", c.in, err.Error(), f)
			}
		}
	}
}

func TestParseAmount_LeadingZerosIgnoredForCap(t *testing.T) {
	got, err := ParseAmount("0000000000001")
	if err != nil || got != 100 {
		t.Fatalf("got %d, %v", got, err)
	}
	if _, err := ParseAmount("1000000000000"); err == nil {
		t.Fatal("13 significant digits accepted")
	}
	if got, err := ParseAmount("0999999999999,99"); err != nil || got != MaxAmount {
		t.Fatalf("got %d, %v", got, err)
	}
}
