package cli

import (
	"errors"
	"log/slog"
	"time"

	"github.com/lwasak/fv-cli/internal/registry"
	"github.com/lwasak/fv-cli/internal/store"
)

// defaultCacheTTLDays is registry.cache_ttl when the config does not set it.
const defaultCacheTTLDays = 30

// whiteListCounter returns the per-day White List call counter for a run: the store, which
// keeps the count in state.json under the archive lock. It is a package-level hook so tests
// can inject a fake.
var whiteListCounter = func(s *session, rc *runContext) registry.Counter {
	return quotaCounter{st: s.store(rc)}
}

// quotaCounter adapts the store's counter to registry.Counter: a store.ErrQuota refusal is
// reported as registry.ErrQuota (keeping the store's message), which the resolver maps to exit 2.
type quotaCounter struct{ st *store.Store }

func (q quotaCounter) WhiteListCall(day string, essential bool) (int, error) {
	n, err := q.st.WhiteListCall(day, essential)
	if err != nil && errors.Is(err, store.ErrQuota) && !errors.Is(err, registry.ErrQuota) {
		err = &quotaError{err}
	}
	return n, err
}

// quotaError prints as the store's refusal and matches both store.ErrQuota (via Unwrap) and
// registry.ErrQuota.
type quotaError struct{ err error }

func (e *quotaError) Error() string        { return e.err.Error() }
func (e *quotaError) Unwrap() error        { return e.err }
func (e *quotaError) Is(target error) bool { return target == registry.ErrQuota }

// registry builds the party resolver for a run: data dir, cache TTL, seller NIP (VIES
// requester) and the White List and VIES endpoints (fixed URLs unless Deps.Endpoints is set).
func (s *session) registry(rc *runContext, l *slog.Logger, counter registry.Counter) *registry.Resolver {
	wlURL, wlClient := s.endpoint("whitelist", registry.DefaultWhiteListURL)
	viesURL, viesClient := s.endpoint("vies", registry.DefaultVIESURL)
	days := defaultCacheTTLDays
	if rc.Config.Registry.CacheTTL != nil {
		days = *rc.Config.Registry.CacheTTL
	}
	return registry.NewResolver(rc.Paths.DataDir, registry.Endpoints{
		WhiteList: wlURL, VIES: viesURL, Client: wlClient, VIESClient: viesClient,
	}, counter, time.Duration(days)*24*time.Hour, s.deps.now, l, rc.Config.Seller.NIP)
}
