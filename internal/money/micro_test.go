package money

import (
	"math"
	"testing"
)

func TestFormatQuantityXML(t *testing.T) {
	for _, c := range []struct {
		q    Quantity
		want string
	}{
		{1_000000, "1"}, {1_500000, "1.5"}, {1, "0.000001"}, {3_250000, "3.25"},
		{1000000000 * quantityScale, "1000000000"}, {100_000001, "100.000001"},
	} {
		if got := FormatQuantityXML(c.q); got != c.want {
			t.Errorf("FormatQuantityXML(%d) = %q, want %q", c.q, got, c.want)
		}
	}
}

func TestConvertMicro(t *testing.T) {
	for _, c := range []struct {
		a    Amount
		rate int64
		want Amount
	}{
		{100, 4_251200, 425},
		{1, 500000, 1}, // half up
		{1, 499999, 0}, // zero allowed
		{0, 4_251200, 0},
		{1850000, 4_251200, 7864720}, // 18500.00 EUR VAT-ish: 1850000*4.2512 = 7864720
		{3, 1_500000, 5},             // 4.5 -> 5
	} {
		got, err := ConvertMicro(c.a, c.rate)
		if err != nil || got != c.want {
			t.Errorf("ConvertMicro(%d, %d) = %d, %v; want %d", c.a, c.rate, got, err, c.want)
		}
	}
	for _, c := range []struct {
		a    Amount
		rate int64
	}{
		{MaxAmount, 4_251200}, {Amount(math.MaxInt64), 1_000000}, {-1, 1_000000}, {1, 0}, {1, -5},
	} {
		if got, err := ConvertMicro(c.a, c.rate); err == nil {
			t.Errorf("ConvertMicro(%d, %d) = %d, want error", c.a, c.rate, got)
		}
	}
}

func TestParseMicro(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
	}{
		{"4.2512", 4251200}, {"4", 4000000}, {"0.000001", 1}, {"4.251234", 4251234}, {"0004.25", 4250000},
	} {
		got, err := ParseMicro(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseMicro(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"", "4.1234567", "0", "0.0", "-1", "+1", "4,25", ".5", "4.", "1e3", "1 2", "1_000", "4.2.1", "1000000000000"} {
		if got, err := ParseMicro(in); err == nil {
			t.Errorf("ParseMicro(%q) = %d, want error", in, got)
		}
	}
}

func TestFormatMicro(t *testing.T) {
	for _, c := range []struct {
		m    int64
		want string
	}{
		{4251200, "4.2512"}, {4000000, "4"}, {1, "0.000001"}, {4251234, "4.251234"}, {0, "0"},
	} {
		if got := FormatMicro(c.m); got != c.want {
			t.Errorf("FormatMicro(%d) = %q, want %q", c.m, got, c.want)
		}
	}
}
