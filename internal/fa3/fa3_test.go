package fa3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/money"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func mustRate(t *testing.T, code string) money.Rate {
	t.Helper()
	r, err := money.ParseRate(code)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func baseInput(t *testing.T) Input {
	return Input{
		Seller: Party{NIP: "5252248481", Name: "Jan Kowalski Usługi IT", AddressL1: "ul. Długa 1/2", AddressL2: "80-001 Gdańsk", Email: "jan@example.pl"},
		Buyer:  Party{NIP: "5260250995", Name: "Odbiorca Sp. z o.o.", AddressL1: "ul. Marszałkowska 10", AddressL2: "00-001 Warszawa"},
		Number: "FV/2026/10/001", PlaceOfIssue: "Gdańsk", SystemInfo: "fv",
		BankAccount: "PL61 1090 1014 0000 0712 1981 2874",
		IssueDate:   day(2026, 10, 30), SaleDate: day(2026, 10, 31), DueDate: day(2026, 11, 13),
		CreatedAt: time.Date(2026, 10, 30, 10, 15, 30, 0, time.UTC),
	}
}

// single sets one line and the matching summary.
func single(t *testing.T, in Input, rate string, unitPrice, net money.Amount) Input {
	r := mustRate(t, rate)
	in.Lines = []Line{{No: 1, Description: "Usługi programistyczne - październik 2026", Unit: "usł.", Qty: 1000000, UnitPrice: unitPrice, Net: net, Rate: r}}
	s, err := money.Summarize([]money.Line{{Rate: r, Net: net}})
	if err != nil {
		t.Fatal(err)
	}
	in.Summary = s
	return in
}

func goldenInputs(t *testing.T) map[string]Input {
	out := map[string]Input{}
	out["rate23"] = single(t, baseInput(t), "23", 1850000, 1850000)
	r8 := baseInput(t)
	r8.SaleDate = day(2026, 10, 30)
	r8.PlaceOfIssue = ""
	r8.Lines = nil
	rate8 := mustRate(t, "8")
	r8.Lines = []Line{{No: 1, Description: "Usługa", Unit: "godz.", Qty: 2500000, UnitPrice: 12000, Net: 30000, Rate: rate8}}
	s, _ := money.Summarize([]money.Line{{Rate: rate8, Net: 30000}})
	r8.Summary = s
	out["rate8"] = r8
	out["rate5"] = single(t, baseInput(t), "5", 99999, 99999)
	zw := single(t, baseInput(t), "zw", 500000, 500000)
	zw.ExemptionBasis = "art. 43 ust. 1 pkt 37 ustawy o VAT"
	out["zw"] = zw

	eur := baseInput(t)
	eur.Currency = "EUR"
	eur.Buyer = Party{EUCode: "EL", VatNumber: "094259216", Name: "Hellas Trading A.E.", CountryCode: "GR", AddressL1: "Ermou 15", AddressL2: "10563 Athina"}
	rate := mustRate(t, "23")
	eur.Lines = []Line{
		{No: 1, Description: "Software development services", Unit: "h", Qty: 80000000, UnitPrice: 7500, Net: 600000, Rate: rate},
		{No: 2, Description: "Hosting", Unit: "mies.", Qty: 1000000, UnitPrice: 1999, Net: 1999, Rate: rate},
	}
	s, err := money.Summarize([]money.Line{{Rate: rate, Net: 600000}, {Rate: rate, Net: 1999}})
	if err != nil {
		t.Fatal(err)
	}
	eur.Summary = s
	eur.Exchange = &Exchange{MidMicro: 4251200, Table: "210/A/NBP/2026", EffectiveDate: "2026-10-29", Currency: "EUR"}
	vatPLN, err := money.ConvertMicro(s.Groups[0].VAT, 4251200)
	if err != nil {
		t.Fatal(err)
	}
	eur.VATPLN = map[string]money.Amount{"23": vatPLN}
	out["eur23"] = eur
	return out
}

func goldenDir() string { return filepath.Join("testdata", "golden") }

