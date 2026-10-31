package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/ksef/ksefake"
)

func (fx *listFx) cachedXMLPath(n string) string {
	return filepath.Join(fx.dataDir, "ksef-cache", "test", ksefNIP, "xml", n+".xml")
}

func TestShow_PurchaseByKSeFNumberDownloadsOnce(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("show", fx.purchaseNo)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"ENVIRONMENT: TEST", "ZAK/09/15", "purchase, KSeF", "Materiały biurowe", "615,00", "KSeF, downloaded", "just now", "SHA-256 checked", fx.purchaseNo, "PL61 1090 1014 0000 0712 1981 2874"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if n := fx.fake.Count("GET", "/invoices/ksef/"+fx.purchaseNo); n != 1 {
		t.Errorf("%d downloads", n)
	}
	if fx.metadataCalls() != 0 {
		t.Errorf("show ran %d metadata queries", fx.metadataCalls())
	}
	before := len(fx.fake.Requests())
	code, out, errs = fx.run("show", fx.purchaseNo)
	if code != 0 || !strings.Contains(out, "from the local cache") {
		t.Fatalf("second show: %d %s%s", code, out, errs)
	}
	if len(fx.fake.Requests()) != before {
		t.Errorf("the second show sent %d requests", len(fx.fake.Requests())-before)
	}
}

func TestShow_SaleOutsideFvIsASale(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("show", fx.saleNo)
	if code != 0 || !strings.Contains(out, "EXT/7   [sale, KSeF]") {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
}

func TestShow_PurchaseByPlainNumberViaTheCache(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("list: %s", errs)
	}
	calls := fx.metadataCalls()
	code, out, errs := fx.run("show", "ZAK/09/15")
	if code != 0 || !strings.Contains(out, "Materiały biurowe") {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
	if fx.metadataCalls() != calls {
		t.Error("show queried the metadata")
	}
	if code, _, errs := fx.run("show", "NOPE/1"); code != 1 || !strings.Contains(errs, "fv list --from") {
		t.Errorf("unknown: %d %s", code, errs)
	}
}

func TestShow_AmbiguousNumberListsKSeFNumbers(t *testing.T) {
	fx := newListFx(t)
	other := fx.q.Add(ksefake.QMeta{InvoiceNumber: "ZAK/09/15", IssueDate: "2026-09-16", SellerNIP: "5260300804", SellerName: "DRUGI SPRZEDAWCA",
		BuyerType: "Nip", BuyerID: ksefNIP, BuyerName: "MOJA FIRMA", Net: "10.00", VAT: "2.30", Gross: "12.30"},
		otherInvoiceXML(t, "ZAK/09/15", "2026-09-16", 1000, "5260300804", "DRUGI SPRZEDAWCA", ksefNIP, "MOJA FIRMA"))
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("list: %s", errs)
	}
	code, out, errs := fx.run("show", "ZAK/09/15")
	if code != 1 || out != "" {
		t.Fatalf("exit %d out %q", code, out)
	}
	for _, want := range []string{"matches 2 invoices", fx.purchaseNo, other, "DRUGI SPRZEDAWCA"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errs)
		}
	}
}

func TestShow_XMLIsExactAndTamperedCacheRefused(t *testing.T) {
	fx := newListFx(t)
	code, out, errs := fx.run("show", fx.purchaseNo, "--xml")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := otherInvoiceXML(t, "ZAK/09/15", "2026-09-15", 50000, supplierNIP, supplierName, ksefNIP, "MOJA FIRMA")
	if out != string(want) {
		t.Errorf("xml differs from what KSeF served")
	}
	if code, out2, _ := fx.run("show", fx.purchaseNo, "--xml"); code != 0 || out2 != out {
		t.Errorf("cached xml differs")
	}
	p := fx.cachedXMLPath(fx.purchaseNo)
	if err := os.WriteFile(p, []byte(strings.Replace(out, "Materiały", "Materialy", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"show", fx.purchaseNo}, {"show", fx.purchaseNo, "--xml"}} {
		code, out, errs := fx.run(args...)
		if code != 1 || out != "" || !strings.Contains(errs, "hash") {
			t.Errorf("%v: exit %d out %q err %q", args, code, out, errs)
		}
	}
}

func TestShow_LocalInvoiceByItsKSeFNumber(t *testing.T) {
	fx := newListFx(t)
	m := fx.meta("test", sendNum)
	if m.KSeF == nil || m.KSeF.KSeFNumber == "" {
		t.Fatal("the sent invoice has no KSeF number")
	}
	num := m.KSeF.KSeFNumber
	code, out, errs := fx.run("show", num)
	if code != 0 || !strings.Contains(out, sendNum) || strings.Contains(out, "KSeF, downloaded") {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
	if fx.fake.Count("GET", "/invoices/ksef/"+num) != 0 {
		t.Error("a local invoice was downloaded")
	}
}

func TestShow_NotYetAvailableAndUnknown(t *testing.T) {
	fx := newListFx(t)
	fx.q.SetNotYetAvailable(fx.purchaseNo, 1)
	code, _, errs := fx.run("show", fx.purchaseNo)
	if code != 1 || !strings.Contains(errs, "not made this invoice downloadable yet; retry in a minute") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if code, out, errs := fx.run("show", fx.purchaseNo); code != 0 || !strings.Contains(out, "ZAK/09/15") {
		t.Errorf("retry: %d %s%s", code, out, errs)
	}
	code, _, errs = fx.run("show", "5260250274-20260101-AAAAAAAAAAAA-AA")
	if code != 1 {
		t.Errorf("unknown KSeF number: exit %d %s", code, errs)
	}
}

func TestShow_DownloadWithoutHashHeaderIsRefused(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("list: %s", errs)
	}
	forged := otherInvoiceXML(t, "ZAK/09/15", "2026-09-15", 9999900, supplierNIP, supplierName, ksefNIP, "MOJA FIRMA")
	fx.fake.Inject("GET", "/invoices/ksef/", 1, ksefake.Response{Status: 200, Body: string(forged), ContentType: "application/xml"})
	code, out, errs := fx.run("show", fx.purchaseNo)
	if code != 1 || out != "" || !strings.Contains(errs, "x-ms-meta-hash") || !strings.Contains(errs, "nothing was cached") {
		t.Fatalf("exit %d out %q err %q", code, out, errs)
	}
	if _, err := os.Stat(fx.cachedXMLPath(fx.purchaseNo)); !os.IsNotExist(err) {
		t.Errorf("a forged body was cached: %v", err)
	}
}

func TestShow_DownloadThatDiffersFromTheListedHashIsRefused(t *testing.T) {
	fx := newListFx(t)
	if code, _, errs := fx.run("list"); code != 0 {
		t.Fatalf("list: %s", errs)
	}
	forged := otherInvoiceXML(t, "ZAK/09/15", "2026-09-15", 9999900, supplierNIP, supplierName, ksefNIP, "MOJA FIRMA")
	sum := sha256.Sum256(forged)
	fx.fake.Inject("GET", "/invoices/ksef/", 1, ksefake.Response{Status: 200, Body: string(forged), ContentType: "application/xml",
		Header: map[string]string{"x-ms-meta-hash": base64.StdEncoding.EncodeToString(sum[:])}})
	code, out, errs := fx.run("show", fx.purchaseNo)
	if code != 1 || out != "" || !strings.Contains(errs, "invoiceHash") {
		t.Fatalf("exit %d out %q err %q", code, out, errs)
	}
	if _, err := os.Stat(fx.cachedXMLPath(fx.purchaseNo)); !os.IsNotExist(err) {
		t.Errorf("a forged body was cached: %v", err)
	}
}
