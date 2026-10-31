package fa3

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/money"
)

func parseSample(t *testing.T, name string) *Document {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "parse", name+".xml"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return d
}

// TestParse_RoundTripsEveryGolden checks Parse(Build(x)) against the inputs of Build.
func TestParse_RoundTripsEveryGolden(t *testing.T) {
	for name, in := range goldenInputs(t) {
		t.Run(name, func(t *testing.T) {
			b, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			d, err := Parse(b)
			if err != nil {
				t.Fatal(err)
			}
			if len(d.Warnings) != 0 {
				t.Errorf("warnings %v", d.Warnings)
			}
			cur := in.Currency
			if cur == "" {
				cur = "PLN"
			}
			sale := ""
			if date(in.SaleDate) != date(in.IssueDate) && !in.SaleDate.IsZero() {
				sale = date(in.SaleDate)
			}
			if d.Namespace != Namespace || d.FormCode != "FA (3)" || d.Kind != "VAT" || d.Number != in.Number ||
				d.IssueDate != date(in.IssueDate) || d.SaleDate != sale || d.Place != in.PlaceOfIssue || d.Currency != cur {
				t.Errorf("header %+v", d)
			}
			if d.Seller.NIP != in.Seller.NIP || d.Seller.Name != in.Seller.Name || d.Seller.AddressL1 != in.Seller.AddressL1 ||
				d.Seller.AddressL2 != in.Seller.AddressL2 || d.Seller.Email != in.Seller.Email || d.Seller.Country != "PL" {
				t.Errorf("seller %+v", d.Seller)
			}
			if d.Buyer.NIP != in.Buyer.NIP || d.Buyer.Name != in.Buyer.Name || d.Buyer.AddressL1 != in.Buyer.AddressL1 {
				t.Errorf("buyer %+v", d.Buyer)
			}
			if in.Buyer.EUCode != "" && (d.Buyer.VatUE != in.Buyer.EUCode+in.Buyer.VatNumber || d.Buyer.Country != in.Buyer.CountryCode) {
				t.Errorf("EU buyer %+v", d.Buyer)
			}
			if len(d.Lines) != len(in.Lines) {
				t.Fatalf("%d lines, want %d", len(d.Lines), len(in.Lines))
			}
			for i, l := range in.Lines {
				g := d.Lines[i]
				if g.No != i+1 || g.Description != l.Description || g.Unit != l.Unit || g.Qty != money.FormatQuantityXML(l.Qty) ||
					g.UnitPrice != money.FormatXML(l.UnitPrice) || g.UnitPriceGross || g.Value != l.Net || g.ValueGross || !g.HasValue || g.Rate != l.Rate.Code {
					t.Errorf("line %d = %+v", i, g)
				}
			}
			if !d.HasGross || d.Gross != in.Summary.Gross {
				t.Errorf("gross %v %v, want %v", d.Gross, d.HasGross, in.Summary.Gross)
			}
			if len(d.Totals) != len(in.Summary.Groups) {
				t.Fatalf("%d totals, want %d", len(d.Totals), len(in.Summary.Groups))
			}
			for i, g := range in.Summary.Groups {
				tt := d.Totals[i]
				if tt.Net != g.Net || (g.Rate.VATField != "" && (!tt.HasVAT || tt.VAT != g.VAT)) {
					t.Errorf("total %d = %+v, want %+v", i, tt, g)
				}
				if v, ok := in.VATPLN[g.Rate.Code]; ok && (!tt.HasVATPLN || tt.VATPLN != v) {
					t.Errorf("total %d VAT in PLN = %+v, want %v", i, tt, v)
				}
			}
			if d.Payment.Method != "transfer" || len(d.Payment.Due) != 1 || d.Payment.Due[0].Date != date(in.DueDate) ||
				len(d.Payment.Accounts) != 1 || !strings.HasSuffix(in.BankAccount, strings.Join(strings.Fields(d.Payment.Accounts[0].Number[len(d.Payment.Accounts[0].Number)-4:]), "")) {
				t.Errorf("payment %+v", d.Payment)
			}
			if in.ExemptionBasis != "" && (!d.Flags.Exempt || len(d.Flags.ExemptionBasis) != 1 || d.Flags.ExemptionBasis[0] != in.ExemptionBasis) {
				t.Errorf("flags %+v", d.Flags)
			}
			if in.ExemptionBasis == "" && d.Flags.Exempt {
				t.Errorf("flags %+v", d.Flags)
			}
			if in.Exchange != nil && (len(d.Notes) != 1 || d.Notes[0].Key != "Kurs waluty" || d.Lines[0].ExchangeRate != money.FormatMicro(in.Exchange.MidMicro)) {
				t.Errorf("exchange: notes %+v line %+v", d.Notes, d.Lines[0])
			}
			if d.Flags.SplitPayment || d.Flags.ReverseCharge || d.Flags.CashAccounting || d.Flags.SelfInvoicing {
				t.Errorf("flags %+v", d.Flags)
			}
		})
	}
}