func TestGolden(t *testing.T) {
	for name, in := range goldenInputs(t) {
		t.Run(name, func(t *testing.T) {
			got, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			again, _ := Build(in)
			if !bytes.Equal(got, again) {
				t.Fatal("Build is not deterministic")
			}
			p := filepath.Join(goldenDir(), name+".xml")
			if *update {
				if err := os.WriteFile(p, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("output differs from %s; rerun with -update and review the diff\n%s", p, got)
			}
		})
	}
}

func TestBuild_Shape(t *testing.T) {
	got, err := Build(single(t, baseInput(t), "23", 1850000, 1850000))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.HasPrefix(s, `<?xml version="1.0" encoding="UTF-8"?>`+"\n<Faktura xmlns=\""+Namespace+"\">\n  <Naglowek>") {
		t.Fatalf("unexpected start:\n%.200s", s)
	}
	if !strings.HasSuffix(s, "</Faktura>\n") || strings.HasSuffix(s, "\n\n") {
		t.Fatal("must end with a single newline")
	}
	if bytes.HasPrefix(got, []byte("\xef\xbb\xbf")) {
		t.Fatal("BOM")
	}
	if n := strings.Count(s, "xmlns"); n != 1 {
		t.Fatalf("xmlns appears %d times, want 1 (root only)", n)
	}
	for _, want := range []string{
		`<KodFormularza kodSystemowy="FA (3)" wersjaSchemy="1-0E">FA</KodFormularza>`,
		"<DataWytworzeniaFa>2026-10-30T10:15:30Z</DataWytworzeniaFa>",
		"<P_1M>Gdańsk</P_1M>", "<P_6>2026-10-31</P_6>", "<P_13_1>18500.00</P_13_1>", "<P_14_1>4255.00</P_14_1>",
		"<P_15>22755.00</P_15>", "<P_8B>1</P_8B>", "<P_12>23</P_12>", "<NrRB>61109010140000071219812874</NrRB>",
		"<JST>2</JST>", "<GV>2</GV>", "<P_19N>1</P_19N>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(s, "P_14_1W") || strings.Contains(s, "KursWaluty") || strings.Contains(s, "DodatkowyOpis") {
		t.Error("PLN invoice must not carry foreign-currency elements")
	}
}

func TestBuild_OptionalOmitted(t *testing.T) {
	in := single(t, baseInput(t), "23", 100, 100)
	in.SaleDate = in.IssueDate
	in.PlaceOfIssue = ""
	in.SystemInfo = ""
	got, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, no := range []string{"P_6>", "P_1M", "SystemInfo"} {
		if strings.Contains(string(got), no) {
			t.Errorf("%s should be omitted", no)
		}
	}
}

func TestBuild_EURShape(t *testing.T) {
	got, err := Build(goldenInputs(t)["eur23"])
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"<KodUE>EL</KodUE>", "<NrVatUE>094259216</NrVatUE>", "<KodKraju>GR</KodKraju>",
		"<KursWaluty>4.2512</KursWaluty>", "<P_14_1W>",
		"<Klucz>Kurs waluty</Klucz>", "<Wartosc>1 EUR = 4.2512 PLN, tabela 210/A/NBP/2026 z 2026-10-29</Wartosc>",
		"<KodWaluty>EUR</KodWaluty>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	if n := strings.Count(s, "<KursWaluty>"); n != 2 {
		t.Errorf("KursWaluty on %d lines, want 2", n)
	}
}

func TestBuild_ZW(t *testing.T) {
	in := goldenInputs(t)["zw"]
	got, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "<P_13_7>5000.00</P_13_7>") || strings.Contains(s, "P_14_") || strings.Contains(s, "P_19N") {
		t.Fatalf("unexpected zw output:\n%s", s)
	}
	if !strings.Contains(s, "<P_19>1</P_19>\n        <P_19A>art. 43") {
		t.Fatal("P_19 and P_19A missing")
	}
	in.ExemptionBasis = " "
	if _, err := Build(in); err == nil || !strings.Contains(err.Error(), "exemption") {
		t.Fatalf("zw without basis: %v", err)
	}
}

