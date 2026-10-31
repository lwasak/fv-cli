package money

import (
	"fmt"
	"strings"
)

// Amount is a money value in grosze (1/100 zł).
type Amount int64

// MaxAmount is the largest accepted amount: 999 999 999 999,99 zł.
const MaxAmount Amount = 99999999999999

const maxWholeDigits = 12

// AcceptedFormats lists example inputs ParseAmount accepts; every rejection
// message ends with it.
const AcceptedFormats = `use digits with one "." or "," and at most 2 decimals, "_" only between digits of the whole part, e.g. 18500, 18500.43, 18500,43, 18_500,43`

// ParseError describes why an amount was rejected.
type ParseError struct {
	Input  string
	Reason string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("invalid amount %q: %s; %s", e.Input, e.Reason, AcceptedFormats)
}

// ParseAmount parses a positive amount of zloty into grosze. It accepts
// digits, "_" between whole-part digits, and an optional "." or "," followed
// by one or two decimals. It never consults the OS locale.
func ParseAmount(s string) (Amount, error) {
	fail := func(reason string) (Amount, error) {
		return 0, &ParseError{Input: s, Reason: reason}
	}
	if s == "" {
		return fail("empty amount")
	}
	for _, c := range []string{"zł", "PLN", "pln", "$", "€", "£", "USD", "EUR"} {
		if strings.Contains(s, c) {
			return fail("currency symbol not allowed")
		}
	}
	switch s[0] {
	case '+', '-':
		return fail("sign not allowed")
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return fail("space not allowed")
	}
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return fail("non-ASCII character")
		}
	}

	var whole, frac []byte
	sep := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			if sep == 0 {
				whole = append(whole, c)
			} else {
				frac = append(frac, c)
			}
		case c == '_':
			if sep != 0 || i == 0 || i == len(s)-1 || s[i-1] < '0' || s[i-1] > '9' || s[i+1] < '0' || s[i+1] > '9' {
				return fail("underscore only allowed between digits of the whole part")
			}
		case c == '.' || c == ',':
			if sep != 0 {
				if sep != c {
					return fail("mixed decimal separators")
				}
				return fail("more than one decimal separator")
			}
			sep = c
		default:
			return fail("invalid character " + fmt.Sprintf("%q", string(c)))
		}
	}
	if len(whole) == 0 {
		return fail("missing whole part")
	}
	if sep != 0 && len(frac) == 0 {
		return fail("missing decimals after the separator")
	}
	if len(frac) > 2 {
		return fail("too many decimals")
	}
	if len(strings.TrimLeft(string(whole), "0")) > maxWholeDigits {
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
	if v == 0 {
		return fail("amount must be greater than zero")
	}
	return Amount(v), nil
}
