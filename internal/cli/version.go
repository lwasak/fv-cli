package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Set at build time with -ldflags "-X github.com/lwasak/fv-cli/internal/cli.Version=..." etc.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func newVersionCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the fv version",
		Long:    "Print the fv version, commit, build date and platform.",
		Example: "  fv version",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(deps.Stdout, "fv %s (%s, %s, %s/%s)\n",
				Version, Commit, Date, runtime.GOOS, runtime.GOARCH)
			return err
		},
	}
}
