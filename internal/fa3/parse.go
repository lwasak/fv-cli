package fa3

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/lwasak/fv-cli/internal/money"
)

// MaxParseSize is the largest document Parse reads (3 MB; KSeF allows 1 MB without
// attachments and 3 MB with them).
const MaxParseSize = 3 << 20

// NamespaceFA2 is the FA(2) target namespace; Parse reads it too, with a warning.
const NamespaceFA2 = "http://crd.gov.pl/wzor/2023/06/29/12648/"

// Document is the readable content of an FA(2) or FA(3) invoice written by any issuer.
// Every element of the schema is optional here: invoices from other software vary, so a
// missing value is the zero value and an unreadable one is left out with a Warning.
type Document struct {
	Namespace string
	// FormCode is kodSystemowy of the header, e.g. "FA (3)".
	FormCode string
	// Kind is RodzajFaktury: VAT, KOR, ZAL, ROZ, UPR, KOR_ZAL or KOR_ROZ.
	Kind                                                               string
	Number, IssueDate, SaleDate, PeriodFrom, PeriodTo, Place, Currency string
	// ExchangeRate is KursWalutyZ (invoice-level rate for a foreign currency invoice).
	ExchangeRate  string
	Seller, Buyer DocParty
	// Others are Podmiot3 (with Role) and the authorised entity.
	Others []DocParty
	Lines  []DocLine
	Totals []DocTotal
	// Gross is P_15; HasGross is false when the element is missing.
	Gross    money.Amount
	HasGross bool
	// Corrected, CorrectionReason and CorrectionType are set on correction invoices.
	Corrected        []CorrectedRef
	CorrectionReason string
	CorrectionType   string
	// OrderLines is the number of ZamowienieWiersz of an advance invoice (they are not parsed).
	OrderLines int
	Payment    DocPayment
	Flags      DocFlags
	// Notes are the DodatkowyOpis key/value pairs.
	Notes    []KV
	Warnings []string
}

// KV is a key/value pair of DodatkowyOpis.
type KV struct{ Key, Value string }

// DocParty is a seller, a buyer or another party of the invoice.
type DocParty struct {
	// Prefix is PrefiksPodatnika of the seller (the EU VAT prefix of the NIP).
	Prefix string
	// NIP, VatUE ("DE123456789", prefix and number) and OtherID ("US 12-345", country and
	// number) are the identifiers present; NoID is BrakID.
	NIP, VatUE, OtherID string
	NoID                bool
	Name                string
	Country             string
	AddressL1           string
	AddressL2           string
	Email, Phone        string
	// Role is the text of Rola for a Podmiot3, or "authorised entity".
	Role string
}

// ID returns the identifier to show: the NIP (with the prefix), the EU VAT number, the
// other identifier, or "".
func (p DocParty) ID() string {
	switch {
	case p.NIP != "":
		return p.Prefix + p.NIP
	case p.VatUE != "":
		return p.VatUE
	case p.OtherID != "":
		return p.OtherID
	}
	return ""
}

// DocLine is one FaWiersz. Quantities and unit prices stay text: a unit price (P_9A) has up
// to 8 decimals and a quantity up to 6, which money.Amount cannot hold.
type DocLine struct {
	No          int
	Description string
	Unit        string
	Qty         string
	// Index is Indeks, GTIN and PKWiU the product identifiers; each is empty when not given.
	Index, GTIN, PKWiU string
	// UnitPrice is P_9A (net) or P_9B (gross, UnitPriceGross true).
	UnitPrice      string
	UnitPriceGross bool
	// Discount is P_10.
	Discount string
	// Value is P_11 (net) or P_11A (gross, ValueGross true); HasValue is false when neither is set.
	Value      money.Amount
	ValueGross bool
	HasValue   bool
	// VAT is P_11Vat; HasVAT is false when it is not set.
	VAT    money.Amount
	HasVAT bool
	// Rate is P_12 as text (23, 8, 0 KR, zw, oo, np I ...), or P_12_XII with a percent sign.
	Rate         string
	ExchangeRate string // KursWaluty
	SaleDate     string // P_6A
	// Before marks a "before correction" row of a correction invoice (StanPrzed).
	Before bool
}

