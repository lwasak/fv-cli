package money

import (
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
)

const (
	microScale    = 1000000
	maxMicroWhole = 12
)

// formatScaled renders a non-negative value in millionths as "." decimal with
// trailing zeros trimmed.
func formatScaled(v int64) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	whole := strconv.FormatInt(v/microScale, 10)
	frac := strconv.FormatInt(v%microScale, 10)
	if frac == "0" {
		return sign + whole
	}
	frac = strings.Repeat("0", quantityDecimals-len(frac)) + frac
	return sign + whole + "." + strings.TrimRight(frac, "0")
}

// FormatMicro renders a rate in millionths (an NBP mid, for KursWaluty) as
// "." decimal with trailing zeros trimmed.
func FormatMicro(m int64) string {
	return formatScaled(m)
}

// ParseMicro parses positive decimal text with "." and at most 6 decimals
// (the NBP mid) into millionths, without floating point.
func ParseMicro(s string) (int64, error) {
	bad := func(reason string) (int64, error) {
		return 0, fmt.Errorf("invalid rate %q: %s", s, reason)
	}
	whole, frac, hasSep := strings.Cut(s, ".")
	if whole == "" {
		return bad("missing whole part")
	}
	if hasSep && frac == "" {
		return bad("missing decimals after the separator")
	}
	for _, part := range []string{whole, frac} {
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return bad("only digits and one \".\" are allowed")
			}
		}
	}
	if len(frac) > quantityDecimals {
		return bad("more than 6 decimals")
	}
	if len(strings.TrimLeft(whole, "0")) > maxMicroWhole {
		return bad("rate too large")
	}
	var v int64
	for i := 0; i < len(whole); i++ {
		v = v*10 + int64(whole[i]-'0')
	}
	v *= microScale
	scale := int64(microScale / 10)
	for i := 0; i < len(frac); i++ {
		v += int64(frac[i]-'0') * scale
		scale /= 10
	}
	if v == 0 {
		return bad("rate must be greater than zero")
	}
	return v, nil
}

// ConvertMicro multiplies an amount by a rate in millionths, rounding half up
// to whole grosze. A zero result is allowed; a result above MaxAmount is an error.
func ConvertMicro(a Amount, rateMicro int64) (Amount, error) {
	if a < 0 || rateMicro <= 0 {
		return 0, errors.New("amount must not be negative and rate must be positive")
	}
	hi, lo := bits.Mul64(uint64(a), uint64(rateMicro))
	var carry uint64
	lo, carry = bits.Add64(lo, microScale/2, 0)
	hi += carry
	if hi >= microScale {
		return 0, errors.New("converted amount too large")
	}
	quo, _ := bits.Div64(hi, lo, microScale)
	if quo > uint64(MaxAmount) {
		return 0, errors.New("converted amount too large")
	}
	return Amount(quo), nil
}
