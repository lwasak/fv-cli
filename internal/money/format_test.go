package money

import "testing"

func TestFormatPL(t *testing.T) {
	tests := []struct {
		in   Amount
		want string
	}{
		{1850000, "18 500,00"},
		{1, "0,01"},
		{150000, "1500,00"},
		{120000000001, "1 200 000 000,01"},
		{1000000000000, "10 000 000 000,00"},
	}
	for _, tc := range tests {
		if got := FormatPL(tc.in); got != tc.want {
			t.Errorf("FormatPL(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatXML(t *testing.T) {
	tests := []struct {
		in   Amount
		want string
	}{
		{1850000, "18500.00"},
		{1, "0.01"},
		{1200000001, "12000000.01"},
	}
	for _, tc := range tests {
		if got := FormatXML(tc.in); got != tc.want {
			t.Errorf("FormatXML(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormat_RoundTrip(t *testing.T) {
	for _, a := range []Amount{1, 99, 100, 150000, 1850043, 1200000001, MaxAmount} {
		got, err := ParseAmount(FormatXML(a))
		if err != nil || got != a {
			t.Errorf("round trip %d: got %d, err %v", a, got, err)
		}
	}
}
