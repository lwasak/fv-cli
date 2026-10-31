package ksef

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lwasak/fv-cli/internal/invoice"
)

// SubjectType says on which side of the invoice the authenticated NIP stands.
type SubjectType string

const (
	// Subject1 is the seller (Podmiot1): sales invoices.
	Subject1 SubjectType = "Subject1"
	// Subject2 is the buyer (Podmiot2): purchase invoices.
	Subject2 SubjectType = "Subject2"
)

// Limits of POST /invoices/query/metadata.
const (
	// MetadataMinPageSize and MetadataMaxPageSize bound pageSize (10..250).
	MetadataMinPageSize = 10
	MetadataMaxPageSize = 250
	// MaxQuerySpanDays is the longest dateRange KSeF accepts (100 days, UTC).
	MaxQuerySpanDays = 100
	// QueryWindowDays is the span of one query window of QueryAllMetadata: 92 days is the
	// longest "current month plus two previous months" range, with a margin under the limit.
	QueryWindowDays = 92
	// DefaultQueryBudget is the number of metadata calls one run may make: half of the
	// 20/hour limit of the invoiceMetadata group.
	DefaultQueryBudget = 10
	// MetadataSpacing is the pause kept between two metadata calls of one client: the
	// invoiceMetadata group allows 8 requests a second, and repeated overruns lengthen the
	// block, so fv stays well below.
	MetadataSpacing = 150 * time.Millisecond
	// MaxInvoiceNumberLength is the longest invoiceNumber filter KSeF accepts.
	MaxInvoiceNumberLength = 256
)

// MetadataFilter narrows a metadata query beyond subject type and dates.
type MetadataFilter struct {
	// InvoiceNumber, when not empty, is an exact match on the number the issuer gave.
	InvoiceNumber string
}

// ErrQueryBudget is returned by QueryAllMetadata, together with the invoices collected so
// far, when the call budget ran out.
var ErrQueryBudget = errors.New("ksef: the metadata query budget of this run is used up")

// ErrQueryStuck is returned when more than 10 000 invoices share one issue date, so the
// window cannot be narrowed any further.
var ErrQueryStuck = errors.New("ksef: more than 10 000 invoices on one day; cannot list them")

// InvoiceMeta is the metadata KSeF keeps of one invoice (InvoiceMetadata in the OpenAPI).
// Amounts stay JSON numbers (never floats); money.ParseXML turns them into grosze.
type InvoiceMeta struct {
	KSeFNumber      string `json:"ksefNumber"`
	InvoiceNumber   string `json:"invoiceNumber"`
	IssueDate       string `json:"issueDate"` // YYYY-MM-DD
	AcquisitionDate string `json:"acquisitionDate,omitempty"`
	SellerNIP       string `json:"sellerNip"`
	SellerName      string `json:"sellerName,omitempty"`
	// BuyerIDType is Nip, VatUe, Other or None.
	BuyerIDType   string      `json:"buyerIdType,omitempty"`
	BuyerID       string      `json:"buyerId,omitempty"`
	BuyerName     string      `json:"buyerName,omitempty"`
	Gross         json.Number `json:"gross,omitempty"`
	Net           json.Number `json:"net,omitempty"`
	VAT           json.Number `json:"vat,omitempty"` // PLN
	Currency      string      `json:"currency,omitempty"`
	InvoiceType   string      `json:"invoiceType,omitempty"` // Vat, Zal, Kor, ...
	FormCode      string      `json:"formCode,omitempty"`    // systemCode, e.g. "FA (3)"
	InvoiceHash   string      `json:"invoiceHash,omitempty"` // SHA-256, Base64
	SelfInvoicing bool        `json:"selfInvoicing,omitempty"`
	HasAttachment bool        `json:"hasAttachment,omitempty"`
}

// MetadataPage is one answer of the metadata query.
type MetadataPage struct {
	Invoices    []InvoiceMeta
	HasMore     bool
	IsTruncated bool
	// Skipped counts records left out because their KSeF number or issue date was unusable.
	Skipped int
}

