// Package credtest generates throwaway certificates and keys for tests. Nothing here is
// committed as key material: every certificate is made in-process.
package credtest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/youmark/pkcs8"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// P12 returns c as a PKCS#12 file protected with pass.
func P12(tb testing.TB, c *Cert, pass string) []byte {
	tb.Helper()
	b, err := pkcs12.Modern.Encode(c.Key, c.Cert, nil, pass)
	if err != nil {
		tb.Fatalf("p12: %v", err)
	}
	return b
}

// CertPEM returns the certificate as PEM.
func CertPEM(c *Cert) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.DER})
}

// EncryptedKeyPEM returns the key as an encrypted PKCS#8 PEM ("ENCRYPTED PRIVATE KEY").
func EncryptedKeyPEM(tb testing.TB, c *Cert, pass string) []byte {
	tb.Helper()
	der, err := pkcs8.ConvertPrivateKeyToPKCS8(c.Key, []byte(pass))
	if err != nil {
		tb.Fatalf("encrypt key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: der})
}

// PlainKeyPEM returns the key as an unencrypted PKCS#8 PEM.
func PlainKeyPEM(tb testing.TB, c *Cert) []byte {
	tb.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(c.Key)
	if err != nil {
		tb.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// LegacyEncryptedKeyPEM returns an OpenSSL-style "Proc-Type: 4,ENCRYPTED" PEM key.
func LegacyEncryptedKeyPEM(tb testing.TB, c *Cert, pass string) []byte {
	tb.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(c.Key)
	if err != nil {
		tb.Fatalf("marshal key: %v", err)
	}
	//nolint:staticcheck // the legacy format is exactly what the test needs to produce
	blk, err := x509.EncryptPEMBlock(rand.Reader, "PRIVATE KEY", der, []byte(pass), x509.PEMCipherAES256)
	if err != nil {
		tb.Fatalf("legacy encrypt: %v", err)
	}
	return pem.EncodeToMemory(blk)
}

// Spec describes a leaf certificate. Zero values give a valid P-256 KSeF
// Authentication certificate without an identifier.
type Spec struct {
	CommonName string
	// SerialNumber is the subject serialNumber attribute (2.5.4.5), e.g. "PNOPL-44051401359".
	SerialNumber string
	// OrganizationIdentifier is the subject organizationIdentifier (2.5.4.97), e.g. "VATPL-5833281095".
	OrganizationIdentifier string
	// GivenName and Surname are the subject givenName (2.5.4.42) and surname (2.5.4.4).
	GivenName, Surname  string
	IssuerCN            string // default "TEST CCK KSeF"
	RSA                 bool   // default key is P-256
	NotBefore, NotAfter time.Time
	// QCType is "", "esign" or "eseal" (QCStatements QcType).
	QCType string
	// NonRepudiationOnly sets keyUsage contentCommitment instead of digitalSignature
	// (the shape of a KSeF Offline certificate).
	NonRepudiationOnly bool
}

// Cert is a generated leaf certificate with its key.
type Cert struct {
	Cert *x509.Certificate
	DER  []byte
	Key  crypto.Signer
}

var (
	oidOrganizationIdentifier = asn1.ObjectIdentifier{2, 5, 4, 97}
	// OIDQCStatements is id-pe-qcStatements.
	oidQCStatements = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 3}
	oidQcType       = asn1.ObjectIdentifier{0, 4, 0, 1862, 1, 6}
	oidESign        = asn1.ObjectIdentifier{0, 4, 0, 1862, 1, 6, 1}
	oidESeal        = asn1.ObjectIdentifier{0, 4, 0, 1862, 1, 6, 2}
)

type qcStatement struct {
	ID   asn1.ObjectIdentifier
	Info []asn1.ObjectIdentifier
}

// New generates a CA and a leaf certificate described by spec.
func New(tb testing.TB, spec Spec) *Cert {
	tb.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("ca key: %v", err)
	}
	issuer := spec.IssuerCN
	if issuer == "" {
		issuer = "TEST CCK KSeF"
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: issuer},
		NotBefore:             time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		tb.Fatalf("ca cert: %v", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		tb.Fatalf("parse ca: %v", err)
	}

	var key crypto.Signer
	if spec.RSA {
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	} else {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	if err != nil {
		tb.Fatalf("leaf key: %v", err)
	}

	cn := spec.CommonName
	if cn == "" {
		cn = "Jan Kowalski (uwierzytelnienie)"
	}
	name := pkix.Name{CommonName: cn, SerialNumber: spec.SerialNumber}
	if spec.OrganizationIdentifier != "" {
		name.ExtraNames = []pkix.AttributeTypeAndValue{{Type: oidOrganizationIdentifier, Value: spec.OrganizationIdentifier}}
	}
	if spec.Surname != "" {
		name.ExtraNames = append(name.ExtraNames, pkix.AttributeTypeAndValue{Type: asn1.ObjectIdentifier{2, 5, 4, 4}, Value: spec.Surname})
	}
	if spec.GivenName != "" {
		name.ExtraNames = append(name.ExtraNames, pkix.AttributeTypeAndValue{Type: asn1.ObjectIdentifier{2, 5, 4, 42}, Value: spec.GivenName})
	}
	nb, na := spec.NotBefore, spec.NotAfter
	if nb.IsZero() {
		nb = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) // far in the past: tests run under fixed clocks
	}
	if na.IsZero() {
		na = time.Now().Add(365 * 24 * time.Hour)
	}
	ku := x509.KeyUsageDigitalSignature
	if spec.NonRepudiationOnly {
		ku = x509.KeyUsageContentCommitment
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(0x1A2B3C4D),
		Subject:      name,
		NotBefore:    nb,
		NotAfter:     na,
		KeyUsage:     ku,
	}
	switch spec.QCType {
	case "":
	case "esign", "eseal":
		t := oidESign
		if spec.QCType == "eseal" {
			t = oidESeal
		}
		val, err := asn1.Marshal([]qcStatement{{ID: oidQcType, Info: []asn1.ObjectIdentifier{t}}})
		if err != nil {
			tb.Fatalf("qc statements: %v", err)
		}
		tmpl.ExtraExtensions = []pkix.Extension{{Id: oidQCStatements, Value: val}}
	default:
		tb.Fatalf("unknown QCType %q", spec.QCType)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, key.Public(), caKey)
	if err != nil {
		tb.Fatalf("leaf cert: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		tb.Fatalf("parse leaf: %v", err)
	}
	return &Cert{Cert: leaf, DER: der, Key: key}
}
