package validate

import (
	"fmt"
	"regexp"
	"strings"
)

var currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)

// Currency checks an ISO 4217 style currency code: three capital letters.
func Currency(c string) error {
	if !currencyRE.MatchString(c) {
		return fmt.Errorf("%q is not a currency code; use 3 capital letters such as PLN or EUR", c)
	}
	return nil
}

// SaleDateRule checks a sale date rule.
func SaleDateRule(r string) error {
	switch r {
	case "issue_date", "last_day_of_month", "last_day_of_previous_month":
		return nil
	}
	return fmt.Errorf("unknown sale date rule %q; use issue_date, last_day_of_month or last_day_of_previous_month", r)
}

// LineTemplate checks an invoice line template: 1-512 characters; the only tokens are
// {month_name}, {month_name_en} and {year}; braces must be balanced.
func LineTemplate(t string) error {
	if err := Text(t, 512); err != nil {
		return err
	}
	for i := 0; i < len(t); {
		switch t[i] {
		case '{':
			end := strings.IndexByte(t[i:], '}')
			if end < 0 {
				return fmt.Errorf("unclosed '{' in template")
			}
			switch tok := t[i+1 : i+end]; tok {
			case "month_name", "month_name_en", "year":
			default:
				return fmt.Errorf("unknown token {%s}; use {month_name}, {month_name_en} or {year}", tok)
			}
			i += end + 1
		case '}':
			return fmt.Errorf("unmatched '}' in template")
		default:
			i++
		}
	}
	return nil
}

// CacheTTL checks registry.cache_ttl (days).
func CacheTTL(days int) error {
	if days < 1 || days > 365 {
		return fmt.Errorf("must be 1-365 days, got %d", days)
	}
	return nil
}