// DocTotal is one rate group of the summary (P_13_x with its P_14_x).
type DocTotal struct {
	// Key is the suffix of the XSD element: "1" for P_13_1, "6_1" for P_13_6_1.
	Key string
	// Label names the group: "23% / 22%", "0% KR", "zw", ...
	Label string
	Net   money.Amount
	// VAT is P_14_x; VATPLN is P_14_xW, the VAT in zloty of a foreign currency invoice.
	VAT, VATPLN       money.Amount
	HasVAT, HasVATPLN bool
}

// CorrectedRef is DaneFaKorygowanej: the invoice a correction refers to.
type CorrectedRef struct {
	Number, IssueDate, KSeFNumber string
	// NoKSeF is NrKSeFN: the corrected invoice has no KSeF number.
	NoKSeF bool
}

// DocPayment is Platnosc.
type DocPayment struct {
	Paid     bool
	PaidDate string
	// Method is FormaPlatnosci as text ("transfer", "cash" ...), or Other (OpisPlatnosci) for PlatnoscInna.
	Method string
	// Due lists TerminPlatnosci: a date, or a description such as "30 days from the delivery".
	Due      []DocDue
	Accounts []DocAccount
}

// DocDue is one TerminPlatnosci.
type DocDue struct{ Date, Description string }

// DocAccount is one RachunekBankowy or RachunekBankowyFaktora.
type DocAccount struct {
	Number, Bank, SWIFT, Description string
	// Factor marks a RachunekBankowyFaktora: the account of the factor, to which the
	// payment must go when the receivable was assigned.
	Factor bool
}

// DocFlags are the annotations (Adnotacje) that change how the invoice is read.
type DocFlags struct {
	CashAccounting    bool // P_16
	SelfInvoicing     bool // P_17
	ReverseCharge     bool // P_18
	SplitPayment      bool // P_18A
	Exempt            bool // Zwolnienie/P_19
	ExemptionBasis    []string
	Margin            bool // any PMarzy procedure
	NewTransportMeans bool // NoweSrodkiTransportu/P_22
	TripartiteSimple  bool // P_23
}

type xmlParty struct {
	Prefix string `xml:"PrefiksPodatnika"`
	ID     struct {
		NIP     string `xml:"NIP"`
		IDWew   string `xml:"IDWew"`
		KodUE   string `xml:"KodUE"`
		NrVatUE string `xml:"NrVatUE"`
		Kraj    string `xml:"KodKraju"`
		NrID    string `xml:"NrID"`
		BrakID  string `xml:"BrakID"`
		Nazwa   string `xml:"Nazwa"`
	} `xml:"DaneIdentyfikacyjne"`
	Adres struct {
		Kraj string `xml:"KodKraju"`
		L1   string `xml:"AdresL1"`
		L2   string `xml:"AdresL2"`
	} `xml:"Adres"`
	Kontakt []struct {
		Email string `xml:"Email"`
		Tel   string `xml:"Telefon"`
	} `xml:"DaneKontaktowe"`
	Rola     string `xml:"Rola"`
	RolaInna string `xml:"RolaInna"`
	OpisRoli string `xml:"OpisRoli"`
	RolaPU   string `xml:"RolaPU"`
}

type xmlDoc struct {
	XMLName  xml.Name `xml:"Faktura"`
	Naglowek struct {
		Kod struct {
			System string `xml:"kodSystemowy,attr"`
		} `xml:"KodFormularza"`
	} `xml:"Naglowek"`
	Podmiot1 xmlParty   `xml:"Podmiot1"`
	Podmiot2 xmlParty   `xml:"Podmiot2"`
	Podmiot3 []xmlParty `xml:"Podmiot3"`
	PU       []xmlParty `xml:"PodmiotUpowazniony"`
	Fa       xmlFa      `xml:"Fa"`
}

