package cli

import "github.com/spf13/cobra"

// decorateBuiltins gives Cobra's built-in help and completion commands (and the four
// completion subcommands) a Short, Long and Example, so they pass the same help rules as
// fv's own commands. The built-ins are created up front (Cobra only adds them at Execute)
// and Execute reuses them.
func decorateBuiltins(root *cobra.Command) {
	for _, c := range root.Commands() {
		switch c.Name() {
		case "help":
			c.Short = "Show help for any fv command"
			c.Long = "Prints the help of the command you name, with its description, usage, flags and examples. " +
				"Without a command it prints the help of fv itself. It is the same text as running the command with --help."
			c.Example = "  fv help\n  fv help issue\n  fv help party add"
		case "completion":
			c.Short = "Generate the shell completion script for bash, zsh, fish or PowerShell"
			c.Long = "Generates a shell completion script for fv. Each subcommand prints the script to standard output " +
				"and explains how to load it in the current session and how to install it permanently. " +
				"Completion covers commands, flags and the values of --env."
			c.Example = "  fv completion bash > /etc/bash_completion.d/fv\n" +
				"  source <(fv completion zsh)\n" +
				"  fv completion fish | source\n" +
				"  fv completion powershell | Out-String | Invoke-Expression"
			for _, sub := range c.Commands() {
				switch sub.Name() {
				case "bash":
					sub.Example = "  source <(fv completion bash)\n  fv completion bash > /etc/bash_completion.d/fv"
				case "zsh":
					sub.Example = "  source <(fv completion zsh)\n  fv completion zsh > \"${fpath[1]}/_fv\""
				case "fish":
					sub.Example = "  fv completion fish | source\n  fv completion fish > ~/.config/fish/completions/fv.fish"
				case "powershell":
					sub.Example = "  fv completion powershell | Out-String | Invoke-Expression\n  fv completion powershell >> $PROFILE"
				}
			}
		}
	}
}
