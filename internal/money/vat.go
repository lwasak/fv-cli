package money

import (
	"errors"
	"fmt"
	"math"
)

// Line is one invoice line's net amount under a rate.
type Line struct {
	Rate Rate
	Net  Amount
}

// Group is the totals of all lines sharing a rate.
type Group struct {
	Rate  Rate
	Net   Amount
	VAT   Amount
	Gross Amount
}

// Summary holds the per-rate groups (FA(3) order, empty groups omitted) and
// the invoice gross total (P_15).
type Summary struct {
	Groups []Group
	Gross  Amount
}

var errOverflow = errors.New("amount overflow while summing the invoice")

func addChecked(a, b Amount) (Amount, error) {
	if a > Amount(math.MaxInt64)-b {
		return 0, errOverflow
	}
	return a + b, nil
}

// Summarize groups net per rate and computes VAT once per group, rounding
// half up on the group's net sum.
func Summarize(lines []Line) (Summary, error) {
	var s Summary
	for _, l := range lines {
		known := false
		for _, r := range rates {
			if l.Rate.Code == r.Code {
				known = true
				break
			}
		}
		if !known {
			return Summary{}, fmt.Errorf("unknown rate code %q", l.Rate.Code)
		}
		if l.Net <= 0 {
			return Summary{}, errors.New("net amount must be greater than zero")
		}
	}
	for _, r := range rates {
		var net Amount
		found := false
		for _, l := range lines {
			if l.Rate.Code != r.Code {
				continue
			}
			found = true
			var err error
			if net, err = addChecked(net, l.Net); err != nil {
				return Summary{}, err
			}
		}
		if !found {
			continue
		}
		if r.Percent > 0 && net > Amount((math.MaxInt64-50)/int64(r.Percent)) {
			return Summary{}, errOverflow
		}
		vat := Amount((int64(net)*int64(r.Percent) + 50) / 100)
		gross, err := addChecked(net, vat)
		if err != nil {
			return Summary{}, err
		}
		if s.Gross, err = addChecked(s.Gross, gross); err != nil {
			return Summary{}, err
		}
		s.Groups = append(s.Groups, Group{Rate: r, Net: net, VAT: vat, Gross: gross})
	}
	return s, nil
}
