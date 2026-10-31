package credentials_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/logging"
)

const (
	testNIP   = "5833281095"
	testPESEL = "44051401359"
)

func TestInspect_NIPFromOrganizationIdentifier(t *testing.T) {
	c := credtest.New(t, credtest.Spec{OrganizationIdentifier: "VATPL-" + testNIP})
	info, err := credentials.Inspect(c.Cert)
	if err != nil {
		t.Fatal(err)
	}
	if info.IdentifierType != "NIP" || info.NIP != testNIP || info.PESEL != "" {
		t.Fatalf("got %+v", info)
	}
	if info.Type != "KSeF Authentication" {
		t.Fatalf("type = %q", info.Type)
	}
}

func TestInspect_NIPFromSerialNumberTINPL(t *testing.T) {
	for _, prefix := range []string{"TINPL-", "VATPL-", "NIP-"} {
		c := credtest.New(t, credtest.Spec{SerialNumber: prefix + testNIP})
		info, err := credentials.Inspect(c.Cert)
		if err != nil {
			t.Fatalf("%s: %v", prefix, err)
		}
		if info.IdentifierType != "NIP" || info.NIP != testNIP {
			t.Fatalf("%s: got %+v", prefix, info)
		}
	}
}

func TestInspect_PESELOnlyKSeFAuthenticationAccepted(t *testing.T) {
	c := credtest.New(t, credtest.Spec{
		CommonName:   "Jan Kowalski (uwierzytelnienie)",
		SerialNumber: "PNOPL-" + testPESEL,
		IssuerCN:     "TEST CCK KSeF",
	})
	info, err := credentials.Inspect(c.Cert)
	if err != nil {
		t.Fatal(err)
	}
	if info.IdentifierType != "PESEL" || info.PESEL != testPESEL || info.NIP != "" {
		t.Fatalf("got %+v", info)
	}
	if info.Type != "KSeF Authentication" {
		t.Fatalf("type = %q", info.Type)
	}
	if !strings.Contains(info.Subject, "Jan Kowalski") {
		t.Fatalf("subject = %q", info.Subject)
	}
}

func TestInspect_PESELRegisteredWithRedactor(t *testing.T) {
	const pesel = "02070803628"
	c := credtest.New(t, credtest.Spec{SerialNumber: "PNOPL-" + pesel})
	if _, err := credentials.Inspect(c.Cert); err != nil {
		t.Fatal(err)
	}
	if got := logging.Default.Redact("pesel " + pesel); strings.Contains(got, pesel) {
		t.Fatalf("PESEL not redacted: %q", got)
	}
}

func TestInspect_QualifiedSealWithoutNIPRefused(t *testing.T) {
	c := credtest.New(t, credtest.Spec{CommonName: "Firma Seal", SerialNumber: "PNOPL-" + testPESEL, QCType: "eseal", IssuerCN: "Some QTSP"})
	_, err := credentials.Inspect(c.Cert)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "Firma Seal") {
		t.Fatalf("error should name the subject: %v", err)
	}
}

func TestInspect_NoNIPNorPESELRefused(t *testing.T) {
	c := credtest.New(t, credtest.Spec{CommonName: "Nobody Special"})
	_, err := credentials.Inspect(c.Cert)
	if err == nil || !strings.Contains(err.Error(), "Nobody Special") {
		t.Fatalf("want error naming subject, got %v", err)
	}
}

func TestInspect_Types(t *testing.T) {
	nip := "VATPL-" + testNIP
	tests := []struct {
		name    string
		spec    credtest.Spec
		want    string
		wantErr bool
	}{
		{"ksef authentication", credtest.Spec{SerialNumber: nip}, "KSeF Authentication", false},
		{"ksef offline refused", credtest.Spec{SerialNumber: nip, NonRepudiationOnly: true}, "", true},
		{"qualified seal", credtest.Spec{SerialNumber: nip, QCType: "eseal", IssuerCN: "Some QTSP"}, "qualified seal", false},
		{"qualified signature", credtest.Spec{SerialNumber: nip, QCType: "esign", IssuerCN: "Some QTSP"}, "qualified signature", false},
		{"plain refused", credtest.Spec{SerialNumber: nip, IssuerCN: "Random CA"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := credtest.New(t, tt.spec)
			info, err := credentials.Inspect(c.Cert)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && info.Type != tt.want {
				t.Fatalf("type = %q, want %q", info.Type, tt.want)
			}
		})
	}
}

func TestCheckValidity(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		nb, na      time.Time
		wantWarning bool
		wantErr     bool
	}{
		{"31 days left", now.Add(-time.Hour), now.Add(31 * 24 * time.Hour), false, false},
		{"30 days left", now.Add(-time.Hour), now.Add(30 * 24 * time.Hour), true, false},
		{"1 day left", now.Add(-time.Hour), now.Add(24 * time.Hour), true, false},
		{"expired 1s", now.Add(-48 * time.Hour), now.Add(-time.Second), false, true},
		{"not yet valid", now.Add(time.Hour), now.Add(100 * 24 * time.Hour), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := credentials.Info{NotBefore: tt.nb, NotAfter: tt.na}
			w, err := info.CheckValidity(now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if (w != "") != tt.wantWarning {
				t.Fatalf("warning = %q", w)
			}
		})
	}
}

