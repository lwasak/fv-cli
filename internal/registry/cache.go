package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/lwasak/fv-cli/internal/fsafe"
)

const (
	cacheFile     = "parties.json"
	cacheLockFile = "parties.lock"
	cacheLockWait = 10 * time.Second
)

type cacheDoc struct {
	Schema  int                   `json:"schema"`
	Entries map[string]CacheEntry `json:"entries"`
}

func (r *Resolver) cachePath() string { return filepath.Join(r.dir, cacheFile) }

// load reads parties.json. A missing file is an empty cache; an unreadable one is logged
// and treated as empty (it is rewritten on the next save).
func (r *Resolver) load() cacheDoc {
	doc := cacheDoc{Schema: 1, Entries: map[string]CacheEntry{}}
	b, err := os.ReadFile(r.cachePath()) // #nosec G304 -- fv's own data dir
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.log.Warn("party cache unreadable", "error", err.Error())
		}
		return doc
	}
	var got cacheDoc
	if err := json.Unmarshal(b, &got); err != nil || got.Entries == nil {
		r.log.Warn("party cache is not valid JSON; ignoring it")
		return doc
	}
	return got
}

// Cached returns the cache entry for key.
func (r *Resolver) Cached(key string) (*CacheEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.load().Entries[key]
	if !ok {
		return nil, false
	}
	return &e, true
}

func (r *Resolver) save(e CacheEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := fsafe.MkdirAll(r.dir); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	// The read-modify-write is held under an OS lock so concurrent fv processes do not
	// lose each other's entries.
	unlock, err := fsafe.Lock(filepath.Join(r.dir, cacheLockFile), cacheLockWait)
	if err != nil {
		return fmt.Errorf("lock party cache: %w", err)
	}
	defer func() { _ = unlock() }()
	doc := r.load()
	doc.Entries[e.Key] = e
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode party cache: %w", err)
	}
	if err := fsafe.WriteFile(r.cachePath(), append(b, '\n')); err != nil {
		return fmt.Errorf("write party cache: %w", err)
	}
	return nil
}
