package ksef

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/fsafe"
	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

var t0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

type pkEntry struct {
	Certificate string   `json:"certificate"`
	ValidFrom   string   `json:"validFrom"`
	ValidTo     string   `json:"validTo"`
	Usage       []string `json:"usage"`
}

// entry builds a public-key-certificates entry whose X.509 and JSON validity are both
// [from, to].
func entry(t *testing.T, from, to time.Time, usage ...string) (pkEntry, *credtest.Cert) {
	t.Helper()
	c := credtest.New(t, credtest.Spec{NotBefore: from, NotAfter: to})
	return pkEntry{
		Certificate: base64.StdEncoding.EncodeToString(c.DER),
		ValidFrom:   from.Format(time.RFC3339),
		ValidTo:     to.Format(time.RFC3339),
		Usage:       usage,
	}, c
}

func body(t *testing.T, es ...pkEntry) string {
	t.Helper()
	b, err := json.Marshal(es)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func goodEntry(t *testing.T) pkEntry {
	e, _ := entry(t, t0.Add(-time.Hour), t0.Add(48*time.Hour), UsageKsefTokenEncryption, UsageSymmetricKeyEncryption)
	return e
}

func clientAt(t *testing.T, f *ksefake.Fake, env config.Env, dir string, now *time.Time) *Client {
	c := newTestClient(t, f, env, dir, nil)
	c.now = func() time.Time { return *now }
	return c
}

func TestPublicKeys_CachedFor24h(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, goodEntry(t)))
	now := t0
	c := clientAt(t, f, config.EnvTest, t.TempDir(), &now)
	ctx := context.Background()
	if _, err := c.PublicKeyFor(ctx, UsageSymmetricKeyEncryption); err != nil {
		t.Fatal(err)
	}
	now = t0.Add(24*time.Hour - time.Second)
	if _, err := c.PublicKeyFor(ctx, UsageSymmetricKeyEncryption); err != nil {
		t.Fatal(err)
	}
	if n := f.Count("GET", "/security/public-key-certificates"); n != 1 {
		t.Fatalf("requests within 24h = %d, want 1", n)
	}
	// Past 24 h the cached entry would still be valid by its dates only if it lasts that long.
	now = t0.Add(24*time.Hour + time.Second)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, mustEntry(t, now)))
	if _, err := c.PublicKeyFor(ctx, UsageSymmetricKeyEncryption); err != nil {
		t.Fatal(err)
	}
	if n := f.Count("GET", "/security/public-key-certificates"); n != 2 {
		t.Fatalf("requests after 24h = %d, want 2", n)
	}
}

func mustEntry(t *testing.T, now time.Time) pkEntry {
	e, _ := entry(t, now.Add(-time.Hour), now.Add(48*time.Hour), UsageSymmetricKeyEncryption)
	return e
}

func TestPublicKeys_CacheFileUserOnly(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, goodEntry(t)))
	dir := t.TempDir()
	now := t0
	c := clientAt(t, f, config.EnvDemo, dir, &now)
	if _, err := c.PublicKeyFor(context.Background(), UsageKsefTokenEncryption); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ksef", "demo", "public-key-certificates.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file: %v", err)
	}
	if err := fsafe.CheckPrivate(path); err != nil {
		t.Errorf("cache file is not private: %v", err)
	}
}

func TestPublicKeys_SkipsExpiredAndNotYetValid(t *testing.T) {
	f := ksefake.New(t)
	expired, _ := entry(t, t0.Add(-72*time.Hour), t0.Add(-time.Hour), UsageSymmetricKeyEncryption)
	future, _ := entry(t, t0.Add(time.Hour), t0.Add(72*time.Hour), UsageSymmetricKeyEncryption)
	good, goodCert := entry(t, t0.Add(-time.Hour), t0.Add(48*time.Hour), UsageSymmetricKeyEncryption)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, expired, future, good))
	now := t0
	c := clientAt(t, f, config.EnvTest, t.TempDir(), &now)
	got, err := c.PublicKeyFor(context.Background(), UsageSymmetricKeyEncryption)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(goodCert.Cert) {
		t.Error("returned a certificate other than the currently valid one")
	}
}

