package ksefcache

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fsafe"
	"github.com/lwasak/fv-cli/internal/ksef"
)

const (
	dirName     = "ksef-cache"
	indexFile   = "index.json"
	lockFile    = "cache.lock"
	xmlDir      = "xml"
	indexSchema = 1
	lockWait    = 10 * time.Second
)

// Errors of the cache.
var (
	// ErrNotCached is returned by XML when the invoice XML is not (or no longer verifiably) in the cache.
	ErrNotCached = errors.New("ksefcache: the invoice is not in the cache")
	// ErrHashMismatch is returned when an XML does not match its recorded SHA-256, in the
	// cache or in the argument of PutXML.
	ErrHashMismatch = errors.New("ksefcache: the invoice XML does not match its SHA-256 hash")
	// ErrCorrupt is returned when index.json cannot be read.
	ErrCorrupt = errors.New("ksefcache: the index is unreadable")
)

var nipRE = regexp.MustCompile(`^\d{10}$`)

// Entry is one cached invoice: what KSeF's metadata query said, when fv saw it, and the
// SHA-256 of the downloaded XML (empty while only the metadata is known).
type Entry struct {
	ksef.InvoiceMeta
	// FetchedAt is when fv last saw the invoice in a KSeF answer.
	FetchedAt time.Time `json:"fetchedAt"`
	// XMLSHA256 is the Base64 SHA-256 of the cached XML file.
	XMLSHA256 string `json:"xmlSha256,omitempty"`
	// XMLFetchedAt is when the XML was downloaded.
	XMLFetchedAt time.Time `json:"xmlFetchedAt,omitzero"`
}

type index struct {
	Schema   int              `json:"schema"`
	LastSync time.Time        `json:"lastSync,omitzero"`
	Entries  map[string]Entry `json:"entries"`
}

// Cache is the cache of one environment and context NIP. It holds no open files, so any
// number of Caches (and processes) may use the same folder.
type Cache struct {
	dir         string
	now         func() time.Time
	lockTimeout time.Duration
}

// Open returns the cache of env and the 10 digit context NIP under dataDir. now nil means
// time.Now. Nothing is created until the first write.
func Open(dataDir string, env config.Env, nip string, now func() time.Time) (*Cache, error) {
	if _, err := config.ParseEnv(string(env)); err != nil {
		return nil, fmt.Errorf("ksefcache: %w", err)
	}
	if !nipRE.MatchString(nip) {
		return nil, fmt.Errorf("ksefcache: %q is not a 10 digit NIP", nip)
	}
	if now == nil {
		now = time.Now
	}
	return &Cache{dir: filepath.Join(dataDir, dirName, string(env), nip), now: now, lockTimeout: lockWait}, nil
}

// Dir is the folder of this cache.
func (c *Cache) Dir() string { return c.dir }

func (c *Cache) indexPath() string { return filepath.Join(c.dir, indexFile) }

func (c *Cache) xmlPath(ksefNumber string) (string, error) {
	// the validated number is [0-9A-F-] only, so it cannot leave the folder
	if err := ksef.ValidateKSeFNumber(ksefNumber); err != nil {
		return "", err
	}
	return filepath.Join(c.dir, xmlDir, ksefNumber+".xml"), nil
}

// load reads the index; a missing file is an empty index.
func (c *Cache) load() (*index, error) {
	idx := &index{Schema: indexSchema, Entries: map[string]Entry{}}
	b, err := os.ReadFile(c.indexPath())
	if errors.Is(err, fs.ErrNotExist) {
		return idx, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ksefcache: %w", err)
	}
	var got index
	if err := json.Unmarshal(b, &got); err != nil {
		return nil, fmt.Errorf("%w: %s: %v (delete the folder %s to rebuild it with fv list)", ErrCorrupt, c.indexPath(), err, c.dir)
	}
	if got.Schema != indexSchema {
		return nil, fmt.Errorf("ksefcache: %s has schema %d, this fv reads %d", c.indexPath(), got.Schema, indexSchema)
	}
	if got.Entries == nil {
		got.Entries = map[string]Entry{}
	}
	return &got, nil
}

func (c *Cache) save(idx *index) error {
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("ksefcache: %w", err)
	}
	return fsafe.WriteFile(c.indexPath(), append(b, '\n'))
}

// update runs fn on the index under the cache lock and writes the result.
func (c *Cache) update(ctx context.Context, recoverCorrupt bool, fn func(*index) error) error {
	if err := fsafe.MkdirAll(c.dir); err != nil {
		return fmt.Errorf("ksefcache: create cache folder: %w", err)
	}
	unlock, err := fsafe.LockContext(ctx, filepath.Join(c.dir, lockFile), c.lockTimeout)
	if err != nil {
		return fmt.Errorf("ksefcache: %w", err)
	}
	defer func() { _ = unlock() }()
	idx, err := c.load()
	if errors.Is(err, ErrCorrupt) && recoverCorrupt {
		// the index is derived from what KSeF answers; start again and let the XML files
		// without an entry be downloaded afresh
		idx, err = &index{Schema: indexSchema, Entries: map[string]Entry{}}, nil
	}
	if err != nil {
		return err
	}
	if err := fn(idx); err != nil {
		return err
	}
	return c.save(idx)
}

