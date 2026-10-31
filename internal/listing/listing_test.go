package listing

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/store"
)

func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func TestDefaultRange(t *testing.T) {
	for _, c := range []struct {
		now      time.Time
		from, to string
	}{
		{at(2026, 10, 31), "2026-08-01", "2026-10-31"},
		{at(2027, 1, 15), "2026-11-01", "2027-01-31"},
		{at(2027, 2, 10), "2026-12-01", "2027-02-28"},
		{at(2028, 2, 10), "2027-12-01", "2028-02-29"}, // leap year
		{at(2026, 3, 1), "2026-01-01", "2026-03-31"},
		{at(2026, 12, 31), "2026-10-01", "2026-12-31"},
		// 23:30 UTC on 30 Oct is already 31 Oct, and 1 Nov, in Warsaw: the month follows Warsaw
		{time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC), "2026-09-01", "2026-11-30"},
	} {
		r := DefaultRange(c.now)
		if r.From != c.from || r.To != c.to {
			t.Errorf("%s: %v, want %s..%s", c.now, r, c.from, c.to)
		}
	}
}

func TestDefaultRange_NeverExceedsTheKSeFWindow(t *testing.T) {
	for d := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC); d.Year() < 2030; d = d.AddDate(0, 0, 1) {
		r := DefaultRange(d)
		a, _ := time.Parse("2006-01-02", r.From)
		b, _ := time.Parse("2006-01-02", r.To)
		if days := int(b.Sub(a).Hours()/24) + 1; days > ksef.QueryWindowDays {
			t.Fatalf("%s: %d days", d, days)
		}
	}
}

func TestParseRange(t *testing.T) {
	now := at(2026, 10, 31)
	ok := []struct {
		from, to, month string
		offline         bool
		want            Range
	}{
		{"", "", "", false, Range{"2026-08-01", "2026-10-31"}},
		{"2026-09-10", "2026-09-20", "", false, Range{"2026-09-10", "2026-09-20"}},
		{"2026-10-01", "", "", false, Range{"2026-10-01", "2026-10-31"}},
		{"", "2026-09-30", "", false, Range{"2026-08-01", "2026-09-30"}},
		{"", "", "2026-02", false, Range{"2026-02-01", "2026-02-28"}},
		{"", "", "2028-02", false, Range{"2028-02-01", "2028-02-29"}},
		{"2026-01-01", "2026-12-31", "", false, Range{"2026-01-01", "2026-12-31"}}, // 365 days
		{"2020-01-01", "2026-10-31", "", true, Range{"2020-01-01", "2026-10-31"}},  // offline has no cap
	}
	for _, c := range ok {
		got, err := ParseRange(c.from, c.to, c.month, now, c.offline)
		if err != nil || got != c.want {
			t.Errorf("%+v: %v %v", c, got, err)
		}
	}
	bad := []struct {
		from, to, month string
		want            string
	}{
		{"2026-09-01", "", "2026-09", "--month cannot be combined"},
		{"", "2026-09-30", "2026-09", "--month cannot be combined"},
		{"", "", "2026-13", "YYYY-MM"},
		{"", "", "2026-9", "YYYY-MM"},
		{"2026-09-30", "2026-09-01", "", "is after --to"},
		{"2026-11-05", "", "", "after the default end"},
		{"", "2026-05-31", "", "before the default start"},
		{"2026-02-30", "", "", "--from"},
		{"", "tomorrow", "", "--to"},
		{"2025-01-01", "2026-01-02", "", "367 days"},
		{"2025-01-01", "2026-10-31", "", "at most 366 days"},
	}
	for _, c := range bad {
		_, err := ParseRange(c.from, c.to, c.month, now, false)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err = %v, want %q", c, err, c.want)
		}
	}
	if _, err := ParseRange("2025-01-01", "2026-01-01", "", now, false); err != nil {
		t.Errorf("exactly 366 days: %v", err)
	}
}

const own = "5252248481"

func rng() Range { return Range{From: "2026-08-01", To: "2026-10-31"} }

