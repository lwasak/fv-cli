package ksefake

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lwasak/fv-cli/internal/money"
)

// QueryOptions configures EnableQuery.
type QueryOptions struct {
	// NIP is the context NIP of the credential: Subject1 returns the invoices it sold and
	// Subject2 the ones it bought (default 5833281095).
	NIP string
	// Bearer, when set, is the only access token the query routes accept (401 otherwise).
	Bearer string
	// TruncateAt is the number of records after which a result is cut and isTruncated is
	// reported (KSeF: 10 000, the default).
	TruncateAt int
	// Online, when set, adds every invoice it accepted (status 200) as a Subject1 row (and
	// as a Subject2 row too when the buyer NIP is NIP).
	Online *Online
	// Now is the clock for acquisition dates and the default "to" (default time.Now).
	Now func() time.Time
}

// QMeta describes one invoice of the fake's metadata store. Empty fields get defaults: a
// valid KSeF number built from the seller NIP and the issue date, PLN, type Vat, form code
// "FA (3)", buyer identified by NIP, and the hash of the XML. Amounts are decimal texts
// written to the JSON as numbers.
type QMeta struct {
	KSeFNumber    string
	InvoiceNumber string
	IssueDate     string // YYYY-MM-DD
	SellerNIP     string
	SellerName    string
	BuyerType     string // Nip (default), VatUe, Other, None
	BuyerID       string
	BuyerName     string
	Net, Gross    string
	VAT           string
	Currency      string
	InvoiceType   string
	FormSystem    string // formCode.systemCode, default "FA (3)"
	FormValue     string // formCode.value, default "FA"
	SelfInvoicing bool
	HasAttachment bool
}

type stored struct {
	m      QMeta
	xml    []byte
	online bool // accepted by the Online fake
}

// Query is the invoice-query half of the fake. Create it with Fake.EnableQuery.
type Query struct {
	f    *Fake
	opts QueryOptions

	mu         sync.Mutex
	rows       []*stored
	seq        int
	notYet     map[string]int
	violations []string
}

// EnableQuery registers the invoice-read routes of KSeF 2.0:
//
//	POST /invoices/query/metadata?sortOrder=&pageOffset=&pageSize=   metadata query
//	GET  /invoices/ksef/{ksefNumber}                                 invoice XML with x-ms-meta-hash
//
// The query checks the bearer, subjectType (Subject1 or Subject2), dateType Issue, the
// date-time format, a span of at most 100 days (400 21405), pageSize 10..250 and
// pageOffset as a page index. Each client mistake is answered 4xx and recorded in
// Violations. A 429 is scripted with Fake.Inject.
func (f *Fake) EnableQuery(opts QueryOptions) *Query {
	if opts.NIP == "" {
		opts.NIP = "5833281095"
	}
	if opts.TruncateAt <= 0 {
		opts.TruncateAt = 10000
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	q := &Query{f: f, opts: opts, notYet: map[string]int{}}
	f.Handle("POST", "/invoices/query/metadata", q.query)
	f.HandlePattern("GET", "/invoices/ksef/*", q.download)
	return q
}

// Add stores an invoice and returns its KSeF number. xml may be nil (the download then
// answers 21164).
func (q *Query) Add(m QMeta, xml []byte) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if m.KSeFNumber == "" {
		q.seq++
		nip := m.SellerNIP
		if nip == "" {
			nip = q.opts.NIP
		}
		date := strings.ReplaceAll(m.IssueDate, "-", "")
		if date == "" {
			date = q.opts.Now().UTC().Format("20060102")
		}
		base := fmt.Sprintf("%s-%s-%012X", nip, date, 0x0200002AF629+q.seq)
		m.KSeFNumber = fmt.Sprintf("%s-%02X", base, crc8(base))
	}
	q.rows = append(q.rows, &stored{m: m, xml: xml})
	return m.KSeFNumber
}

// SetNotYetAvailable makes the first n downloads of ksefNumber answer 21165.
func (q *Query) SetNotYetAvailable(ksefNumber string, n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.notYet[ksefNumber] = n
}

// Violations lists protocol errors the client made (each was answered 4xx).
func (q *Query) Violations() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.violations...)
}

func (q *Query) violation(format string, a ...any) string {
	msg := fmt.Sprintf(format, a...)
	q.violations = append(q.violations, msg)
	return msg
}

func (q *Query) bad(format string, a ...any) Response {
	msg := q.violation(format, a...)
	return problem(400, "Bad Request", "Żądanie jest nieprawidłowe.",
		apiErr{Code: 21405, Description: "Błąd walidacji danych wejściowych.", Details: []string{msg}})
}

func (q *Query) auth(r Request) *Response {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" || (q.opts.Bearer != "" && got != q.opts.Bearer) {
		q.violation("%s %s: missing or wrong bearer token", r.Method, r.Path)
		p := problem(401, "Unauthorized", "Wymagane jest uwierzytelnienie.")
		return &p
	}
	return nil
}

