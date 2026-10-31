package xades

import (
	"fmt"
	"strings"
)

// escapeText escapes character data the way Canonical XML 1.0 does: & < > and
// carriage return. Nothing else is escaped, so the output is already canonical.
func escapeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeAttr escapes an attribute value the way Canonical XML 1.0 does.
func escapeAttr(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '"':
			b.WriteString("&quot;")
		case '\t':
			b.WriteString("&#x9;")
		case '\n':
			b.WriteString("&#xA;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// checkXMLText refuses characters that cannot appear in an XML 1.0 document.
func checkXMLText(name, s string) error {
	for _, r := range s {
		ok := r == 0x9 || r == 0xA || r == 0xD ||
			(r >= 0x20 && r <= 0xD7FF) || (r >= 0xE000 && r <= 0xFFFD) || (r >= 0x10000 && r <= 0x10FFFF)
		if !ok {
			return fmt.Errorf("xades: %s contains a character that is not valid in XML", name)
		}
	}
	return nil
}
