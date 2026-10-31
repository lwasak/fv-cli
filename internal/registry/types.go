// Package registry resolves party data from the official registries by tax number only:
// the MF VAT White List for Polish NIPs and EU VIES for EU VAT numbers. Results are cached
// in <data>/parties.json.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Fixed production hosts. They are constants on purpose: no flag, config key or environment
// variable can change them (tests use Endpoints).
const (
	DefaultWhiteListURL = "https://wl-api.mf.gov.pl"
	DefaultVIESURL      = "https://ec.europa.eu/taxation_customs/vies/rest-api"
)

// DailyLimit is the number of White List calls per day at which non-essential calls are
// refused. MF's own limit is 100.
const DailyLimit = 90

// BuyerRecheck is how old a buyer's cached entry may be when used for an invoice.
const BuyerRecheck = 24 * time.Hour

// Sources named in Party.Provenance.
const (
	SourceWhiteList = "whitelist"
	SourceVIES      = "vies"
	SourceConfig    = "config"
)

// Errors. Both are wrapped with exitcode.ErrRegistry when surfaced, so they map to exit 2.
var (
	ErrNotRegistered = errors.New("not registered")
	ErrQuota         = errors.New("White List daily quota reached")

	// errUnavailable marks a failure that a cached copy may paper over: network error,
	// timeout, 5xx, 429 or a registry-side outage.
	errUnavailable = errors.New("registry unavailable")
)

// notRegisteredError is the White List's "no such subject" answer. It keeps the requestId so
// an invoice built from config overrides can still cite the lookup.
type notRegisteredError struct {
	msg       string
	requestID string
}

func (e *notRegisteredError) Error() string { return e.msg }
func (e *notRegisteredError) Unwrap() error { return ErrNotRegistered }

// StatusNotRegistered is the statusVat shown for a NIP the White List does not know.
const StatusNotRegistered = "Niezarejestrowany"

// Counter counts White List calls per day. WhiteListCall increments the counter for day and
// returns the new count; it must return an error wrapping ErrQuota (or return a count at or
// above DailyLimit with any error) to refuse a non-essential call.
type Counter interface {
	WhiteListCall(day string, essential bool) (int, error)
}

// Purpose says why a party is resolved; it decides when the cache is good enough.
type Purpose int

// Purposes.
const (
	PurposeInvoiceBuyer Purpose = iota + 1
	PurposeInvoiceSeller
	PurposeAdd
	PurposeRefresh
	PurposeShow
)

// Party is a resolved party with provenance.
type Party struct {
	Alias string `json:"-"`
	NIP   string `json:"nip,omitempty"`
	VatID string `json:"vatId,omitempty"`
	// EUCode is the VIES prefix (EL for Greece); VatNumber is the number without it.
	EUCode      string `json:"euCode,omitempty"`
	VatNumber   string `json:"vatNumber,omitempty"`
	CountryCode string `json:"countryCode,omitempty"` // ISO 3166 (GR for Greece)

	Name           string   `json:"name,omitempty"`
	Address        string   `json:"address,omitempty"`
	StatusVat      string   `json:"statusVat,omitempty"`
	AccountNumbers []string `json:"accountNumbers,omitempty"`

	RequestID          string    `json:"-"`
	ConsultationNumber string    `json:"-"`
	FetchedAt          time.Time `json:"-"`
	QueryDate          string    `json:"-"`

	// Provenance maps "name", "address", "statusVat", "accounts" to "whitelist", "vies" or "config".
	Provenance map[string]string `json:"-"`
	// Stale is true when the data is older than the cache TTL (show) or was served from the
	// cache because the registry could not be reached.
	Stale    bool     `json:"-"`
	Warnings []string `json:"-"`
}

// Endpoints locates the registries. Zero values mean the fixed production URLs and a
// hardened httpx client.
type Endpoints struct {
	WhiteList string
	VIES      string
	Client    *http.Client
	// VIESClient is used for VIES when set, else Client.
	VIESClient *http.Client
}

// CacheEntry is one record of parties.json.
type CacheEntry struct {
	Key                string          `json:"key"`
	Source             string          `json:"source"`
	Raw                json.RawMessage `json:"raw"`
	Party              Party           `json:"party"`
	RequestID          string          `json:"requestId,omitempty"`
	ConsultationNumber string          `json:"consultationNumber,omitempty"`
	FetchedAt          time.Time       `json:"fetchedAt"`
	QueryDate          string          `json:"queryDate,omitempty"`
}

// CacheKey is the cache key of a party: "nip:<nip>" or "vat:<ID>".
func CacheKey(nip, vatID string) string {
	if nip != "" {
		return "nip:" + nip
	}
	return "vat:" + strings.ToUpper(strings.ReplaceAll(vatID, " ", ""))
}

// NormalizeAccount returns an account number in the form the White List uses: a PL IBAN as
// 26 digits (no prefix, no spaces); any other IBAN uppercase without spaces.
func NormalizeAccount(iban string) string {
	v := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(iban))
	if strings.HasPrefix(v, "PL") && len(v) == 28 {
		return v[2:]
	}
	return v
}

// AccountOnWhiteList reports whether iban is one of p's White List account numbers. known is
// false when p has no account list to compare with (VIES party or none published).
func AccountOnWhiteList(p Party, iban string) (found, known bool) {
	if len(p.AccountNumbers) == 0 {
		return false, false
	}
	want := NormalizeAccount(iban)
	for _, a := range p.AccountNumbers {
		if NormalizeAccount(a) == want {
			return true, true
		}
	}
	return false, true
}

// AccountWarning returns the warning for a bank account that is missing from the party's
// White List accounts, or "" when it is listed or cannot be checked.
func AccountWarning(p Party, iban string) string {
	if found, known := AccountOnWhiteList(p, iban); known && !found {
		return fmt.Sprintf("bank account %s is not in the White List accounts of %s", NormalizeAccount(iban), p.Name)
	}
	return ""
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(nopWriter{}, nil)) }

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
