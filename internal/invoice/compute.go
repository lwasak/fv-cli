package invoice

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lwasak/fv-cli/internal/money"
)

// MaxLines is the most lines FA(3) allows in one invoice (FaWiersz maxOccurs).
const MaxLines = 10000

// LineSpec is one requested invoice line, before amounts are derived.
type LineSpec struct {
	Source    string // names the origin in errors: `--item "dev=160"`, "" for the single-line flags
	Item      string // config key of a saved item, "" for an ad-hoc line
	Template  string // description; tokens are rendered with the sale date
	Unit      string
	Qty       *money.Quantity // nil means 1; not allowed together with Net
	UnitPrice *money.Amount   // exactly one of UnitPrice and Net
	Net       *money.Amount
	Rate      money.Rate
	Index     string
	PKWiU     string
	GTIN      string
}

// Request is everything Compute needs; all dates are injected.
type Request struct {
	IssueDate    time.Time
	SaleDateFlag *time.Time // explicit --sale-date; wins over SaleRule
	SaleRule     string     // issue_date (default) | last_day_of_month | last_day_of_previous_month
	PaymentDays  int
	Lines        []LineSpec
}

// Computed is the result of Compute.
type Computed struct {
	IssueDate, SaleDate, DueDate time.Time
	Lines                        []Line
	Summary                      money.Summary
}

// Line is one invoice line.
type Line struct {
	No          int
	Item        string
	Description string
	Unit        string
	Qty         money.Quantity
	UnitPrice   money.Amount
	Net         money.Amount
	Rate        money.Rate
	Index       string
	PKWiU       string
	GTIN        string
}

// oneUnit is quantity 1 in money.Quantity units (millionths).
const oneUnit money.Quantity = 1000000

// Compute derives the dates, the invoice lines and the VAT summary. VAT is computed once per
// rate from the summed line nets (see money.Summarize), never per line.
func Compute(r Request) (Computed, error) {
	if r.IssueDate.IsZero() {
		return Computed{}, errors.New("issue date is required")
	}
	if len(r.Lines) == 0 {
		return Computed{}, errors.New("the invoice has no lines: give a net amount, or quantity with unit price")
	}
	if len(r.Lines) > MaxLines {
		return Computed{}, fmt.Errorf("the invoice has %d lines; FA(3) allows at most %d", len(r.Lines), MaxLines)
	}
	issue := midnight(r.IssueDate)

	var sale time.Time
	if r.SaleDateFlag != nil {
		sale = midnight(*r.SaleDateFlag)
	} else {
		var err error
		if sale, err = SaleDate(issue, r.SaleRule); err != nil {
			return Computed{}, err
		}
	}
	due, err := DueDate(issue, r.PaymentDays)
	if err != nil {
		return Computed{}, err
	}

	lines := make([]Line, 0, len(r.Lines))
	mlines := make([]money.Line, 0, len(r.Lines))
	for i, spec := range r.Lines {
		l, err := computeLine(i+1, spec, sale)
		if err != nil {
			if spec.Source != "" {
				return Computed{}, fmt.Errorf("line %d (%s): %w", i+1, spec.Source, err)
			}
			return Computed{}, err
		}
		lines = append(lines, l)
		mlines = append(mlines, money.Line{Rate: l.Rate, Net: l.Net})
	}
	sum, err := money.Summarize(mlines)
	if err != nil {
		return Computed{}, err
	}
	return Computed{IssueDate: issue, SaleDate: sale, DueDate: due, Lines: lines, Summary: sum}, nil
}

func computeLine(no int, spec LineSpec, sale time.Time) (Line, error) {
	if spec.Rate.Code == "" {
		return Line{}, errors.New("VAT rate is required")
	}
	if strings.TrimSpace(spec.Template) == "" {
		return Line{}, errors.New("line description is empty: pass --desc or set line_template")
	}
	desc, err := RenderTemplate(spec.Template, sale)
	if err != nil {
		return Line{}, err
	}
	var qty money.Quantity
	var price, net money.Amount
	switch {
	case spec.Net != nil && (spec.Qty != nil || spec.UnitPrice != nil):
		return Line{}, errors.New("give either net, or quantity with unit price, not both")
	case spec.Net != nil:
		if *spec.Net <= 0 {
			return Line{}, errors.New("net amount must be positive")
		}
		qty, price, net = oneUnit, *spec.Net, *spec.Net
	case spec.UnitPrice != nil:
		qty, price = oneUnit, *spec.UnitPrice
		if spec.Qty != nil {
			qty = *spec.Qty
		}
		if net, err = money.NetOf(qty, price); err != nil {
			return Line{}, fmt.Errorf("net amount: %w", err)
		}
	case spec.Qty != nil:
		return Line{}, errors.New("quantity and unit price must be given together")
	default:
		return Line{}, errors.New("give a net amount, or quantity with unit price")
	}
	return Line{No: no, Item: spec.Item, Description: desc, Unit: spec.Unit, Qty: qty, UnitPrice: price,
		Net: net, Rate: spec.Rate, Index: spec.Index, PKWiU: spec.PKWiU, GTIN: spec.GTIN}, nil
}
