// Package listing decides what fv list shows: the date range and the merge of the local
// archive with the invoices KSeF reports. It does no I/O, so every rule is a plain test.
package listing

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/lwasak/fv-cli/internal/invoice"
)

const dateLayout = "2006-01-02"

// MaxKSeFSpanDays is the longest range fv list asks KSeF about (offline has no limit).
const MaxKSeFSpanDays = 366

// Range is a range of issue dates, YYYY-MM-DD, both ends inclusive (Warsaw dates).
type Range struct{ From, To string }

// DefaultRange is the current month and the two before it: from the first day of month-2 to
// the last day of the current month. time.Date normalises the month, so January gives
// November of the previous year.
func DefaultRange(now time.Time) Range {
	t := invoice.Today(now)
	first := time.Date(t.Year(), t.Month()-2, 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	return Range{From: first.Format(dateLayout), To: last.Format(dateLayout)}
}

var monthRE = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// ParseRange builds the range from the flags. month (YYYY-MM) excludes from and to; with
// only one of from and to the other end is the one of DefaultRange. from must not be after
// to. Unless offline, the range may span at most MaxKSeFSpanDays days.
func ParseRange(from, to, month string, now time.Time, offline bool) (Range, error) {
	var r Range
	switch {
	case month != "":
		if from != "" || to != "" {
			return r, errors.New("--month cannot be combined with --from or --to")
		}
		if !monthRE.MatchString(month) {
			return r, fmt.Errorf("--month %q: use the form YYYY-MM, for example 2026-10", month)
		}
		first, _ := time.Parse(dateLayout, month+"-01")
		r = Range{From: month + "-01", To: first.AddDate(0, 1, -1).Format(dateLayout)}
	default:
		def := DefaultRange(now)
		r = def
		if from != "" {
			d, err := invoice.ParseDate(from)
			if err != nil {
				return r, fmt.Errorf("--from: %w", err)
			}
			r.From = d.Format(dateLayout)
		}
		if to != "" {
			d, err := invoice.ParseDate(to)
			if err != nil {
				return r, fmt.Errorf("--to: %w", err)
			}
			r.To = d.Format(dateLayout)
		}
		if r.From > r.To {
			switch {
			case from == "":
				return r, fmt.Errorf("--to %s is before the default start %s; give --from as well", r.To, r.From)
			case to == "":
				return r, fmt.Errorf("--from %s is after the default end %s; give --to as well", r.From, r.To)
			}
			return r, fmt.Errorf("--from %s is after --to %s", r.From, r.To)
		}
	}
	if !offline {
		a, _ := time.Parse(dateLayout, r.From)
		b, _ := time.Parse(dateLayout, r.To)
		if days := int(b.Sub(a).Hours()/24) + 1; days > MaxKSeFSpanDays {
			return r, fmt.Errorf("the range %s to %s is %d days; KSeF is queried for at most %d days (KSeF allows 20 queries an hour). Use a shorter range, or --offline to list what is already stored",
				r.From, r.To, days, MaxKSeFSpanDays)
		}
	}
	return r, nil
}

// Contains reports whether a YYYY-MM-DD issue date lies in the range.
func (r Range) Contains(date string) bool { return date >= r.From && date <= r.To }
