package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"sbctl/internal/profile"
	"sbctl/internal/service"
	"sbctl/internal/ui"
)

func (a *App) editCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "edit <name>",
		Short:   "Edit a profile and validate it",
		GroupID: groupProfiles,
		Long: "Open a profile in $EDITOR, then validate it with sing-box.\n\n" +
			"If validation fails you can edit again, discard your changes, or keep the file as\n" +
			"it is.\n\n" +
			"Editing the profile that is currently in service also restarts sing-box, so the\n" +
			"change takes effect immediately instead of silently waiting for the next restart.",
		Example:           "  sbctl edit work-vpn\n  EDITOR=vim sbctl edit work-vpn",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.profileNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.editAction(cmd.Context(), args[0])
		},
	}
}

func (a *App) editAction(ctx context.Context, name string) error {
	if err := profile.ValidateName(name); err != nil {
		return err
	}

	profiles, err := profile.List(a.Layout.ProfilesDir)
	if err != nil {
		return a.storeError(err, "read")
	}
	target, ok := profile.Find(profiles, name)
	if !ok {
		return notFoundError(name, profile.Names(profiles))
	}

	original, err := os.ReadFile(target.Path)
	if err != nil {
		return a.storeError(err, "read")
	}
	// Capture the original mode so a discard restores the file exactly as it
	// was, rather than resetting it to a hardcoded default and quietly changing
	// its permissions.
	mode := fs.FileMode(0o644)
	if info, statErr := os.Stat(target.Path); statErr == nil {
		mode = info.Mode().Perm()
	}

	kept, err := a.editLoop(ctx, target.Path, func() error {
		return a.restore(target.Path, original, mode)
	})
	if err != nil {
		return err
	}
	if !kept {
		return &Error{Code: ExitValidation, Message: fmt.Sprintf("%s is not a valid configuration", name)}
	}

	a.success("%s is valid", name)
	return a.restartIfActive(ctx, name)
}

// restartIfActive puts an edited profile into service when it is the one
// currently configured.
//
// Restarting goes through the service manager, which already performs its own
// privilege escalation via the narrow sudo rules, so no extra elevation step is
// needed here.
func (a *App) restartIfActive(ctx context.Context, name string) error {
	active, err := a.Activator.ActiveName()
	if err != nil {
		// Without knowing which profile is in service there is nothing to
		// reload. Report it under --verbose rather than failing an edit that
		// otherwise succeeded.
		a.debugf("could not determine the active profile: %v", err)
		return nil
	}
	if active != name {
		return nil
	}

	health, err := a.Manager.Probe(ctx)
	if err != nil {
		a.debugf("could not read service state after editing: %v", err)
		return nil
	}

	// On copy-based platforms the edit changed the profile, not the active
	// config, so it must be copied across before a restart means anything.
	if a.Layout.ActiveNamePath != "" {
		if _, err := a.Activator.Activate(profile.PathFor(a.Layout.ProfilesDir, name)); err != nil {
			return a.storeError(err, "update the active configuration for")
		}
	}

	if health.State != service.StateRunning {
		a.println(a.Theme.Hintf("start it with: sbctl use %s", name))
		return nil
	}

	a.debugf("restarting because %s is the active profile", name)
	if err := a.Manager.Restart(ctx); err != nil {
		return err
	}
	if problem := a.verifyHealthy(ctx); problem != "" {
		return (&Error{
			Code:    ExitService,
			Message: fmt.Sprintf("sing-box reloaded %s but %s", name, problem),
		}).withHints(
			"inspect the service output with: sbctl logs",
			fmt.Sprintf("revise the profile with: sbctl edit %s", name),
		)
	}
	a.success("reloaded sing-box with the updated %s", name)
	return nil
}

func (a *App) addCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "add <name>",
		Short:   "Create a profile from the template",
		GroupID: groupProfiles,
		Long: "Create a new profile from the bundled template, open it in $EDITOR, and validate\n" +
			"it with sing-box.\n\n" +
			"If validation fails you can edit again, discard the new profile, or keep it. The\n" +
			"template contains TODO_ placeholders that must all be replaced before the profile\n" +
			"can be activated.",
		Example: "  sbctl add work-vpn",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.addAction(cmd.Context(), args[0])
		},
	}
}

