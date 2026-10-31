package money

import (
	"errors"
	"fmt"
)

// Rate is a supported VAT rate and the FA(3) fields it maps to.
type Rate struct {
	Code     string
	Percent  int
	NetField string
	VATField string // empty for zw
}

// ErrRateNotSupported matches codes that are known but not supported yet.
var ErrRateNotSupported = errors.New("rate code not supported yet")

type notSupportedError struct{ code string }

func (e *notSupportedError) Error() string {
	return fmt.Sprintf("rate code %q is not supported yet", e.code)
}

func (e *notSupportedError) Is(target error) bool { return target == ErrRateNotSupported }

var rates = []Rate{
	{"23", 23, "P_13_1", "P_14_1"},
	{"8", 8, "P_13_2", "P_14_2"},
	{"5", 5, "P_13_3", "P_14_3"},
	{"zw", 0, "P_13_7", ""},
}

// Rates returns the supported rates in FA(3) order: 23, 8, 5, zw.
func Rates() []Rate {
	return append([]Rate(nil), rates...)
}

// ParseRate maps a rate code to its Rate. Codes are case-sensitive.
func ParseRate(code string) (Rate, error) {
	for _, r := range rates {
		if r.Code == code {
			return r, nil
		}
	}
	switch code {
	case "0", "np":
		return Rate{}, &notSupportedError{code}
	}
	return Rate{}, fmt.Errorf("unknown rate code %q; supported: 23, 8, 5, zw", code)
}
