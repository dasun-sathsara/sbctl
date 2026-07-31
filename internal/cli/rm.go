package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"sbctl/internal/profile"
	"sbctl/internal/service"
)

func (a *App) rmCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove", "delete"},
		Short:   "Delete a profile",
		GroupID: groupProfiles,
		Long: "Delete a profile after confirmation.\n\n" +
			"Deleting the profile that is currently in service is refused unless --force is\n" +
			"given, because the running service would be left with no configuration to read.\n" +
			"With --force, sing-box is stopped first so it is never left running against a\n" +
			"file that no longer exists.",
		Example:           "  sbctl rm old-profile\n  sbctl rm active-profile --force",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.profileNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.rmAction(cmd.Context(), args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "delete even if the profile is in use, stopping sing-box first")
	return cmd
}

func (a *App) rmAction(ctx context.Context, name string, force bool) error {
	if err := profile.ValidateName(name); err != nil {
		return err
	}

	profiles, err := profile.List(a.Layout.ProfilesDir)
	if err != nil {
		return a.storeError(err, "read")
	}
	// Check existence before prompting, so a typo produces a useful message
	// instead of a confirmation dialog followed by a raw filesystem error.
	if _, ok := profile.Find(profiles, name); !ok {
		return notFoundError(name, profile.Names(profiles))
	}

	active, _ := a.Activator.ActiveName()
	isActive := active == name

	running := false
	if isActive {
		if health, probeErr := a.Manager.Probe(ctx); probeErr == nil {
			running = health.State == service.StateRunning
		}
	}

	if isActive && running && !force {
		return (&Error{
			Code:    ExitError,
			Message: fmt.Sprintf("%s is in use by the running service", name),
		}).withHints(
			"stop sing-box first with: sbctl off",
			fmt.Sprintf("or delete it anyway with: sbctl rm %s --force", name),
		)
	}

	warning := ""
	if isActive {
		warning = "This is the active profile, so sing-box will be left with no configuration."
	}

	if a.interactive() {
		confirmed, err := a.Theme.ConfirmDelete(name, warning)
		if err != nil {
			if cancelled(err) {
				a.println(a.Theme.MutedStyle().Render("nothing was deleted"))
				return nil
			}
			return err
		}
		if !confirmed {
			a.println(a.Theme.MutedStyle().Render("nothing was deleted"))
			return nil
		}
	} else if !force {
		// Never destroy data on the strength of an unanswerable prompt.
		return (&Error{
			Code:    ExitError,
			Message: "deleting a profile needs confirmation, and there is no terminal to ask on",
		}).withHints(fmt.Sprintf("re-run with: sbctl rm %s --force", name))
	}

	// Stop before deleting so the service is never left reading a file that is
	// about to disappear.
	if isActive && running {
		a.debugf("stopping sing-box before deleting the profile it is using")
		if err := a.Manager.Stop(ctx); err != nil {
			return err
		}
		a.success("sing-box stopped")
	}

	if err := os.Remove(profile.PathFor(a.Layout.ProfilesDir, name)); err != nil {
		return a.storeError(err, "delete")
	}
	a.success("deleted %s", name)

	if isActive {
		// The active config now points at nothing. Removing it would need a
		// privilege sbctl deliberately does not hold, so report the state and
		// the one-step fix instead.
		a.warn("the active configuration still refers to %s, which no longer exists", name)
		a.println(a.Theme.Hintf("point it somewhere valid with: sbctl use <name>"))
	}
	return nil
}
