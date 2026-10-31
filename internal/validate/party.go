package validate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
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

// Token checks a text that the invoice schema stores as a token: whitespace is collapsed,
// so after trimming it must be 1..max characters (runes) and not blank.
func Token(s string, max int) error {
	t := strings.TrimSpace(s)
	if t == "" {
		return errors.New("must not be blank")
	}
	if n := len([]rune(t)); n > max {
		return fmt.Errorf("must be 1-%d characters, got %d", max, n)
	}
	return nil
}

// Text checks that s is 1..max characters long (runes).
func Text(s string, max int) error {
	n := len([]rune(s))
	if n < 1 || n > max {
		return fmt.Errorf("must be 1-%d characters, got %d", max, n)
	}
	return nil
}

// Email checks an e-mail address loosely: 3-255 characters with something on both sides
// of a single '@'.
func Email(s string) error {
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
