// Package nbp is the client for the NBP table A (average rates) API. The exchange rate for
// a foreign-currency invoice is the table A mid rate from the last business day before the
// tax point. Rates are handled as decimal text and integer millionths, never as floats.
package nbp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lwasak/fv-cli/internal/httpx"
	"github.com/lwasak/fv-cli/internal/money"
)

// BaseURL is the production NBP API root, used when New gets an empty base.
const BaseURL = "https://api.nbp.pl"

// MaxWalkBack is the most days tried, starting at the tax point minus one day.
const MaxWalkBack = 10

var (
	// ErrNotYetPublished means the day before the tax point is after today.
	ErrNotYetPublished = errors.New("rate not yet published")
	// ErrNoRate means no table A rate was found within MaxWalkBack days.
	ErrNoRate = errors.New("no NBP rate found")
	// ErrUnknownCurrency means NBP rejected the currency code (HTTP 400) or the code is malformed.
	ErrUnknownCurrency = errors.New("unknown currency")
	// ErrPLN means a rate was asked for PLN, which needs none.
	ErrPLN = errors.New("PLN needs no exchange rate")
)

var codeRE = regexp.MustCompile(`^[A-Z]{3}$`)

// Rate is one table A mid rate.
type Rate struct {
	Currency      string // ISO 4217 code, e.g. EUR
	Table         string // table number, e.g. 211/A/NBP/2026
	EffectiveDate string // YYYY-MM-DD
	Mid           string // decimal text exactly as NBP sent it
	MidMicro      int64  // Mid in millionths
}

// Client calls the NBP API.
type Client struct {
	base string
	hc   *http.Client
	now  func() time.Time
}

// New returns a client. An empty base means BaseURL; a nil hc means httpx.NewClient(); a nil
// now means time.Now. The base must be https (enforced by httpx on each call).
func New(base string, hc *http.Client, now func() time.Time) *Client {
	if base == "" {
		base = BaseURL
	}
	if hc == nil {
		hc = httpx.NewClient()
	}
	if now == nil {
		now = time.Now
	}
	return &Client{base: strings.TrimRight(base, "/"), hc: hc, now: now}
}

// TaxPoint returns the date the rate is looked up for: the sale date when there is one (a
// non-zero sale), else the issue date; when the invoice is issued before the sale date the
// issue date is used (art. 31a ust. 2). Only the calendar date is kept, in issue's zone.
func TaxPoint(issue, sale time.Time) time.Time {
	iy, im, id := issue.Date()
	i := time.Date(iy, im, id, 0, 0, 0, 0, issue.Location())
	if sale.IsZero() {
		return i
	}
	sy, sm, sd := sale.Date()
	s := time.Date(sy, sm, sd, 0, 0, 0, 0, issue.Location())
	if i.Before(s) {
		return i
	}
	return s
}

type response struct {
	Table string `json:"table"`
	Code  string `json:"code"`
	Rates []struct {
		No            string      `json:"no"`
		EffectiveDate string      `json:"effectiveDate"`
		Mid           json.Number `json:"mid"`
	} `json:"rates"`
}

// MidBefore returns the table A mid rate of code from the last business day before
// taxPoint: it asks for taxPoint minus one day and walks back one day at a time on HTTP 404,
// at most MaxWalkBack days. It never walks back past an unpublished table: it fails with
// ErrNotYetPublished when taxPoint minus one day is after today, or is today and today's
// table answers 404, ErrUnknownCurrency on HTTP 400 and ErrPLN for PLN (no call is made).
func (c *Client) MidBefore(ctx context.Context, code string, taxPoint time.Time) (Rate, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "PLN" {
		return Rate{}, ErrPLN
	}
	if !codeRE.MatchString(code) {
		return Rate{}, fmt.Errorf("%w %q: expected a 3-letter ISO 4217 code", ErrUnknownCurrency, code)
	}
	y, m, d := taxPoint.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	ny, nm, nd := c.now().In(taxPoint.Location()).Date()
	today := time.Date(ny, nm, nd, 0, 0, 0, 0, time.UTC)
	if day.After(today) {
		return Rate{}, fmt.Errorf("%w: %s is after today (%s)", ErrNotYetPublished, day.Format(time.DateOnly), today.Format(time.DateOnly))
	}
	for i := 0; i < MaxWalkBack; i++ {
		ds := day.AddDate(0, 0, -i).Format(time.DateOnly)
		r, found, err := c.get(ctx, code, ds)
		if err != nil {
			return Rate{}, err
		}
		if found {
			return r, nil
		}
		if i == 0 && day.Equal(today) {
			// Today's table is published around noon. A 404 here usually means "not yet", and
			// walking back would freeze an older day's rate into the invoice, so refuse.
			return Rate{}, fmt.Errorf("%w: NBP has no table A for %s yet (it is published around noon on business days); issue later or set --sale-date/--issue-date", ErrNotYetPublished, ds)
		}
	}
	return Rate{}, fmt.Errorf("%w for %s: no table A in the %d days before %s", ErrNoRate, code, MaxWalkBack, day.AddDate(0, 0, 1).Format(time.DateOnly))
}

func (c *Client) get(ctx context.Context, code, date string) (Rate, bool, error) {
	var out response
	u := c.base + "/api/exchangerates/rates/A/" + code + "/" + date + "/?format=json"
	status, body, err := httpx.GetJSON(ctx, c.hc, u, &out)
	if err != nil {
		return Rate{}, false, fmt.Errorf("nbp: %w", err)
	}
	switch {
	case status == http.StatusNotFound:
		return Rate{}, false, nil
	case status == http.StatusBadRequest:
		return Rate{}, false, fmt.Errorf("%w %q: NBP answered 400 (%s)", ErrUnknownCurrency, code, snippet(body))
	case status != http.StatusOK:
		return Rate{}, false, fmt.Errorf("nbp: unexpected HTTP %d for %s on %s (%s)", status, code, date, snippet(body))
	}
	if len(out.Rates) != 1 || out.Rates[0].Mid == "" {
		return Rate{}, false, fmt.Errorf("nbp: no rate in the answer for %s on %s", code, date)
	}
	r := out.Rates[0]
	mid := r.Mid.String()
	micro, err := money.ParseMicro(mid)
	if err != nil {
		return Rate{}, false, fmt.Errorf("nbp: %w", err)
	}
	if micro <= 0 {
		return Rate{}, false, fmt.Errorf("nbp: non-positive rate %q for %s", mid, code)
	}
	return Rate{Currency: code, Table: r.No, EffectiveDate: r.EffectiveDate, Mid: mid, MidMicro: micro}, true, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
