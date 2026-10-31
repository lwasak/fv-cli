package money

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRate_Mapping(t *testing.T) {
	cases := []struct {
		code    string
		percent int
		net     string
		vat     string
	}{
		{"23", 23, "P_13_1", "P_14_1"},
		{"8", 8, "P_13_2", "P_14_2"},
		{"5", 5, "P_13_3", "P_14_3"},
		{"zw", 0, "P_13_7", ""},
	}
	for _, c := range cases {
		r, err := ParseRate(c.code)
		if err != nil {
			t.Fatalf("ParseRate(%q): %v", c.code, err)
		}
		if r.Code != c.code || r.Percent != c.percent || r.NetField != c.net || r.VATField != c.vat {
			t.Errorf("ParseRate(%q) = %+v", c.code, r)
		}
	}
}

func TestParseRate_ZeroAndNPNotSupported(t *testing.T) {
	for _, code := range []string{"0", "np"} {
		_, err := ParseRate(code)
		if !errors.Is(err, ErrRateNotSupported) {
			t.Fatalf("ParseRate(%q) err = %v", code, err)
		}
		if !strings.Contains(err.Error(), "not supported yet") {
			t.Errorf("message %q", err)
		}
	}
}

func TestParseRate_Unknown(t *testing.T) {
	for _, code := range []string{"7", "23%", "ZW", ""} {
		_, err := ParseRate(code)
		if err == nil || errors.Is(err, ErrRateNotSupported) {
			t.Fatalf("ParseRate(%q) err = %v", code, err)
		}
		if !strings.Contains(err.Error(), "unknown rate code") {
			t.Errorf("message %q", err)
		}
	}
}

func TestRates_Order(t *testing.T) {
	var got []string
	for _, r := range Rates() {
		got = append(got, r.Code)
	}
	if strings.Join(got, ",") != "23,8,5,zw" {
		t.Errorf("Rates() = %v", got)
	}
}
