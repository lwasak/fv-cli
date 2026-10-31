package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/money"
)

// newHelpAmountsCmd is a help-only topic: "fv help amounts" (and "fv amounts").
func newHelpAmountsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "amounts",
		Short:   "How to write amounts and quote them in your shell",
		Long:    "Accepted amount formats and the shell quoting that keeps them intact.",
		Example: "  fv help amounts",
		Args:    cobra.NoArgs,
	}
	c.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), money.AmountHelp)
	})
	c.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Help() }
	return c
}
