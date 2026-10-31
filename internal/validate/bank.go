package validate

import (
	"fmt"
	"regexp"
	"strings"
)

var swiftRE = regexp.MustCompile(`^[A-Z]{6}[A-Z0-9]{2}([A-Z0-9]{3})?$`)

// SWIFT checks a SWIFT/BIC code and returns it normalised: spaces removed, upper case.
// The accepted shape is the one FA(3) uses: 8 or 11 characters, a 6-letter bank and
// country part, 2 letters or digits for the location, an optional 3-character branch.
func SWIFT(s string) (string, error) {
	n := strings.ToUpper(strings.Join(strings.Fields(s), ""))
	if !swiftRE.MatchString(n) {
		return "", fmt.Errorf("SWIFT/BIC %q is not valid; use 8 or 11 characters: 4-letter bank code, 2-letter country, 2 letters or digits, optional 3-character branch, e.g. BREXPLPW or BREXPLPWXXX", s)
	}
	return n, nil
}

// GTIN checks a GTIN: 8, 12, 13 or 14 digits with a valid GS1 mod-10 check digit.
func GTIN(s string) error {
	switch len(s) {
	case 8, 12, 13, 14:
	default:
		return fmt.Errorf("GTIN %q must have 8, 12, 13 or 14 digits", s)
	}
	sum := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return fmt.Errorf("GTIN %q must contain digits only", s)
		}
		if i == len(s)-1 {
			break
		}
		w := 3
		if (len(s)-1-i)%2 == 0 {
			w = 1
		}
		sum += int(c-'0') * w
	}
	check := (10 - sum%10) % 10
	if int(s[len(s)-1]-'0') != check {
		return fmt.Errorf("GTIN %q has a wrong check digit (expected %d)", s, check)
	}
	return nil
}
