// Package validate checks user-supplied identifiers and settings.
package validate

import (
	"errors"
	"regexp"
	"strings"
)

var nipWeights = [9]int{6, 5, 7, 2, 3, 4, 5, 6, 7}

// NIP checks a Polish tax identification number: exactly 10 digits, no separators,
// valid checksum.
func NIP(s string) error {
	if strings.ContainsAny(s, "- ") {
		return errors.New("NIP must not contain separators; remove spaces and dashes")
	}
	if len(s) != 10 {
		return errors.New("NIP must be exactly 10 digits")
	}
	sum := 0
	for i := 0; i < 10; i++ {
		if s[i] < '0' || s[i] > '9' {
			return errors.New("NIP must be exactly 10 digits")
		}
		if i < 9 {
			sum += int(s[i]-'0') * nipWeights[i]
		}
	}
	if sum%11 == 10 || sum%11 != int(s[9]-'0') {
		return errors.New("NIP checksum is wrong")
	}
	return nil
}

var vatIDPattern = regexp.MustCompile(`^[A-Za-z]{2}[A-Za-z0-9]+$`)

// PartyIdentity checks that exactly one of nip and vatID is given and that it is well formed.
func PartyIdentity(nip, vatID string) error {
	switch {
	case nip != "" && vatID != "":
		return errors.New("set exactly one of nip and vat_id, not both")
	case nip == "" && vatID == "":
		return errors.New("set exactly one of nip and vat_id")
	case nip != "":
		return NIP(nip)
	}
	if !vatIDPattern.MatchString(vatID) {
		return errors.New("vat_id must be a 2-letter country prefix followed by letters or digits")
	}
	return nil
}
