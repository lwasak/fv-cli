package credentials

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"strings"
)

// subjectNames gives readable names to the attribute types that Go's pkix.Name.String
// prints as raw dotted OIDs with hex values.
var subjectNames = map[string]string{
	"2.5.4.3":  "CN",
	"2.5.4.4":  "SN",
	"2.5.4.5":  "SERIALNUMBER",
	"2.5.4.6":  "C",
	"2.5.4.7":  "L",
	"2.5.4.8":  "ST",
	"2.5.4.9":  "STREET",
	"2.5.4.10": "O",
	"2.5.4.11": "OU",
	"2.5.4.12": "T",
	"2.5.4.42": "GN",
	"2.5.4.97": "organizationIdentifier",

	"1.2.840.113549.1.9.1": "emailAddress",
}

// formatSubject renders the subject like RFC 2253 (most specific first) with readable
// attribute names and decoded string values. Unknown attributes keep their dotted OID.
func formatSubject(cert *x509.Certificate) string {
	var seq pkix.RDNSequence
	if rest, err := asn1.Unmarshal(cert.RawSubject, &seq); err != nil || len(rest) != 0 {
		return cert.Subject.String()
	}
	var parts []string
	for i := len(seq) - 1; i >= 0; i-- {
		var rdn []string
		for _, atv := range seq[i] {
			name, ok := subjectNames[atv.Type.String()]
			if !ok {
				name = atv.Type.String()
			}
			rdn = append(rdn, name+"="+escapeValue(attrValue(atv.Value)))
		}
		parts = append(parts, strings.Join(rdn, "+"))
	}
	return strings.Join(parts, ",")
}

func attrValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return fmt.Sprintf("#%x", t)
	default:
		return fmt.Sprint(t)
	}
}

func escapeValue(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case strings.ContainsRune(`,+"\<>;=`, r), i == 0 && (r == '#' || r == ' '):
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
