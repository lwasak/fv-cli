package money

import (
	"strings"
	"testing"
)

func TestParseXML_Accepted(t *testing.T) {
	cases := []struct {
		in   string
		want Amount
	}{
		{"123.45", 12345},
		{"123.4", 12340},
		{"123", 12300},
		{"0", 0},
		{"0.00", 0},
		{"-0.00", 0},
		{"-12.30", -1230},
		{"-12", -1200},
		{"-0.01", -1},
		{"007.5", 750},
		{"1230.0", 123000},
		{"999999999999.99", 99999999999999},
		{"-999999999999.99", -99999999999999},
	}
	for _, c := range cases {
		got, err := ParseXML(c.in)
		if err != nil {
			t.Errorf("ParseXML(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseXML(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseXML_Rejected(t *testing.T) {
	cases := []struct{ in, reason string }{
		{"", "empty"},
		{"1e3", "invalid character"},
		{"1E3", "invalid character"},
		{"1.5e+06", "invalid character"},
		{"1.234", "too many decimals"},
		{"1,5", "invalid character"},
		{"+5", "invalid character"},
		{"--5", "invalid character"},
		{"-", "missing whole part"},
		{".5", "missing whole part"},
		{"5.", "missing decimals"},
		{"1.2.3", "more than one"},
		{" 5", "invalid character"},
		{"5 ", "invalid character"},
		{"1_000", "invalid character"},
		{"1000000000000", "too large"},
		{"-1000000000000.00", "too large"},
	}
	for _, c := range cases {
		_, err := ParseXML(c.in)
		if err == nil {
			t.Errorf("ParseXML(%q) accepted", c.in)
			continue
		}
		if !strings.Contains(err.Error(), c.reason) {
			t.Errorf("ParseXML(%q) error %q lacks %q", c.in, err, c.reason)
		}
	}
}

func TestParseXML_RoundTripsFormatXML(t *testing.T) {
	for _, a := range []Amount{0, 1, -1, 12345, -12345, 99999999999999, -99999999999999} {
		got, err := ParseXML(FormatXML(a))
		if err != nil || got != a {
			t.Errorf("round trip %d: got %d, err %v", a, got, err)
		}
	}
}
