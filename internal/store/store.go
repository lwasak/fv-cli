// Package store is fv's local archive: invoice numbering counters in state.json and one
// folder per invoice holding invoice.xml, meta.json and upo.xml. Every mutation runs under
// an OS lock on <data>/state.lock, so parallel fv processes never share a number.
package store

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
	"sort"
	"strings"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fsafe"
	"github.com/lwasak/fv-cli/internal/numbering"
)

// Errors.
var (
	ErrIllegalTransition = errors.New("illegal state transition")
	ErrImmutable         = errors.New("invoice.xml is immutable once the invoice is being sent")
	ErrNotFound          = errors.New("invoice not found")
	// ErrInvalidNumber is wrapped when a number cannot be an archive folder name, so fv never issued it.
	ErrInvalidNumber = errors.New("invalid invoice number")
	ErrLockTimeout   = errors.New("another fv process holds the archive lock")
	ErrQuota         = errors.New("White List daily quota reached")
	ErrHashMismatch  = errors.New("invoice.xml does not match the hash in meta.json")
)

const (
	stateSchema     = 1
	defaultLockWait = 10 * time.Second
	whiteListLimit  = 90
	stateFile       = "state.json"
	lockFile        = "state.lock"
	xmlFile         = "invoice.xml"
	metaFile        = "meta.json"
	upoFile         = "upo.xml"
	invoicesDirName = "invoices"
)

// Store is the archive under one data directory. It holds no open files, so any number
// of Stores (and processes) may use the same directory.
type Store struct {
	dataDir     string
	now         func() time.Time
	lockTimeout time.Duration
	ctx         context.Context
}

// WithContext returns a Store whose lock waits end when ctx is cancelled (Ctrl-C).
func (s *Store) WithContext(ctx context.Context) *Store {
	c := *s
	c.ctx = ctx
	return &c
}

// Open returns the Store for dataDir. now nil means time.Now.
func Open(dataDir string, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{dataDir: dataDir, now: now, lockTimeout: defaultLockWait}
}

// Reservation is a reserved invoice number.
type Reservation struct {
	Env        config.Env
	Number     string
	Seq        int
	CounterKey string
	Dir        string
}

type counter struct {
	Last int `json:"last"`
}

type whiteListState struct {
	Day   string `json:"day"`
	Calls int    `json:"calls"`
}

type stateDoc struct {
	Schema    int                           `json:"schema"`
	Counters  map[string]map[string]counter `json:"counters"`
	WhiteList whiteListState                `json:"whitelist"`
}

func (s *Store) statePath() string { return filepath.Join(s.dataDir, stateFile) }

func (s *Store) envDir(env config.Env) string {
	return filepath.Join(s.dataDir, invoicesDirName, string(env))
}

// withLock runs fn holding the archive lock.
func (s *Store) withLock(fn func() error) error {
	if err := fsafe.MkdirAll(s.dataDir); err != nil {
		return fmt.Errorf("store: create data dir: %w", err)
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	unlock, err := fsafe.LockContext(ctx, filepath.Join(s.dataDir, lockFile), s.lockTimeout)
	if err != nil {
		if errors.Is(err, fsafe.ErrLockTimeout) {
			return fmt.Errorf("%w: %w", ErrLockTimeout, err)
		}
		return fmt.Errorf("store: %w", err)
	}
	defer func() { _ = unlock() }()
	return fn()
}

func (s *Store) readState() (*stateDoc, error) {
	st := &stateDoc{Schema: stateSchema, Counters: map[string]map[string]counter{}}
	b, err := os.ReadFile(s.statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read state.json: %w", err)
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("store: state.json is corrupt: %w", err)
	}
	if st.Schema != stateSchema {
		return nil, fmt.Errorf("store: state.json has schema %d, this fv understands %d", st.Schema, stateSchema)
	}
	if st.Counters == nil {
		st.Counters = map[string]map[string]counter{}
	}
	return st, nil
}

func (s *Store) writeState(st *stateDoc) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := fsafe.WriteFile(s.statePath(), append(b, '\n')); err != nil {
		return fmt.Errorf("store: write state.json: %w", err)
	}
	return nil
}

func (st *stateDoc) last(env config.Env, key string) int {
	return st.Counters[string(env)][key].Last
}

