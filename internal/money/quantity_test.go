package money

import "testing"

func TestParseQuantity(t *testing.T) {
	ok := []struct {
		in   string
		want Quantity
	}{
		{"1", 1000000},
		{"3", 3000000},
		{"1,5", 1500000},
		{"1.5", 1500000},
		{"0,333333", 333333},
		{"0.000001", 1},
		{"1_000,25", 1000250000},
		{"1000000000", 1000000000 * 1000000},
	}
	for _, tc := range ok {
		got, err := ParseQuantity(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseQuantity(%q) = %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}
	bad := []string{"", "0", "0,0", "0,0000001", "1,1234567", "1.2,3", "-1", "+1", "1 5", "_1", "1_", "1e3", ".5", "1.", "1000000001", "٣"}
	for _, in := range bad {
		if q, err := ParseQuantity(in); err == nil {
			t.Errorf("ParseQuantity(%q) = %d, want error", in, q)
		}
	}
}

func TestNetOf(t *testing.T) {
	tests := []struct {
		name  string
		q     string
		price string
		want  Amount
	}{
		{"whole", "3", "100,00", 30000},
		{"half up", "1,5", "0,01", 2},
		{"fraction", "0,333333", "100,00", 3333},
	}
	for _, tc := range tests {
		q, err := ParseQuantity(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		p, err := ParseAmount(tc.price)
		if err != nil {
			t.Fatal(err)
		}
		got, err := NetOf(q, p)
		if err != nil || got != tc.want {
			t.Errorf("%s: NetOf = %d, %v; want %d", tc.name, got, err, tc.want)
		}
	}
}

func TestNetOf_RoundsToZeroIsError(t *testing.T) {
	q, _ := ParseQuantity("0,000001")
	p, _ := ParseAmount("0,01")
	if got, err := NetOf(q, p); err == nil {
		t.Errorf("NetOf = %d, want error", got)
	}
}

func TestNetOf_OverflowIsError(t *testing.T) {
	q, _ := ParseQuantity("1000000000")
	p, _ := ParseAmount("999_999_999_999,99")
	if got, err := NetOf(q, p); err == nil {
		t.Errorf("NetOf = %d, want error", got)
	}
	q2, _ := ParseQuantity("2")
	p2, _ := ParseAmount("600_000_000_000")
	if got, err := NetOf(q2, p2); err == nil {
		t.Errorf("NetOf = %d, want error", got)
	}
}
