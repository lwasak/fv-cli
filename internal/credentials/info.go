package credentials

import (
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"strings"
	"time"

	"github.com/lwasak/fv-cli/internal/logging"
)

// Identifier types of the certificate subject.
const (
	IdentifierNIP   = "NIP"
	IdentifierPESEL = "PESEL"
)

// Certificate types accepted for login.
const (
	TypeKSeFAuthentication = "KSeF Authentication"
	TypeQualifiedSeal      = "qualified seal"
	TypeQualifiedSignature = "qualified signature"
)

// ExpiryWarning is how long before NotAfter CheckValidity starts warning.
const ExpiryWarning = 30 * 24 * time.Hour

var (
	oidOrganizationIdentifier = asn1.ObjectIdentifier{2, 5, 4, 97}
	oidQCStatements           = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 3}
	oidQcType                 = asn1.ObjectIdentifier{0, 4, 0, 1862, 1, 6}
	oidQcTypeESign            = asn1.ObjectIdentifier{0, 4, 0, 1862, 1, 6, 1}
	oidQcTypeESeal            = asn1.ObjectIdentifier{0, 4, 0, 1862, 1, 6, 2}
)

// Info is what fv knows about a login certificate.
type Info struct {
	Subject        string
	IdentifierType string // IdentifierNIP or IdentifierPESEL
	NIP            string // set for IdentifierNIP
	PESEL          string // set for IdentifierPESEL
	Type           string
	Serial         string // hexadecimal
	NotBefore      time.Time
	NotAfter       time.Time
}

type qcStatement struct {
	ID   asn1.ObjectIdentifier
	Info asn1.RawValue `asn1:"optional"`
}

// Inspect reads the subject identifier, type, serial and validity of cert and refuses
// certificates that cannot be used to log in. A PESEL found in the subject is registered
// with the log redactor.
func Inspect(cert *x509.Certificate) (Info, error) {
	info := Info{
		Subject:   formatSubject(cert),
		Serial:    strings.ToUpper(cert.SerialNumber.Text(16)),
		NotBefore: cert.NotBefore.UTC(),
		NotAfter:  cert.NotAfter.UTC(),
	}
	typ, err := certType(cert)
	if err != nil {
		return Info{}, fmt.Errorf("certificate %q: %w", info.Subject, err)
	}
	info.Type = typ

	nip, pesel := identifiers(cert)
	switch {
	case nip != "":
		info.IdentifierType, info.NIP = IdentifierNIP, nip
	case pesel != "" && typ != TypeQualifiedSeal:
		info.IdentifierType, info.PESEL = IdentifierPESEL, pesel
		logging.Default.Register(pesel)
	case pesel != "":
		return Info{}, fmt.Errorf("certificate %q: a qualified seal must carry a NIP", info.Subject)
	default:
		return Info{}, fmt.Errorf("certificate %q carries neither a NIP nor a PESEL", info.Subject)
	}
	return info, nil
}

func certType(cert *x509.Certificate) (string, error) {
	// A certificate issued by the KSeF certificate authority is a KSeF certificate even when its
	// QCStatements say eSign (the KSeF Authentication certificate of a natural person is
	// personal and carries the QcType of a signature). Decide by keyUsage as KSeF documents it.
	if strings.Contains(cert.Issuer.CommonName, "KSeF") {
		switch {
		case cert.KeyUsage&x509.KeyUsageDigitalSignature != 0:
			return TypeKSeFAuthentication, nil
		case cert.KeyUsage&x509.KeyUsageContentCommitment != 0:
			return "", fmt.Errorf("a KSeF Offline certificate cannot be used to log in")
		}
	}
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidQCStatements) {
			continue
		}
		var stmts []qcStatement
		if _, err := asn1.Unmarshal(ext.Value, &stmts); err != nil {
			return "", fmt.Errorf("unreadable QCStatements extension: %w", err)
		}
		for _, s := range stmts {
			if !s.ID.Equal(oidQcType) {
				continue
			}
			var types []asn1.ObjectIdentifier
			if _, err := asn1.Unmarshal(s.Info.FullBytes, &types); err != nil {
				return "", fmt.Errorf("unreadable QcType statement: %w", err)
			}
			for _, t := range types {
				switch {
				case t.Equal(oidQcTypeESeal):
					return TypeQualifiedSeal, nil
				case t.Equal(oidQcTypeESign):
					return TypeQualifiedSignature, nil
				}
			}
		}
	}
	return "", fmt.Errorf("unsupported certificate type: need a KSeF Authentication, qualified seal or qualified signature certificate")
}

// identifiers returns the NIP and PESEL carried by the subject (either may be empty).
func identifiers(cert *x509.Certificate) (nip, pesel string) {
	for _, n := range cert.Subject.Names {
		if n.Type.Equal(oidOrganizationIdentifier) {
			if s, ok := n.Value.(string); ok {
				if v, ok := strings.CutPrefix(s, "VATPL-"); ok && digits(v, 10) {
					nip = v
				}
			}
		}
	}
	if nip == "" {
		sn := cert.Subject.SerialNumber
		for _, p := range []string{"TINPL-", "VATPL-", "NIP-"} {
			if v, ok := strings.CutPrefix(sn, p); ok && digits(v, 10) {
				nip = v
				break
			}
		}
		if v, ok := strings.CutPrefix(sn, "PNOPL-"); ok && digits(v, 11) {
			pesel = v
		}
	}
	return nip, pesel
}

func digits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// CheckValidity refuses a certificate that is expired or not yet valid at now, and returns a
// warning when 30 days or fewer remain.
func (i Info) CheckValidity(now time.Time) (string, error) {
	if now.Before(i.NotBefore) {
		return "", fmt.Errorf("certificate is not valid before %s", i.NotBefore.UTC().Format(time.RFC3339))
	}
	if now.After(i.NotAfter) {
		return "", fmt.Errorf("certificate expired at %s", i.NotAfter.UTC().Format(time.RFC3339))
	}
	if left := i.NotAfter.Sub(now); left <= ExpiryWarning {
		days := int(left / (24 * time.Hour))
		return fmt.Sprintf("certificate expires in %d days (%s)", days, i.NotAfter.UTC().Format(time.RFC3339)), nil
	}
	return "", nil
}

// ContextNIP returns the NIP to send as the authentication context, which is always the
// seller's NIP. A certificate that carries a NIP must carry that one.
func (i Info) ContextNIP(sellerNIP string) (string, error) {
	if i.IdentifierType == IdentifierNIP && i.NIP != sellerNIP {
		return "", fmt.Errorf("certificate NIP %s does not match seller.nip %s", i.NIP, sellerNIP)
	}
	return sellerNIP, nil
}

// Format renders the lines that `fv ksef check` prints for the certificate.
func (i Info) Format(contextNIP string) string {
	nip := i.NIP
	if i.IdentifierType == IdentifierPESEL {
		nip = contextNIP + " (context; none in certificate)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "subject: %s\n", i.Subject)
	fmt.Fprintf(&b, "identifier: %s\n", i.IdentifierType)
	fmt.Fprintf(&b, "nip: %s\n", nip)
	fmt.Fprintf(&b, "type: %s\n", i.Type)
	fmt.Fprintf(&b, "serial: %s\n", i.Serial)
	fmt.Fprintf(&b, "validity: %s to %s UTC\n", i.NotBefore.UTC().Format("2006-01-02 15:04"), i.NotAfter.UTC().Format("2006-01-02 15:04"))
	return b.String()
}
