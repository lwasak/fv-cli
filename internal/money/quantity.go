package money

import (
	"errors"
	"fmt"
	"math/bits"
	"strings"
)

// Quantity is a positive quantity in millionths of a unit.
type Quantity int64

const (
	quantityScale    = 1000000
	maxQuantityUnits = 1000000000
	quantityDecimals = 6
)

// QuantityFormats lists example inputs ParseQuantity accepts.
const QuantityFormats = `use digits with one "." or "," and at most 6 decimals, "_" only between digits of the whole part, e.g. 3, 1.5, 1,5, 1_000,25`

func quantityErr(in, reason string) error {
	return fmt.Errorf("invalid quantity %q: %s; %s", in, reason, QuantityFormats)
}

// ParseQuantity parses a positive quantity (up to 6 decimals, at most 10^9
// units) into millionths. It follows the same separator rules as ParseAmount.
func ParseQuantity(s string) (Quantity, error) {
	if s == "" {
		return 0, quantityErr(s, "empty quantity")
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
				return 0, quantityErr(s, "underscore only allowed between digits of the whole part")
			}
		case c == '.' || c == ',':
			if sep != 0 {
				if sep != c {
					return 0, quantityErr(s, "mixed decimal separators")
				}
				return 0, quantityErr(s, "more than one decimal separator")
			}
			sep = c
		default:
			return 0, quantityErr(s, "invalid character "+fmt.Sprintf("%q", string(c)))
		}
	}
	if len(whole) == 0 {
		return 0, quantityErr(s, "missing whole part")
	}
	if sep != 0 && len(frac) == 0 {
		return 0, quantityErr(s, "missing decimals after the separator")
	}
	if len(frac) > quantityDecimals {
		return 0, quantityErr(s, "too many decimals")
	}
	if len(strings.TrimLeft(string(whole), "0")) > 10 {
		return 0, quantityErr(s, "quantity too large")
	}
	var v int64
	for _, c := range whole {
		v = v*10 + int64(c-'0')
	}
	if v > maxQuantityUnits {
		return 0, quantityErr(s, "quantity too large")
	}
	v *= quantityScale
	scale := int64(quantityScale / 10)
	for _, c := range frac {
		v += int64(c-'0') * scale
		scale /= 10
	}
	if v == 0 {
		return 0, quantityErr(s, "quantity must be greater than zero")
	}
	if v > maxQuantityUnits*quantityScale {
		return 0, quantityErr(s, "quantity too large")
	}
	return Quantity(v), nil
}

// NetOf returns quantity x unit price in whole grosze, rounded half up. A
// result of zero or above MaxAmount is an error.
func NetOf(q Quantity, unitPrice Amount) (Amount, error) {
	if q <= 0 || unitPrice <= 0 {
		return 0, errors.New("quantity and unit price must be positive")
	}
	hi, lo := bits.Mul64(uint64(q), uint64(unitPrice))
	var carry uint64
	lo, carry = bits.Add64(lo, quantityScale/2, 0)
	hi += carry
	if hi >= quantityScale {
		return 0, errors.New("net amount too large")
	}
	quo, _ := bits.Div64(hi, lo, quantityScale)
	if quo > uint64(MaxAmount) {
		return 0, errors.New("net amount too large")
	}
	if quo == 0 {
		return 0, errors.New("net amount rounds to zero")
	}
	return Amount(quo), nil
}

// FormatQuantityXML renders a quantity as plain "." decimal with trailing
// zeros trimmed ("1", "1.5", at most 6 decimals).
func FormatQuantityXML(q Quantity) string {
	return formatScaled(int64(q))
}
