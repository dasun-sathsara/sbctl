package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"sbctl/internal/ui"
)

func (a *App) useCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "use [name]",
		Short:   "Switch to a profile",
		GroupID: groupProfiles,
		Long: "Validate a profile, make it the active configuration, restart sing-box, and\n" +
			"confirm the service is still running afterwards.\n\n" +
			"If the service starts and then immediately fails — which a configuration can do\n" +
			"even when it is syntactically valid — the previous profile is restored\n" +
			"automatically so you are not left without a working connection.\n\n" +
			"With no name, an interactive picker opens.",
		Example: "  sbctl use work-vpn\n  sbctl use            # choose from a list",
		Args:    cobra.MaximumNArgs(1),
		// Suggest real profile names for shells with completion installed.
		ValidArgsFunction: a.profileNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return a.runInteractive(cmd)
			}
			return a.useAction(cmd.Context(), args[0])
		},
	}
	return cmd
}

// useAction activates a profile, elevating the process first where the platform
// requires it.
func (a *App) useAction(ctx context.Context, name string) error {
	handled, err := a.elevateAndRun("use", name)
	if err != nil {
		return err
	}
	if handled {
		// The elevated process performed the work and reported its own result.
		return nil
	}
	return a.activate(ctx, name)
}

func (a *App) offCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "off",
		Short:   "Stop sing-box",
		GroupID: groupService,
		Long: "Stop the sing-box service, removing the TUN interface and restoring direct\n" +
			"network access. The active profile is remembered, so `sbctl use` without a name\n" +
			"can bring it back.",
		Example: "  sbctl off",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.offAction(cmd.Context())
		},
	}
}

// offAction stops the service, elevating first where required.
func (a *App) offAction(ctx context.Context) error {
	handled, err := a.elevateAndRun("off")
	if err != nil {
		return err
	}
	if handled {
		return nil
	}
	return a.stop(ctx)
}

// cancelled reports whether an error is a user cancellation.
func cancelled(err error) bool { return errors.Is(err, ui.ErrCancelled) }