type xmlFa struct {
	Waluta string `xml:"KodWaluty"`
	P1     string `xml:"P_1"`
	P1M    string `xml:"P_1M"`
	P2     string `xml:"P_2"`
	P6     string `xml:"P_6"`
	P6Od   string `xml:"OkresFa>P_6_Od"`
	P6Do   string `xml:"OkresFa>P_6_Do"`
	P131   string `xml:"P_13_1"`
	P141   string `xml:"P_14_1"`
	P141W  string `xml:"P_14_1W"`
	P132   string `xml:"P_13_2"`
	P142   string `xml:"P_14_2"`
	P142W  string `xml:"P_14_2W"`
	P133   string `xml:"P_13_3"`
	P143   string `xml:"P_14_3"`
	P143W  string `xml:"P_14_3W"`
	P134   string `xml:"P_13_4"`
	P144   string `xml:"P_14_4"`
	P144W  string `xml:"P_14_4W"`
	P135   string `xml:"P_13_5"`
	P145   string `xml:"P_14_5"`
	P1361  string `xml:"P_13_6_1"`
	P1362  string `xml:"P_13_6_2"`
	P1363  string `xml:"P_13_6_3"`
	P137   string `xml:"P_13_7"`
	P138   string `xml:"P_13_8"`
	P139   string `xml:"P_13_9"`
	P1310  string `xml:"P_13_10"`
	P1311  string `xml:"P_13_11"`
	P15    string `xml:"P_15"`
	KursZ  string `xml:"KursWalutyZ"`
	Adn    struct {
		P16  string `xml:"P_16"`
		P17  string `xml:"P_17"`
		P18  string `xml:"P_18"`
		P18A string `xml:"P_18A"`
		Zw   struct {
			P19  string `xml:"P_19"`
			P19A string `xml:"P_19A"`
			P19B string `xml:"P_19B"`
			P19C string `xml:"P_19C"`
		} `xml:"Zwolnienie"`
		Nst struct {
			P22 string `xml:"P_22"`
		} `xml:"NoweSrodkiTransportu"`
		P23 string `xml:"P_23"`
		Mar struct {
			P   string `xml:"P_PMarzy"`
			P2  string `xml:"P_PMarzy_2"`
			P31 string `xml:"P_PMarzy_3_1"`
			P32 string `xml:"P_PMarzy_3_2"`
			P33 string `xml:"P_PMarzy_3_3"`
		} `xml:"PMarzy"`
	} `xml:"Adnotacje"`
	Rodzaj     string `xml:"RodzajFaktury"`
	Przycz     string `xml:"PrzyczynaKorekty"`
	Typ        string `xml:"TypKorekty"`
	Korygowana []struct {
		Data string `xml:"DataWystFaKorygowanej"`
		Nr   string `xml:"NrFaKorygowanej"`
		KSeF string `xml:"NrKSeFFaKorygowanej"`
		NoK  string `xml:"NrKSeFN"`
	} `xml:"DaneFaKorygowanej"`
	Opisy []struct {
		Klucz   string `xml:"Klucz"`
		Wartosc string `xml:"Wartosc"`
	} `xml:"DodatkowyOpis"`
	Wiersze []struct {
		Nr     string `xml:"NrWierszaFa"`
		P6A    string `xml:"P_6A"`
		P7     string `xml:"P_7"`
		Indeks string `xml:"Indeks"`
		GTIN   string `xml:"GTIN"`
		PKWiU  string `xml:"PKWiU"`
		P8A    string `xml:"P_8A"`
		P8B    string `xml:"P_8B"`
		P9A    string `xml:"P_9A"`
		P9B    string `xml:"P_9B"`
		P10    string `xml:"P_10"`
		P11    string `xml:"P_11"`
		P11A   string `xml:"P_11A"`
		P11Vat string `xml:"P_11Vat"`
		P12    string `xml:"P_12"`
		P12XII string `xml:"P_12_XII"`
		Kurs   string `xml:"KursWaluty"`
		Przed  string `xml:"StanPrzed"`
	} `xml:"FaWiersz"`
	Platnosc struct {
		Zaplacono string `xml:"Zaplacono"`
		Data      string `xml:"DataZaplaty"`
		Terminy   []struct {
			Termin string `xml:"Termin"`
			Opis   struct {
				Ilosc string `xml:"Ilosc"`
				Jedn  string `xml:"Jednostka"`
				Zdarz string `xml:"ZdarzeniePoczatkowe"`
			} `xml:"TerminOpis"`
		} `xml:"TerminPlatnosci"`
		Forma    string `xml:"FormaPlatnosci"`
		Inna     string `xml:"PlatnoscInna"`
		Opis     string `xml:"OpisPlatnosci"`
		Rachunki []struct {
			Nr    string `xml:"NrRB"`
			Swift string `xml:"SWIFT"`
			Bank  string `xml:"NazwaBanku"`
			Opis  string `xml:"OpisRachunku"`
		} `xml:"RachunekBankowy"`
		RachunkiFaktora []struct {
			Nr    string `xml:"NrRB"`
			Swift string `xml:"SWIFT"`
			Bank  string `xml:"NazwaBanku"`
			Opis  string `xml:"OpisRachunku"`
		} `xml:"RachunekBankowyFaktora"`
	} `xml:"Platnosc"`
	Zamowienie struct {
		Wiersze []struct{} `xml:"ZamowienieWiersz"`
	} `xml:"Zamowienie"`
}

