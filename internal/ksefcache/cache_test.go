package ksefcache

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/fsafe"
	"github.com/lwasak/fv-cli/internal/ksef"
)

const (
	nipA = "5252248481"
	nipB = "5260250995"
	numA = "5252248481-20261005-010020A0A242-9A"
	numB = "5260250995-20260915-0100001AF629-64"
)

var t0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func open(t *testing.T) *Cache {
	t.Helper()
	c, err := Open(t.TempDir(), config.EnvDemo, nipA, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// number builds a valid KSeF number for a date, with a distinct technical part.
func number(t *testing.T, seller, date string, n int) string {
	t.Helper()
	base := seller + "-" + date + "-" + strings.ToUpper(strings.Repeat("0", 6)) + upperHex(n, 6)
	for i := 0; i < 256; i++ {
		cand := base + "-" + upperHex(i, 2)
		if ksef.ValidateKSeFNumber(cand) == nil {
			return cand
		}
	}
	t.Fatal("no valid check digits")
	return ""
}

func upperHex(n, width int) string {
	const hex = "0123456789ABCDEF"
	b := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		b[i] = hex[n&15]
		n >>= 4
	}
	return string(b)
}

func meta(t *testing.T, seller, date, invoiceNo string, n int) ksef.InvoiceMeta {
	return ksef.InvoiceMeta{KSeFNumber: number(t, seller, strings.ReplaceAll(date, "-", ""), n), InvoiceNumber: invoiceNo,
		IssueDate: date, SellerNIP: seller, SellerName: "Dostawca " + invoiceNo, BuyerIDType: "Nip", BuyerID: nipA,
		Gross: "123.00", Net: "100.00", VAT: "23.00", Currency: "PLN", InvoiceType: "Vat", FormCode: "FA (3)"}
}

func TestOpen_RejectsBadArguments(t *testing.T) {
	for _, c := range []struct {
		env config.Env
		nip string
	}{{"prod2", nipA}, {config.EnvDemo, "123"}, {config.EnvDemo, "../../etc"}, {config.EnvDemo, ""}} {
		if _, err := Open(t.TempDir(), c.env, c.nip, nil); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

func TestMerge_AddsAndIsIdempotent(t *testing.T) {
	c := open(t)
	ms := []ksef.InvoiceMeta{meta(t, nipB, "2026-09-15", "FV/1", 1), meta(t, nipB, "2026-10-01", "FV/2", 2)}
	if err := c.Merge(context.Background(), ms); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(c.Dir(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Merge(context.Background(), ms); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(c.Dir(), "index.json"))
	if string(first) != string(second) {
		t.Errorf("merging the same metadata changed the index:\n%s\n%s", first, second)
	}
	all, err := c.InRange("", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("%v %v", all, err)
	}
	if ls, _ := c.LastSync(); !ls.Equal(t0) {
		t.Errorf("last sync %v", ls)
	}
	if all[0].Gross.String() != "123.00" || all[0].FetchedAt != t0 {
		t.Errorf("entry %+v", all[0])
	}
}

func TestMerge_UpdatesMetadataAndKeepsXMLHash(t *testing.T) {
	c := open(t)
	m := meta(t, nipB, "2026-09-15", "FV/1", 1)
	xml := []byte("<Faktura/>")
	if err := c.Merge(context.Background(), []ksef.InvoiceMeta{m}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutXML(context.Background(), m.KSeFNumber, xml, hashOf(xml)); err != nil {
		t.Fatal(err)
	}
	m.SellerName = "Nowa nazwa"
	if err := c.Merge(context.Background(), []ksef.InvoiceMeta{m}); err != nil {
		t.Fatal(err)
	}
	e, ok, err := c.ByKSeFNumber(m.KSeFNumber)
	if err != nil || !ok || e.SellerName != "Nowa nazwa" || e.XMLSHA256 != hashOf(xml) {
		t.Errorf("%+v %v %v", e, ok, err)
	}
	if got, err := c.XML(m.KSeFNumber); err != nil || string(got) != "<Faktura/>" {
		t.Errorf("xml %q %v", got, err)
	}
}

func TestMerge_InvalidNumberWritesNothing(t *testing.T) {
	c := open(t)
	bad := meta(t, nipB, "2026-09-15", "FV/1", 1)
	bad.KSeFNumber = "../../x"
	err := c.Merge(context.Background(), []ksef.InvoiceMeta{meta(t, nipB, "2026-09-15", "FV/2", 2), bad})
	if !errors.Is(err, ksef.ErrBadKSeFNumber) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(c.Dir()); !os.IsNotExist(err) {
		t.Errorf("folder created: %v", err)
	}
}

func TestInRange_FiltersAndSorts(t *testing.T) {
	c := open(t)
	ms := []ksef.InvoiceMeta{
		meta(t, nipB, "2026-10-01", "C", 3), meta(t, nipB, "2026-08-31", "A", 1), meta(t, nipB, "2026-09-15", "B", 2), meta(t, nipB, "2026-09-15", "B2", 4),
	}
	if err := c.Merge(context.Background(), ms); err != nil {
		t.Fatal(err)
	}
	got, err := c.InRange("2026-09-01", "2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range got {
		names = append(names, e.InvoiceNumber)
	}
	if strings.Join(names, ",") != "B,B2,C" {
		t.Errorf("got %v", names)
	}
	if got, _ := c.InRange("2026-09-15", "2026-09-15"); len(got) != 2 {
		t.Errorf("single day: %d", len(got))
	}
	if got, _ := c.InRange("2027-01-01", ""); len(got) != 0 {
		t.Errorf("future: %d", len(got))
	}
}

func TestByInvoiceNumber_ReturnsEverySupplierWithThatNumber(t *testing.T) {
	c := open(t)
	ms := []ksef.InvoiceMeta{meta(t, nipB, "2026-09-15", "FV/7", 1), meta(t, nipA, "2026-09-16", "FV/7", 2), meta(t, nipB, "2026-09-17", "FV/8", 3)}
	if err := c.Merge(context.Background(), ms); err != nil {
		t.Fatal(err)
	}
	got, err := c.ByInvoiceNumber("FV/7")
	if err != nil || len(got) != 2 || got[0].SellerNIP != nipB || got[1].SellerNIP != nipA {
		t.Errorf("%+v %v", got, err)
	}
	if got, _ := c.ByInvoiceNumber("fv/7"); len(got) != 0 {
		t.Error("matching must be exact")
	}
	if _, ok, _ := c.ByKSeFNumber(ms[2].KSeFNumber); !ok {
		t.Error("by KSeF number")
	}
	if _, ok, _ := c.ByKSeFNumber(numA); ok {
		t.Error("unknown number found")
	}
}

func TestXML_RoundTripAndMinimalEntry(t *testing.T) {
	c := open(t)
	xml := []byte("<?xml version=\"1.0\"?><Faktura><Fa/></Faktura>\n")
	if _, err := c.XML(numA); !errors.Is(err, ErrNotCached) {
		t.Fatalf("before: %v", err)
	}
	if err := c.PutXML(context.Background(), numA, xml, hashOf(xml)); err != nil {
		t.Fatal(err)
	}
	got, err := c.XML(numA)
	if err != nil || string(got) != string(xml) {
		t.Fatalf("%q %v", got, err)
	}
	e, ok, _ := c.ByKSeFNumber(numA)
	if !ok || e.XMLSHA256 != hashOf(xml) || e.KSeFNumber != numA {
		t.Errorf("entry %+v", e)
	}
}

func TestPutXML_RefusesWrongHashAndBadNumber(t *testing.T) {
	c := open(t)
	if err := c.PutXML(context.Background(), numA, []byte("<a/>"), hashOf([]byte("<b/>"))); !errors.Is(err, ErrHashMismatch) {
		t.Errorf("hash: %v", err)
	}
	if _, err := os.Stat(c.Dir()); !os.IsNotExist(err) {
		t.Errorf("something was written: %v", err)
	}
	for _, n := range []string{"", "../x", "5252248481-20261005-010020A0A242-00"} {
		if err := c.PutXML(context.Background(), n, []byte("<a/>"), hashOf([]byte("<a/>"))); !errors.Is(err, ksef.ErrBadKSeFNumber) {
			t.Errorf("%q: %v", n, err)
		}
		if _, err := c.XML(n); !errors.Is(err, ksef.ErrBadKSeFNumber) {
			t.Errorf("XML(%q): %v", n, err)
		}
	}
}

func TestXML_TamperedFileIsRefused(t *testing.T) {
	c := open(t)
	xml := []byte("<Faktura><P_15>100.00</P_15></Faktura>")
	if err := c.PutXML(context.Background(), numA, xml, hashOf(xml)); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(c.Dir(), "xml", numA+".xml")
	if err := os.WriteFile(p, []byte("<Faktura><P_15>1.00</P_15></Faktura>"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := c.XML(numA)
	if !errors.Is(err, ErrHashMismatch) || got != nil {
		t.Errorf("%q %v", got, err)
	}
}

func TestXML_FallsBackToTheMetadataHash(t *testing.T) {
	c := open(t)
	xml := []byte("<Faktura/>")
	m := meta(t, nipB, "2026-09-15", "FV/1", 1)
	m.InvoiceHash = hashOf(xml)
	if err := c.Merge(context.Background(), []ksef.InvoiceMeta{m}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(c.Dir(), "xml"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(c.Dir(), "xml", m.KSeFNumber+".xml")
	if err := os.WriteFile(p, xml, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := c.XML(m.KSeFNumber); err != nil || string(got) != "<Faktura/>" {
		t.Errorf("%q %v", got, err)
	}
	if err := os.WriteFile(p, []byte("<Other/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.XML(m.KSeFNumber); !errors.Is(err, ErrHashMismatch) {
		t.Errorf("tampered: %v", err)
	}
}

func TestXML_FileWithoutAnyHashIsNotTrusted(t *testing.T) {
	c := open(t)
	if err := os.MkdirAll(filepath.Join(c.Dir(), "xml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Dir(), "xml", numA+".xml"), []byte("<x/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.XML(numA); !errors.Is(err, ErrNotCached) {
		t.Errorf("err = %v", err)
	}
}

func TestCorruptIndex(t *testing.T) {
	c := open(t)
	if err := os.MkdirAll(c.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Dir(), "index.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InRange("", ""); !errors.Is(err, ErrCorrupt) {
		t.Errorf("read: %v", err)
	}
	// a merge replaces the unreadable index, because it only holds data KSeF can give again
	if err := c.Merge(context.Background(), []ksef.InvoiceMeta{meta(t, nipB, "2026-09-15", "FV/1", 1)}); err != nil {
		t.Fatal(err)
	}
	if got, err := c.InRange("", ""); err != nil || len(got) != 1 {
		t.Errorf("%v %v", got, err)
	}
}

func TestUnknownSchemaIsRefused(t *testing.T) {
	c := open(t)
	if err := os.MkdirAll(c.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"schema": 99, "entries": map[string]any{}})
	if err := os.WriteFile(filepath.Join(c.Dir(), "index.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Merge(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "schema 99") {
		t.Errorf("err = %v", err)
	}
}

func TestEnvironmentsAndNIPsNeverMix(t *testing.T) {
	dir := t.TempDir()
	mk := func(env config.Env, nip string) *Cache {
		c, err := Open(dir, env, nip, func() time.Time { return t0 })
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a := mk(config.EnvDemo, nipA)
	if err := a.Merge(context.Background(), []ksef.InvoiceMeta{meta(t, nipB, "2026-09-15", "FV/1", 1)}); err != nil {
		t.Fatal(err)
	}
	for _, other := range []*Cache{mk(config.EnvTest, nipA), mk(config.EnvDemo, nipB)} {
		if got, err := other.InRange("", ""); err != nil || len(got) != 0 {
			t.Errorf("%s: %v %v", other.Dir(), got, err)
		}
	}
	if want := filepath.Join(dir, "ksef-cache", "demo", nipA); a.Dir() != want {
		t.Errorf("dir %s, want %s", a.Dir(), want)
	}
}

func TestFilesAreUserOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are checked by fsafe.CheckPrivate through ACLs on Windows")
	}
	c := open(t)
	xml := []byte("<a/>")
	if err := c.Merge(context.Background(), []ksef.InvoiceMeta{meta(t, nipB, "2026-09-15", "FV/1", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutXML(context.Background(), numA, xml, hashOf(xml)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{c.Dir(), filepath.Join(c.Dir(), "xml"), filepath.Join(c.Dir(), "index.json"),
		filepath.Join(c.Dir(), "xml", numA+".xml"), filepath.Join(c.Dir(), "cache.lock")} {
		if err := fsafe.CheckPrivate(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestMerge_WaitsForTheLockAndTimesOut(t *testing.T) {
	c := open(t)
	if err := os.MkdirAll(c.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	unlock, err := fsafe.Lock(filepath.Join(c.Dir(), "cache.lock"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.lockTimeout = 100 * time.Millisecond
	err = c.Merge(context.Background(), []ksef.InvoiceMeta{meta(t, nipB, "2026-09-15", "FV/1", 1)})
	if !errors.Is(err, fsafe.ErrLockTimeout) {
		t.Errorf("held lock: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(c.Dir(), "index.json")); !os.IsNotExist(serr) {
		t.Errorf("index written while locked: %v", serr)
	}
	_ = unlock()
	c.lockTimeout = time.Second
	if err := c.Merge(context.Background(), []ksef.InvoiceMeta{meta(t, nipB, "2026-09-15", "FV/1", 1)}); err != nil {
		t.Errorf("after release: %v", err)
	}
}

func TestMerge_CancelledContextStopsWaiting(t *testing.T) {
	c := open(t)
	_ = os.MkdirAll(c.Dir(), 0o700)
	unlock, err := fsafe.Lock(filepath.Join(c.Dir(), "cache.lock"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Merge(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestMerge_ConcurrentWritersLoseNothing(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _ := Open(dir, config.EnvDemo, nipA, func() time.Time { return t0 })
			if err := c.Merge(context.Background(), []ksef.InvoiceMeta{meta(t, nipB, "2026-09-15", "FV/"+upperHex(i, 2), i+1)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	c, _ := Open(dir, config.EnvDemo, nipA, nil)
	if got, err := c.InRange("", ""); err != nil || len(got) != 6 {
		t.Errorf("%d entries, %v", len(got), err)
	}
}
