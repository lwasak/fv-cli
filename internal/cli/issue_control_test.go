package cli

import (
	"strings"
	"testing"
)

// Control and invisible formatting characters must never reach the XML, the snapshot or the
// terminal: every input path rejects them and names the field.
func TestIssueControl_FlagsAreRejected(t *testing.T) {
	fx := newIssueFx(t, multiConfig)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"line desc", []string{"--line", "desc=Bad\x01\x1b[31mRED; net=10"}, `field "desc": control characters are not allowed (U+0001 at character 4)`},
		{"line unit", []string{"--line", "desc=A; net=10; unit=m\x07"}, `field "unit": control characters are not allowed`},
		{"line index", []string{"--line", "desc=A; net=10; index=A\x7fB"}, `field "index": control characters are not allowed`},
		{"line pkwiu", []string{"--line", "desc=A; price=10; pkwiu=6\u202e2"}, `field "pkwiu": invisible formatting characters are not allowed (U+202E`},
		{"line desc bidi", []string{"--line", "desc=A\u2066B; net=10"}, `field "desc": invisible formatting characters`},
		{"desc flag", []string{"--net", "100", "--desc", "x\ny"}, `--desc: control characters are not allowed (U+000A at character 2)`},
	}
	for _, tc := range tests {
		fx.mustFail(tc.want, append([]string{"--to", "acme"}, tc.args...)...)
		code, _, errs := fx.run(append([]string{"issue", "--to", "acme"}, tc.args...)...)
		if code != 1 || strings.ContainsAny(errs, "\x01\x1b\x07\x7f\u202e\u2066") {
			t.Errorf("%s: raw control characters in stderr: %q", tc.name, errs)
		}
	}
}

func TestIssueControl_ConfigFieldsAreRefusedAtIssueAndInValidate(t *testing.T) {
	tests := []struct{ name, old, new, key string }{
		{"item name", `name = "Handbook"`, `name = "Hand\u0001book"`, "items.book.name"},
		{"item unit", `unit = "h"`, `unit = "h\u001b"`, "items.dev.unit"},
		{"item index", `index = "DEV-01"`, `index = "DEV\u200b01"`, "items.dev.index"},
		{"item pkwiu", `pkwiu = "62.01.12.0"`, `pkwiu = "62\u00ad01"`, "items.support.pkwiu"},
		{"bank name", `bank_name = "Default Bank S.A."`, `bank_name = "Bank\u0001"`, "defaults.bank_name"},
		{"party name", `[parties.monthly]`, "[parties.monthly]\nname = \"A\\u0001B\"", "parties.monthly.name"},
		{"party address", `[parties.monthly]`, "[parties.monthly]\naddress = \"A\\u202eB\"", "parties.monthly.address"},
	}
	for _, tc := range tests {
		base := strings.Replace(multiConfig, `bank_account = "PL61 1090 1014 0000 0712 1981 2874"`,
			"bank_account = \"PL61 1090 1014 0000 0712 1981 2874\"\nbank_name = \"Default Bank S.A.\"", 1)
		body := strings.Replace(base, tc.old, tc.new, 1)
		if body == base {
			t.Fatalf("%s: the fixture does not contain %q", tc.name, tc.old)
		}
		fx := newIssueFx(t, body)
		fx.mustFail(tc.key+": ", "--to", "acme", "--net", "100")
		code, _, errs := fx.run("config", "validate")
		if code != 1 || !strings.Contains(errs, tc.key+": ") || !strings.Contains(errs, "not allowed") {
			t.Errorf("%s: config validate exit %d: %q", tc.name, code, errs)
		}
	}
}
