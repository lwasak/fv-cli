package invoice

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxDescriptionRunes is the longest rendered line description.
const MaxDescriptionRunes = 512

var monthsPL = [...]string{"styczeń", "luty", "marzec", "kwiecień", "maj", "czerwiec",
	"lipiec", "sierpień", "wrzesień", "październik", "listopad", "grudzień"}

var monthsEN = [...]string{"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December"}

// RenderTemplate replaces {month_name} (Polish nominative, lower case),
// {month_name_en} and {year} using the sale date. An unknown token, an
// unbalanced brace or a result over 512 runes is an error.
func RenderTemplate(tpl string, sale time.Time) (string, error) {
	if sale.IsZero() {
		return "", fmt.Errorf("sale date is required to render the description")
	}
	s := sale.In(Warsaw())
	var b strings.Builder
	for i := 0; i < len(tpl); {
		c := tpl[i]
		switch c {
		case '}':
			return "", fmt.Errorf("description template %q: unmatched \"}\"", tpl)
		case '{':
			end := strings.IndexByte(tpl[i:], '}')
			if end < 0 {
				return "", fmt.Errorf("description template %q: unclosed \"{\"", tpl)
			}
			tok := tpl[i+1 : i+end]
			switch tok {
			case "month_name":
				b.WriteString(monthsPL[s.Month()-1])
			case "month_name_en":
				b.WriteString(monthsEN[s.Month()-1])
			case "year":
				b.WriteString(strconv.Itoa(s.Year()))
			default:
				return "", fmt.Errorf("description template %q: unknown token {%s} (use {month_name}, {month_name_en}, {year})", tpl, tok)
			}
			i += end + 1
		default:
			b.WriteByte(c)
			i++
		}
	}
	out := b.String()
	if n := utf8.RuneCountInString(out); n > MaxDescriptionRunes {
		return "", fmt.Errorf("description is %d characters, the limit is %d", n, MaxDescriptionRunes)
	}
	return out, nil
}