func localMeta(seq int, number, issue string, state store.State, ksefNo string, gross money.Amount) store.Meta {
	m := store.Meta{Number: number, Env: config.EnvDemo, State: state, Seq: seq, IssueDate: issue, Currency: "PLN", Gross: gross,
		Seller: store.PartySnapshot{NIP: own, Name: "Jan Kowalski"}, Buyer: store.PartySnapshot{NIP: "5260250995", Name: "Odbiorca"}}
	if ksefNo != "" {
		m.KSeF = &store.KSeFRef{KSeFNumber: ksefNo}
	}
	return m
}

func remoteMeta(ksefNo, number, issue, seller, buyer, gross string) ksef.InvoiceMeta {
	return ksef.InvoiceMeta{KSeFNumber: ksefNo, InvoiceNumber: number, IssueDate: issue, SellerNIP: seller, SellerName: "S " + seller,
		BuyerIDType: "Nip", BuyerID: buyer, BuyerName: "B " + buyer, Gross: json.Number(gross), Currency: "PLN", InvoiceType: "Vat"}
}

func summary(rows []Row) string {
	var o []string
	for _, r := range rows {
		o = append(o, fmt.Sprintf("%s/%s/%s/%s", r.Issued, r.Dir, r.Number, r.State))
	}
	return strings.Join(o, " ")
}

const (
	kA = "5252248481-20261005-010020A0A242-9A"
	kB = "5260250995-20260915-0100001AF629-64"
	kC = "5252248481-20260915-0100001AF629-BC"
	kD = "5252248481-20261020-0100001AF62A-00" // the checksum is not checked by Merge
)

func TestMerge_LocalDataWinsAndSaleAppearsOnce(t *testing.T) {
	local := []store.Meta{localMeta(1, "FV/2026/10/001", "2026-10-05", store.StateAccepted, kA, 12300)}
	remote := []ksef.InvoiceMeta{remoteMeta(kA, "FV/2026/10/001", "2026-10-05", own, "5260250995", "123.00")}
	rows := Merge(local, remote, own, SideAll, rng())
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	r := rows[0]
	if r.State != "accepted" || r.Local == nil || r.KSeFNumber != kA || r.Gross != 12300 || !r.GrossOK || r.Hint != "" || r.Dir != DirSale || r.BuyerName != "Odbiorca" {
		t.Errorf("row %+v", r)
	}
}

func TestMerge_DifferingGrossAddsAHintAndKeepsLocal(t *testing.T) {
	local := []store.Meta{localMeta(1, "FV/2026/10/001", "2026-10-05", store.StateAccepted, kA, 12300)}
	remote := []ksef.InvoiceMeta{remoteMeta(kA, "FV/2026/10/001", "2026-10-05", own, "5260250995", "124.00")}
	rows := Merge(local, remote, own, SideAll, rng())
	if len(rows) != 1 || rows[0].Gross != 12300 || !strings.Contains(rows[0].Hint, "124,00") || !strings.Contains(rows[0].Hint, "123,00") {
		t.Errorf("rows %+v", rows)
	}
}

func TestMerge_LocalWithKSeFNumberTheQueryMissedStays(t *testing.T) {
	local := []store.Meta{localMeta(1, "FV/2026/10/001", "2026-10-05", store.StateAccepted, kA, 12300)}
	rows := Merge(local, nil, own, SideAll, rng())
	if len(rows) != 1 || rows[0].State != "accepted" || rows[0].KSeFNumber != kA {
		t.Errorf("rows %+v", rows)
	}
}

func TestMerge_LocalWithoutKSeFNumberShowsInEveryState(t *testing.T) {
	var local []store.Meta
	for i, st := range []store.State{store.StateReserved, store.StateDraft, store.StateSending, store.StateUnknown, store.StateRejected, store.StateVoid} {
		local = append(local, localMeta(i+1, fmt.Sprintf("FV/2026/10/%03d", i+1), "2026-10-05", st, "", 100))
	}
	rows := Merge(local, nil, own, SideAll, rng())
	if len(rows) != 6 {
		t.Fatalf("%d rows", len(rows))
	}
	for i, r := range rows {
		if r.State != string(local[i].State) || r.KSeFNumber != "" || r.Hint != "" {
			t.Errorf("row %d = %+v", i, r)
		}
	}
}

