package ksef

import (
	"errors"
	"fmt"
	"regexp"
)

// ksefNumberRE is the pattern of TNumerKSeF in the OpenAPI document (KsefNumber): the 35
// character KSeF 2.0 form, or the 36 character KSeF 1.0 form with a dash inside the
// technical part.
// ErrBadKSeFNumber is wrapped by every ValidateKSeFNumber failure.
var ErrBadKSeFNumber = errors.New("invalid KSeF number")

var ksefNumberRE = regexp.MustCompile(`^([1-9](\d[1-9]|[1-9]\d)\d{7})-(20[2-9][0-9]|2[1-9]\d{2}|[3-9]\d{3})(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])-([0-9A-F]{6})-?([0-9A-F]{6})-([0-9A-F]{2})$`)

// ValidateKSeFNumber checks s against the TNumerKSeF pattern and, for the 35 character form,
// the CRC-8 check digits (polynomial 0x07, initial value 0) over the first 32 characters.
func ValidateKSeFNumber(s string) error {
	if !ksefNumberRE.MatchString(s) {
		return fmt.Errorf("ksef: %q does not match the KSeF number pattern: %w", s, ErrBadKSeFNumber)
	}
	if len(s) == 35 {
		if want := fmt.Sprintf("%02X", crc8(s[:32])); s[33:] != want {
			return fmt.Errorf("ksef: %q has a wrong checksum (want %s): %w", s, want, ErrBadKSeFNumber)
		}
	}
	return nil
}

func crc8(s string) byte {
	var c byte
	for i := 0; i < len(s); i++ {
		c ^= s[i]
		for range 8 {
			if c&0x80 != 0 {
				c = c<<1 ^ 0x07
			} else {
				c <<= 1
			}
		}
	}
	return c
}