var paymentMethods = map[string]string{"1": "cash", "2": "card", "3": "voucher", "4": "cheque", "5": "credit", "6": "transfer", "7": "mobile"}

var thirdPartyRoles = map[string]string{
	"1": "factor", "2": "recipient", "3": "original entity", "4": "additional buyer", "5": "invoice issuer",
	"6": "payer", "7": "local government unit (issuer)", "8": "local government unit (recipient)",
	"9": "VAT group member (issuer)", "10": "VAT group member (recipient)", "11": "employee",
}

// Parse reads an FA(2) or FA(3) invoice. Only the local names of elements count, so any
// namespace prefix works. The root must be Faktura; another namespace than FA(3) adds a
// warning. Input over MaxParseSize is refused. encoding/xml does not resolve external
// entities.
func Parse(b []byte) (*Document, error) {
	if len(b) > MaxParseSize {
		return nil, fmt.Errorf("fa3: the document is %d bytes, over the %d byte limit of the viewer", len(b), MaxParseSize)
	}
	var x xmlDoc
	dec := xml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&x); err != nil {
		return nil, fmt.Errorf("fa3: not a readable invoice: %w", err)
	}
	d := &Document{Namespace: x.XMLName.Space, FormCode: strings.TrimSpace(x.Naglowek.Kod.System)}
	switch x.XMLName.Space {
	case Namespace:
	case NamespaceFA2:
		d.Warnings = append(d.Warnings, "the invoice uses the FA(2) schema; fields new in FA(3) are not shown")
	default:
		d.Warnings = append(d.Warnings, fmt.Sprintf("unexpected namespace %q; read as FA(3)", x.XMLName.Space))
	}
	f := &x.Fa
	d.Kind = strings.TrimSpace(f.Rodzaj)
	d.Number, d.IssueDate, d.SaleDate = strings.TrimSpace(f.P2), strings.TrimSpace(f.P1), strings.TrimSpace(f.P6)
	d.PeriodFrom, d.PeriodTo = strings.TrimSpace(f.P6Od), strings.TrimSpace(f.P6Do)
	d.Place, d.Currency, d.ExchangeRate = strings.TrimSpace(f.P1M), strings.TrimSpace(f.Waluta), strings.TrimSpace(f.KursZ)
	d.Seller, d.Buyer = party(x.Podmiot1), party(x.Podmiot2)
	for _, p := range x.Podmiot3 {
		dp := party(p)
		dp.Role = thirdPartyRoles[strings.TrimSpace(p.Rola)]
		if dp.Role == "" {
			dp.Role = strings.TrimSpace(p.OpisRoli)
		}
		if dp.Role == "" {
			dp.Role = "other party"
		}
		d.Others = append(d.Others, dp)
	}
	for _, p := range x.PU {
		dp := party(p)
		dp.Role = "authorised entity"
		d.Others = append(d.Others, dp)
	}

	amount := func(what, s string) (money.Amount, bool) {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, false
		}
		a, err := money.ParseXML(s)
		if err != nil {
			d.Warnings = append(d.Warnings, fmt.Sprintf("%s: unreadable amount %q", what, s))
			return 0, false
		}
		return a, true
	}
	d.Gross, d.HasGross = amount("P_15", f.P15)
	d.Totals = totals(f, amount)

	for i, w := range f.Wiersze {
		l := DocLine{Description: strings.TrimSpace(w.P7), Unit: strings.TrimSpace(w.P8A), Qty: strings.TrimSpace(w.P8B),
			Index: strings.TrimSpace(w.Indeks), GTIN: strings.TrimSpace(w.GTIN), PKWiU: strings.TrimSpace(w.PKWiU),
			Discount: strings.TrimSpace(w.P10), ExchangeRate: strings.TrimSpace(w.Kurs), SaleDate: strings.TrimSpace(w.P6A),
			Before: strings.TrimSpace(w.Przed) == "1"}
		if n, err := strconv.Atoi(strings.TrimSpace(w.Nr)); err == nil {
			l.No = n
		}
		if l.No == 0 {
			l.No = i + 1
		}
		if s := strings.TrimSpace(w.P9A); s != "" {
			l.UnitPrice = s
		} else if s := strings.TrimSpace(w.P9B); s != "" {
			l.UnitPrice, l.UnitPriceGross = s, true
		}
		what := fmt.Sprintf("line %d", l.No)
		if a, ok := amount(what+" P_11", w.P11); ok {
			l.Value, l.HasValue = a, true
		} else if a, ok := amount(what+" P_11A", w.P11A); ok {
			l.Value, l.ValueGross, l.HasValue = a, true, true
		}
		l.VAT, l.HasVAT = amount(what+" P_11Vat", w.P11Vat)
		l.Rate = strings.TrimSpace(w.P12)
		if l.Rate == "" && strings.TrimSpace(w.P12XII) != "" {
			l.Rate = strings.TrimSpace(w.P12XII) + "%"
		}
		d.Lines = append(d.Lines, l)
	}
	d.OrderLines = len(f.Zamowienie.Wiersze)
	labelTotals(d)

	d.CorrectionReason, d.CorrectionType = strings.TrimSpace(f.Przycz), strings.TrimSpace(f.Typ)
	for _, c := range f.Korygowana {
		d.Corrected = append(d.Corrected, CorrectedRef{Number: strings.TrimSpace(c.Nr), IssueDate: strings.TrimSpace(c.Data),
			KSeFNumber: strings.TrimSpace(c.KSeF), NoKSeF: strings.TrimSpace(c.NoK) == "1"})
	}
	for _, o := range f.Opisy {
		d.Notes = append(d.Notes, KV{strings.TrimSpace(o.Klucz), strings.TrimSpace(o.Wartosc)})
	}

	p := &f.Platnosc
	d.Payment.Paid, d.Payment.PaidDate = strings.TrimSpace(p.Zaplacono) == "1", strings.TrimSpace(p.Data)
	switch {
	case strings.TrimSpace(p.Forma) != "":
		d.Payment.Method = paymentMethods[strings.TrimSpace(p.Forma)]
		if d.Payment.Method == "" {
			d.Payment.Method = strings.TrimSpace(p.Forma)
		}
	case strings.TrimSpace(p.Inna) == "1":
		d.Payment.Method = strings.TrimSpace(p.Opis)
	}
	for _, t := range p.Terminy {
		due := DocDue{Date: strings.TrimSpace(t.Termin)}
		if o := t.Opis; strings.TrimSpace(o.Ilosc) != "" || strings.TrimSpace(o.Jedn) != "" || strings.TrimSpace(o.Zdarz) != "" {
			due.Description = strings.Join(strings.Fields(strings.TrimSpace(o.Ilosc)+" "+strings.TrimSpace(o.Jedn)+" from "+strings.TrimSpace(o.Zdarz)), " ")
		}
		d.Payment.Due = append(d.Payment.Due, due)
	}
	for _, r := range p.Rachunki {
		d.Payment.Accounts = append(d.Payment.Accounts, DocAccount{Number: strings.TrimSpace(r.Nr), Bank: strings.TrimSpace(r.Bank),
			SWIFT: strings.TrimSpace(r.Swift), Description: strings.TrimSpace(r.Opis)})
	}
	for _, r := range p.RachunkiFaktora {
		d.Payment.Accounts = append(d.Payment.Accounts, DocAccount{Number: strings.TrimSpace(r.Nr), Bank: strings.TrimSpace(r.Bank),
			SWIFT: strings.TrimSpace(r.Swift), Description: strings.TrimSpace(r.Opis), Factor: true})
	}

	a := &f.Adn
	one := func(s string) bool { return strings.TrimSpace(s) == "1" }
	d.Flags = DocFlags{
		CashAccounting: one(a.P16), SelfInvoicing: one(a.P17), ReverseCharge: one(a.P18), SplitPayment: one(a.P18A),
		Exempt: one(a.Zw.P19), NewTransportMeans: one(a.Nst.P22), TripartiteSimple: one(a.P23),
		Margin: one(a.Mar.P) || one(a.Mar.P2) || one(a.Mar.P31) || one(a.Mar.P32) || one(a.Mar.P33),
	}
	for _, s := range []string{a.Zw.P19A, a.Zw.P19B, a.Zw.P19C} {
		if s = strings.TrimSpace(s); s != "" {
			d.Flags.ExemptionBasis = append(d.Flags.ExemptionBasis, s)
		}
	}
	if d.Number == "" && d.IssueDate == "" && len(d.Lines) == 0 && !d.HasGross {
		return nil, errors.New("fa3: the document has no invoice content (Fa)")
	}
	return d, nil
}

