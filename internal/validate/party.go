package validate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var aliasRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// PartyAlias checks a party alias: lower-case letters, digits, '_' and '-', starting with a
// letter or digit, at most 32 characters.
func PartyAlias(a string) error {
	if !aliasRE.MatchString(a) {
		return fmt.Errorf("alias %q is not valid; use 1-32 characters a-z 0-9 _ -, starting with a letter or digit", a)
	}
	return nil
}

// ItemKey checks the key of an [items.<key>] table: same shape as a party alias.
func ItemKey(k string) error {
	if !aliasRE.MatchString(k) {
		return fmt.Errorf("item key %q is not valid; use 1-32 characters a-z 0-9 _ -, starting with a letter or digit", k)
	}
	return nil
}

// NoControl rejects text that would break a terminal or that an XML 1.0 document cannot
// hold: control characters (Unicode Cc, which includes tab, newline and ESC), invisible
// formatting characters (Cf, which includes the bidirectional controls and the byte order
// mark), the line and paragraph separators, non-characters and invalid UTF-8. No invoice
// text legitimately needs any of them. It is the one rule behind every text field, in
// "fv config validate" and when an invoice is issued.
func NoControl(s string) error {
	pos := 0
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		pos++
		switch {
		case r == utf8.RuneError && w == 1:
			return fmt.Errorf("invalid UTF-8 is not allowed (at character %d)", pos)
		case unicode.IsControl(r):
			return fmt.Errorf("control characters are not allowed (U+%04X at character %d)", r, pos)
		case unicode.Is(unicode.Cf, r), r == 0x2028, r == 0x2029, r == 0xFFFE, r == 0xFFFF, r >= 0xFDD0 && r <= 0xFDEF:
			return fmt.Errorf("invisible formatting characters are not allowed (U+%04X at character %d)", r, pos)
		}
		i += w
	}
	return nil
}

// Token checks a text that the invoice schema stores as a token: whitespace is collapsed,
// so after trimming it must be 1..max characters (runes), not blank and free of control
// characters.
func Token(s string, max int) error {
	if err := NoControl(s); err != nil {
		return err
	}
	t := strings.TrimSpace(s)
	if t == "" {
		return errors.New("must not be blank")
	}
	if n := len([]rune(t)); n > max {
		return fmt.Errorf("must be 1-%d characters, got %d", max, n)
	}
	return nil
}

// Text checks that s is 1..max characters long (runes) and free of control characters.
func Text(s string, max int) error {
	if err := NoControl(s); err != nil {
		return err
	}
	n := len([]rune(s))
	if n < 1 || n > max {
		return fmt.Errorf("must be 1-%d characters, got %d", max, n)
	}
	return nil
}

// Email checks an e-mail address loosely: 3-255 characters with something on both sides
// of a single '@'.
func Email(s string) error {
	if err := NoControl(s); err != nil {
		return err
	}
	n := len([]rune(s))
	if n < 3 || n > 255 {
		return fmt.Errorf("must be 3-255 characters, got %d", n)
	}
	at := -1
	for i, c := range s {
		if c == '@' {
			if at >= 0 {
				return errors.New("must contain exactly one @")
			}
			at = i
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			return errors.New("must not contain whitespace")
		}
	}
	if at <= 0 || at == len(s)-1 {
		return errors.New("must look like name@domain")
	}
	return nil
}