// Merge adds the metadata to the cache (read, merge and write under the cache lock) and
// records the sync time. An invoice already cached gets its metadata replaced and keeps
// its XML hash. A number that does not pass ValidateKSeFNumber is an error and nothing is
// written. A corrupt index is replaced.
func (c *Cache) Merge(ctx context.Context, metas []ksef.InvoiceMeta) error {
	return c.merge(ctx, metas, true)
}

// Remember is Merge for a partial answer (a query for one invoice number): the metadata is
// stored but the sync time, which says when the cache last held a full listing, stays.
func (c *Cache) Remember(ctx context.Context, metas []ksef.InvoiceMeta) error {
	return c.merge(ctx, metas, false)
}

func (c *Cache) merge(ctx context.Context, metas []ksef.InvoiceMeta, sync bool) error {
	for _, m := range metas {
		if err := ksef.ValidateKSeFNumber(m.KSeFNumber); err != nil {
			return err
		}
	}
	return c.update(ctx, true, func(idx *index) error {
		now := c.now().UTC()
		for _, m := range metas {
			e := idx.Entries[m.KSeFNumber]
			e.InvoiceMeta, e.FetchedAt = m, now
			idx.Entries[m.KSeFNumber] = e
		}
		if sync {
			idx.LastSync = now
		}
		return nil
	})
}

// sorted returns the entries that pass keep, oldest issue date first.
func (c *Cache) sorted(keep func(Entry) bool) ([]Entry, error) {
	idx, err := c.load()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range idx.Entries {
		if keep(e) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IssueDate != out[j].IssueDate {
			return out[i].IssueDate < out[j].IssueDate
		}
		return out[i].KSeFNumber < out[j].KSeFNumber
	})
	return out, nil
}

// InRange returns the entries issued from from to to (YYYY-MM-DD, inclusive), oldest first.
// An empty bound is open.
func (c *Cache) InRange(from, to string) ([]Entry, error) {
	return c.sorted(func(e Entry) bool {
		return (from == "" || e.IssueDate >= from) && (to == "" || e.IssueDate <= to)
	})
}

// ByKSeFNumber returns the entry of a KSeF number.
func (c *Cache) ByKSeFNumber(n string) (Entry, bool, error) {
	idx, err := c.load()
	if err != nil {
		return Entry{}, false, err
	}
	e, ok := idx.Entries[n]
	return e, ok, nil
}

// ByInvoiceNumber returns the entries whose invoice number (as the issuer wrote it) equals
// number. Two suppliers can use the same number, so there can be several.
func (c *Cache) ByInvoiceNumber(number string) ([]Entry, error) {
	return c.sorted(func(e Entry) bool { return e.InvoiceNumber == number })
}

// LastSync is when Merge last ran, or the zero time.
func (c *Cache) LastSync() (time.Time, error) {
	idx, err := c.load()
	if err != nil {
		return time.Time{}, err
	}
	return idx.LastSync, nil
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// PutXML stores the XML of an invoice and records its hash. hash must be the Base64
// SHA-256 of b, and equal to the invoiceHash of the cached metadata when there is one
// (ErrHashMismatch otherwise, nothing is written). An invoice without an entry gets a
// minimal one, so the XML can be verified later.
func (c *Cache) PutXML(ctx context.Context, ksefNumber string, b []byte, hash string) error {
	p, err := c.xmlPath(ksefNumber)
	if err != nil {
		return err
	}
	if got := hashOf(b); got != hash {
		return fmt.Errorf("%w: %s", ErrHashMismatch, ksefNumber)
	}
	return c.update(ctx, true, func(idx *index) error {
		if known := idx.Entries[ksefNumber].InvoiceHash; known != "" && known != hash {
			return fmt.Errorf("%w: %s differs from the invoiceHash KSeF listed", ErrHashMismatch, ksefNumber)
		}
		if err := fsafe.MkdirAll(filepath.Dir(p)); err != nil {
			return fmt.Errorf("ksefcache: create xml folder: %w", err)
		}
		if err := fsafe.WriteFile(p, b); err != nil {
			return fmt.Errorf("ksefcache: %w", err)
		}
		e, ok := idx.Entries[ksefNumber]
		if !ok {
			e = Entry{InvoiceMeta: ksef.InvoiceMeta{KSeFNumber: ksefNumber}, FetchedAt: c.now().UTC()}
		}
		e.XMLSHA256, e.XMLFetchedAt = hash, c.now().UTC()
		idx.Entries[ksefNumber] = e
		return nil
	})
}

// XML returns the cached XML of an invoice after checking it against the recorded hash
// (the one of the download, else the invoiceHash of the metadata). ErrNotCached means
// there is no file or no hash to check it against; ErrHashMismatch means the file was
// changed.
func (c *Cache) XML(ksefNumber string) ([]byte, error) {
	p, err := c.xmlPath(ksefNumber)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotCached
	}
	if err != nil {
		return nil, fmt.Errorf("ksefcache: %w", err)
	}
	idx, err := c.load()
	if err != nil {
		return nil, err
	}
	e := idx.Entries[ksefNumber]
	want := e.XMLSHA256
	if want == "" {
		want = e.InvoiceHash
	}
	if want == "" {
		return nil, ErrNotCached
	}
	if hashOf(b) != want {
		return nil, fmt.Errorf("%w: %s (%s); delete the file to download it again", ErrHashMismatch, ksefNumber, p)
	}
	return b, nil
}
