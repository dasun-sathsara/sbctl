package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"sbctl/internal/profile"
	"sbctl/internal/ui"
)

// Command groups, so `sbctl --help` reads as a set of related tasks rather than
// one undifferentiated alphabetical list.
const (
	groupProfiles = "profiles"
	groupService  = "service"
	groupDiag     = "diagnostics"
)

// Execute parses args, runs the requested command, and returns the exit code.
func (a *App) Execute(args []string) int {
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	root.SetIn(a.In)

	if err := root.Execute(); err != nil {
		// Cancelling is a deliberate user choice, not a failure.
		if errors.Is(err, ui.ErrCancelled) {
			return ExitOK
		}
		return a.reportError(err)
	}
	return ExitOK
}

func (a *App) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "sbctl",
		Short: "Switch sing-box profiles and control the service that runs them",
		Long: "sbctl manages sing-box client profiles and the privileged service that runs\n" +
			"sing-box in TUN mode.\n\n" +
			"Run it with no arguments to pick a profile interactively. Every action is also\n" +
			"available as a subcommand so it can be scripted.",
		Example: "  sbctl                    # pick a profile interactively\n" +
			"  sbctl use work-vpn       # switch to a profile\n" +
			"  sbctl status --json      # machine-readable state\n" +
			"  sbctl off                # stop sing-box",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runInteractive(cmd)
		},
	}

	flags := root.PersistentFlags()
	flags.BoolVar(&a.Format.JSON, "json", false, "emit machine-readable JSON instead of formatted text")
	flags.BoolVar(&a.Format.Plain, "plain", false, "use ASCII symbols and no borders, for basic terminals and stable scripting")
	flags.BoolVar(&a.Format.NoColor, "no-color", false, "disable colour (also honoured via NO_COLOR)")
	flags.BoolVarP(&a.Format.Quiet, "quiet", "q", false, "suppress informational output; errors still print")
	flags.BoolVarP(&a.Format.Verbose, "verbose", "v", false, "print the commands and paths sbctl uses")

	// Resolve presentation once, after flags are parsed but before any handler
	// renders anything.
	root.PersistentPreRun = func(_ *cobra.Command, _ []string) {
		a.Theme = ui.NewTheme(a.Out, ui.Options{Plain: a.Format.Plain, NoColor: a.Format.NoColor})
	}

	root.AddGroup(
		&cobra.Group{ID: groupProfiles, Title: "Profiles:"},
		&cobra.Group{ID: groupService, Title: "Service:"},
		&cobra.Group{ID: groupDiag, Title: "Diagnostics:"},
	)

	root.AddCommand(
		a.listCmd(),
		a.useCmd(),
		a.addCmd(),
		a.editCmd(),
		a.rmCmd(),
		a.offCmd(),
		a.statusCmd(),
		a.logsCmd(),
		a.checkCmd(),
		a.doctorCmd(),
		a.ipCmd(),
		a.versionCmd(),
		a.printSudoersCmd(),
	)
	root.SetHelpCommandGroupID(groupDiag)
	root.SetCompletionCommandGroupID(groupDiag)
	return root
}

// profileNames provides shell completion for arguments that take a profile.
func (a *App) profileNames(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	profiles, err := profile.List(a.Layout.ProfilesDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	var names []string
	for _, p := range profiles {
		if prefix == "" || len(prefix) <= len(p.Name) && p.Name[:len(prefix)] == prefix {
			names = append(names, p.Name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// runInteractive renders the status panel and opens the picker.
func (a *App) runInteractive(cmd *cobra.Command) error {
	ctx := cmd.Context()

	snap, err := a.snapshot(ctx)
	if err != nil {
		return err
	}

	if len(snap.Profiles) == 0 {
		a.println(a.Theme.RenderStatus(a.statusView(snap)))
		a.printf("\n%s\n", a.Theme.MutedStyle().Render("There are no profiles yet."))
		a.println(a.Theme.Hintf("create one with: sbctl add <name>"))
		return nil
	}

	// Without a terminal there is nothing to interact with, so fall back to the
	// output the user can actually consume rather than hanging on input.
	if !a.interactive() {
		return a.printStatus(ctx, snap)
	}

	// On Windows, elevation must be resolved before the picker takes over the
	// terminal; see elevateAndRun.
	result, err := ui.RunPicker(a.Theme, a.statusView(snap), snap.Profiles, func(choice string) error {
		if choice == ui.TurnOffChoice {
			return a.offAction(ctx)
		}
		return a.useAction(ctx, choice)
	})
	if err != nil {
		if errors.Is(err, ui.ErrCancelled) {
			return nil
		}
		return err
	}
	return result.Err
}
