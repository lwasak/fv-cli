// Package fa3 builds FA(3) invoice XML (KSeF schema 1-0E) deterministically from
// already computed amounts, and ships the vendored XSD used to validate it.
//
// The structs below follow the XSD element order exactly: encoding/xml emits fields
// in declaration order, so reordering a field breaks schema validity.
package fa3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lwasak/fv-cli/internal/money"
	"github.com/lwasak/fv-cli/internal/validate"
)

// Namespace is the FA(3) target namespace.
const Namespace = "http://crd.gov.pl/wzor/2025/06/25/13775/"

const header = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"

// Party is a seller or a buyer. A buyer is identified by NIP, or by EUCode and
// VatNumber (the EU VAT prefix and the number without it).
type Party struct {
	NIP         string
	EUCode      string // EU VAT prefix as the XSD names it: EL for Greece
	VatNumber   string // without the prefix
	Name        string
	CountryCode string // ISO 3166 for the address; empty means PL
	AddressL1   string
	AddressL2   string
	Email       string
}

// Exchange is the NBP rate used for a foreign-currency invoice.
type Exchange struct {
	MidMicro      int64  // PLN per one unit of Currency, in millionths
	Table         string // e.g. 211/A/NBP/2026
	EffectiveDate string // YYYY-MM-DD
	Currency      string
}

// Line is one invoice line, with amounts already computed.
type Line struct {
	No          int
	Description string
	Unit        string
	Qty         money.Quantity
	UnitPrice   money.Amount
	Net         money.Amount
	Rate        money.Rate
	Index       string // Indeks, optional, at most 50 characters
	GTIN        string // optional, at most 20 characters
	PKWiU       string // optional, at most 50 characters
}

// MaxLines is the most lines the schema allows (FaWiersz maxOccurs).
const MaxLines = 10000

// Input is everything Build needs. Build performs no VAT arithmetic:
// all amounts, including VATPLN, are computed by the caller.
type Input struct {
	Seller, Buyer  Party
	Number         string
	PlaceOfIssue   string // optional, written as P_1M
	Currency       string // ISO code; empty means PLN
	SystemInfo     string // optional
	ExemptionBasis string // required when a zw line exists
	BankAccount    string
	BankName       string // optional NazwaBanku, 1-256 characters
	BankSWIFT      string // optional SWIFT/BIC, already normalised
	IssueDate      time.Time
	SaleDate       time.Time // zero or equal to IssueDate: P_6 omitted
	DueDate        time.Time
	CreatedAt      time.Time // DataWytworzeniaFa, written in UTC
	Lines          []Line
	Summary        money.Summary
	VATPLN         map[string]money.Amount // rate code -> VAT in PLN, foreign currency only
	Exchange       *Exchange               // required when Currency is not PLN
}

type kodFormularza struct {
	Value        string `xml:",chardata"`
	KodSystemowy string `xml:"kodSystemowy,attr"`
	WersjaSchemy string `xml:"wersjaSchemy,attr"`
}

type naglowek struct {
	KodFormularza     kodFormularza
	WariantFormularza int
	DataWytworzeniaFa string
	SystemInfo        string `xml:",omitempty"`
}

type daneID struct {
	NIP     string `xml:",omitempty"`
	KodUE   string `xml:",omitempty"`
	NrVatUE string `xml:",omitempty"`
	Nazwa   string
}

type adres struct {
	KodKraju string
	AdresL1  string
	AdresL2  string `xml:",omitempty"`
}

type kontakt struct {
	Email string
}

type podmiot1 struct {
	DaneIdentyfikacyjne daneID
	Adres               adres
	DaneKontaktowe      *kontakt `xml:",omitempty"`
}

type podmiot2 struct {
	DaneIdentyfikacyjne daneID
	Adres               *adres   `xml:",omitempty"`
	DaneKontaktowe      *kontakt `xml:",omitempty"`
	JST                 int
	GV                  int
}

type zwolnienie struct {
	P19  int    `xml:"P_19,omitempty"`
	P19A string `xml:"P_19A,omitempty"`
	P19N int    `xml:"P_19N,omitempty"`
}

