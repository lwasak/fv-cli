package store

import (
	"time"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/money"
)

// State is the lifecycle state of an archived invoice.
type State string

// States: reserved -> draft -> sending -> accepted | rejected | unknown, plus void-draft.
const (
	StateReserved State = "reserved"
	StateDraft    State = "draft"
	StateSending  State = "sending"
	StateAccepted State = "accepted"
	StateRejected State = "rejected"
	StateUnknown  State = "unknown"
	StateVoid     State = "void-draft"
)

// metaSchema is the current meta.json schema version.
const metaSchema = 1

// Meta is the content of meta.json.
type Meta struct {
	Schema     int        `json:"schema"`
	Number     string     `json:"number"`
	Env        config.Env `json:"env"`
	State      State      `json:"state"`
	Seq        int        `json:"seq"`
	CounterKey string     `json:"counterKey"`

	IssueDate string `json:"issueDate"` // YYYY-MM-DD
	SaleDate  string `json:"saleDate"`
	DueDate   string `json:"dueDate"`

	CreatedAt time.Time `json:"createdAt"` // DataWytworzeniaFa
	UpdatedAt time.Time `json:"updatedAt"`

	Seller PartySnapshot `json:"seller"`
	Buyer  PartySnapshot `json:"buyer"`

	Currency string         `json:"currency"`
	Lines    []LineSnapshot `json:"lines"`
	Totals   []RateTotal    `json:"totals"`
	Gross    money.Amount   `json:"gross"`

	BankAccount     string `json:"bankAccount"`
	BankOnWhiteList *bool  `json:"bankOnWhiteList,omitempty"`

	Exchange *Exchange `json:"exchange,omitempty"`

	XMLSHA256 string `json:"xmlSha256,omitempty"` // base64 std, equals the KSeF invoiceHash
	XMLSize   int    `json:"xmlSize,omitempty"`
	UPOSHA256 string `json:"upoSha256,omitempty"`

	KSeF *KSeFRef `json:"ksef,omitempty"`
	// SentSessions lists every KSeF session reference this invoice was sent in, oldest first.
	// KSeF is cleared when an unknown send goes back to draft, but this list stays, so a later
	// duplicate (440) pointing at one of these sessions is recognised as this very invoice.
	SentSessions []string     `json:"sentSessions,omitempty"`
	Warnings     []string     `json:"warnings,omitempty"`
	History      []Transition `json:"history"`
}

// PartySnapshot is a party as it was when the invoice was drafted.
type PartySnapshot struct {
	Alias              string    `json:"alias,omitempty"`
	NIP                string    `json:"nip,omitempty"`
	VatID              string    `json:"vatId,omitempty"`
	Name               string    `json:"name,omitempty"`
	Address            string    `json:"address,omitempty"`
	CountryCode        string    `json:"countryCode,omitempty"`
	StatusVat          string    `json:"statusVat,omitempty"`
	Source             string    `json:"source,omitempty"`
	RequestID          string    `json:"requestId,omitempty"`
	ConsultationNumber string    `json:"consultationNumber,omitempty"`
	CheckedAt          time.Time `json:"checkedAt,omitzero"`
}

// LineSnapshot is one invoice line.
type LineSnapshot struct {
	No          int          `json:"no"`
	Description string       `json:"description"`
	Unit        string       `json:"unit"`
	Quantity    string       `json:"quantity"` // XML decimal
	UnitPrice   money.Amount `json:"unitPrice"`
	Net         money.Amount `json:"net"`
	Rate        string       `json:"rate"`
}

// RateTotal is the totals of one VAT rate.
type RateTotal struct {
	Rate   string        `json:"rate"`
	Net    money.Amount  `json:"net"`
	VAT    money.Amount  `json:"vat"`
	Gross  money.Amount  `json:"gross"`
	VATPLN *money.Amount `json:"vatPln,omitempty"`
}

// Exchange is the NBP rate used for a foreign-currency invoice.
type Exchange struct {
	Currency      string `json:"currency"`
	Table         string `json:"table"`
	EffectiveDate string `json:"effectiveDate"`
	Mid           string `json:"mid"` // decimal text from NBP
	MidMicro      int64  `json:"midMicro"`
}

// KSeFRef holds the KSeF references of a sent invoice.
type KSeFRef struct {
	SessionRef        string     `json:"sessionRef,omitempty"`
	SessionValidUntil string     `json:"sessionValidUntil,omitempty"`
	InvoiceRef        string     `json:"invoiceRef,omitempty"`
	KSeFNumber        string     `json:"ksefNumber,omitempty"`
	AcquisitionDate   string     `json:"acquisitionDate,omitempty"`
	StatusCode        int        `json:"statusCode,omitempty"`
	StatusDescription string     `json:"statusDescription,omitempty"`
	Details           []string   `json:"details,omitempty"`
	SentAt            *time.Time `json:"sentAt,omitempty"`
	// Duplicate marks a KSeF 440: the number (seller NIP, invoice kind and P_2) is already
	// used by an invoice that KSeF holds, so it must never be given out again.
	// OriginalKSeFNumber names that invoice when KSeF said so.
	Duplicate          bool   `json:"duplicate,omitempty"`
	OriginalKSeFNumber string `json:"originalKsefNumber,omitempty"`
	// OriginalSessionRef is the session KSeF names as the one that holds the original.
	OriginalSessionRef string `json:"originalSessionRef,omitempty"`
}

// Transition is one history entry.
type Transition struct {
	From State     `json:"from"`
	To   State     `json:"to"`
	At   time.Time `json:"at"`
}

// allowed is the transition table. Releasing a number (deleting the folder) is not a
// transition; see Store.Discard.
var allowed = map[State][]State{
	StateReserved: {StateDraft, StateVoid},
	StateDraft:    {StateSending, StateVoid},
	StateSending:  {StateAccepted, StateRejected, StateUnknown, StateDraft},
	StateUnknown:  {StateAccepted, StateRejected, StateDraft},
	StateRejected: {StateVoid},
}

func canTransition(from, to State) bool {
	for _, s := range allowed[from] {
		if s == to {
			return true
		}
	}
	return false
}

// immutableXML reports whether invoice.xml must not change in this state.
func immutableXML(s State) bool {
	switch s {
	case StateSending, StateAccepted, StateRejected, StateUnknown:
		return true
	}
	return false
}
