package validate

import (
	"errors"
	"fmt"
	"strings"
)

// NumberingPattern checks an invoice numbering pattern. Tokens: {YYYY} {YY} {MM} {DD}
// {SEQ} {SEQ:n} (n 1..9). Exactly one SEQ token; literals are A-Z a-z 0-9 / - _ .
func NumberingPattern(p string) error {
	if p == "" {
		return errors.New("pattern is empty")
	}
	seq := 0
	for i := 0; i < len(p); {
		c := p[i]
		switch {
		case c == '{':
			end := strings.IndexByte(p[i:], '}')
			if end < 0 {
				return errors.New("unclosed '{' in pattern")
			}
			tok := p[i+1 : i+end]
			i += end + 1
			switch {
			case tok == "YYYY", tok == "YY", tok == "MM", tok == "DD":
			case tok == "SEQ":
				seq++
			case strings.HasPrefix(tok, "SEQ:"):
				w := tok[4:]
				if len(w) != 1 || w[0] < '1' || w[0] > '9' {
					return fmt.Errorf("{%s}: width must be a single digit 1..9", tok)
				}
				seq++
			default:
				return fmt.Errorf("unknown token {%s}", tok)
			}
		case isLiteral(c):
			i++
		default:
			return fmt.Errorf("literal %q is not allowed; use A-Z a-z 0-9 / - _ .", string(c))
		}
	}
	if seq == 0 {
		return errors.New("pattern needs a {SEQ} token")
	}
	if seq > 1 {
		return errors.New("pattern must contain exactly one {SEQ} token")
	}
	return nil
}

func isLiteral(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		c == '/' || c == '-' || c == '_' || c == '.'
}

// NumberingReset checks a numbering.reset value against the pattern it applies to. An empty
// reset is valid (the period is derived from the pattern). monthly needs {MM} and a year
// token, yearly needs a year token; otherwise two periods would produce the same number.
func NumberingReset(pattern, reset string) error {
	switch reset {
	case "":
		return nil
	case "monthly", "yearly", "never":
	default:
		return fmt.Errorf("unknown reset %q; use monthly, yearly or never", reset)
	}
	hasYear := strings.Contains(pattern, "{YYYY}") || strings.Contains(pattern, "{YY}")
	switch reset {
	case "monthly":
		if !strings.Contains(pattern, "{MM}") || !hasYear {
			return errors.New("reset monthly needs {MM} and {YYYY} or {YY} in the pattern, or numbers would repeat")
		}
	case "yearly":
		if !hasYear {
			return errors.New("reset yearly needs {YYYY} or {YY} in the pattern, or numbers would repeat")
		}
	}
	return nil
}