type metadataWire struct {
	HasMore     bool `json:"hasMore"`
	IsTruncated bool `json:"isTruncated"`
	Invoices    []struct {
		KSeFNumber      string `json:"ksefNumber"`
		InvoiceNumber   string `json:"invoiceNumber"`
		IssueDate       string `json:"issueDate"`
		AcquisitionDate string `json:"acquisitionDate"`
		Seller          struct {
			NIP  string `json:"nip"`
			Name string `json:"name"`
		} `json:"seller"`
		Buyer struct {
			Identifier struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"identifier"`
			Name string `json:"name"`
		} `json:"buyer"`
		NetAmount     json.Number `json:"netAmount"`
		GrossAmount   json.Number `json:"grossAmount"`
		VATAmount     json.Number `json:"vatAmount"`
		Currency      string      `json:"currency"`
		InvoiceType   string      `json:"invoiceType"`
		IsSelfInvoice bool        `json:"isSelfInvoicing"`
		HasAttachment bool        `json:"hasAttachment"`
		InvoiceHash   string      `json:"invoiceHash"`
		FormCode      struct {
			SystemCode string `json:"systemCode"`
		} `json:"formCode"`
	} `json:"invoices"`
}

// forbiddenHint is appended to a 403 of the read endpoints.
const forbiddenHint = "the credential lacks the InvoiceRead permission for this NIP"

// withHint adds the InvoiceRead hint to a 403 so the user knows what to ask for.
func withHint(err error) error {
	if IsForbidden(err) {
		return fmt.Errorf("%w (%s)", err, forbiddenHint)
	}
	return err
}

// QueryMetadata calls POST /invoices/query/metadata for one page. The date range is always
// by issue date (dateType Issue), sorted ascending. from and to are sent in UTC; pageOffset
// is the page index (0 is the first page). The POST is never retried on 429 (the limit is
// 20 per hour and an overrun lengthens the block): the *Error carries RetryAfter.
func (c *Client) QueryMetadata(ctx context.Context, s *Session, subj SubjectType, from, to time.Time, pageOffset, pageSize int) (MetadataPage, error) {
	return c.QueryMetadataFiltered(ctx, s, subj, from, to, MetadataFilter{}, pageOffset, pageSize)
}

// QueryMetadataFiltered is QueryMetadata with a filter. Every call after the first of a
// client waits until MetadataSpacing has passed since the previous one (through the
// injectable sleep). A record with an unusable ksefNumber or issueDate is skipped (counted
// in Page.Skipped and in SkippedRecords) instead of failing the page.
func (c *Client) QueryMetadataFiltered(ctx context.Context, s *Session, subj SubjectType, from, to time.Time, filter MetadataFilter, pageOffset, pageSize int) (MetadataPage, error) {
	if subj != Subject1 && subj != Subject2 {
		return MetadataPage{}, fmt.Errorf("ksef: unsupported subject type %q", subj)
	}
	if pageSize < MetadataMinPageSize || pageSize > MetadataMaxPageSize || pageOffset < 0 {
		return MetadataPage{}, fmt.Errorf("ksef: invalid page %d of size %d (size %d..%d)", pageOffset, pageSize, MetadataMinPageSize, MetadataMaxPageSize)
	}
	if len(filter.InvoiceNumber) > MaxInvoiceNumberLength {
		return MetadataPage{}, fmt.Errorf("ksef: an invoice number filter is at most %d characters", MaxInvoiceNumberLength)
	}
	reqBody := map[string]any{
		"subjectType": string(subj),
		"dateRange": map[string]any{
			"dateType": "Issue",
			"from":     from.UTC().Format("2006-01-02T15:04:05Z"),
			"to":       to.UTC().Format("2006-01-02T15:04:05Z"),
		},
	}
	if filter.InvoiceNumber != "" {
		reqBody["invoiceNumber"] = filter.InvoiceNumber
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return MetadataPage{}, err
	}
	if err := c.paceMetadata(ctx); err != nil {
		return MetadataPage{}, err
	}
	q := url.Values{}
	q.Set("sortOrder", "Asc")
	q.Set("pageOffset", strconv.Itoa(pageOffset))
	q.Set("pageSize", strconv.Itoa(pageSize))
	resp, err := c.send(ctx, request{method: "POST", path: "/invoices/query/metadata", query: q.Encode(),
		contentType: "application/json", body: body, bearer: s.AccessToken()})
	if err != nil {
		return MetadataPage{}, fmt.Errorf("ksef: query invoices: %w", withHint(err))
	}
	var w metadataWire
	dec := json.NewDecoder(strings.NewReader(string(resp.Body)))
	dec.UseNumber()
	if err := dec.Decode(&w); err != nil {
		return MetadataPage{}, fmt.Errorf("ksef: decode invoice query answer: %w", err)
	}
	page := MetadataPage{HasMore: w.HasMore, IsTruncated: w.IsTruncated}
	for _, x := range w.Invoices {
		issue := x.IssueDate
		if len(issue) > 10 {
			issue = issue[:10]
		}
		verr := ValidateKSeFNumber(x.KSeFNumber)
		if verr == nil {
			if _, derr := invoice.ParseDate(issue); derr != nil {
				verr = fmt.Errorf("issue date of %s: %w", x.KSeFNumber, derr)
			}
		}
		if verr != nil {
			page.Skipped++
			c.skippedRecords.Add(1)
			c.log.Warn("ksef invoice query: record skipped", "error", verr.Error())
			continue
		}
		page.Invoices = append(page.Invoices, InvoiceMeta{
			KSeFNumber: x.KSeFNumber, InvoiceNumber: x.InvoiceNumber, IssueDate: issue,
			AcquisitionDate: x.AcquisitionDate,
			SellerNIP:       x.Seller.NIP, SellerName: x.Seller.Name,
			BuyerIDType: x.Buyer.Identifier.Type, BuyerID: x.Buyer.Identifier.Value, BuyerName: x.Buyer.Name,
			Gross: x.GrossAmount, Net: x.NetAmount, VAT: x.VATAmount,
			Currency: x.Currency, InvoiceType: x.InvoiceType, FormCode: x.FormCode.SystemCode,
			InvoiceHash: x.InvoiceHash, SelfInvoicing: x.IsSelfInvoice, HasAttachment: x.HasAttachment,
		})
	}
	return page, nil
}

// paceMetadata makes sure MetadataSpacing has passed since the previous metadata call.
func (c *Client) paceMetadata(ctx context.Context) error {
	c.paceMu.Lock()
	defer c.paceMu.Unlock()
	if !c.lastMetadata.IsZero() {
		if wait := MetadataSpacing - c.now().Sub(c.lastMetadata); wait > 0 {
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
	c.lastMetadata = c.now()
	return nil
}

// SkippedRecords is the number of metadata records this client left out because their
// KSeF number or issue date was unusable.
func (c *Client) SkippedRecords() int { return int(c.skippedRecords.Load()) }

const dateLayout = "2006-01-02"

// QueryAllMetadata lists every invoice of the subject type issued from fromDate to toDate
// (YYYY-MM-DD, inclusive; toDate is capped at today in Warsaw). It splits the range into
// windows of at most QueryWindowDays, pages with pageSize 250, restarts a truncated
// (10 000 record) result from the date of its last record and removes duplicates. Each
// request asks for one day more on both sides of its window (the end never later than
// now, UTC), and the issueDate of every record is checked against the exact range here, so
// a different reading of the bounds on KSeF's side cannot lose the edge days.
//
// budget, when not nil, is the number of calls still allowed; every call uses one. When it
// is used up the invoices collected so far come back with ErrQueryBudget. Any other error
// also returns what was collected before it.
func (c *Client) QueryAllMetadata(ctx context.Context, s *Session, subj SubjectType, fromDate, toDate string, budget *int) ([]InvoiceMeta, error) {
	return c.QueryAllMetadataFiltered(ctx, s, subj, fromDate, toDate, MetadataFilter{}, budget)
}

// QueryAllMetadataFiltered is QueryAllMetadata with a filter.
func (c *Client) QueryAllMetadataFiltered(ctx context.Context, s *Session, subj SubjectType, fromDate, toDate string, filter MetadataFilter, budget *int) ([]InvoiceMeta, error) {
	from, err := time.Parse(dateLayout, fromDate)
	if err != nil {
		return nil, fmt.Errorf("ksef: invalid from date %q: %w", fromDate, err)
	}
	to, err := time.Parse(dateLayout, toDate)
	if err != nil {
		return nil, fmt.Errorf("ksef: invalid to date %q: %w", toDate, err)
	}
	if today := invoice.Today(c.now()); today.Format(dateLayout) < toDate {
		to = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	}
	var out []InvoiceMeta
	seen := map[string]bool{}
	for wf := from; !wf.After(to); wf = wf.AddDate(0, 0, QueryWindowDays) {
		wt := wf.AddDate(0, 0, QueryWindowDays-1)
		if wt.After(to) {
			wt = to
		}
		got, err := c.queryWindow(ctx, s, subj, wf, wt, filter, budget, seen)
		out = append(out, got...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// queryWindow pages through one window of at most QueryWindowDays.
func (c *Client) queryWindow(ctx context.Context, s *Session, subj SubjectType, from, to time.Time, filter MetadataFilter, budget *int, seen map[string]bool) ([]InvoiceMeta, error) {
	var out []InvoiceMeta
	lo, hi := from.Format(dateLayout), to.Format(dateLayout)
	cur := from
	offset := 0
	for {
		if budget != nil {
			if *budget <= 0 {
				return out, ErrQueryBudget
			}
			*budget--
		}
		apiFrom := cur.AddDate(0, 0, -1)
		apiTo := to.AddDate(0, 0, 1).Add(24*time.Hour - time.Second)
		if now := c.now().UTC().Truncate(time.Second); apiTo.After(now) {
			apiTo = now
		}
		if apiTo.Before(apiFrom) {
			apiTo = apiFrom
		}
		page, err := c.QueryMetadataFiltered(ctx, s, subj, apiFrom, apiTo, filter, offset, MetadataMaxPageSize)
		if err != nil {
			return out, err
		}
		last := ""
		for _, m := range page.Invoices {
			if m.IssueDate > last {
				last = m.IssueDate
			}
			if m.IssueDate < lo || m.IssueDate > hi || seen[m.KSeFNumber] {
				continue
			}
			seen[m.KSeFNumber] = true
			out = append(out, m)
		}
		switch {
		case page.IsTruncated && page.HasMore:
			next, err := time.Parse(dateLayout, last)
			if err != nil || !next.After(cur) {
				return out, ErrQueryStuck
			}
			cur, offset = next, 0
		case page.HasMore && len(page.Invoices) > 0:
			offset++
		default:
			return out, nil
		}
	}
}
