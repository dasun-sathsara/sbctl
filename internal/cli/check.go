package cli

import (
	"context"

	"github.com/spf13/cobra"

	"sbctl/internal/profile"
)

func (a *App) checkCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "check [name]",
		Short:   "Validate a profile",
		GroupID: groupDiag,
		Long: "Validate a profile with sing-box, or the active one when no name is given.\n\n" +
			"This reports both kinds of problem: unreplaced TODO_ placeholders, and\n" +
			"configurations that sing-box itself rejects. It does not check whether the server\n" +
			"is reachable — a profile can be perfectly valid and still not connect.",
		Example:           "  sbctl check\n  sbctl check work-vpn\n  sbctl check --json",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.profileNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return a.checkAction(cmd.Context(), name)
		},
	}
}

func (a *App) checkAction(ctx context.Context, name string) error {
	if name == "" {
		active, err := a.Activator.ActiveName()
		if err != nil {
			return err
		}
		if active == "" {
			return (&Error{
				Code:    ExitError,
				Message: "no profile is active, so there is nothing to check",
			}).withHints(
				"name one explicitly: sbctl check <name>",
				"or activate one first: sbctl use <name>",
			)
		}
		name = active
	}

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

	if !target.Ready() {
		if a.Format.JSON {
			if err := a.emitJSON(map[string]any{
				"profile":      target.Name,
				"valid":        false,
				"placeholders": target.Placeholders,
				"error":        "unreplaced placeholder values",
			}); err != nil {
				return err
			}
			return &Error{Code: ExitValidation, Message: "unreplaced placeholder values", Reported: true}
		}
		return placeholderError(target.Name, target.Placeholders)
	}

	if err := a.Checker.Check(ctx, target.Path); err != nil {
		if a.Format.JSON {
			if jsonErr := a.emitJSON(map[string]any{
				"profile": target.Name,
				"valid":   false,
				"error":   err.Error(),
			}); jsonErr != nil {
				return jsonErr
			}
			return &Error{Code: ExitValidation, Message: err.Error(), Err: err, Reported: true}
		}
		return err
	}

	if a.Format.JSON {
		return a.emitJSON(map[string]any{
			"profile":      target.Name,
			"valid":        true,
			"placeholders": []string{},
		})
	}
	a.success("%s is a valid configuration", target.Name)
	return nil
}
