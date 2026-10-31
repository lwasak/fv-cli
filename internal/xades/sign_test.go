package xades_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"

	"github.com/lwasak/fv-cli/internal/credentials/credtest"
	"github.com/lwasak/fv-cli/internal/xades"
)

const (
	nsDS    = "http://www.w3.org/2000/09/xmldsig#"
	nsXAdES = "http://uri.etsi.org/01903/v1.3.2#"
)

// keySigner signs a digest the way credentials.Credential does: PKCS#1 v1.5 for RSA,
// raw r||s for ECDSA.
type keySigner struct {
	key     crypto.Signer
	digests [][]byte
}

func (k *keySigner) Sign(digest []byte, hash crypto.Hash) ([]byte, error) {
	k.digests = append(k.digests, append([]byte(nil), digest...))
	switch key := k.key.(type) {
	case *rsa.PrivateKey:
		return rsa.SignPKCS1v15(rand.Reader, key, hash, digest)
	case *ecdsa.PrivateKey:
		r, s, err := ecdsa.Sign(rand.Reader, key, digest)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 64)
		r.FillBytes(out[:32])
		s.FillBytes(out[32:])
		return out, nil
	}
	return nil, nil
}

var fixedNow = time.Date(2026, 10, 30, 12, 0, 5, 0, time.UTC)

func sign(t *testing.T, spec credtest.Spec, challenge string, now time.Time) (*credtest.Cert, *keySigner, []byte) {
	t.Helper()
	c := credtest.New(t, spec)
	s := &keySigner{key: c.Key}
	out, err := xades.SignAuthTokenRequest(xades.AuthTokenRequest{
		Challenge:             challenge,
		ContextNIP:            "5833281095",
		SubjectIdentifierType: "certificateSubject",
	}, c.Cert, s, now)
	if err != nil {
		t.Fatalf("SignAuthTokenRequest: %v", err)
	}
	return c, s, out
}

func parse(t *testing.T, b []byte) *etree.Element {
	t.Helper()
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(b); err != nil {
		t.Fatalf("parse: %v\n%s", err, b)
	}
	return doc.Root()
}

func child(t *testing.T, e *etree.Element, tag string) *etree.Element {
	t.Helper()
	for _, c := range e.ChildElements() {
		if c.Tag == tag {
			return c
		}
	}
	t.Fatalf("no child %q in <%s>", tag, e.Tag)
	return nil
}

func canon(t *testing.T, e *etree.Element) []byte {
	t.Helper()
	b, err := dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("").Canonicalize(e)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	return b
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(h[:])
}

func findByID(e *etree.Element, id string) *etree.Element {
	if e.SelectAttrValue("Id", "") == id {
		return e
	}
	for _, c := range e.ChildElements() {
		if f := findByID(c, id); f != nil {
			return f
		}
	}
	return nil
}

// references returns the two Reference elements of SignedInfo.
func references(t *testing.T, root *etree.Element) (signature, signedInfo, docRef, propRef *etree.Element) {
	t.Helper()
	signature = child(t, root, "Signature")
	signedInfo = child(t, signature, "SignedInfo")
	var refs []*etree.Element
	for _, c := range signedInfo.ChildElements() {
		if c.Tag == "Reference" {
			refs = append(refs, c)
		}
	}
	if len(refs) != 2 {
		t.Fatalf("want 2 references, got %d", len(refs))
	}
	return signature, signedInfo, refs[0], refs[1]
}

func digestOf(t *testing.T, ref *etree.Element) string {
	t.Helper()
	return child(t, ref, "DigestValue").Text()
}

func TestSign_DocumentDigestMatchesIndependentExcC14N(t *testing.T) {
	_, _, out := sign(t, credtest.Spec{}, "20261030-CR-ABC", fixedNow)
	root := parse(t, out)
	sig, _, docRef, _ := references(t, root)
	if docRef.SelectAttrValue("URI", "x") != "" {
		t.Fatalf("document reference URI must be empty")
	}
	if sig != root.ChildElements()[len(root.ChildElements())-1] {
		t.Fatalf("Signature must be the last child of the root")
	}
	root.RemoveChild(sig) // enveloped-signature transform
	got := sum(canon(t, root))
	if want := digestOf(t, docRef); got != want {
		t.Fatalf("document digest %s, independent exc-c14n gives %s", want, got)
	}
}