func TestMerge_SendingOrUnknownMatchesItsOwnSaleByNumber(t *testing.T) {
	for _, st := range []store.State{store.StateSending, store.StateUnknown} {
		local := []store.Meta{localMeta(1, "FV/2026/10/001", "2026-10-05", st, "", 12300)}
		remote := []ksef.InvoiceMeta{remoteMeta(kA, "FV/2026/10/001", "2026-10-05", own, "5260250995", "123.00")}
		rows := Merge(local, remote, own, SideAll, rng())
		if len(rows) != 1 {
			t.Fatalf("%s: %d rows", st, len(rows))
		}
		r := rows[0]
		if r.State != string(st) || r.KSeFNumber != kA || !strings.Contains(r.Hint, "fv status FV/2026/10/001") || r.Local == nil {
			t.Errorf("%s: row %+v", st, r)
		}
	}
}

func TestMerge_NumberMatchNeedsTheOwnNIPAsSeller(t *testing.T) {
	local := []store.Meta{localMeta(1, "FV/2026/10/001", "2026-10-05", store.StateUnknown, "", 12300)}
	// a supplier used the same invoice number: that is a purchase, not our invoice
	remote := []ksef.InvoiceMeta{remoteMeta(kB, "FV/2026/10/001", "2026-10-05", "5260250995", own, "50.00")}
	rows := Merge(local, remote, own, SideAll, rng())
	if len(rows) != 2 || rows[0].KSeFNumber != "" || rows[1].State != StateKSeF || rows[1].Dir != DirPurchase {
		t.Errorf("rows %+v", rows)
	}
}

func TestMerge_DraftIsNeverMatchedByNumber(t *testing.T) {
	local := []store.Meta{localMeta(1, "FV/2026/10/001", "2026-10-05", store.StateDraft, "", 12300)}
	remote := []ksef.InvoiceMeta{remoteMeta(kA, "FV/2026/10/001", "2026-10-05", own, "5260250995", "123.00")}
	rows := Merge(local, remote, own, SideAll, rng())
	if len(rows) != 2 || rows[0].State != "draft" || rows[1].State != StateKSeF {
		t.Errorf("rows %+v", rows)
	}
}

func TestMerge_RemoteOnlyRowsAreKSeFRowsWithDirection(t *testing.T) {
	remote := []ksef.InvoiceMeta{
		remoteMeta(kB, "ZAK/9", "2026-09-15", "5260250995", own, "-50.25"), // purchase, a correction
		remoteMeta(kC, "EXT/1", "2026-09-15", own, "5260250995", "10.00"),  // sale made outside fv
	}
	rows := Merge(nil, remote, own, SideAll, rng())
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	if r := rows[0]; r.Dir != DirSale || r.Number != "EXT/1" || r.State != StateKSeF || r.Local != nil || r.Kind != "VAT" || r.SellerID != own || r.BuyerID != "5260250995" {
		t.Errorf("sale %+v", r)
	}
	if r := rows[1]; r.Dir != DirPurchase || r.Gross != -5025 || !r.GrossOK || r.SellerName != "S 5260250995" || r.BuyerID != own || r.KSeFNumber != kB {
		t.Errorf("purchase %+v", r)
	}
}

func TestMerge_RemoteDuplicatesShowOnce(t *testing.T) {
	self := remoteMeta(kC, "SELF/1", "2026-09-15", own, own, "10.00")
	rows := Merge(nil, []ksef.InvoiceMeta{self, self}, own, SideAll, rng())
	if len(rows) != 1 || rows[0].Dir != DirSale {
		t.Errorf("rows %+v", rows)
	}
}

func TestMerge_UnreadableAndMissingGross(t *testing.T) {
	odd := remoteMeta(kB, "X/1", "2026-09-15", "5260250995", own, "1.5e3")
	none := remoteMeta(kC, "X/2", "2026-09-15", "5260250995", own, "")
	none.BuyerIDType = "None"
	rows := Merge(nil, []ksef.InvoiceMeta{odd, none}, own, SideAll, rng())
	if len(rows) != 2 || rows[0].GrossOK || rows[1].GrossOK || rows[1].BuyerID != "" {
		t.Errorf("rows %+v", rows)
	}
}