func party(p xmlParty) DocParty {
	id := p.ID
	dp := DocParty{Prefix: strings.TrimSpace(p.Prefix), NIP: strings.TrimSpace(id.NIP), Name: strings.TrimSpace(id.Nazwa),
		NoID: strings.TrimSpace(id.BrakID) == "1", Country: strings.TrimSpace(p.Adres.Kraj),
		AddressL1: strings.TrimSpace(p.Adres.L1), AddressL2: strings.TrimSpace(p.Adres.L2)}
	if dp.NIP == "" && strings.TrimSpace(id.IDWew) != "" {
		dp.NIP = strings.TrimSpace(id.IDWew)
	}
	if v := strings.TrimSpace(id.NrVatUE); v != "" {
		dp.VatUE = strings.TrimSpace(id.KodUE) + v
	}
	if v := strings.TrimSpace(id.NrID); v != "" {
		dp.OtherID = strings.TrimSpace(strings.TrimSpace(id.Kraj) + " " + v)
	}
	for _, k := range p.Kontakt {
		if dp.Email == "" {
			dp.Email = strings.TrimSpace(k.Email)
		}
		if dp.Phone == "" {
			dp.Phone = strings.TrimSpace(k.Tel)
		}
	}
	return dp
}

type amountFn func(what, s string) (money.Amount, bool)