func TestPublicKeys_JSONValidityAlsoChecked(t *testing.T) {
	f := ksefake.New(t)
	e := goodEntry(t)
	e.ValidTo = t0.Add(-time.Minute).Format(time.RFC3339) // X.509 still valid, JSON window over
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, e))
	now := t0
	c := clientAt(t, f, config.EnvTest, t.TempDir(), &now)
	if _, err := c.PublicKeyFor(context.Background(), UsageSymmetricKeyEncryption); err == nil {
		t.Fatal("entry outside its JSON validity was returned")
	}
}

func TestPublicKeys_WrongUsageNotReturned(t *testing.T) {
	f := ksefake.New(t)
	e, _ := entry(t, t0.Add(-time.Hour), t0.Add(48*time.Hour), UsageKsefTokenEncryption)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, e))
	now := t0
	c := clientAt(t, f, config.EnvTest, t.TempDir(), &now)
	_, err := c.PublicKeyFor(context.Background(), UsageSymmetricKeyEncryption)
	if err == nil {
		t.Fatal("certificate without the requested usage was returned")
	}
	if !strings.Contains(err.Error(), UsageSymmetricKeyEncryption) {
		t.Errorf("error %q does not name the usage", err)
	}
}

func TestPublicKeys_NoValidEntryIsError(t *testing.T) {
	f := ksefake.New(t)
	expired, _ := entry(t, t0.Add(-72*time.Hour), t0.Add(-time.Hour), UsageSymmetricKeyEncryption)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, expired))
	now := t0
	c := clientAt(t, f, config.EnvTest, t.TempDir(), &now)
	if _, err := c.PublicKeyFor(context.Background(), UsageSymmetricKeyEncryption); err == nil {
		t.Fatal("expected an error")
	}
	// One fetch, plus at most one refetch after the cache had no valid entry.
	if n := f.Count("GET", "/security/public-key-certificates"); n != 1 {
		t.Errorf("requests = %d, want 1 (fresh fetch is not refetched)", n)
	}
}

func TestPublicKeys_CachedButNoValidEntryRefetchesOnce(t *testing.T) {
	f := ksefake.New(t)
	short, _ := entry(t, t0.Add(-time.Hour), t0.Add(time.Hour), UsageSymmetricKeyEncryption)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, short))
	now := t0
	c := clientAt(t, f, config.EnvTest, t.TempDir(), &now)
	ctx := context.Background()
	if _, err := c.PublicKeyFor(ctx, UsageSymmetricKeyEncryption); err != nil {
		t.Fatal(err)
	}
	now = t0.Add(2 * time.Hour) // cached entry expired, cache itself under 24 h old
	fresh, freshCert := entry(t, now.Add(-time.Hour), now.Add(48*time.Hour), UsageSymmetricKeyEncryption)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, fresh))
	got, err := c.PublicKeyFor(ctx, UsageSymmetricKeyEncryption)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(freshCert.Cert) {
		t.Error("did not return the refetched certificate")
	}
	if n := f.Count("GET", "/security/public-key-certificates"); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
}

func TestPublicKeys_CorruptCacheRefetches(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, goodEntry(t)))
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "ksef", "test")
	if err := fsafe.MkdirAll(cacheDir); err != nil {
		t.Fatal(err)
	}
	if err := fsafe.WriteFile(filepath.Join(cacheDir, "public-key-certificates.json"), []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	now := t0
	c := clientAt(t, f, config.EnvTest, dir, &now)
	if _, err := c.PublicKeyFor(context.Background(), UsageSymmetricKeyEncryption); err != nil {
		t.Fatal(err)
	}
	if n := f.Count("GET", "/security/public-key-certificates"); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestPublicKeys_CachePerEnvironment(t *testing.T) {
	f := ksefake.New(t)
	f.Respond("GET", "/security/public-key-certificates", 200, body(t, goodEntry(t)))
	dir := t.TempDir()
	now := t0
	for _, env := range []config.Env{config.EnvTest, config.EnvDemo} {
		c := clientAt(t, f, env, dir, &now)
		if _, err := c.PublicKeyFor(context.Background(), UsageSymmetricKeyEncryption); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.Count("GET", "/security/public-key-certificates"); n != 2 {
		t.Errorf("requests = %d, want 2 (one per environment)", n)
	}
	for _, env := range []string{"test", "demo"} {
		if _, err := os.Stat(filepath.Join(dir, "ksef", env, "public-key-certificates.json")); err != nil {
			t.Errorf("no cache for %s: %v", env, err)
		}
	}
}
