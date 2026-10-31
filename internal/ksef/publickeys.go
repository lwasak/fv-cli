package ksef

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/lwasak/fv-cli/internal/fsafe"
)

// Usages of the Ministry of Finance public-key certificates.
const (
	UsageKsefTokenEncryption    = "KsefTokenEncryption" // #nosec G101 -- certificate usage name from the KSeF API, not a credential
	UsageSymmetricKeyEncryption = "SymmetricKeyEncryption"
)

// publicKeyTTL is how long the cached list is reused.
const publicKeyTTL = 24 * time.Hour

// publicKeyEntry is one element of GET /security/public-key-certificates.
type publicKeyEntry struct {
	Certificate string    `json:"certificate"` // base64 DER
	PublicKeyID string    `json:"publicKeyId,omitempty"`
	ValidFrom   time.Time `json:"validFrom"`
	ValidTo     time.Time `json:"validTo"`
	Usage       []string  `json:"usage"`
}

// publicKeyCache is the file stored in the data directory (public data).
type publicKeyCache struct {
	FetchedAt    time.Time        `json:"fetchedAt"`
	Certificates []publicKeyEntry `json:"certificates"`
}

func (c *Client) cachePath() string {
	return filepath.Join(c.dataDir, "ksef", string(c.env), "public-key-certificates.json")
}

// PublicKeyFor returns a Ministry of Finance certificate usable for usage right now: both
// its JSON window and its X.509 validity contain the current time and its usage list names
// usage. The list is cached for 24 h per environment. A corrupt or stale cache, or a cache
// without a valid entry, is replaced by one fresh fetch; if that has none either, it is an
// error.
func (c *Client) PublicKeyFor(ctx context.Context, usage string) (*x509.Certificate, error) {
	cert, _, err := c.publicKey(ctx, usage)
	return cert, err
}

// SymmetricKeyCert returns the current SymmetricKeyEncryption certificate and its
// publicKeyId (empty when the cache entry or the listing carries none). Same caching and
// validity rules as PublicKeyFor.
func (c *Client) SymmetricKeyCert(ctx context.Context) (*x509.Certificate, string, error) {
	return c.publicKey(ctx, UsageSymmetricKeyEncryption)
}

func (c *Client) publicKey(ctx context.Context, usage string) (*x509.Certificate, string, error) {
	now := c.now()
	if cache, ok := c.readCache(now); ok {
		if cert, id := pick(cache.Certificates, usage, now); cert != nil {
			return cert, id, nil
		}
	}
	cache, err := c.fetchPublicKeys(ctx)
	if err != nil {
		return nil, "", err
	}
	if cert, id := pick(cache.Certificates, usage, now); cert != nil {
		return cert, id, nil
	}
	return nil, "", fmt.Errorf("ksef: KSeF %s offers no currently valid public-key certificate for %s", c.env, usage)
}

// readCache returns the cache when it exists, parses and is younger than 24 h.
func (c *Client) readCache(now time.Time) (*publicKeyCache, bool) {
	if c.dataDir == "" {
		return nil, false
	}
	data, err := os.ReadFile(c.cachePath())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.log.Warn("ksef public-key cache unreadable", "error", err)
		}
		return nil, false
	}
	var cache publicKeyCache
	if err := json.Unmarshal(data, &cache); err != nil {
		c.log.Warn("ksef public-key cache corrupt; refetching")
		return nil, false
	}
	age := now.Sub(cache.FetchedAt)
	if age < 0 || age > publicKeyTTL {
		return nil, false
	}
	return &cache, true
}

func (c *Client) fetchPublicKeys(ctx context.Context) (*publicKeyCache, error) {
	resp, err := c.do(ctx, "GET", "/security/public-key-certificates", "", nil, "")
	if err != nil {
		return nil, err
	}
	var entries []publicKeyEntry
	if err := json.Unmarshal(resp.Body, &entries); err != nil {
		return nil, fmt.Errorf("ksef: decode public-key certificates: %w", err)
	}
	cache := &publicKeyCache{FetchedAt: c.now().UTC(), Certificates: entries}
	if c.dataDir != "" {
		data, err := json.Marshal(cache)
		if err != nil {
			return nil, fmt.Errorf("ksef: encode public-key cache: %w", err)
		}
		if err := fsafe.MkdirAll(filepath.Dir(c.cachePath())); err != nil {
			return nil, fmt.Errorf("ksef: create cache directory: %w", err)
		}
		if err := fsafe.WriteFile(c.cachePath(), data); err != nil {
			return nil, fmt.Errorf("ksef: write public-key cache: %w", err)
		}
	}
	return cache, nil
}

// pick returns the first entry valid at now whose usage list contains usage, with its
// publicKeyId.
func pick(entries []publicKeyEntry, usage string, now time.Time) (*x509.Certificate, string) {
	for _, e := range entries {
		if !slices.Contains(e.Usage, usage) {
			continue
		}
		if now.Before(e.ValidFrom) || now.After(e.ValidTo) {
			continue
		}
		der, err := base64.StdEncoding.DecodeString(e.Certificate)
		if err != nil {
			continue
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			continue
		}
		if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
			continue
		}
		return cert, e.PublicKeyID
	}
	return nil, ""
}