func TestSign_SignedPropertiesDigestMatchesIndependentExcC14N(t *testing.T) {
	_, _, out := sign(t, credtest.Spec{}, "ch", fixedNow)
	root := parse(t, out)
	_, _, _, propRef := references(t, root)
	if got := propRef.SelectAttrValue("Type", ""); got != "http://uri.etsi.org/01903#SignedProperties" {
		t.Fatalf("Type = %q", got)
	}
	uri := propRef.SelectAttrValue("URI", "")
	if !strings.HasPrefix(uri, "#SignedProperties-") {
		t.Fatalf("URI = %q", uri)
	}
	sp := findByID(root, strings.TrimPrefix(uri, "#"))
	if sp == nil || sp.Tag != "SignedProperties" {
		t.Fatalf("SignedProperties %q not found", uri)
	}
	if got, want := sum(canon(t, sp)), digestOf(t, propRef); got != want {
		t.Fatalf("SignedProperties digest %s, independent exc-c14n gives %s", want, got)
	}
}

func TestSign_SignedInfoCanonicalMatchesIndependent(t *testing.T) {
	_, s, out := sign(t, credtest.Spec{}, "ch", fixedNow)
	root := parse(t, out)
	_, si, _, _ := references(t, root)
	h := sha256.Sum256(canon(t, si))
	if len(s.digests) != 1 || string(s.digests[0]) != string(h[:]) {
		t.Fatalf("signer was not given the digest of the independent canonical SignedInfo")
	}
	if got := child(t, si, "CanonicalizationMethod").SelectAttrValue("Algorithm", ""); got != "http://www.w3.org/2001/10/xml-exc-c14n#" {
		t.Fatalf("c14n = %q", got)
	}
}

func sigValue(t *testing.T, root *etree.Element) []byte {
	t.Helper()
	v, err := base64.StdEncoding.DecodeString(child(t, child(t, root, "Signature"), "SignatureValue").Text())
	if err != nil {
		t.Fatalf("signature value: %v", err)
	}
	return v
}

func TestSign_SignatureVerifiesRSA(t *testing.T) {
	c, _, out := sign(t, credtest.Spec{RSA: true}, "ch", fixedNow)
	root := parse(t, out)
	sig, si, _, _ := references(t, root)
	if got := child(t, si, "SignatureMethod").SelectAttrValue("Algorithm", ""); got != "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256" {
		t.Fatalf("method = %q", got)
	}
	h := sha256.Sum256(canon(t, si))
	if err := rsa.VerifyPKCS1v15(c.Cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, h[:], sigValue(t, root)); err != nil {
		t.Fatalf("verify: %v", err)
	}
	_ = sig
}

func TestSign_SignatureVerifiesECDSA(t *testing.T) {
	c, _, out := sign(t, credtest.Spec{}, "ch", fixedNow)
	root := parse(t, out)
	_, si, _, _ := references(t, root)
	if got := child(t, si, "SignatureMethod").SelectAttrValue("Algorithm", ""); got != "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256" {
		t.Fatalf("method = %q", got)
	}
	v := sigValue(t, root)
	if len(v) != 64 {
		t.Fatalf("signature length %d, want 64 (r||s)", len(v))
	}
	h := sha256.Sum256(canon(t, si))
	r, s := new(big.Int).SetBytes(v[:32]), new(big.Int).SetBytes(v[32:])
	if !ecdsa.Verify(c.Cert.PublicKey.(*ecdsa.PublicKey), h[:], r, s) {
		t.Fatalf("ECDSA signature does not verify")
	}
}

