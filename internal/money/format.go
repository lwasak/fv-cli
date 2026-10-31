package money

import "strconv"

func split(a Amount) (sign string, whole string, frac string) {
	v := int64(a)
	if v < 0 {
		sign = "-"
		v = -v
	}
	whole = strconv.FormatInt(v/100, 10)
	f := strconv.FormatInt(v%100, 10)
	if len(f) < 2 {
		f = "0" + f
	}
	return sign, whole, f
}

// FormatXML renders an amount as plain "." decimal with two decimals.
func FormatXML(a Amount) string {
	sign, whole, frac := split(a)
	return sign + whole + "." + frac
}

// FormatPL renders an amount for Polish display: comma decimal, ASCII space
// thousands separator, grouping only from five integer digits.
func FormatPL(a Amount) string {
	sign, whole, frac := split(a)
	if len(whole) >= 5 {
		var out []byte
		for i := 0; i < len(whole); i++ {
			if i > 0 && (len(whole)-i)%3 == 0 {
				out = append(out, ' ')
			}
			out = append(out, whole[i])
		}
		whole = string(out)
	}
	return sign + whole + "," + frac
}
