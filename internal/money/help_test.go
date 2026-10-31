package money

import (
	"strings"
	"testing"
)

func TestAmountHelp_ListsFormatsAndShells(t *testing.T) {
	for _, want := range []string{"18500", "18500.43", "18500,43", "18_500,43", "PowerShell", "cmd", "bash", "zsh", `"18500,43"`} {
		if !strings.Contains(AmountHelp, want) {
			t.Errorf("AmountHelp lacks %q", want)
		}
	}
}

func TestAmountHelp_SeveralLines(t *testing.T) {
	for _, want := range []string{"Several lines and rounding", "ONCE PER RATE", "art. 106e ust. 1 pkt 14", "art. 106e ust. 10",
		"--item dev=1,5", `--line "desc=Hosting; price=19,99; qty=2"`, "always needs quotes"} {
		if !strings.Contains(AmountHelp, want) {
			t.Errorf("AmountHelp lacks %q", want)
		}
	}
	if strings.Contains(AmountHelp, "ust. 10-11") {
		t.Error("AmountHelp cites the wrong provision")
	}
}

func TestQuotingAdvice_ZshMarkedByAnalogy(t *testing.T) {
	if !strings.Contains(QuotingAdvice, "by analogy with bash") {
		t.Error("QuotingAdvice must say zsh is covered by analogy with bash")
	}
	if !strings.Contains(QuotingAdvice, "double quotes") {
		t.Error("QuotingAdvice must recommend double quotes")
	}
}
