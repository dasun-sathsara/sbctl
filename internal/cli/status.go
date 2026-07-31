package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show what sing-box is currently doing",
		GroupID: groupService,
		Long: "Report the service run state, which profile is configured, the server it points\n" +
			"at, the TUN interface name and the process id.\n\n" +
			"State is read once per invocation, so this costs a single privileged call.",
		Example: "  sbctl status\n  sbctl status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snap, err := a.snapshot(cmd.Context())
			if err != nil {
				return err
			}
			return a.printStatus(cmd.Context(), snap)
		},
	}
}

// printStatus renders a snapshot in whichever format was requested.
func (a *App) printStatus(_ context.Context, snap snapshot) error {
	view := a.statusView(snap)

	if a.Format.JSON {
		payload := map[string]any{
			"state":        string(view.State),
			"profile":      view.Profile,
			"running":      view.Running,
			"tun":          view.Tun,
			"server":       view.Server,
			"placeholders": view.Placeholders,
		}
		if view.PID > 0 {
			payload["pid"] = view.PID
		}
		if view.Broken != "" {
			payload["broken"] = view.Broken
		}
		return a.emitJSON(payload)
	}

	a.println(a.Theme.RenderStatus(view))
	if view.Broken != "" {
		a.println(a.Theme.Hintf("choose a working profile with: sbctl use <name>"))
	}
	return nil
}
