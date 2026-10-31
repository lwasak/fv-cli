package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lwasak/fv-cli/internal/config"
	"github.com/lwasak/fv-cli/internal/credentials"
	"github.com/lwasak/fv-cli/internal/validate"
)

func newConfigCmd(s *session) *cobra.Command {
	cfgCmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and check the fv configuration",
		Long:  "Commands that work with the fv config.toml file. This help ends with the reference of every config key: its type, default, allowed values and an example.",
		Example: "  fv config validate\n" +
			"  fv --config ./config.toml config validate",
	}
	defaultHelp := cfgCmd.HelpFunc()
	cfgCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd.Name() != "config" {
			// Children inherit this func; they keep the normal help.
			defaultHelp(cmd, args)
			return
		}
		defaultHelp(cmd, args)
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		_, _ = fmt.Fprint(cmd.OutOrStdout(), config.FormatReference())
	})
	cfgCmd.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Load the config file and report problems",
		Long: "Loads the config file strictly (unknown keys and syntax errors are errors), " +
			"checks ksef.environment and every setting (NIP, IBAN, rate codes, numbering pattern, " +
			"party identity), and prints one '<key>: <message>' line per problem. " +
			"Exits 0 only when the config is valid.",
		Example: "  fv config validate\n  fv --env demo config validate",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rc, err := s.requireConfig(cmd)
			if err != nil {
				return err
			}
			findings := validate.Config(rc.Config)
			for _, f := range findings {
				_, _ = fmt.Fprintln(s.deps.Stderr, f.String())
			}
			failed := len(findings) > 0
			// The credential is only looked at once the config itself (incl. seller.nip) is valid.
			if nip := rc.Config.Seller.NIP; nip != "" && !failed {
				res := credentials.Check(rc.Paths.DataDir, rc.Env, nip, s.deps.now())
				for _, f := range res.Findings {
					_, _ = fmt.Fprintf(s.deps.Stderr, "ksef credential: %s\n", f)
				}
				for _, w := range res.Warnings {
					_, _ = fmt.Fprintf(s.deps.Stderr, "warning: %s\n", w)
				}
				failed = len(res.Findings) > 0
			}
			if failed {
				return errReported
			}
			l, closeLog := s.logger(rc.Paths)
			defer closeLog()
			l.Info("config validated", "path", rc.Paths.ConfigFile, "env", string(rc.Env))
			_, err = fmt.Fprintf(s.deps.Stdout, "config OK: %s\n", rc.Paths.ConfigFile)
			return err
		},
	})
	return cfgCmd
}
