package money

import "fmt"

// ParseXML parses a signed decimal as it appears in FA(3) XML (TKwotowy) and in the amount
// fields of KSeF JSON: an optional "-", digits, and optionally "." with one or two decimals.
// It accepts zero and negative values (correction invoices), refuses a "+" sign, exponents,
// separators other than ".", spaces and more than 2 decimals, and never consults the OS locale.
func ParseXML(s string) (Amount, error) {
	fail := func(reason string) (Amount, error) {
		return 0, fmt.Errorf("invalid XML amount %q: %s", s, reason)
	}
	if s == "" {
		return fail("empty amount")
	}
	i := 0
	neg := false
	if s[0] == '-' {
		neg = true
		i++
	}
	var whole, frac []byte
	sep := false
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			if sep {
				frac = append(frac, c)
			} else {
				whole = append(whole, c)
			}
		case c == '.':
			if sep {
				return fail("more than one decimal separator")
			}
			sep = true
		default:
			return fail(fmt.Sprintf("invalid character %q", string(c)))
		}
	}
	if len(whole) == 0 {
		return fail("missing whole part")
	}
	if sep && len(frac) == 0 {
		return fail("missing decimals after the separator")
	}
	if len(frac) > 2 {
		return fail("too many decimals")
	}
	for len(whole) > 1 && whole[0] == '0' {
		whole = whole[1:]
	}
	if len(whole) > maxWholeDigits {
		return fail("amount too large")
	}
	var v int64
	for _, c := range whole {
		v = v*10 + int64(c-'0')
	}
	v *= 100
	switch len(frac) {
	case 1:
		v += int64(frac[0]-'0') * 10
	case 2:
		v += int64(frac[0]-'0')*10 + int64(frac[1]-'0')
	}
	if neg {
		v = -v
	}
	return Amount(v), nil
}
