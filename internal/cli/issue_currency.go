package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lwasak/fv-cli/internal/exitcode"
	"github.com/lwasak/fv-cli/internal/nbp"
	"github.com/lwasak/fv-cli/internal/store"
)

// exchangeFor returns the NBP table A mid rate for a foreign-currency invoice, or nil for PLN
// (which makes no NBP request). The rate is the last business day before the tax point (the
// sale date, or the issue date when it is earlier). Input problems (unknown currency, no rate
// within ten days, a tax point whose rate is not published yet) are usage errors (exit 1); a
// failed NBP call is a registry-style failure (exit 2).
func (s *session) exchangeFor(ctx context.Context, _ *runContext, currency string, issue, sale time.Time) (*store.Exchange, error) {
	if currency == "" || currency == "PLN" {
		return nil, nil
	}
	base, hc := s.endpoint("nbp", nbp.BaseURL)
	taxPoint := nbp.TaxPoint(issue, sale)
	r, err := nbp.New(base, hc, s.deps.now).MidBefore(ctx, currency, taxPoint)
	switch {
	case err == nil:
		return &store.Exchange{Currency: r.Currency, Table: r.Table, EffectiveDate: r.EffectiveDate, Mid: r.Mid, MidMicro: r.MidMicro}, nil
	case errors.Is(err, nbp.ErrUnknownCurrency):
		return nil, fmt.Errorf("currency %s: NBP table A has no such currency (%w); check the code or issue in PLN", currency, err)
	case errors.Is(err, nbp.ErrNoRate):
		return nil, fmt.Errorf("%w; check the dates or the currency %s, or issue in PLN", err, currency)
	case errors.Is(err, nbp.ErrNotYetPublished):
		return nil, fmt.Errorf("%w (tax point %s: the rate comes from the last table before it)", err, taxPoint.Format(time.DateOnly))
	}
	return nil, exitcode.Wrap(exitcode.ErrRegistry, fmt.Errorf("NBP exchange rate lookup failed: %w", err))
}
