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

func TestQuotingAdvice_ZshMarkedByAnalogy(t *testing.T) {
	if !strings.Contains(QuotingAdvice, "by analogy with bash") {
		t.Error("QuotingAdvice must say zsh is covered by analogy with bash")
	}
	if !strings.Contains(QuotingAdvice, "double quotes") {
		t.Error("QuotingAdvice must recommend double quotes")
	}
}