type noweSrodki struct {
	P22N int `xml:"P_22N"`
}

type pMarzy struct {
	P int `xml:"P_PMarzyN"`
}

type adnotacje struct {
	P16                  int `xml:"P_16"`
	P17                  int `xml:"P_17"`
	P18                  int `xml:"P_18"`
	P18A                 int `xml:"P_18A"`
	Zwolnienie           zwolnienie
	NoweSrodkiTransportu noweSrodki
	P23                  int `xml:"P_23"`
	PMarzy               pMarzy
}

type opis struct {
	Klucz   string
	Wartosc string
}

type wiersz struct {
	NrWierszaFa int
	P7          string `xml:"P_7"`
	Indeks      string `xml:",omitempty"`
	GTIN        string `xml:",omitempty"`
	PKWiU       string `xml:",omitempty"`
	P8A         string `xml:"P_8A,omitempty"`
	P8B         string `xml:"P_8B,omitempty"`
	P9A         string `xml:"P_9A"`
	P11         string `xml:"P_11"`
	P12         string `xml:"P_12"`
	KursWaluty  string `xml:",omitempty"`
}

type termin struct {
	Termin string
}

type rachunek struct {
	NrRB       string
	SWIFT      string `xml:",omitempty"`
	NazwaBanku string `xml:",omitempty"`
}

type platnosc struct {
	TerminPlatnosci termin
	FormaPlatnosci  int
	RachunekBankowy rachunek
}

type fa struct {
	KodWaluty     string
	P1            string `xml:"P_1"`
	P1M           string `xml:"P_1M,omitempty"`
	P2            string `xml:"P_2"`
	P6            string `xml:"P_6,omitempty"`
	P131          string `xml:"P_13_1,omitempty"`
	P141          string `xml:"P_14_1,omitempty"`
	P141W         string `xml:"P_14_1W,omitempty"`
	P132          string `xml:"P_13_2,omitempty"`
	P142          string `xml:"P_14_2,omitempty"`
	P142W         string `xml:"P_14_2W,omitempty"`
	P133          string `xml:"P_13_3,omitempty"`
	P143          string `xml:"P_14_3,omitempty"`
	P143W         string `xml:"P_14_3W,omitempty"`
	P137          string `xml:"P_13_7,omitempty"`
	P15           string `xml:"P_15"`
	Adnotacje     adnotacje
	RodzajFaktury string
	DodatkowyOpis []opis `xml:",omitempty"`
	FaWiersz      []wiersz
	Platnosc      platnosc
}

type faktura struct {
	XMLName  xml.Name `xml:"http://crd.gov.pl/wzor/2025/06/25/13775/ Faktura"`
	Naglowek naglowek
	Podmiot1 podmiot1
	Podmiot2 podmiot2
	Fa       fa
}

var (
	nipRe   = regexp.MustCompile(`^[1-9]((\d[1-9])|([1-9]\d))\d{7}$`)
	vatUERe = regexp.MustCompile(`^(\d|[A-Z]|\+|\*){1,12}$`)
	ccRe    = regexp.MustCompile(`^[A-Z]{2}$`)
	curRe   = regexp.MustCompile(`^[A-Z]{3}$`)
	plRBRe  = regexp.MustCompile(`^\d{26}$`)
	ibanRe  = regexp.MustCompile(`^[A-Z0-9]{10,34}$`)
	swiftRe = regexp.MustCompile(`^[A-Z]{6}[A-Z0-9]{2}([A-Z0-9]{3}){0,1}$`)
)

var euCodes = map[string]bool{
	"AT": true, "BE": true, "BG": true, "CY": true, "CZ": true, "DK": true, "EE": true, "FI": true,
	"FR": true, "DE": true, "EL": true, "HR": true, "HU": true, "IE": true, "IT": true, "LV": true,
	"LT": true, "LU": true, "MT": true, "NL": true, "PL": true, "PT": true, "RO": true, "SK": true,
	"SI": true, "ES": true, "SE": true, "XI": true,
}

