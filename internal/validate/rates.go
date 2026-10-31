package validate

import "github.com/lwasak/fv-cli/internal/money"

// RateCode checks a VAT rate code. Codes 0 and np are known but not supported yet.
func RateCode(code string) error {
	_, err := money.ParseRate(code)
	return err
}
