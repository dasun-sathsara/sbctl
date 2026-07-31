package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) listCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available profiles",
		GroupID: groupProfiles,
		Long: "List every profile in the profiles directory.\n\n" +
			"The active profile is marked. A profile that is configured but whose service is\n" +
			"stopped is labelled as such rather than hidden, so it is clear what `sbctl use`\n" +
			"would bring back. Profiles with unreplaced placeholders are flagged, because they\n" +
			"cannot be activated until those are filled in.",
		Example: "  sbctl list\n  sbctl list --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snap, err := a.snapshot(cmd.Context())
			if err != nil {
				return err
			}

			if a.Format.JSON {
				entries := make([]map[string]any, 0, len(snap.Profiles))
				for _, p := range snap.Profiles {
					entries = append(entries, map[string]any{
						"name":         p.Name,
						"active":       p.Name == snap.Active && snap.Running,
						"configured":   p.Name == snap.Active,
						"placeholders": p.Placeholders,
					})
				}
				return a.emitJSON(map[string]any{"profiles": entries})
			}

			if len(snap.Profiles) == 0 {
				a.println(a.Theme.MutedStyle().Render("There are no profiles yet."))
				a.println(a.Theme.Hintf("create one with: sbctl add <name>"))
				return nil
			}

			a.printf("%s", a.Theme.RenderProfileList(snap.Profiles, snap.Active, snap.Running))
			if snap.Broken != "" {
				a.warn("%s", snap.Broken)
			}
			return nil
		},
	}
}