func (a *App) addAction(ctx context.Context, name string) error {
	if err := profile.ValidateName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(a.Layout.ProfilesDir, 0o755); err != nil {
		return a.storeError(err, "create")
	}

	path := profile.PathFor(a.Layout.ProfilesDir, name)
	// O_EXCL so an existing profile is never silently overwritten.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return (&Error{Code: ExitError, Message: fmt.Sprintf("a profile named %q already exists", name)}).
				withHints(fmt.Sprintf("edit it with: sbctl edit %s", name))
		}
		return a.storeError(err, "create")
	}
	if _, err := file.WriteString(a.Skeleton); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return a.storeError(err, "write")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return a.storeError(err, "finish writing")
	}

	// A discard on a brand-new profile means removing it, since there is no
	// previous content to restore. Leaving a broken file behind — as the
	// previous implementation did — left the profile list littered with
	// unusable entries.
	kept, err := a.editLoop(ctx, path, func() error { return os.Remove(path) })
	if err != nil {
		return err
	}
	if !kept {
		return &Error{Code: ExitValidation, Message: fmt.Sprintf("%s was not created because it is not valid", name)}
	}

	a.success("created %s", name)
	if markers, err := profile.Placeholders(path); err == nil && len(markers) > 0 {
		a.println(a.Theme.Warnf("it still has placeholders: %s", joinAnd(markers)))
		a.println(a.Theme.Hintf("fill them in with: sbctl edit %s", name))
		a.println(a.Theme.Hintf("then activate it with: sbctl use %s", name))
		return nil
	}
	a.println(a.Theme.Hintf("activate it with: sbctl use %s", name))
	return nil
}

// editLoop opens the editor and validates, offering retry/discard/keep on
// failure. It reports whether a usable file was left behind.
//
// Both `edit` and `add` route through this so their behaviour cannot drift; they
// differ only in what "discard" means, which the caller supplies.
func (a *App) editLoop(ctx context.Context, path string, discard func() error) (bool, error) {
	for {
		if err := a.Editor(ctx, path); err != nil {
			return false, failf("could not open an editor: %v", err).
				withHints("set $EDITOR to an editor you have installed")
		}

		checkErr := a.Checker.Check(ctx, path)
		if checkErr == nil {
			return true, nil
		}
		if errors.Is(checkErr, profile.ErrSingBoxMissing) {
			return false, checkErr
		}

		// Without a terminal there is nobody to answer the prompt, so report
		// the validation failure and stop rather than blocking forever.
		if !a.interactive() {
			return false, checkErr
		}

		action, promptErr := a.Theme.ResolveEditFailure(summarise(checkErr))
		if promptErr != nil {
			if cancelled(promptErr) {
				// Dismissing the prompt means "leave it as it is", which is a
				// choice rather than a failure. Returning the validation error
				// here would exit non-zero and contradict the documented
				// contract that cancelling succeeds.
				a.warn("left %s unvalidated", filepath.Base(path))
				return false, promptErr
			}
			return false, promptErr
		}

		switch action {
		case ui.EditRetry:
			continue
		case ui.EditRevert:
			if err := discard(); err != nil {
				return false, a.storeError(err, "restore")
			}
			a.println(a.Theme.Okf("changes discarded"))
			return false, nil
		case ui.EditKeep:
			a.warn("keeping an invalid configuration; it cannot be activated until it validates")
			return false, nil
		default:
			return false, failf("unrecognised choice %q", action)
		}
	}
}

// restore writes content back with its original permissions.
func (a *App) restore(path string, content []byte, mode fs.FileMode) error {
	return os.WriteFile(path, content, mode)
}

// summarise trims validator output to something that fits in a prompt.
func summarise(err error) string {
	const limit = 400
	text := err.Error()
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}
