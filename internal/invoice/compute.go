package invoice

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lwasak/fv-cli/internal/money"
)

// Request is everything Compute needs; all dates are injected.
type Request struct {
	IssueDate    time.Time
	SaleDateFlag *time.Time // explicit --sale-date; wins over SaleRule
	SaleRule     string     // issue_date (default) | last_day_of_month | last_day_of_previous_month
	PaymentDays  int
	Template     string // line_template, used when DescFlag is empty
	DescFlag     string // --desc; wins over Template; tokens are rendered too
	Unit         string
	Net          *money.Amount
	Qty          *money.Quantity
	UnitPrice    *money.Amount
	Rate         money.Rate
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
	Description string
	Unit        string
	Qty         money.Quantity
	UnitPrice   money.Amount
	Net         money.Amount
	Rate        money.Rate
}

// oneUnit is quantity 1 in money.Quantity units (millionths).
const oneUnit money.Quantity = 1000000

// Compute derives dates, the single invoice line and the VAT summary.
func Compute(r Request) (Computed, error) {
	if r.IssueDate.IsZero() {
		return Computed{}, errors.New("issue date is required")
	}
	if r.Rate.Code == "" {
		return Computed{}, errors.New("VAT rate is required")
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

	desc := r.DescFlag
	if strings.TrimSpace(desc) == "" {
		desc = r.Template
	}
	if strings.TrimSpace(desc) == "" {
		return Computed{}, errors.New("line description is empty: pass --desc or set line_template")
	}
	if desc, err = RenderTemplate(desc, sale); err != nil {
		return Computed{}, err
	}

	var qty money.Quantity
	var price, net money.Amount
	switch {
	case r.Net != nil && (r.Qty != nil || r.UnitPrice != nil):
		return Computed{}, errors.New("give either net, or quantity with unit price, not both")
	case r.Net != nil:
		if *r.Net <= 0 {
			return Computed{}, errors.New("net amount must be positive")
		}
		qty, price, net = oneUnit, *r.Net, *r.Net
	case r.Qty != nil && r.UnitPrice != nil:
		qty, price = *r.Qty, *r.UnitPrice
		if net, err = money.NetOf(qty, price); err != nil {
			return Computed{}, fmt.Errorf("net amount: %w", err)
		}
	case r.Qty != nil || r.UnitPrice != nil:
		return Computed{}, errors.New("quantity and unit price must be given together")
	default:
		return Computed{}, errors.New("give a net amount, or quantity with unit price")
	}

	sum, err := money.Summarize([]money.Line{{Rate: r.Rate, Net: net}})
	if err != nil {
		return Computed{}, err
	}
	return Computed{
		IssueDate: issue, SaleDate: sale, DueDate: due,
		Lines:   []Line{{No: 1, Description: desc, Unit: r.Unit, Qty: qty, UnitPrice: price, Net: net, Rate: r.Rate}},
		Summary: sum,
	}, nil
}