// totals walks the fixed table of summary groups in schema order.
func totals(f *xmlFa, amount amountFn) []DocTotal {
	rows := []struct{ key, label, net, vat, vatw string }{
		{"1", "23% / 22%", f.P131, f.P141, f.P141W},
		{"2", "8% / 7%", f.P132, f.P142, f.P142W},
		{"3", "5%", f.P133, f.P143, f.P143W},
		{"4", "4% / 3% (flat rate)", f.P134, f.P144, f.P144W},
		{"5", "OSS", f.P135, f.P145, ""},
		{"6_1", "0% KR", f.P1361, "", ""},
		{"6_2", "0% WDT", f.P1362, "", ""},
		{"6_3", "0% EX", f.P1363, "", ""},
		{"7", "zw", f.P137, "", ""},
		{"8", "np I", f.P138, "", ""},
		{"9", "np II", f.P139, "", ""},
		{"10", "oo", f.P1310, "", ""},
		{"11", "margin", f.P1311, "", ""},
	}
	var out []DocTotal
	for _, r := range rows {
		net, okNet := amount("P_13_"+r.key, r.net)
		vat, okVat := amount("P_14_"+r.key, r.vat)
		vatw, okW := amount("P_14_"+r.key+"W", r.vatw)
		if !okNet && !okVat && !okW {
			continue
		}
		out = append(out, DocTotal{Key: r.key, Label: r.label, Net: net, VAT: vat, VATPLN: vatw, HasVAT: okVat, HasVATPLN: okW})
	}
	return out
}

// bucketRates lists the P_12 values that fall into the summary groups P_13_1 .. P_13_4.
var bucketRates = map[string][]string{"1": {"23", "22"}, "2": {"8", "7"}, "3": {"5"}, "4": {"4", "3"}}

// labelTotals replaces the name of a rate bucket ("23% / 22%") with the rate or rates the
// lines of the invoice actually use ("23%"). A bucket no line points to keeps its name.
func labelTotals(d *Document) {
	used := map[string]bool{}
	var oss []string
	for _, l := range d.Lines {
		if l.Before {
			continue
		}
		used[l.Rate] = true
		if strings.HasSuffix(l.Rate, "%") && !slices.Contains(oss, l.Rate) {
			oss = append(oss, l.Rate)
		}
	}
	for i, t := range d.Totals {
		var rates []string
		switch t.Key {
		case "1", "2", "3", "4":
			for _, r := range bucketRates[t.Key] {
				if used[r] {
					rates = append(rates, r+"%")
				}
			}
		case "5":
			if len(oss) > 0 {
				rates = []string{"OSS " + strings.Join(oss, " / ")}
			}
		}
		if len(rates) > 0 {
			d.Totals[i].Label = strings.Join(rates, " / ")
		}
	}
}
