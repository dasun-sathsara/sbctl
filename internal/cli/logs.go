package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func (a *App) logsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "logs",
		Short:   "Follow sing-box output",
		GroupID: groupDiag,
		Long: "Follow the service's log output using whichever source the platform provides:\n" +
			"the journal on systemd hosts, and the service log file on macOS and Windows.\n\n" +
			"Press Ctrl-C to stop following.",
		Example: "  sbctl logs",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Only check for a log file when the follower actually reads one.
			//
			// This is the fix for a bug that made `sbctl logs` unusable on
			// Linux: the previous implementation stat'ed the configured
			// error-log path unconditionally, but on a journald system that
			// file never exists, so the command failed before it could run
			// journalctl — which needed no file in the first place.
			if path, required := a.Follower.NeedsFile(); required && path != "" {
				if _, err := os.Stat(path); os.IsNotExist(err) {
					return (&Error{
						Code:    ExitError,
						Message: "there is no log file at " + path + " yet",
					}).withHints(
						"sing-box writes it once it has run; start it with: sbctl use <name>",
						"check the service state with: sbctl status",
					)
				}
			}

			a.debugf("following logs from %s", a.followerDescription())

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.Follower.Follow(ctx, a.Out)
		},
	}
}

func (a *App) followerDescription() string {
	if path, required := a.Follower.NeedsFile(); required && path != "" {
		return path
	}
	return "the system journal"
}