func date(t time.Time) string { return t.Format("2006-01-02") }

// normalizeNrRB writes a Polish IBAN as 26 digits and any other IBAN in full without spaces.
func normalizeNrRB(s string) (string, error) {
	s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
	if s == "" {
		return "", errors.New("bank account is required")
	}
	if rest, ok := strings.CutPrefix(s, "PL"); ok {
		s = rest
	}
	if plRBRe.MatchString(s) {
		return s, nil
	}
	if ibanRe.MatchString(s) && !strings.HasPrefix(s, "PL") && len(s) >= 15 {
		return s, nil
	}
	return "", fmt.Errorf("bank account %q is not a 26-digit Polish account or an IBAN", s)
}

func buildParty1(p Party) (podmiot1, error) {
	if !nipRe.MatchString(p.NIP) {
		return podmiot1{}, fmt.Errorf("seller NIP %q is not a valid NIP", p.NIP)
	}
	if strings.TrimSpace(p.Name) == "" {
		return podmiot1{}, errors.New("seller name is required")
	}
	if err := checkParty("seller", p); err != nil {
		return podmiot1{}, err
	}
	a, err := buildAdres("seller", p)
	if err != nil {
		return podmiot1{}, err
	}
	out := podmiot1{DaneIdentyfikacyjne: daneID{NIP: p.NIP, Nazwa: p.Name}, Adres: *a}
	if p.Email != "" {
		out.DaneKontaktowe = &kontakt{Email: p.Email}
	}
	return out, nil
}

// checkParty rejects control characters in the free text of a party.
func checkParty(who string, p Party) error {
	for _, f := range []struct{ what, v string }{{"name", p.Name}, {"address", p.AddressL1}, {"address line 2", p.AddressL2}, {"e-mail", p.Email}} {
		if err := validate.NoControl(f.v); err != nil {
			return fmt.Errorf("%s %s: %w", who, f.what, err)
		}
	}
	return nil
}

func buildAdres(who string, p Party) (*adres, error) {
	if strings.TrimSpace(p.AddressL1) == "" {
		return nil, fmt.Errorf("%s address is required", who)
	}
	cc := p.CountryCode
	if cc == "" {
		cc = "PL"
	}
	if !ccRe.MatchString(cc) {
		return nil, fmt.Errorf("%s country code %q is not a two-letter ISO code", who, cc)
	}
	return &adres{KodKraju: cc, AdresL1: p.AddressL1, AdresL2: p.AddressL2}, nil
}

func buildParty2(p Party) (podmiot2, error) {
	if strings.TrimSpace(p.Name) == "" {
		return podmiot2{}, errors.New("buyer name is required")
	}
	if err := checkParty("buyer", p); err != nil {
		return podmiot2{}, err
	}
	id := daneID{Nazwa: p.Name}
	switch {
	case p.NIP != "":
		if !nipRe.MatchString(p.NIP) {
			return podmiot2{}, fmt.Errorf("buyer NIP %q is not a valid NIP", p.NIP)
		}
		id.NIP = p.NIP
	case p.EUCode != "" || p.VatNumber != "":
		if !euCodes[p.EUCode] {
			return podmiot2{}, fmt.Errorf("buyer EU code %q is not an EU VAT prefix (Greece is EL)", p.EUCode)
		}
		if !vatUERe.MatchString(p.VatNumber) {
			return podmiot2{}, fmt.Errorf("buyer VAT number %q must be 1-12 digits or capital letters, without the country prefix", p.VatNumber)
		}
		id.KodUE, id.NrVatUE = p.EUCode, p.VatNumber
	default:
		return podmiot2{}, errors.New("buyer needs a NIP, or an EU code and VAT number")
	}
	a, err := buildAdres("buyer", p)
	if err != nil {
		return podmiot2{}, err
	}
	out := podmiot2{DaneIdentyfikacyjne: id, Adres: a, JST: 2, GV: 2}
	if p.Email != "" {
		out.DaneKontaktowe = &kontakt{Email: p.Email}
	}
	return out, nil
}