func TestSign_CertDigestAndIssuerSerial(t *testing.T) {
	c, _, out := sign(t, credtest.Spec{}, "ch", fixedNow)
	root := parse(t, out)
	sig := child(t, root, "Signature")
	// KeyInfo carries the certificate.
	x509Cert := child(t, child(t, child(t, sig, "KeyInfo"), "X509Data"), "X509Certificate")
	der, err := base64.StdEncoding.DecodeString(x509Cert.Text())
	if err != nil {
		t.Fatalf("X509Certificate: %v", err)
	}
	if _, err := x509.ParseCertificate(der); err != nil || string(der) != string(c.DER) {
		t.Fatalf("KeyInfo certificate differs from the signing certificate (%v)", err)
	}
	qp := child(t, child(t, sig, "Object"), "QualifyingProperties")
	ssp := child(t, child(t, qp, "SignedProperties"), "SignedSignatureProperties")
	cert := child(t, child(t, ssp, "SigningCertificate"), "Cert")
	if got, want := child(t, child(t, cert, "CertDigest"), "DigestValue").Text(), sum(c.DER); got != want {
		t.Fatalf("CertDigest %s, want %s", got, want)
	}
	is := child(t, cert, "IssuerSerial")
	if got, want := child(t, is, "X509IssuerName").Text(), c.Cert.Issuer.String(); got != want {
		t.Fatalf("issuer %q, want %q", got, want)
	}
	if got, want := child(t, is, "X509SerialNumber").Text(), c.Cert.SerialNumber.String(); got != want {
		t.Fatalf("serial %q, want %q", got, want)
	}
	if got := qp.SelectAttrValue("Target", ""); got != "#"+sig.SelectAttrValue("Id", "?") {
		t.Fatalf("Target = %q", got)
	}
}

func TestSign_EscapesSpecialCharacters(t *testing.T) {
	const challenge = `a&b<c>d"e'f` + "\r"
	_, _, out := sign(t, credtest.Spec{}, challenge, fixedNow)
	root := parse(t, out)
	if got := child(t, root, "Challenge").Text(); got != "a&b<c>d\"e'f\n" && got != challenge {
		t.Fatalf("challenge round trip = %q", got)
	}
	sig, _, docRef, _ := references(t, root)
	root.RemoveChild(sig)
	if got, want := sum(canon(t, root)), digestOf(t, docRef); got != want {
		t.Fatalf("digest with special characters %s, independent %s", want, got)
	}
	if strings.Contains(string(out), "<c>") {
		t.Fatalf("unescaped markup in output")
	}
}

func TestSign_SigningTimeUTC(t *testing.T) {
	zone := time.FixedZone("CEST", 2*3600)
	_, _, out := sign(t, credtest.Spec{}, "ch", time.Date(2026, 10, 30, 14, 0, 5, 0, zone))
	root := parse(t, out)
	qp := child(t, child(t, child(t, root, "Signature"), "Object"), "QualifyingProperties")
	ssp := child(t, child(t, qp, "SignedProperties"), "SignedSignatureProperties")
	if got := child(t, ssp, "SigningTime").Text(); got != "2026-10-30T12:00:05Z" {
		t.Fatalf("SigningTime = %q", got)
	}
}

func TestSign_TamperedChallengeBreaksDigest(t *testing.T) {
	_, _, out := sign(t, credtest.Spec{}, "original", fixedNow)
	root := parse(t, out)
	sig, _, docRef, _ := references(t, root)
	child(t, root, "Challenge").SetText("tampered")
	root.RemoveChild(sig)
	if sum(canon(t, root)) == digestOf(t, docRef) {
		t.Fatalf("digest still matches after tampering")
	}
}

func TestSign_EnvelopedDocumentShape(t *testing.T) {
	_, _, out := sign(t, credtest.Spec{}, "ch", fixedNow)
	root := parse(t, out)
	if root.Tag != "AuthTokenRequest" || root.Space != "" {
		t.Fatalf("root = %s:%s", root.Space, root.Tag)
	}
	if got := root.SelectAttrValue("xmlns", ""); got != "http://ksef.mf.gov.pl/auth/token/2.0" {
		t.Fatalf("namespace = %q", got)
	}
	nip := child(t, child(t, root, "ContextIdentifier"), "Nip").Text()
	if nip != "5833281095" || child(t, root, "SubjectIdentifierType").Text() != "certificateSubject" {
		t.Fatalf("unexpected body: %s", out)
	}
	_ = nsDS
	_ = nsXAdES
}

func TestSign_RefusesUnsupportedKeyType(t *testing.T) {
	c := credtest.New(t, credtest.Spec{})
	bad := *c.Cert
	bad.PublicKeyAlgorithm = x509.Ed25519
	bad.PublicKey = ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
	_, err := xades.SignAuthTokenRequest(xades.AuthTokenRequest{Challenge: "c", ContextNIP: "1", SubjectIdentifierType: "certificateSubject"}, &bad, &keySigner{key: c.Key}, fixedNow)
	if err == nil {
		t.Fatalf("expected an error for an unsupported key type")
	}
}
