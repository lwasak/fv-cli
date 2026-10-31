package registry

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxText = 512

// clean makes a registry string safe for XML and KSeF: control characters (including
// #x7F-#x9F) and XML-forbidden code points become spaces, whitespace is collapsed, and the
// result is capped at 512 runes.
func clean(s string) string {
	m := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError || r == 0xFFFE || r == 0xFFFF || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	return capRunes(strings.Join(strings.Fields(m), " "), maxText)
}

// cleanAddress is clean for a multi-line address: lines are joined with ", ".
func cleanAddress(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		if l := clean(line); l != "" {
			parts = append(parts, l)
		}
	}
	return capRunes(strings.Join(parts, ", "), maxText)
}

func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n]))
}

// Warsaw returns Europe/Warsaw, falling back to a fixed UTC+1 zone when the tz database is
// unavailable.
func Warsaw() *time.Location {
	if l, err := time.LoadLocation("Europe/Warsaw"); err == nil {
		return l
	}
	return time.FixedZone("CET", 3600)
}

// queryDate is min(issue date, today in Warsaw) as YYYY-MM-DD.
func queryDate(issue, now time.Time) string {
	w := Warsaw()
	today := now.In(w).Format("2006-01-02")
	d := issue.In(w).Format("2006-01-02")
	if d > today {
		return today
	}
	return d
}