func TestMerge_RangeFiltersLocalAndRemote(t *testing.T) {
	local := []store.Meta{
		localMeta(1, "FV/2026/07/001", "2026-07-31", store.StateDraft, "", 1),
		localMeta(2, "FV/2026/08/001", "2026-08-01", store.StateDraft, "", 1),
		localMeta(3, "FV/2026/10/009", "2026-10-31", store.StateDraft, "", 1),
		localMeta(4, "FV/2026/11/001", "2026-11-01", store.StateDraft, "", 1),
	}
	remote := []ksef.InvoiceMeta{remoteMeta(kB, "OLD", "2026-07-31", "5260250995", own, "1"), remoteMeta(kC, "IN", "2026-10-31", "5260250995", own, "1")}
	got := summary(Merge(local, remote, own, SideAll, rng()))
	want := "2026-08-01/sale/FV/2026/08/001/draft 2026-10-31/sale/FV/2026/10/009/draft 2026-10-31/purchase/IN/ksef"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestMerge_AsFiltersBothLocalAndRemote(t *testing.T) {
	local := []store.Meta{localMeta(1, "FV/2026/10/001", "2026-10-05", store.StateAccepted, kA, 100)}
	remote := []ksef.InvoiceMeta{
		remoteMeta(kA, "FV/2026/10/001", "2026-10-05", own, "5260250995", "1.00"),
		remoteMeta(kB, "ZAK/9", "2026-09-15", "5260250995", own, "5.00"),
		remoteMeta(kC, "EXT/1", "2026-09-16", own, "5260250995", "10.00"),
	}
	if got := summary(Merge(local, remote, own, SideSeller, rng())); got != "2026-09-16/sale/EXT/1/ksef 2026-10-05/sale/FV/2026/10/001/accepted" {
		t.Errorf("seller: %s", got)
	}
	if got := summary(Merge(local, remote, own, SideBuyer, rng())); got != "2026-09-15/purchase/ZAK/9/ksef" {
		t.Errorf("buyer: %s", got)
	}
	if got := summary(Merge(local, remote, own, SideAll, rng())); strings.Count(got, " ") != 2 {
		t.Errorf("all: %s", got)
	}
	if got := summary(Merge(local, remote, own, "", rng())); strings.Count(got, " ") != 2 {
		t.Errorf("default side: %s", got)
	}
}

func TestMerge_Order(t *testing.T) {
	local := []store.Meta{
		localMeta(3, "FV/2026/10/003", "2026-10-05", store.StateDraft, "", 1),
		localMeta(1, "FV/2026/10/001", "2026-10-05", store.StateDraft, "", 1),
		localMeta(2, "FV/2026/09/001", "2026-09-30", store.StateDraft, "", 1),
	}
	remote := []ksef.InvoiceMeta{
		remoteMeta(kD, "B/2", "2026-10-05", "5260250995", own, "1"),
		remoteMeta(kB, "B/1", "2026-10-05", "5260250995", own, "1"),
		remoteMeta(kC, "A/1", "2026-10-05", own, "5260250995", "1"),
		remoteMeta(kA, "C/1", "2026-08-01", "5260250995", own, "1"),
	}
	got := summary(Merge(local, remote, own, SideAll, rng()))
	want := strings.Join([]string{
		"2026-08-01/purchase/C/1/ksef",
		"2026-09-30/sale/FV/2026/09/001/draft",
		"2026-10-05/sale/FV/2026/10/001/draft", "2026-10-05/sale/FV/2026/10/003/draft", "2026-10-05/sale/A/1/ksef",
		"2026-10-05/purchase/B/1/ksef", "2026-10-05/purchase/B/2/ksef",
	}, " ")
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestParseSide(t *testing.T) {
	for in, want := range map[string]Side{"": SideAll, "all": SideAll, "seller": SideSeller, "buyer": SideBuyer, "BUYER": SideBuyer} {
		if got, err := ParseSide(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	if _, err := ParseSide("both"); err == nil || !strings.Contains(err.Error(), "seller, buyer or all") {
		t.Errorf("err = %v", err)
	}
}
