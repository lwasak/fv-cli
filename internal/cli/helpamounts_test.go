package cli

import (
	"strings"
	"testing"

	"github.com/lwasak/fv-cli/internal/money"
)

func TestHelpAmounts_PrintsAmountHelp(t *testing.T) {
	out := runHelp(t, "help", "amounts")
	if !strings.Contains(out, money.AmountHelp) {
		t.Errorf("fv help amounts does not print money.AmountHelp:\n%s", out)
	}
}
