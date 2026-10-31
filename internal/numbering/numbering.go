// Package numbering renders invoice numbers from a pattern and derives the counter period.
// It is pure: no I/O and no clock.
package numbering

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lwasak/fv-cli/internal/validate"
)

// Reset is how often the sequence restarts at 1.
type Reset string

// Reset values.
const (
	ResetMonthly Reset = "monthly"
	ResetYearly  Reset = "yearly"
	ResetNever   Reset = "never"
)

// DefaultPattern is used when numbering.pattern is unset.
const DefaultPattern = "FV/{YYYY}/{MM}/{SEQ:3}"

// ParseReset parses monthly, yearly or never.
func ParseReset(s string) (Reset, error) {
	switch Reset(s) {
	case ResetMonthly, ResetYearly, ResetNever:
		return Reset(s), nil
	}
	return "", fmt.Errorf("unknown reset %q; use monthly, yearly or never", s)
}

func hasYear(pattern string) bool {
	return strings.Contains(pattern, "{YYYY}") || strings.Contains(pattern, "{YY}")
}

// DefaultReset derives the period from the pattern: monthly when it has {MM} (and a year
// token), else yearly when it has a year token, else never.
func DefaultReset(pattern string) Reset {
	switch {
	case strings.Contains(pattern, "{MM}") && hasYear(pattern):
		return ResetMonthly
	case hasYear(pattern):
		return ResetYearly
	case strings.Contains(pattern, "{MM}"):
		// A month without a year cannot tell years apart; a yearly or monthly reset
		// would repeat numbers, so keep counting.
		return ResetNever
	}
	return ResetNever
}

// CheckResetFits refuses a reset whose period is not visible in the pattern.
func CheckResetFits(pattern string, r Reset) error {
	return validate.NumberingReset(pattern, string(r))
}

// PeriodKey is the counter period of the issue date: "2026-10", "2026" or "all".
func PeriodKey(r Reset, issue time.Time) string {
	switch r {
	case ResetMonthly:
		return fmt.Sprintf("%04d-%02d", issue.Year(), int(issue.Month()))
	case ResetYearly:
		return fmt.Sprintf("%04d", issue.Year())
	}
	return "all"
}

// Render fills pattern for the issue date and sequence number. {SEQ:n} zero-pads to n
// digits; a wider number is never truncated.
func Render(pattern string, issue time.Time, seq int) (string, error) {
	if err := validate.NumberingPattern(pattern); err != nil {
		return "", fmt.Errorf("numbering pattern %q: %w", pattern, err)
	}
	if seq < 1 {
		return "", fmt.Errorf("sequence number must be at least 1, got %d", seq)
	}
	var b strings.Builder
	for i := 0; i < len(pattern); {
		if pattern[i] != '{' {
			b.WriteByte(pattern[i])
			i++
			continue
		}
		end := strings.IndexByte(pattern[i:], '}')
		tok := pattern[i+1 : i+end]
		i += end + 1
		switch {
		case tok == "YYYY":
			fmt.Fprintf(&b, "%04d", issue.Year())
		case tok == "YY":
			fmt.Fprintf(&b, "%02d", issue.Year()%100)
		case tok == "MM":
			fmt.Fprintf(&b, "%02d", int(issue.Month()))
		case tok == "DD":
			fmt.Fprintf(&b, "%02d", issue.Day())
		case tok == "SEQ":
			b.WriteString(strconv.Itoa(seq))
		default: // SEQ:n, validated above
			w := int(tok[4] - '0')
			s := strconv.Itoa(seq)
			b.WriteString(strings.Repeat("0", max(0, w-len(s))) + s)
		}
	}
	return b.String(), nil
}

// Sanitize makes a number usable as a folder name: "/" and "\" become "_".
func Sanitize(number string) string {
	return strings.NewReplacer("/", "_", "\\", "_").Replace(number)
}