func TestContextNIP_NIPCertificateMismatchRefused(t *testing.T) {
	info := credentials.Info{IdentifierType: "NIP", NIP: testNIP}
	if _, err := info.ContextNIP("1234563218"); err == nil {
		t.Fatal("want mismatch error")
	}
}

func TestContextNIP_NIPCertificateMatchReturnsSellerNIP(t *testing.T) {
	info := credentials.Info{IdentifierType: "NIP", NIP: testNIP}
	got, err := info.ContextNIP(testNIP)
	if err != nil || got != testNIP {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestContextNIP_PESELCertificateReturnsSellerNIP(t *testing.T) {
	info := credentials.Info{IdentifierType: "PESEL", PESEL: testPESEL}
	got, err := info.ContextNIP(testNIP)
	if err != nil || got != testNIP {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestInfoFormat_ShowsSubjectIdentifierTypeNIPTypeSerialValidity(t *testing.T) {
	nb := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	na := time.Date(2028, 1, 2, 3, 4, 5, 0, time.UTC)
	nipInfo := credentials.Info{Subject: "CN=Acme", IdentifierType: "NIP", NIP: testNIP, Type: "KSeF Authentication", Serial: "1A2B", NotBefore: nb, NotAfter: na}
	out := nipInfo.Format(testNIP)
	for _, want := range []string{"subject: CN=Acme", "identifier: NIP", "nip: " + testNIP, "type: KSeF Authentication", "serial: 1A2B", "2026-01-02", "2028-01-02"} {
		if !strings.Contains(out, want) {
			t.Errorf("NIP output missing %q:\n%s", want, out)
		}
	}
	peselInfo := credentials.Info{Subject: "CN=Jan,SERIALNUMBER=PNOPL-" + testPESEL, IdentifierType: "PESEL", PESEL: testPESEL, Type: "KSeF Authentication", Serial: "FF", NotBefore: nb, NotAfter: na}
	out = peselInfo.Format("9999999999")
	for _, want := range []string{"identifier: PESEL", "nip: 9999999999 (context; none in certificate)"} {
		if !strings.Contains(out, want) {
			t.Errorf("PESEL output missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, testPESEL) != 1 {
		t.Errorf("PESEL should appear only in the subject line:\n%s", out)
	}
}

// A KSeF Authentication certificate of a natural person as issued by the TEST CCK: personal
// (PESEL), digitalSignature, but with the eSign QcType in its QCStatements.
func TestInspect_KSeFIssuedPersonalCertificateIsKSeFAuthentication(t *testing.T) {
	c := credtest.New(t, credtest.Spec{
		CommonName:   "JAN KOWALSKI (uwierzytelnienie)",
		Surname:      "KOWALSKI",
		GivenName:    "JAN",
		SerialNumber: "PNOPL-" + testPESEL,
		IssuerCN:     "TEST CCK KSeF",
		QCType:       "esign",
	})
	info, err := credentials.Inspect(c.Cert)
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != "KSeF Authentication" {
		t.Fatalf("type = %q, want KSeF Authentication", info.Type)
	}
	if info.IdentifierType != "PESEL" {
		t.Fatalf("identifier = %q", info.IdentifierType)
	}
	// Offline (non-repudiation) KSeF certificates are still refused, whatever their QCStatements.
	off := credtest.Spec{SerialNumber: "PNOPL-" + testPESEL, QCType: "esign", NonRepudiationOnly: true}
	if _, err := credentials.Inspect(credtest.New(t, off).Cert); err == nil {
		t.Fatal("KSeF Offline certificate accepted")
	}
}

func TestInspect_SubjectIsReadable(t *testing.T) {
	c := credtest.New(t, credtest.Spec{
		CommonName:             "JAN KOWALSKI (uwierzytelnienie)",
		Surname:                "KOWALSKI",
		GivenName:              "JAN",
		SerialNumber:           "PNOPL-" + testPESEL,
		OrganizationIdentifier: "VATPL-" + testNIP,
	})
	info, err := credentials.Inspect(c.Cert)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CN=JAN KOWALSKI (uwierzytelnienie)", "SN=KOWALSKI", "GN=JAN", "SERIALNUMBER=PNOPL-", "organizationIdentifier=VATPL-" + testNIP} {
		if !strings.Contains(info.Subject, want) {
			t.Errorf("subject %q lacks %q", info.Subject, want)
		}
	}
	if strings.Contains(info.Subject, "2.5.4.") || strings.Contains(info.Subject, "#") {
		t.Errorf("subject %q still has raw OIDs or hex", info.Subject)
	}
}
