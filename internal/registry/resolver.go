package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/exitcode"
	"github.com/lwasak/fv-cli/internal/httpx"
)

// Resolver resolves parties through the cache and the registries.
type Resolver struct {
	dir          string
	whiteListURL string
	viesURL      string
	client       *http.Client
	viesClient   *http.Client
	counter      Counter
	ttl          time.Duration
	now          func() time.Time
	log          *slog.Logger
	sellerNIP    string
	mu           sync.Mutex
}

// NewResolver builds a Resolver. counter may be nil (no counting); sellerNIP is sent to VIES
// as the requester; ttl is the cache lifetime (registry.cache_ttl).
func NewResolver(dataDir string, ep Endpoints, counter Counter, ttl time.Duration, now func() time.Time, log *slog.Logger, sellerNIP string) *Resolver {
	r := &Resolver{
		dir: dataDir, whiteListURL: strings.TrimRight(ep.WhiteList, "/"), viesURL: strings.TrimRight(ep.VIES, "/"),
		client: ep.Client, viesClient: ep.VIESClient, counter: counter, ttl: ttl, now: now, log: log, sellerNIP: sellerNIP,
	}
	if r.whiteListURL == "" {
		r.whiteListURL = DefaultWhiteListURL
	}
	if r.viesURL == "" {
		r.viesURL = DefaultVIESURL
	}
	if r.client == nil {
		r.client = httpx.NewClient()
	}
	if r.viesClient == nil {
		r.viesClient = r.client
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.log == nil {
		r.log = discardLogger()
	}
	return r
}

type lookup struct {
	alias, nip, vatID, nameOv, addrOv, cfgPrefix string
}

// Resolve resolves the configured party alias for an invoice issued on issue (or for the
// given purpose). Config name/address overrides win over registry data.
func (r *Resolver) Resolve(ctx context.Context, alias string, p config.Party, issue time.Time, purpose Purpose) (Party, error) {
	return r.resolve(ctx, lookup{
		alias: alias, nip: p.NIP, vatID: p.VatID, nameOv: p.Name, addrOv: p.Address,
		cfgPrefix: "parties." + alias + ".",
	}, issue, purpose)
}

// ResolveSeller resolves the seller from seller.nip; seller.name and seller.address win.
func (r *Resolver) ResolveSeller(ctx context.Context, s config.Seller, issue time.Time) (Party, error) {
	return r.resolve(ctx, lookup{
		alias: "seller", nip: s.NIP, nameOv: s.Name, addrOv: s.Address, cfgPrefix: "seller.",
	}, issue, PurposeInvoiceSeller)
}

func (r *Resolver) fresh(e *CacheEntry, purpose Purpose, qd string, now time.Time) bool {
	age := now.Sub(e.FetchedAt)
	switch purpose {
	case PurposeShow:
		return true
	case PurposeInvoiceBuyer:
		// VIES has no query date, so only the White List entry is tied to one.
		return age <= BuyerRecheck && (e.Source == SourceVIES || e.QueryDate == qd)
	case PurposeInvoiceSeller:
		return age <= r.ttl
	}
	return false // add and refresh always fetch
}

func (r *Resolver) resolve(ctx context.Context, lk lookup, issue time.Time, purpose Purpose) (Party, error) {
	if (lk.nip == "") == (lk.vatID == "") {
		return Party{}, fmt.Errorf("%s: set exactly one of nip and vat_id", strings.TrimSuffix(lk.cfgPrefix, "."))
	}
	now := r.now()
	qd := queryDate(issue, now)
	key := CacheKey(lk.nip, lk.vatID)
	entry, have := r.Cached(key)

	var warnings []string
	stale := false
	if !have || !r.fresh(entry, purpose, qd, now) {
		fe, err := r.fetch(ctx, lk, qd, now, purpose != PurposeRefresh)
		switch {
		case err == nil:
			if err := r.save(fe); err != nil {
				return Party{}, err
			}
			entry = &fe
		case errors.Is(err, ErrNotRegistered) && lk.nip != "" && lk.nameOv != "" && lk.addrOv != "" && purpose != PurposeRefresh && purpose != PurposeInvoiceSeller:
			// D1: a NIP the White List does not know (a foundation, a public body, a buyer that
			// never registered for VAT) can still be invoiced when the config gives both name
			// and address. Nothing is cached, so the next lookup asks the White List again.
			var nr *notRegisteredError
			reqID := ""
			if errors.As(err, &nr) {
				reqID = nr.requestID
			}
			entry = &CacheEntry{
				Key: key, Source: SourceConfig, FetchedAt: now, QueryDate: qd, RequestID: reqID,
				Party: Party{NIP: lk.nip, CountryCode: "PL", StatusVat: StatusNotRegistered},
			}
			warnings = append(warnings, fmt.Sprintf(
				"%s is not on the VAT White List (status %s); name and address come from the config",
				lk.nip, StatusNotRegistered))
		case errors.Is(err, errUnavailable) && have && (purpose == PurposeInvoiceBuyer || purpose == PurposeInvoiceSeller):
			stale = true
			warnings = append(warnings, fmt.Sprintf("registry unavailable, using cached data fetched %s (%s): %v",
				AgeText(now, entry.FetchedAt), entry.FetchedAt.In(Warsaw()).Format("2006-01-02 15:04"), err))
		case errors.Is(err, errUnavailable):
			return Party{}, exitcode.Wrap(exitcode.ErrRegistry, fmt.Errorf("%w; no cached copy of %s to fall back on", err, key))
		case ctx.Err() != nil:
			return Party{}, err
		default:
			return Party{}, exitcode.Wrap(exitcode.ErrRegistry, err)
		}
	}
	if purpose == PurposeShow && now.Sub(entry.FetchedAt) > r.ttl {
		stale = true
	}

	p := entry.Party
	p.AccountNumbers = append([]string(nil), p.AccountNumbers...)
	p.Alias, p.Stale = lk.alias, stale
	p.RequestID, p.ConsultationNumber = entry.RequestID, entry.ConsultationNumber
	p.FetchedAt, p.QueryDate = entry.FetchedAt, entry.QueryDate
	p.Provenance = map[string]string{}
	set := func(field, v, src string) {
		if v != "" {
			p.Provenance[field] = src
		}
	}
	set("name", p.Name, entry.Source)
	set("address", p.Address, entry.Source)
	set("statusVat", p.StatusVat, entry.Source)
	if len(p.AccountNumbers) > 0 {
		p.Provenance["accounts"] = entry.Source
	}
	if lk.nameOv != "" {
		p.Name = clean(lk.nameOv)
		p.Provenance["name"] = SourceConfig
	}
	if lk.addrOv != "" {
		p.Address = cleanAddress(lk.addrOv)
		p.Provenance["address"] = SourceConfig
	}
	if purpose != PurposeShow && purpose != PurposeRefresh {
		for _, f := range []struct{ field, val string }{{"name", p.Name}, {"address", p.Address}} {
			if f.val == "" {
				return Party{}, exitcode.Wrap(exitcode.ErrRegistry, fmt.Errorf(
					"the registry has no %s for %s (%s); set %s%s in the config",
					f.field, lk.alias, key, lk.cfgPrefix, f.field))
			}
		}
	}
	p.Warnings = warnings
	if purpose != PurposeInvoiceSeller && entry.Source == SourceWhiteList && p.StatusVat != "Czynny" {
		st := p.StatusVat
		if st == "" {
			st = "unknown"
		}
		p.Warnings = append(p.Warnings, fmt.Sprintf("VAT status of %s is %s, not Czynny (checked %s)",
			p.Name, st, p.FetchedAt.In(Warsaw()).Format("2006-01-02 15:04")))
	}
	return p, nil
}

func (r *Resolver) fetch(ctx context.Context, lk lookup, qd string, now time.Time, essential bool) (CacheEntry, error) {
	if lk.vatID != "" {
		return r.fetchVIES(ctx, lk.vatID)
	}
	if r.counter != nil {
		day := now.In(Warsaw()).Format("2006-01-02")
		n, err := r.counter.WhiteListCall(day, essential)
		if err != nil {
			if errors.Is(err, ErrQuota) || (!essential && n >= DailyLimit) {
				return CacheEntry{}, fmt.Errorf("%w: %d White List calls already counted for %s (limit %d for refreshes); try again tomorrow",
					ErrQuota, n, day, DailyLimit)
			}
			return CacheEntry{}, fmt.Errorf("White List call counter: %w", err)
		}
	}
	e, err := r.fetchWhiteList(ctx, lk.nip, qd)
	if err == nil {
		r.log.Debug("white list lookup", "nip", lk.nip, "date", qd, "requestId", e.RequestID)
	}
	return e, err
}

// AgeText renders how long ago t was, for example "3 d ago".
func AgeText(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d d ago", int(d.Hours()/24))
}
