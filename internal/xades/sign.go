package xades

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	nsDS    = "http://www.w3.org/2000/09/xmldsig#"
	nsXAdES = "http://uri.etsi.org/01903/v1.3.2#"

	algC14N      = "http://www.w3.org/2001/10/xml-exc-c14n#"
	algEnveloped = "http://www.w3.org/2000/09/xmldsig#enveloped-signature"
	algSHA256    = "http://www.w3.org/2001/04/xmlenc#sha256"
	algRSA       = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"
	algECDSA     = "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256"
	typeSignedP  = "http://uri.etsi.org/01903#SignedProperties"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func sha(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func randomID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("xades: random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// SignAuthTokenRequest returns the AuthTokenRequest document with an enveloped
// XAdES-BES signature made by s with the key belonging to cert. now becomes the
// SigningTime (UTC).
func SignAuthTokenRequest(req AuthTokenRequest, cert *x509.Certificate, s Signer, now time.Time) ([]byte, error) {
	if cert == nil || s == nil {
		return nil, errors.New("xades: certificate and signer are required")
	}
	for name, v := range map[string]string{
		"challenge": req.Challenge, "context NIP": req.ContextNIP, "subject identifier type": req.SubjectIdentifierType,
	} {
		if v == "" {
			return nil, fmt.Errorf("xades: %s is empty", name)
		}
		if err := checkXMLText(name, v); err != nil {
			return nil, err
		}
	}

	var sigAlg string
	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		sigAlg = algRSA
	case *ecdsa.PublicKey:
		if pub.Curve != elliptic.P256() {
			return nil, errors.New("xades: only P-256 ECDSA keys are supported")
		}
		sigAlg = algECDSA
	default:
		return nil, fmt.Errorf("xades: unsupported key type %s (RSA and ECDSA P-256 only)", cert.PublicKeyAlgorithm)
	}

	sigID, err := randomID()
	if err != nil {
		return nil, err
	}
	propsID, err := randomID()
	if err != nil {
		return nil, err
	}
	sigID = "Signature-" + sigID
	propsID = "SignedProperties-" + propsID

	// The document without the signature (enveloped-signature transform), in C14N form.
	body := `<Challenge>` + escapeText(req.Challenge) + `</Challenge>` +
		`<ContextIdentifier><Nip>` + escapeText(req.ContextNIP) + `</Nip></ContextIdentifier>` +
		`<SubjectIdentifierType>` + escapeText(req.SubjectIdentifierType) + `</SubjectIdentifierType>`
	rootOpen := `<AuthTokenRequest xmlns="` + AuthTokenNamespace + `">`
	const rootClose = `</AuthTokenRequest>`
	docDigest := sha([]byte(rootOpen + body + rootClose))

	// SignedProperties, canonical standalone: the xades namespace on the apex, the ds
	// namespace on every ds element (the nearest output ancestor never declares it).
	dsAttr := ` xmlns:ds="` + nsDS + `"`
	props := `<xades:SignedProperties xmlns:xades="` + nsXAdES + `" Id="` + propsID + `">` +
		`<xades:SignedSignatureProperties>` +
		`<xades:SigningTime>` + now.UTC().Format("2006-01-02T15:04:05Z") + `</xades:SigningTime>` +
		`<xades:SigningCertificate><xades:Cert>` +
		`<xades:CertDigest>` +
		`<ds:DigestMethod` + dsAttr + ` Algorithm="` + algSHA256 + `"></ds:DigestMethod>` +
		`<ds:DigestValue` + dsAttr + `>` + b64(sha(cert.Raw)) + `</ds:DigestValue>` +
		`</xades:CertDigest>` +
		`<xades:IssuerSerial>` +
		`<ds:X509IssuerName` + dsAttr + `>` + escapeText(cert.Issuer.String()) + `</ds:X509IssuerName>` +
		`<ds:X509SerialNumber` + dsAttr + `>` + cert.SerialNumber.String() + `</ds:X509SerialNumber>` +
		`</xades:IssuerSerial>` +
		`</xades:Cert></xades:SigningCertificate>` +
		`</xades:SignedSignatureProperties></xades:SignedProperties>`
	propsDigest := sha([]byte(props))

	signedInfo := `<ds:SignedInfo` + dsAttr + `>` +
		`<ds:CanonicalizationMethod Algorithm="` + algC14N + `"></ds:CanonicalizationMethod>` +
		`<ds:SignatureMethod Algorithm="` + sigAlg + `"></ds:SignatureMethod>` +
		`<ds:Reference URI="">` +
		`<ds:Transforms>` +
		`<ds:Transform Algorithm="` + algEnveloped + `"></ds:Transform>` +
		`<ds:Transform Algorithm="` + algC14N + `"></ds:Transform>` +
		`</ds:Transforms>` +
		`<ds:DigestMethod Algorithm="` + algSHA256 + `"></ds:DigestMethod>` +
		`<ds:DigestValue>` + b64(docDigest) + `</ds:DigestValue>` +
		`</ds:Reference>` +
		`<ds:Reference Type="` + typeSignedP + `" URI="#` + propsID + `">` +
		`<ds:Transforms><ds:Transform Algorithm="` + algC14N + `"></ds:Transform></ds:Transforms>` +
		`<ds:DigestMethod Algorithm="` + algSHA256 + `"></ds:DigestMethod>` +
		`<ds:DigestValue>` + b64(propsDigest) + `</ds:DigestValue>` +
		`</ds:Reference>` +
		`</ds:SignedInfo>`

	sigValue, err := s.Sign(sha([]byte(signedInfo)), crypto.SHA256)
	if err != nil {
		return nil, fmt.Errorf("xades: sign: %w", err)
	}
	if sigAlg == algECDSA && len(sigValue) != 64 {
		return nil, fmt.Errorf("xades: ECDSA signature has %d bytes, want 64 (r||s)", len(sigValue))
	}

	var out strings.Builder
	out.WriteString(rootOpen)
	out.WriteString(body)
	out.WriteString(`<ds:Signature` + dsAttr + ` Id="` + sigID + `">`)
	out.WriteString(signedInfo)
	out.WriteString(`<ds:SignatureValue>` + b64(sigValue) + `</ds:SignatureValue>`)
	out.WriteString(`<ds:KeyInfo><ds:X509Data><ds:X509Certificate>` + b64(cert.Raw) + `</ds:X509Certificate></ds:X509Data></ds:KeyInfo>`)
	out.WriteString(`<ds:Object><xades:QualifyingProperties xmlns:xades="` + nsXAdES + `" Target="#` + sigID + `">`)
	out.WriteString(props)
	out.WriteString(`</xades:QualifyingProperties></ds:Object>`)
	out.WriteString(`</ds:Signature>`)
	out.WriteString(rootClose)
	return []byte(out.String()), nil
}
