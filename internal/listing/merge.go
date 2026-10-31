package listing

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lwasak/fv-cli/internal/ksef"
	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/store"
)

// Side selects which invoices fv list shows.
type Side string

// The values of --as.
const (
	SideAll    Side = "all"
	SideSeller Side = "seller"
	SideBuyer  Side = "buyer"
)

// ParseSide parses the value of --as; empty means all.
func ParseSide(s string) (Side, error) {
	switch Side(strings.ToLower(s)) {
	case "", SideAll:
		return SideAll, nil
	case SideSeller:
		return SideSeller, nil
	case SideBuyer:
		return SideBuyer, nil
	}
	return "", fmt.Errorf("--as %q: use seller, buyer or all", s)
}

// Directions of a Row.
const (
	DirSale     = "sale"
	DirPurchase = "purchase"
)

// StateKSeF is the state of a row that only KSeF knows: a purchase, or a sale made outside fv.
const StateKSeF = "ksef"

// Row is one line of fv list.
type Row struct {
	Issued, Number string
	// Dir is sale when the seller is the own NIP, otherwise purchase.
	Dir                                      string
	SellerName, SellerID, BuyerName, BuyerID string
	Gross                                    money.Amount
	// GrossOK is false when KSeF's amount could not be read exactly.
	GrossOK                           bool
	Currency, State, KSeFNumber, Kind string
	// Local is the archived invoice, when there is one.
	Local *store.Meta
	// Hint is a note for stderr, e.g. a pointer to fv status.
	Hint string
}

// kinds maps the KSeF invoice type to the FA(3) RodzajFaktury names.
var kinds = map[string]string{
	"Vat": "VAT", "Zal": "ZAL", "Kor": "KOR", "Roz": "ROZ", "Upr": "UPR", "KorZal": "KOR_ZAL", "KorRoz": "KOR_ROZ",
}

func kindOf(t string) string {
	if k, ok := kinds[t]; ok {
		return k
	}
	return t
}

// Merge combines the archive (local, already limited to one environment) with the invoices
// KSeF reported or the cache holds (remote). The rules:
//
//  1. remote invoices are de-duplicated by KSeF number (a self-invoice comes back from both
//     the seller and the buyer query);
//  2. a local invoice whose KSeF number equals a remote one is one row, and the local data
//     wins (state, names, gross; a differing gross adds a hint);
//  3. a local invoice with a KSeF number the query did not return keeps its local row;
//  4. a local invoice without a KSeF number is a row in every state;
//  5. a local invoice still sending or unknown, whose own sale in KSeF has the same invoice
//     number, is one row that takes the KSeF number and the hint to run fv status (the
//     archive is not changed);
//  6. every other remote invoice is a row in state "ksef".
//
// Rows are limited to the range (by issue date) and to the side. Local invoices are always
// sales. The order is by issue date, sale before purchase, local before KSeF-only, then
// archive sequence, number and KSeF number.
func Merge(local []store.Meta, remote []ksef.InvoiceMeta, ownNIP string, as Side, r Range) []Row {
	if as == "" {
		as = SideAll
	}
	var rem []ksef.InvoiceMeta
	byNumber := map[string]int{}
	for _, m := range remote {
		if _, dup := byNumber[m.KSeFNumber]; dup || !r.Contains(m.IssueDate) {
			continue
		}
		byNumber[m.KSeFNumber] = len(rem)
		rem = append(rem, m)
	}
	used := make([]bool, len(rem))
	var rows []Row

	// rule 5 needs the remote sales by invoice number
	salesByNumber := map[string][]int{}
	for i, m := range rem {
		if m.SellerNIP == ownNIP {
			salesByNumber[m.InvoiceNumber] = append(salesByNumber[m.InvoiceNumber], i)
		}
	}

	for i := range local {
		m := &local[i]
		if !r.Contains(m.IssueDate) {
			continue
		}
		row := localRow(m)
		switch {
		case m.KSeF != nil && m.KSeF.KSeFNumber != "":
			if j, ok := byNumber[m.KSeF.KSeFNumber]; ok {
				used[j] = true
				if g, ok := remoteGross(rem[j]); ok && g != m.Gross && (rem[j].Currency == "" || rem[j].Currency == m.Currency || m.Currency == "") {
					row.Hint = fmt.Sprintf("%s: KSeF shows gross %s, the archive %s", m.Number, money.FormatPL(g), money.FormatPL(m.Gross))
				}
			}
		case m.State == store.StateSending || m.State == store.StateUnknown:
			for _, j := range salesByNumber[m.Number] {
				if used[j] {
					continue
				}
				used[j] = true
				row.KSeFNumber = rem[j].KSeFNumber
				row.Hint = fmt.Sprintf("%s is in KSeF as %s: run `fv status %s`", m.Number, rem[j].KSeFNumber, m.Number)
				break
			}
		}
		rows = append(rows, row)
	}
	for j, m := range rem {
		if !used[j] {
			rows = append(rows, remoteRow(m, ownNIP))
		}
	}

	out := rows[:0]
	for _, row := range rows {
		switch {
		case as == SideSeller && row.Dir != DirSale, as == SideBuyer && row.Dir != DirPurchase:
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func localRow(m *store.Meta) Row {
	row := Row{
		Issued: m.IssueDate, Number: m.Number, Dir: DirSale,
		SellerName: m.Seller.Name, SellerID: m.Seller.NIP,
		BuyerName: m.Buyer.Name, BuyerID: firstOf(m.Buyer.NIP, m.Buyer.VatID),
		Gross: m.Gross, GrossOK: true, Currency: m.Currency, State: string(m.State), Kind: "VAT", Local: m,
	}
	if m.KSeF != nil {
		row.KSeFNumber = m.KSeF.KSeFNumber
	}
	return row
}

func remoteRow(m ksef.InvoiceMeta, ownNIP string) Row {
	row := Row{
		Issued: m.IssueDate, Number: m.InvoiceNumber, Dir: DirPurchase,
		SellerName: m.SellerName, SellerID: m.SellerNIP, BuyerName: m.BuyerName,
		Currency: m.Currency, State: StateKSeF, KSeFNumber: m.KSeFNumber, Kind: kindOf(m.InvoiceType),
	}
	if m.SellerNIP == ownNIP {
		row.Dir = DirSale
	}
	if m.BuyerIDType != "None" {
		row.BuyerID = m.BuyerID
	}
	row.Gross, row.GrossOK = remoteGross(m)
	return row
}

func remoteGross(m ksef.InvoiceMeta) (money.Amount, bool) {
	a, err := money.ParseXML(m.Gross.String())
	return a, err == nil
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func less(a, b Row) bool {
	if a.Issued != b.Issued {
		return a.Issued < b.Issued
	}
	if a.Dir != b.Dir {
		return a.Dir == DirSale
	}
	if (a.Local == nil) != (b.Local == nil) {
		return a.Local != nil
	}
	if a.Local != nil && a.Local.Seq != b.Local.Seq {
		return a.Local.Seq < b.Local.Seq
	}
	if a.Number != b.Number {
		return a.Number < b.Number
	}
	return a.KSeFNumber < b.KSeFNumber
}
