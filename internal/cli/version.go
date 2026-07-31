package cli

import (
	"runtime"

	"github.com/spf13/cobra"

	"sbctl/internal/ui"
)

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Show version information",
		GroupID: groupDiag,
		Long: "Show the sbctl version, the platform it was built for, and the version of\n" +
			"sing-box it will drive.",
		Example: "  sbctl version\n  sbctl version --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			singBox, err := a.Checker.Version(cmd.Context())
			if err != nil {
				singBox = ""
				a.debugf("could not read the sing-box version: %v", err)
			}

			if a.Format.JSON {
				return a.emitJSON(map[string]any{
					"version":  a.Version,
					"sing_box": singBox,
					"platform": runtime.GOOS + "/" + runtime.GOARCH,
				})
			}

			reported := singBox
			if reported == "" {
				reported = "not installed"
			}
			a.printf("%s", a.Theme.KVBlock([]ui.KV{
				{Label: "sbctl", Value: a.Version},
				{Label: "sing-box", Value: reported},
				{Label: "platform", Value: runtime.GOOS + "/" + runtime.GOARCH},
			}))
			return nil
		},
	}
}