// all returns the stored rows and, last, the invoices the Online fake accepted.
func (q *Query) all() []*stored {
	rows := append([]*stored(nil), q.rows...)
	if q.opts.Online == nil {
		return rows
	}
	have := map[string]bool{}
	for _, r := range rows {
		have[r.m.KSeFNumber] = true
	}
	for _, inv := range q.opts.Online.Invoices() {
		if inv.Status != 200 || inv.XML == nil || have[inv.KSeFNumber] {
			continue
		}
		rows = append(rows, &stored{m: metaFromXML(inv.KSeFNumber, inv.XML), xml: inv.XML, online: true})
	}
	return rows
}

// metaFromXML reads the few FA fields the metadata needs; the fake does not validate.
func metaFromXML(ksefNumber string, b []byte) QMeta {
	var d struct {
		Fa struct {
			KodWaluty string `xml:"KodWaluty"`
			P1        string `xml:"P_1"`
			P2        string `xml:"P_2"`
			P15       string `xml:"P_15"`
			Rodzaj    string `xml:"RodzajFaktury"`
			Any       []struct {
				XMLName xml.Name
				Value   string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"Fa"`
		S struct {
			NIP   string `xml:"DaneIdentyfikacyjne>NIP"`
			Nazwa string `xml:"DaneIdentyfikacyjne>Nazwa"`
		} `xml:"Podmiot1"`
		B struct {
			NIP   string `xml:"DaneIdentyfikacyjne>NIP"`
			Nazwa string `xml:"DaneIdentyfikacyjne>Nazwa"`
		} `xml:"Podmiot2"`
	}
	_ = xml.Unmarshal(b, &d)
	m := QMeta{KSeFNumber: ksefNumber, InvoiceNumber: d.Fa.P2, IssueDate: d.Fa.P1,
		SellerNIP: d.S.NIP, SellerName: d.S.Nazwa, BuyerType: "Nip", BuyerID: d.B.NIP, BuyerName: d.B.Nazwa,
		Gross: d.Fa.P15, Currency: d.Fa.KodWaluty, InvoiceType: titleType(d.Fa.Rodzaj)}
	if d.B.NIP == "" {
		m.BuyerType = "None"
	}
	// net and VAT: sum every P_13_* and P_14_* element of Fa
	var net, vat money.Amount
	for _, e := range d.Fa.Any {
		n := e.XMLName.Local
		switch {
		case strings.HasPrefix(n, "P_13_"):
			if a, err := money.ParseXML(strings.TrimSpace(e.Value)); err == nil {
				net += a
			}
		case strings.HasPrefix(n, "P_14_") && !strings.HasSuffix(n, "W"):
			if a, err := money.ParseXML(strings.TrimSpace(e.Value)); err == nil {
				vat += a
			}
		}
	}
	m.Net, m.VAT = money.FormatXML(net), money.FormatXML(vat)
	return m
}

func titleType(rodzaj string) string {
	switch rodzaj {
	case "KOR":
		return "Kor"
	case "ZAL":
		return "Zal"
	case "ROZ":
		return "Roz"
	case "UPR":
		return "Upr"
	case "KOR_ZAL":
		return "KorZal"
	case "KOR_ROZ":
		return "KorRoz"
	}
	return "Vat"
}

func num(s string) json.Number {
	if s == "" {
		return "0"
	}
	return json.Number(s)
}

func (q *Query) wire(r *stored) map[string]any {
	m := r.m
	def := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	sum := sha256.Sum256(r.xml)
	now := q.opts.Now().UTC().Format(time.RFC3339Nano)
	buyer := map[string]any{"identifier": map[string]any{"type": def(m.BuyerType, "Nip"), "value": m.BuyerID}}
	if m.BuyerName != "" {
		buyer["name"] = m.BuyerName
	}
	seller := map[string]any{"nip": m.SellerNIP}
	if m.SellerName != "" {
		seller["name"] = m.SellerName
	}
	return map[string]any{
		"ksefNumber": m.KSeFNumber, "invoiceNumber": m.InvoiceNumber, "issueDate": m.IssueDate,
		"invoicingDate": now, "acquisitionDate": now, "permanentStorageDate": now,
		"seller": seller, "buyer": buyer,
		"netAmount": num(m.Net), "grossAmount": num(m.Gross), "vatAmount": num(m.VAT),
		"currency": def(m.Currency, "PLN"), "invoicingMode": "Online", "invoiceType": def(m.InvoiceType, "Vat"),
		"formCode":        map[string]string{"systemCode": def(m.FormSystem, "FA (3)"), "schemaVersion": "1-0E", "value": def(m.FormValue, "FA")},
		"isSelfInvoicing": m.SelfInvoicing, "hasAttachment": m.HasAttachment,
		"invoiceHash": base64.StdEncoding.EncodeToString(sum[:]), "thirdSubjects": []any{},
	}
}

func parseTime(s string) (time.Time, error) {
	for _, l := range []string{time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("not an ISO-8601 date-time: %q", s)
}

func (q *Query) query(r Request) Response {
	q.mu.Lock()
	defer q.mu.Unlock()
	if p := q.auth(r); p != nil {
		return *p
	}
	vals, _ := url.ParseQuery(r.Query)
	size, err := strconv.Atoi(vals.Get("pageSize"))
	if vals.Get("pageSize") == "" {
		size, err = 10, nil
	}
	if err != nil || size < 10 || size > 250 {
		return q.bad("pageSize %q must be 10..250", vals.Get("pageSize"))
	}
	offset, err := strconv.Atoi(vals.Get("pageOffset"))
	if vals.Get("pageOffset") == "" {
		offset, err = 0, nil
	}
	if err != nil || offset < 0 {
		return q.bad("pageOffset %q must be a page index >= 0", vals.Get("pageOffset"))
	}
	order := vals.Get("sortOrder")
	if order == "" {
		order = "Asc"
	}
	if order != "Asc" && order != "Desc" {
		return q.bad("sortOrder %q", order)
	}
	var req struct {
		SubjectType string `json:"subjectType"`
		DateRange   struct {
			DateType string `json:"dateType"`
			From     string `json:"from"`
			To       string `json:"to"`
		} `json:"dateRange"`
	}
	if err := json.Unmarshal(r.Body, &req); err != nil {
		return q.bad("query body is not JSON: %v", err)
	}
	if req.SubjectType != "Subject1" && req.SubjectType != "Subject2" {
		return q.bad("subjectType %q: fv only queries Subject1 and Subject2", req.SubjectType)
	}
	if req.DateRange.DateType != "Issue" {
		return q.bad("dateType %q: the client must use Issue", req.DateRange.DateType)
	}
	from, err := parseTime(req.DateRange.From)
	if err != nil {
		return q.bad("dateRange.from: %v", err)
	}
	to := q.opts.Now().UTC()
	if req.DateRange.To != "" {
		if to, err = parseTime(req.DateRange.To); err != nil {
			return q.bad("dateRange.to: %v", err)
		}
	}
	if to.Before(from) {
		return q.bad("dateRange.to is before from")
	}
	if to.Sub(from) > 100*24*time.Hour {
		return q.bad("dateRange spans more than 100 days (%s .. %s)", req.DateRange.From, req.DateRange.To)
	}

	var match []*stored
	for _, row := range q.all() {
		d, err := time.Parse("2006-01-02", row.m.IssueDate)
		if err != nil || d.Before(from) || d.After(to) {
			continue
		}
		inSeller := row.online || row.m.SellerNIP == q.opts.NIP
		inBuyer := row.m.BuyerType != "None" && row.m.BuyerType != "Other" && row.m.BuyerID == q.opts.NIP
		if (req.SubjectType == "Subject1" && inSeller) || (req.SubjectType == "Subject2" && inBuyer) {
			match = append(match, row)
		}
	}
	sort.SliceStable(match, func(i, j int) bool {
		a, b := match[i].m, match[j].m
		if a.IssueDate != b.IssueDate {
			return (a.IssueDate < b.IssueDate) == (order == "Asc")
		}
		return (a.KSeFNumber < b.KSeFNumber) == (order == "Asc")
	})
	truncated := false
	if len(match) > q.opts.TruncateAt {
		match, truncated = match[:q.opts.TruncateAt], true
	}
	start := min(offset*size, len(match))
	end := min(start+size, len(match))
	trunc := truncated && end == len(match) && start < end
	hasMore := end < len(match) || trunc
	invoices := []any{}
	for _, row := range match[start:end] {
		invoices = append(invoices, q.wire(row))
	}
	return jsonResp(200, map[string]any{
		"hasMore": hasMore, "isTruncated": trunc, "permanentStorageHwmDate": nil, "invoices": invoices,
	})
}

func exception(status, code int, desc, detail string) Response {
	b, _ := json.Marshal(map[string]any{"exception": map[string]any{
		"serviceCtx": "ksefake", "serviceCode": "x", "serviceName": "ksefake",
		"timestamp": "2026-10-31T10:00:00Z", "referenceNumber": "ksefake-ref",
		"exceptionDetailList": []map[string]any{{"exceptionCode": code, "exceptionDescription": desc, "details": []string{detail}}},
	}})
	return Response{Status: status, Body: string(b)}
}

func (q *Query) download(r Request) Response {
	q.mu.Lock()
	defer q.mu.Unlock()
	if p := q.auth(r); p != nil {
		return *p
	}
	n := r.Params[0]
	if left := q.notYet[n]; left > 0 {
		q.notYet[n] = left - 1
		return exception(http.StatusBadRequest, 21165, "Faktura o podanym numerze KSeF nie jest jeszcze dostępna.",
			"Faktura o numerze KSeF "+n+" została przetworzona, ale nie jest jeszcze dostępna do pobrania. Spróbuj ponownie później.")
	}
	for _, row := range q.all() {
		if row.m.KSeFNumber == n && row.xml != nil {
			sum := sha256.Sum256(row.xml)
			return Response{Status: 200, Body: string(row.xml), ContentType: "application/xml",
				Header: map[string]string{"x-ms-meta-hash": base64.StdEncoding.EncodeToString(sum[:])}}
		}
	}
	return exception(http.StatusBadRequest, 21164, "Faktura o podanym identyfikatorze nie istnieje.",
		"Faktura o numerze KSeF "+n+" nie została znaleziona.")
}