func TestParse_ForeignIssuerWithPrefixAndGrossLines(t *testing.T) {
	d := parseSample(t, "foreign")
	if len(d.Warnings) != 0 {
		t.Errorf("warnings %v", d.Warnings)
	}
	if d.Namespace != Namespace || d.Number != "HB/10/2026/0457" || d.IssueDate != "2026-10-12" || d.SaleDate != "2026-10-10" {
		t.Errorf("header %+v", d)
	}
	if d.Seller.Prefix != "PL" || d.Seller.ID() != "PL5252248481" || d.Seller.Phone != "583001122" || d.Seller.Email != "biuro@hurtownia.example" ||
		!strings.HasPrefix(d.Seller.Name, "Przykładowa Hurtownia") {
		t.Errorf("seller %+v", d.Seller)
	}
	if len(d.Others) != 1 || d.Others[0].Role != "recipient" || d.Others[0].NIP != "5272525995" {
		t.Errorf("others %+v", d.Others)
	}
	if len(d.Lines) != 5 {
		t.Fatalf("%d lines", len(d.Lines))
	}
	l1 := d.Lines[0]
	if !l1.UnitPriceGross || l1.UnitPrice != "123.00" || !l1.ValueGross || l1.Value != 12300 || !l1.HasVAT || l1.VAT != 2300 || l1.Rate != "23" {
		t.Errorf("line 1 %+v", l1)
	}
	if l := d.Lines[1]; l.Qty != "2.5" || l.UnitPrice != "43.20" || l.Value != 10800 || !l.ValueGross {
		t.Errorf("line 2 %+v", l)
	}
	if l := d.Lines[2]; l.UnitPriceGross || l.ValueGross || l.UnitPrice != "12.5" || l.Value != 5000 || l.Rate != "0 KR" || l.HasVAT {
		t.Errorf("line 3 %+v", l)
	}
	if d.Lines[3].Rate != "np I" || d.Lines[4].Rate != "oo" {
		t.Errorf("rates %q %q", d.Lines[3].Rate, d.Lines[4].Rate)
	}
	var labels []string
	for _, tt := range d.Totals {
		labels = append(labels, tt.Label)
	}
	if strings.Join(labels, "|") != "23% / 22%|8% / 7%|0% KR|np I|oo" {
		t.Errorf("totals %v", labels)
	}
	if d.Totals[0].Net != 10000 || d.Totals[0].VAT != 2300 || d.Totals[2].HasVAT || d.Totals[2].Net != 5000 {
		t.Errorf("totals %+v", d.Totals)
	}
	if !d.HasGross || d.Gross != 33100 {
		t.Errorf("gross %v", d.Gross)
	}
	if !d.Flags.SplitPayment || !d.Flags.ReverseCharge || d.Flags.Exempt || d.Flags.Margin {
		t.Errorf("flags %+v", d.Flags)
	}
	p := d.Payment
	if len(p.Due) != 2 || p.Due[0].Date != "2026-10-26" || p.Due[1].Description != "30 dni from dostawy" || p.Method != "transfer" {
		t.Errorf("payment %+v", p)
	}
	if len(p.Accounts) != 2 || p.Accounts[0].Bank != "Bank Przykładowy S.A." || p.Accounts[1].SWIFT != "COBADEFFXXX" || p.Accounts[1].Number != "DE89370400440532013000" {
		t.Errorf("accounts %+v", p.Accounts)
	}
	if len(d.Notes) != 2 || d.Notes[1] != (KV{"Osoba kontaktowa", "Anna Nowak"}) {
		t.Errorf("notes %+v", d.Notes)
	}
}

func TestParse_Correction(t *testing.T) {
	d := parseSample(t, "kor")
	if d.Kind != "KOR" || d.CorrectionReason != "Zmniejszenie ilości usług" || d.CorrectionType != "1" {
		t.Errorf("header %+v", d)
	}
	if len(d.Corrected) != 1 || d.Corrected[0] != (CorrectedRef{Number: "FV/2026/10/001", IssueDate: "2026-10-05", KSeFNumber: "5252248481-20261005-010020A0A242-0A"}) {
		t.Errorf("corrected %+v", d.Corrected)
	}
	if !d.HasGross || d.Gross != -12300 || len(d.Totals) != 1 || d.Totals[0].Net != -10000 || d.Totals[0].VAT != -2300 {
		t.Errorf("amounts %v %+v", d.Gross, d.Totals)
	}
	if len(d.Lines) != 2 || !d.Lines[0].Before || d.Lines[1].Before || d.Lines[0].Value != 20000 || d.Lines[1].Value != 10000 {
		t.Errorf("lines %+v", d.Lines)
	}
	if !d.Payment.Paid || d.Payment.PaidDate != "2026-10-06" {
		t.Errorf("payment %+v", d.Payment)
	}
}

