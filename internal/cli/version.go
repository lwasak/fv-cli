package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Set at build time with -ldflags "-X github.com/lwasak/fv-cli/internal/cli.Version=..." etc. A build
// without ldflags (go install) takes them from the embedded build information, see withBuildInfo.
var (
	Version = devVersion
	Commit  = unknownDetail
	Date    = unknownDetail
)

// Defaults of the three variables above: what a build without -ldflags reports.
const (
	devVersion     = "dev"
	unknownDetail  = "unknown"
	shortCommitLen = 7
)

func init() {
	if bi, ok := debug.ReadBuildInfo(); ok {
		Version, Commit, Date = withBuildInfo(Version, Commit, Date, bi)
	}
}

// withBuildInfo fills the values a build without -ldflags left at their defaults from the
// build information the Go toolchain embeds, which is what `go install <module>@<version>`
// has: the module version (without the leading v, as goreleaser writes it; "(devel)" is no
// version) and, for a build from a checked out tree, the vcs.revision (shortened to 7
// characters, marked -dirty when vcs.modified is true) and vcs.time. A value set with
// -ldflags is never replaced.
func withBuildInfo(version, commit, date string, bi *debug.BuildInfo) (string, string, string) {
	if bi == nil {
		return version, commit, date
	}
	if v := strings.TrimPrefix(bi.Main.Version, "v"); version == devVersion && v != "" && v != "(devel)" {
		version = v
	}
	var rev, vcsTime string
	dirty := false
	for _, kv := range bi.Settings {
		switch kv.Key {
		case "vcs.revision":
			rev = kv.Value
		case "vcs.time":
			vcsTime = kv.Value
		case "vcs.modified":
			dirty = kv.Value == "true"
		}
	}
	if commit == unknownDetail && rev != "" {
		commit = rev[:min(len(rev), shortCommitLen)]
		if dirty {
			commit += "-dirty"
		}
	}
	if date == unknownDetail && vcsTime != "" {
		date = vcsTime
	}
	return version, commit, date
}

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
