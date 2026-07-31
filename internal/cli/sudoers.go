package cli

import (
	"os"
	"os/user"

	"github.com/spf13/cobra"
)

func (a *App) printSudoersCmd() *cobra.Command {
	var forUser string
	cmd := &cobra.Command{
		Use:   "print-sudoers",
		Short: "Print the sudo rules sbctl requires",
		Long: "Print the exact contents of /etc/sudoers.d/sbctl that this build of sbctl needs.\n\n" +
			"The installer obtains the file this way rather than templating it in shell, so the\n" +
			"rules can never drift from the commands the binary actually issues. A mismatch\n" +
			"would leave every privileged operation failing for want of a password.\n\n" +
			"Each rule is scoped to a single command with fixed arguments, so the grant cannot\n" +
			"be reused to run arbitrary programs as root.",
		Example: "  sbctl print-sudoers | sudo tee /etc/sudoers.d/sbctl\n" +
			"  sbctl print-sudoers --user alice",
		// Hidden: this is installer plumbing, not something a user runs day to day.
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target := forUser
			if target == "" {
				resolved, err := invokingUser()
				if err != nil {
					return failf("could not determine which user to grant access to: %v", err).
						withHints("name it explicitly with: sbctl print-sudoers --user <name>")
				}
				target = resolved
			}

			content, err := a.Layout.Sudoers(target)
			if err != nil {
				return err
			}
			_, err = a.Out.Write([]byte(content))
			return err
		},
	}
	cmd.Flags().StringVar(&forUser, "user", "", "the account to grant access to (defaults to the invoking user)")
	return cmd
}

// invokingUser resolves the human behind the command, preferring SUDO_USER so
// that running the installer under sudo still grants rights to the real account
// rather than to root.
func invokingUser() (string, error) {
	if name := os.Getenv("SUDO_USER"); name != "" {
		return name, nil
	}
	current, err := user.Current()
	if err != nil {
		return "", err
	}
	return current.Username, nil
}
