package validate

import (
	"errors"
	"math/big"
	"regexp"
	"strings"
)

var ibanShape = regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{1,30}$`)

// IBAN checks an IBAN: spaces are ignored, case is ignored, ISO 13616 mod-97;
// PL IBANs must be 28 characters.
func IBAN(s string) error {
	v := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if !ibanShape.MatchString(v) {
		return errors.New("IBAN must be 2 letters, 2 digits and up to 30 letters or digits")
	}
	if strings.HasPrefix(v, "PL") && len(v) != 28 {
		return errors.New("a PL IBAN must be 28 characters")
	}
	rearranged := v[4:] + v[:4]
	var b strings.Builder
	for _, r := range rearranged {
		if r >= 'A' && r <= 'Z' {
			b.WriteString(big.NewInt(int64(r-'A') + 10).String())
		} else {
			b.WriteRune(r)
		}
	}
	n, ok := new(big.Int).SetString(b.String(), 10)
	if !ok || new(big.Int).Mod(n, big.NewInt(97)).Int64() != 1 {
		return errors.New("IBAN checksum is wrong")
	}
	return nil
}