func TestParse_AdvanceInvoiceHasNoLines(t *testing.T) {
	d := parseSample(t, "zal")
	if d.Kind != "ZAL" || len(d.Lines) != 0 || d.OrderLines != 2 || d.Gross != 12300 {
		t.Errorf("%+v", d)
	}
}

func TestParse_ExemptionWithPeriodAndOtherPayment(t *testing.T) {
	d := parseSample(t, "zw")
	if d.PeriodFrom != "2026-10-01" || d.PeriodTo != "2026-10-31" || d.SaleDate != "" {
		t.Errorf("period %q..%q sale %q", d.PeriodFrom, d.PeriodTo, d.SaleDate)
	}
	if !d.Flags.Exempt || len(d.Flags.ExemptionBasis) != 1 || !strings.HasPrefix(d.Flags.ExemptionBasis[0], "art. 43 ust. 1 pkt 26") || !d.Flags.CashAccounting {
		t.Errorf("flags %+v", d.Flags)
	}
	if len(d.Totals) != 1 || d.Totals[0].Label != "zw" || d.Totals[0].HasVAT || d.Totals[0].Net != 150000 {
		t.Errorf("totals %+v", d.Totals)
	}
	if d.Payment.Method != "Wpłata gotówką w sekretariacie" {
		t.Errorf("payment %+v", d.Payment)
	}
}

func TestParse_FA2WarnsButReads(t *testing.T) {
	d := parseSample(t, "fa2")
	if d.Namespace != NamespaceFA2 || d.FormCode != "FA (2)" || d.Number != "S/2025/03/12" || d.Gross != 1230 {
		t.Errorf("%+v", d)
	}
	if len(d.Warnings) != 1 || !strings.Contains(d.Warnings[0], "FA(2)") {
		t.Errorf("warnings %v", d.Warnings)
	}
	if d.Buyer.AddressL1 != "" || len(d.Lines) != 1 {
		t.Errorf("%+v", d)
	}
}

func TestParse_UnknownNamespaceWarns(t *testing.T) {
	b := []byte(`<Faktura xmlns="urn:other"><Fa><P_2>X/1</P_2><P_15>1.00</P_15></Fa></Faktura>`)
	d, err := Parse(b)
	if err != nil || d.Number != "X/1" || len(d.Warnings) != 1 || !strings.Contains(d.Warnings[0], "urn:other") {
		t.Errorf("%+v %v", d, err)
	}
}

func TestParse_UnreadableAmountIsWarnedAndLeftOut(t *testing.T) {
	b := []byte(`<Faktura xmlns="` + Namespace + `"><Fa><P_2>X/1</P_2><P_15>1e3</P_15><FaWiersz><NrWierszaFa>1</NrWierszaFa><P_11>abc</P_11></FaWiersz></Fa></Faktura>`)
	d, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if d.HasGross || d.Lines[0].HasValue || len(d.Warnings) != 2 {
		t.Errorf("%+v", d)
	}
}

func TestParse_Errors(t *testing.T) {
	long := append([]byte(`<Faktura xmlns="`+Namespace+`"><Fa><P_2>`), bytes.Repeat([]byte("x"), MaxParseSize)...)
	long = append(long, []byte(`</P_2></Fa></Faktura>`)...)
	cases := map[string][]byte{
		"empty":      nil,
		"not xml":    []byte("hello"),
		"wrong root": []byte(`<Invoice xmlns="` + Namespace + `"><Fa><P_2>1</P_2></Fa></Invoice>`),
		"no content": []byte(`<Faktura xmlns="` + Namespace + `"><Naglowek/></Faktura>`),
		"too large":  long,
	}
	b, err := os.ReadFile(filepath.Join("testdata", "parse", "malformed.xml"))
	if err != nil {
		t.Fatal(err)
	}
	cases["malformed"] = b
	for name, in := range cases {
		if d, err := Parse(in); err == nil {
			t.Errorf("%s: accepted %+v", name, d)
		}
	}
}

func TestParse_DoesNotResolveEntities(t *testing.T) {
	b := []byte(`<?xml version="1.0"?><!DOCTYPE x [<!ENTITY e SYSTEM "file:///etc/passwd">]><Faktura xmlns="` + Namespace + `"><Fa><P_2>&e;</P_2></Fa></Faktura>`)
	if d, err := Parse(b); err == nil && strings.Contains(d.Number, "root:") {
		t.Fatal("external entity resolved")
	}
}

func TestParse_SamplesValidateAgainstTheSchema(t *testing.T) {
	work := t.TempDir()
	for _, name := range []string{"foreign", "kor", "zal", "zw"} {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata", "parse", name+".xml"))
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateWithXmllint(context.Background(), b, work)
			if errors.Is(err, ErrNoXmllint) {
				t.Skip("xmllint not on PATH")
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