// Build renders the invoice as FA(3) XML: declaration, indented document, final newline.
// The same Input always yields the same bytes. Dates are written as the calendar date in
// the location of the given time values (callers pass Europe/Warsaw dates).
func Build(in Input) ([]byte, error) {
	cur := in.Currency
	if cur == "" {
		cur = "PLN"
	}
	if !curRe.MatchString(cur) {
		return nil, fmt.Errorf("currency %q is not a three-letter ISO code", cur)
	}
	foreign := cur != "PLN"
	if strings.TrimSpace(in.Number) == "" {
		return nil, errors.New("invoice number is required")
	}
	if in.IssueDate.IsZero() {
		return nil, errors.New("issue date is required")
	}
	if in.DueDate.IsZero() {
		return nil, errors.New("due date is required")
	}
	if in.CreatedAt.IsZero() {
		return nil, errors.New("creation time is required")
	}
	if len(in.Lines) == 0 {
		return nil, errors.New("invoice has no lines")
	}
	if len(in.Lines) > MaxLines {
		return nil, fmt.Errorf("invoice has %d lines; FA(3) allows at most %d", len(in.Lines), MaxLines)
	}
	p1, err := buildParty1(in.Seller)
	if err != nil {
		return nil, err
	}
	p2, err := buildParty2(in.Buyer)
	if err != nil {
		return nil, err
	}
	rb, err := normalizeNrRB(in.BankAccount)
	if err != nil {
		return nil, err
	}
	if in.BankSWIFT != "" && !swiftRe.MatchString(in.BankSWIFT) {
		return nil, fmt.Errorf("bank SWIFT %q is not valid: use 8 or 11 capital letters and digits, e.g. BREXPLPW", in.BankSWIFT)
	}
	if in.BankName != "" {
		if err := checkToken("bank name", in.BankName, 256); err != nil {
			return nil, err
		}
	}
	if foreign && (in.Exchange == nil || in.Exchange.MidMicro <= 0) {
		return nil, fmt.Errorf("an exchange rate is required for %s", cur)
	}

	f := fa{
		KodWaluty:     cur,
		P1:            date(in.IssueDate),
		P1M:           in.PlaceOfIssue,
		P2:            in.Number,
		RodzajFaktury: "VAT",
		Platnosc: platnosc{
			TerminPlatnosci: termin{Termin: date(in.DueDate)},
			FormaPlatnosci:  6,
			RachunekBankowy: rachunek{NrRB: rb, SWIFT: in.BankSWIFT, NazwaBanku: in.BankName},
		},
		P15: money.FormatXML(in.Summary.Gross),
	}
	if !in.SaleDate.IsZero() && date(in.SaleDate) != date(in.IssueDate) {
		f.P6 = date(in.SaleDate)
	}

	hasZW := false
	for _, g := range in.Summary.Groups {
		net := money.FormatXML(g.Net)
		vat := money.FormatXML(g.VAT)
		var vatW string
		if foreign && g.Rate.VATField != "" {
			v, ok := in.VATPLN[g.Rate.Code]
			if !ok {
				return nil, fmt.Errorf("VAT in PLN is missing for rate %s", g.Rate.Code)
			}
			vatW = money.FormatXML(v)
		}
		switch g.Rate.NetField {
		case "P_13_1":
			f.P131, f.P141, f.P141W = net, vat, vatW
		case "P_13_2":
			f.P132, f.P142, f.P142W = net, vat, vatW
		case "P_13_3":
			f.P133, f.P143, f.P143W = net, vat, vatW
		case "P_13_7":
			f.P137 = net
			hasZW = true
		default:
			return nil, fmt.Errorf("rate %q has no supported FA(3) net field %q", g.Rate.Code, g.Rate.NetField)
		}
	}
	if len(in.Summary.Groups) == 0 {
		return nil, errors.New("invoice summary has no rate groups")
	}

	kurs := ""
	if foreign {
		kurs = money.FormatMicro(in.Exchange.MidMicro)
		f.DodatkowyOpis = []opis{{
			Klucz: "Kurs waluty",
			Wartosc: fmt.Sprintf("1 %s = %s PLN, tabela %s z %s",
				cur, kurs, in.Exchange.Table, in.Exchange.EffectiveDate),
		}}
	}
	for i, l := range in.Lines {
		if l.Rate.NetField == "" {
			return nil, fmt.Errorf("line %d has no VAT rate", i+1)
		}
		if l.Rate.Code == "zw" {
			hasZW = true
		}
		if strings.TrimSpace(l.Description) == "" {
			return nil, fmt.Errorf("line %d has no description", i+1)
		}
		for _, c := range []struct{ what, v string }{{"description", l.Description}, {"unit", l.Unit}} {
			if err := validate.NoControl(c.v); err != nil {
				return nil, fmt.Errorf("line %d %s: %w", i+1, c.what, err)
			}
		}
		for _, c := range []struct {
			what, v string
			max     int
		}{{"index", l.Index, 50}, {"GTIN", l.GTIN, 20}, {"PKWiU", l.PKWiU, 50}} {
			if c.v != "" {
				if err := checkToken(fmt.Sprintf("line %d %s", i+1, c.what), c.v, c.max); err != nil {
					return nil, err
				}
			}
		}
		w := wiersz{
			NrWierszaFa: l.No,
			P7:          l.Description,
			Indeks:      l.Index,
			GTIN:        l.GTIN,
			PKWiU:       l.PKWiU,
			P8A:         l.Unit,
			P8B:         money.FormatQuantityXML(l.Qty),
			P9A:         money.FormatXML(l.UnitPrice),
			P11:         money.FormatXML(l.Net),
			P12:         l.Rate.Code,
			KursWaluty:  kurs,
		}
		if l.No <= 0 {
			w.NrWierszaFa = i + 1
		}
		f.FaWiersz = append(f.FaWiersz, w)
	}

	f.Adnotacje = adnotacje{
		P16: 2, P17: 2, P18: 2, P18A: 2,
		Zwolnienie:           zwolnienie{P19N: 1},
		NoweSrodkiTransportu: noweSrodki{P22N: 1},
		P23:                  2,
		PMarzy:               pMarzy{P: 1},
	}
	if hasZW {
		if strings.TrimSpace(in.ExemptionBasis) == "" {
			return nil, errors.New("a zw line needs an exemption basis (exemption_basis)")
		}
		f.Adnotacje.Zwolnienie = zwolnienie{P19: 1, P19A: in.ExemptionBasis}
	}

	doc := faktura{
		Naglowek: naglowek{
			KodFormularza:     kodFormularza{Value: "FA", KodSystemowy: "FA (3)", WersjaSchemy: "1-0E"},
			WariantFormularza: 3,
			DataWytworzeniaFa: in.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			SystemInfo:        in.SystemInfo,
		},
		Podmiot1: p1,
		Podmiot2: p2,
		Fa:       f,
	}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("fa3: marshal: %w", err)
	}
	out := append([]byte(header), append(body, '\n')...)
	if err := checkSize(out); err != nil {
		return nil, err
	}
	return out, nil
}

// checkToken rejects a value the schema (xsd:token, whitespace collapsed) would not accept:
// blank, or longer than max characters.
func checkToken(what, v string, max int) error {
	if err := validate.NoControl(v); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	t := strings.TrimSpace(v)
	if t == "" {
		return fmt.Errorf("%s must not be blank", what)
	}
	if n := len([]rune(t)); n > max {
		return fmt.Errorf("%s is %d characters; the schema allows at most %d", what, n, max)
	}
	return nil
}

// MaxXMLSize is KSeF's limit for an invoice without attachments: 1 MB (1 000 000 bytes).
const MaxXMLSize = 1_000_000

func checkSize(xml []byte) error {
	if len(xml) > MaxXMLSize {
		return fmt.Errorf("fa3: the invoice XML is %d bytes, over KSeF's limit of %d bytes (1 MB)", len(xml), MaxXMLSize)
	}
	return nil
}
