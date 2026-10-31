package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func walk(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, c := range cmd.Commands() {
		walk(c, fn)
	}
}

func TestHelp_EveryCommandHasShortLongExample(t *testing.T) {
	root := NewRootCmd(DefaultDeps())
	walk(root, func(c *cobra.Command) {
		if strings.TrimSpace(c.Short) == "" {
			t.Errorf("%s: Short is empty", c.CommandPath())
		}
		if strings.TrimSpace(c.Long) == "" {
			t.Errorf("%s: Long is empty", c.CommandPath())
		}
		if strings.TrimSpace(c.Example) == "" {
			t.Errorf("%s: Example is empty", c.CommandPath())
		}
	})
}

func TestHelp_EveryFlagHasUsage(t *testing.T) {
	root := NewRootCmd(DefaultDeps())
	walk(root, func(c *cobra.Command) {
		check := func(f *pflag.Flag) {
			if strings.TrimSpace(f.Usage) == "" {
				t.Errorf("%s: flag --%s has no usage", c.CommandPath(), f.Name)
			}
		}
		c.Flags().VisitAll(check)
		c.PersistentFlags().VisitAll(check)
	})
	for _, name := range []string{"config", "env"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("root is missing global flag --%s", name)
		}
	}
}