func TestBuild_Errors(t *testing.T) {
	ok := single(t, baseInput(t), "23", 100, 100)
	cases := map[string]func(*Input){
		"empty rate":     func(in *Input) { in.Lines[0].Rate = money.Rate{} },
		"bad seller nip": func(in *Input) { in.Seller.NIP = "123" },
		"no buyer id":    func(in *Input) { in.Buyer.NIP = "" },
		"bad eu code":    func(in *Input) { in.Buyer = Party{EUCode: "GR", VatNumber: "1", Name: "x", AddressL1: "y"} },
		"vat with prefix": func(in *Input) {
			in.Buyer = Party{EUCode: "EL", VatNumber: "EL0942592161234", Name: "x", AddressL1: "y"}
		},
		"no bank":         func(in *Input) { in.BankAccount = "" },
		"bad bank":        func(in *Input) { in.BankAccount = "12345" },
		"no lines":        func(in *Input) { in.Lines = nil },
		"no number":       func(in *Input) { in.Number = "" },
		"foreign no rate": func(in *Input) { in.Currency = "EUR" },
		"foreign no vatpln": func(in *Input) {
			in.Currency = "EUR"
			in.Exchange = &Exchange{MidMicro: 4000000, Table: "1/A/NBP/2026", EffectiveDate: "2026-10-30"}
		},
		"bad currency": func(in *Input) { in.Currency = "eur" },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			in := ok
			in.Lines = append([]Line(nil), ok.Lines...)
			mod(&in)
			if _, err := Build(in); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestNormalizeNrRB(t *testing.T) {
	for in, want := range map[string]string{
		"PL61 1090 1014 0000 0712 1981 2874": "61109010140000071219812874",
		"61109010140000071219812874":         "61109010140000071219812874",
		"de89 3704 0044 0532 0130 00":        "DE89370400440532013000",
	} {
		got, err := normalizeNrRB(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestGolden_XSD(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(goldenDir(), "*.xml"))
	if err != nil || len(files) < 5 {
		t.Fatalf("goldens: %v %v", files, err)
	}
	work := t.TempDir()
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			b, err := os.ReadFile(f)
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

func TestValidate_RejectsBadXML(t *testing.T) {
	err := ValidateWithXmllint(context.Background(), []byte(`<Faktura xmlns="`+Namespace+`"><Naglowek/></Faktura>`), "")
	if errors.Is(err, ErrNoXmllint) {
		t.Skip("xmllint not on PATH")
	}
	if err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestSchema_ImportPatched(t *testing.T) {
	got, err := fs.ReadFile(SchemaFS(), SchemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`schemaLocation="bazowe/StrukturyDanych_v10-0E.xsd"`)) {
		t.Fatal("vendored FA(3) must import bazowe/StrukturyDanych_v10-0E.xsd")
	}
	if bytes.Contains(got, []byte("http://crd.gov.pl/xml/schematy/dziedzinowe/mf/2022/01/05/eD/DefinicjeTypy/StrukturyDanych")) {
		t.Fatal("an http schemaLocation is left")
	}
	for _, f := range []string{"ElementarneTypyDanych_v10-0E.xsd", "KodyKrajow_v10-0E.xsd", "StrukturyDanych_v10-0E.xsd"} {
		b, err := fs.ReadFile(SchemaFS(), "bazowe/"+f)
		if err != nil {
			t.Fatal(err)
		}
		for _, loc := range regexp.MustCompile(`schemaLocation="([^"]*)"`).FindAllSubmatch(b, -1) {
			if bytes.Contains(loc[1], []byte("://")) {
				t.Errorf("%s has a remote schemaLocation %s", f, loc[1])
			}
		}
	}
	// Only the schemaLocation attribute differs from upstream: restoring it must give
	// the upstream hash recorded in UPSTREAM.md.
	restored := bytes.Replace(got,
		[]byte(`schemaLocation="bazowe/StrukturyDanych_v10-0E.xsd"`),
		[]byte(`schemaLocation="http://crd.gov.pl/xml/schematy/dziedzinowe/mf/2022/01/05/eD/DefinicjeTypy/StrukturyDanych_v10-0E.xsd"`), 1)
	sum := sha256.Sum256(restored)
	up, err := fs.ReadFile(SchemaFS(), "UPSTREAM.md")
	if err != nil {
		t.Fatal(err)
	}
	const upstream = "b646b6b525f51adf1bb2545f111fc8ca6e7aa6dd2f98948f1667d3695c06d958"
	if !bytes.Contains(up, []byte(upstream)) {
		t.Fatal("UPSTREAM.md lacks the upstream SHA-256")
	}
	if hex := func() string {
		const d = "0123456789abcdef"
		b := make([]byte, 0, 64)
		for _, c := range sum {
			b = append(b, d[c>>4], d[c&15])
		}
		return string(b)
	}(); hex != upstream {
		t.Fatalf("patched schema differs from upstream by more than schemaLocation: %s", hex)
	}
}

func TestCheckSize_OneMegabyteLimit(t *testing.T) {
	if err := checkSize(make([]byte, MaxXMLSize)); err != nil {
		t.Errorf("exactly the limit must pass: %v", err)
	}
	if err := checkSize(make([]byte, MaxXMLSize+1)); err == nil || !strings.Contains(err.Error(), "1 MB") {
		t.Errorf("over the limit: %v", err)
	}
}