func (st *stateDoc) setLast(env config.Env, key string, n int) {
	m := st.Counters[string(env)]
	if m == nil {
		m = map[string]counter{}
		st.Counters[string(env)] = m
	}
	m[key] = counter{Last: n}
}

func checkEnv(env config.Env) error {
	_, err := config.ParseEnv(string(env))
	return err
}

// folderName turns a number into its folder name, refusing anything that could leave the
// archive directory.
func folderName(number string) (string, error) {
	n := numbering.Sanitize(number)
	if n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\:`) || strings.ContainsRune(n, 0) {
		return "", fmt.Errorf("%w %q", ErrInvalidNumber, number)
	}
	return n, nil
}

func (s *Store) invoiceDir(env config.Env, issueYear int, number string) (string, error) {
	n, err := folderName(number)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.envDir(env), fmt.Sprintf("%04d", issueYear), n), nil
}

// Reserve takes the next number of the counter for env and the issue date's period, and
// writes a "reserved" meta.json before releasing the lock, so no number is ever without a
// folder. pattern must already be validated; reset must fit it.
func (s *Store) Reserve(ctx context.Context, env config.Env, pattern string, reset numbering.Reset, issue time.Time) (Reservation, error) {
	var res Reservation
	if err := checkEnv(env); err != nil {
		return res, err
	}
	if err := numbering.CheckResetFits(pattern, reset); err != nil {
		return res, err
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	key := numbering.PeriodKey(reset, issue)
	err := s.withLock(func() error {
		st, err := s.readState()
		if err != nil {
			return err
		}
		seq := st.last(env, key) + 1
		number, err := numbering.Render(pattern, issue, seq)
		if err != nil {
			return err
		}
		dir, err := s.invoiceDir(env, issue.Year(), number)
		if err != nil {
			return err
		}
		if existing, err := readMeta(dir); err == nil {
			// An orphan left by a crash between meta and counter write is taken over;
			// anything else is a number collision that must not be overwritten.
			if existing.State != StateReserved || existing.Seq != seq || existing.CounterKey != key {
				return fmt.Errorf("store: invoice number %s already exists in %s (state %s); check numbering.pattern and numbering.reset", number, env, existing.State)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		} else if _, serr := os.Stat(dir); serr == nil {
			return fmt.Errorf("store: folder %s exists without a readable meta.json", dir)
		}
		if err := fsafe.MkdirAll(dir); err != nil {
			return fmt.Errorf("store: create invoice folder: %w", err)
		}
		now := s.now()
		m := &Meta{
			Schema: metaSchema, Number: number, Env: env, State: StateReserved, Seq: seq, CounterKey: key,
			IssueDate: issue.Format("2006-01-02"), CreatedAt: now, UpdatedAt: now,
			History: []Transition{{To: StateReserved, At: now}},
		}
		if err := writeMeta(dir, m); err != nil {
			return err
		}
		st.setLast(env, key, seq)
		if err := s.writeState(st); err != nil {
			_ = os.RemoveAll(dir)
			return err
		}
		res = Reservation{Env: env, Number: number, Seq: seq, CounterKey: key, Dir: dir}
		return nil
	})
	return res, err
}

func readMeta(dir string) (*Meta, error) {
	b, err := os.ReadFile(filepath.Join(dir, metaFile)) // #nosec G304 -- dir is resolved inside the archive and validated by the store
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("store: %s is corrupt: %w", filepath.Join(dir, metaFile), err)
	}
	if m.Schema != metaSchema {
		return nil, fmt.Errorf("store: %s has schema %d, this fv understands %d", filepath.Join(dir, metaFile), m.Schema, metaSchema)
	}
	return &m, nil
}

func writeMeta(dir string, m *Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := fsafe.WriteFile(filepath.Join(dir, metaFile), append(b, '\n')); err != nil {
		return fmt.Errorf("store: write meta.json: %w", err)
	}
	return nil
}

// findDir locates the folder of number in env by scanning the year folders.
func (s *Store) findDir(env config.Env, number string) (string, error) {
	if err := checkEnv(env); err != nil {
		return "", err
	}
	name, err := folderName(number)
	if err != nil {
		return "", err
	}
	years, err := os.ReadDir(s.envDir(env))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s in %s", ErrNotFound, number, env)
		}
		return "", fmt.Errorf("store: %w", err)
	}
	for _, y := range years {
		if !y.IsDir() {
			continue
		}
		dir := filepath.Join(s.envDir(env), y.Name(), name)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("%w: %s in %s", ErrNotFound, number, env)
}

// Load reads the meta.json of number in env.
func (s *Store) Load(env config.Env, number string) (*Meta, error) {
	dir, err := s.findDir(env, number)
	if err != nil {
		return nil, err
	}
	return readMeta(dir)
}

// FindByKSeFNumber returns the invoice of env that KSeF numbered ksefNumber, by scanning
// the archive. ErrNotFound when there is none.
func (s *Store) FindByKSeFNumber(env config.Env, ksefNumber string) (*Meta, error) {
	if err := checkEnv(env); err != nil {
		return nil, err
	}
	if ksefNumber == "" {
		return nil, fmt.Errorf("%w: no KSeF number given", ErrNotFound)
	}
	metas, _, err := s.ListWithWarnings(Filter{Env: &env})
	if err != nil {
		return nil, err
	}
	for i := range metas {
		if metas[i].KSeF != nil && metas[i].KSeF.KSeFNumber == ksefNumber {
			return &metas[i], nil
		}
	}
	return nil, fmt.Errorf("%w: KSeF number %s in %s", ErrNotFound, ksefNumber, env)
}

// Locate finds number in prefer first, then in the other environments. When prefer does
// not hold it and several other environments do, the error lists them.
func (s *Store) Locate(number string, prefer config.Env) (*Meta, error) {
	if prefer != "" {
		if m, err := s.Load(prefer, number); err == nil {
			return m, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	var found []*Meta
	for _, env := range []config.Env{config.EnvTest, config.EnvDemo, config.EnvProd} {
		if env == prefer {
			continue
		}
		m, err := s.Load(env, number)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		found = append(found, m)
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, number)
	case 1:
		return found[0], nil
	}
	envs := make([]string, len(found))
	for i, m := range found {
		envs[i] = string(m.Env)
	}
	return nil, fmt.Errorf("invoice %s exists in several environments (%s); pass --env", number, strings.Join(envs, ", "))
}

func hashB64(b []byte) string {
	h := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(h[:])
}

// SaveDraft stores invoice.xml and the meta of a reserved (or already drafted) number and
// moves it to draft. m supplies the invoice content; identity fields, history and KSeF
// references stay as stored. It refuses with ErrImmutable once the invoice is sending or
// later, and with ErrIllegalTransition for a void-draft.
func (s *Store) SaveDraft(env config.Env, number string, xml []byte, m Meta) error {
	return s.withLock(func() error {
		dir, err := s.findDir(env, number)
		if err != nil {
			return err
		}
		cur, err := readMeta(dir)
		if err != nil {
			return err
		}
		switch {
		case immutableXML(cur.State):
			return fmt.Errorf("%w: %s is %s", ErrImmutable, cur.Number, cur.State)
		case cur.State != StateReserved && cur.State != StateDraft:
			return fmt.Errorf("%w: %s is %s", ErrIllegalTransition, cur.Number, cur.State)
		}
		now := s.now()
		out := m
		out.Schema, out.Number, out.Env, out.Seq, out.CounterKey = metaSchema, cur.Number, cur.Env, cur.Seq, cur.CounterKey
		out.State = StateDraft
		if out.IssueDate == "" {
			out.IssueDate = cur.IssueDate
		}
		if out.CreatedAt.IsZero() {
			out.CreatedAt = cur.CreatedAt
		}
		out.UpdatedAt = now
		out.XMLSHA256, out.XMLSize = hashB64(xml), len(xml)
		out.UPOSHA256, out.KSeF = cur.UPOSHA256, cur.KSeF
		out.History = cur.History
		if cur.State != StateDraft {
			out.History = append(append([]Transition(nil), cur.History...), Transition{From: cur.State, To: StateDraft, At: now})
		}
		if err := fsafe.WriteFile(filepath.Join(dir, xmlFile), xml); err != nil {
			return fmt.Errorf("store: write invoice.xml: %w", err)
		}
		return writeMeta(dir, &out)
	})
}

// XML returns invoice.xml after checking it against the hash in meta.json.
func (s *Store) XML(env config.Env, number string) ([]byte, error) {
	dir, err := s.findDir(env, number)
	if err != nil {
		return nil, err
	}
	m, err := readMeta(dir)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, xmlFile)) // #nosec G304 -- dir is resolved inside the archive and validated by the store
	if err != nil {
		return nil, fmt.Errorf("store: read invoice.xml of %s: %w", m.Number, err)
	}
	if m.XMLSHA256 == "" || hashB64(b) != m.XMLSHA256 || len(b) != m.XMLSize {
		return nil, fmt.Errorf("%w: %s", ErrHashMismatch, m.Number)
	}
	return b, nil
}

// Transition moves number to state `to` under the lock, appends to History and sets
// UpdatedAt. mutate (may be nil) edits a copy of the meta first; its error aborts the
// transition, and it cannot change the XML hash (ErrImmutable).
func (s *Store) Transition(env config.Env, number string, to State, mutate func(*Meta) error) (*Meta, error) {
	var out *Meta
	err := s.withLock(func() error {
		dir, err := s.findDir(env, number)
		if err != nil {
			return err
		}
		cur, err := readMeta(dir)
		if err != nil {
			return err
		}
		if !canTransition(cur.State, to) {
			return fmt.Errorf("%w: %s cannot go from %s to %s", ErrIllegalTransition, cur.Number, cur.State, to)
		}
		next := *cur
		if mutate != nil {
			if err := mutate(&next); err != nil {
				return err
			}
		}
		if next.XMLSHA256 != cur.XMLSHA256 || next.XMLSize != cur.XMLSize {
			return fmt.Errorf("%w: the XML hash cannot be changed by a transition", ErrImmutable)
		}
		now := s.now()
		next.Schema, next.Number, next.Env, next.Seq, next.CounterKey = cur.Schema, cur.Number, cur.Env, cur.Seq, cur.CounterKey
		next.State = to
		next.UpdatedAt = now
		next.History = append(append([]Transition(nil), cur.History...), Transition{From: cur.State, To: to, At: now})
		if err := writeMeta(dir, &next); err != nil {
			return err
		}
		out = &next
		return nil
	})
	return out, err
}

// UpdateKSeF edits the KSeF references of an invoice that is in state sending or unknown
// without changing its state or History (for example to record the invoice reference number
// as soon as KSeF has received the invoice). mutate edits a copy; its error aborts the update.
func (s *Store) UpdateKSeF(env config.Env, number string, mutate func(*KSeFRef) error) (*Meta, error) {
	var out *Meta
	err := s.withLock(func() error {
		dir, err := s.findDir(env, number)
		if err != nil {
			return err
		}
		cur, err := readMeta(dir)
		if err != nil {
			return err
		}
		if cur.State != StateSending && cur.State != StateUnknown {
			return fmt.Errorf("%w: the KSeF references of %s (%s) are not updated in place", ErrIllegalTransition, cur.Number, cur.State)
		}
		next := *cur
		ref := KSeFRef{}
		if cur.KSeF != nil {
			ref = *cur.KSeF
		}
		if err := mutate(&ref); err != nil {
			return err
		}
		next.KSeF = &ref
		next.UpdatedAt = s.now()
		if err := writeMeta(dir, &next); err != nil {
			return err
		}
		out = &next
		return nil
	})
	return out, err
}

// WriteUPO stores upo.xml next to the invoice and records its hash in meta.json.
func (s *Store) WriteUPO(env config.Env, number string, upo []byte) error {
	return s.withLock(func() error {
		dir, err := s.findDir(env, number)
		if err != nil {
			return err
		}
		m, err := readMeta(dir)
		if err != nil {
			return err
		}
		if err := fsafe.WriteFile(filepath.Join(dir, upoFile), upo); err != nil {
			return fmt.Errorf("store: write upo.xml: %w", err)
		}
		m.UPOSHA256 = hashB64(upo)
		m.UpdatedAt = s.now()
		return writeMeta(dir, m)
	})
}

// DiscardResult says what Discard did.
type DiscardResult string

// Discard outcomes.
const (
	Released DiscardResult = "released"
	Voided   DiscardResult = "voided"
)

// Discard drops a draft. The invoice must be reserved, draft or rejected. When its number
// is the latest of its counter, the counter steps back and the folder is deleted
// (Released); otherwise the folder stays and the state becomes void-draft (Voided), so
// every gap can be explained. A rejected invoice that KSeF flagged as a duplicate (440) is
// always voided, never released: the number is already used in KSeF, so a new invoice that
// got it again would be refused again.
func (s *Store) Discard(env config.Env, number string) (DiscardResult, error) {
	var res DiscardResult
	err := s.withLock(func() error {
		dir, err := s.findDir(env, number)
		if err != nil {
			return err
		}
		m, err := readMeta(dir)
		if err != nil {
			return err
		}
		switch m.State {
		case StateReserved, StateDraft, StateRejected:
		case StateVoid:
			return fmt.Errorf("%w: %s is already void-draft", ErrIllegalTransition, m.Number)
		default:
			return fmt.Errorf("%w: %s is %s and cannot be discarded", ErrIllegalTransition, m.Number, m.State)
		}
		st, err := s.readState()
		if err != nil {
			return err
		}
		burned := m.KSeF != nil && m.KSeF.Duplicate
		if !burned && st.last(env, m.CounterKey) == m.Seq {
			st.setLast(env, m.CounterKey, m.Seq-1)
			if err := s.writeState(st); err != nil {
				return err
			}
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("store: remove %s: %w", dir, err)
			}
			res = Released
			return nil
		}
		now := s.now()
		from := m.State
		m.State, m.UpdatedAt = StateVoid, now
		m.History = append(m.History, Transition{From: from, To: StateVoid, At: now})
		if err := writeMeta(dir, m); err != nil {
			return err
		}
		res = Voided
		return nil
	})
	return res, err
}

// Filter narrows List. A nil Env means all environments; Month is YYYY-MM of the issue
// date, or empty. From and To (YYYY-MM-DD, inclusive, either may be empty) bound the issue
// date.
type Filter struct {
	Env   *config.Env
	Month string
	From  string
	To    string
}

// List returns the archived invoices sorted by environment, issue date and sequence.
//
// An invoice folder whose meta.json cannot be read (corrupt, unknown schema) is skipped, so one
// bad folder does not hide the rest; see ListWithWarnings.
func (s *Store) List(f Filter) ([]Meta, error) {
	out, _, err := s.ListWithWarnings(f)
	return out, err
}

// ListWithWarnings is List that also returns one line per skipped folder.
func (s *Store) ListWithWarnings(f Filter) ([]Meta, []string, error) {
	var warnings []string
	envs := []config.Env{config.EnvTest, config.EnvDemo, config.EnvProd}
	if f.Env != nil {
		if err := checkEnv(*f.Env); err != nil {
			return nil, nil, err
		}
		envs = []config.Env{*f.Env}
	}
	var out []Meta
	for _, env := range envs {
		years, err := os.ReadDir(s.envDir(env))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("store: %w", err)
		}
		for _, y := range years {
			if !y.IsDir() {
				continue
			}
			entries, err := os.ReadDir(filepath.Join(s.envDir(env), y.Name()))
			if err != nil {
				return nil, nil, fmt.Errorf("store: %w", err)
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				m, err := readMeta(filepath.Join(s.envDir(env), y.Name(), e.Name()))
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("skipped %s/%s/%s: %v", env, y.Name(), e.Name(), err))
					continue
				}
				if f.Month != "" && !strings.HasPrefix(m.IssueDate, f.Month) {
					continue
				}
				if (f.From != "" && m.IssueDate < f.From) || (f.To != "" && m.IssueDate > f.To) {
					continue
				}
				out = append(out, *m)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Env != b.Env {
			return a.Env < b.Env
		}
		if a.IssueDate != b.IssueDate {
			return a.IssueDate < b.IssueDate
		}
		return a.Seq < b.Seq
	})
	return out, warnings, nil
}

// WhiteListCall counts one White List request for day (YYYY-MM-DD, Warsaw) and returns
// the count including this call. The count restarts when the day changes. A non-essential
// call at or over 90 is refused with ErrQuota and not counted.
func (s *Store) WhiteListCall(day string, essential bool) (int, error) {
	var n int
	err := s.withLock(func() error {
		st, err := s.readState()
		if err != nil {
			return err
		}
		if st.WhiteList.Day != day {
			st.WhiteList = whiteListState{Day: day}
		}
		if !essential && st.WhiteList.Calls >= whiteListLimit {
			n = st.WhiteList.Calls
			return fmt.Errorf("%w: %d calls today, non-essential calls stop at %d", ErrQuota, n, whiteListLimit)
		}
		st.WhiteList.Calls++
		n = st.WhiteList.Calls
		return s.writeState(st)
	})
	return n, err
}
