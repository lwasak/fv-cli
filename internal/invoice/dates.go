// Package invoice is the pure computation behind "fv issue": dates, the line
// description template and the amounts. It does no I/O and never reads the
// clock; callers inject every date.
package invoice

import (
	"fmt"
	"sync"
	"time"
	_ "time/tzdata" // Europe/Warsaw must resolve on hosts without a zoneinfo database (Windows)
)

// Sale date rules.
const (
	RuleIssueDate              = "issue_date"
	RuleLastDayOfMonth         = "last_day_of_month"
	RuleLastDayOfPreviousMonth = "last_day_of_previous_month"
)

var (
	warsawOnce sync.Once
	warsawLoc  *time.Location
)

// Warsaw returns the Europe/Warsaw location, which all invoice dates use.
func Warsaw() *time.Location {
	warsawOnce.Do(func() {
		loc, err := time.LoadLocation("Europe/Warsaw")
		if err != nil {
			panic("invoice: Europe/Warsaw unavailable: " + err.Error())
		}
		warsawLoc = loc
	})
	return warsawLoc
}

// midnight returns the Warsaw calendar day of t at 00:00 Warsaw time.
func midnight(t time.Time) time.Time {
	t = t.In(Warsaw())
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Warsaw())
}

// Today returns the Warsaw calendar date of now, at 00:00 Warsaw time.
func Today(now time.Time) time.Time { return midnight(now) }

// ParseDate parses YYYY-MM-DD (strictly) as a Warsaw date at 00:00.
func ParseDate(s string) (time.Time, error) {
	if len(s) != 10 {
		return time.Time{}, fmt.Errorf("invalid date %q: use YYYY-MM-DD, e.g. 2026-10-31", s)
	}
	t, err := time.ParseInLocation("2006-01-02", s, Warsaw())
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: use YYYY-MM-DD, e.g. 2026-10-31", s)
	}
	return t, nil
}

// SaleDate applies a sale date rule to the issue date. An empty rule means
// issue_date.
func SaleDate(issue time.Time, rule string) (time.Time, error) {
	if issue.IsZero() {
		return time.Time{}, fmt.Errorf("issue date is required")
	}
	d := midnight(issue)
	first := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, Warsaw())
	switch rule {
	case "", RuleIssueDate:
		return d, nil
	case RuleLastDayOfMonth:
		return first.AddDate(0, 1, -1), nil
	case RuleLastDayOfPreviousMonth:
		return first.AddDate(0, 0, -1), nil
	}
	return time.Time{}, fmt.Errorf("unknown sale date rule %q: use %s, %s or %s",
		rule, RuleIssueDate, RuleLastDayOfMonth, RuleLastDayOfPreviousMonth)
}

// DueDate is the issue date plus days (0 allowed).
func DueDate(issue time.Time, days int) (time.Time, error) {
	if days < 0 {
		return time.Time{}, fmt.Errorf("payment days must not be negative, got %d", days)
	}
	if days > 3650 {
		return time.Time{}, fmt.Errorf("payment days too large: %d", days)
	}
	return midnight(issue).AddDate(0, 0, days), nil
}
