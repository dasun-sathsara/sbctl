package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"sbctl/internal/profile"
	"sbctl/internal/service"
	"sbctl/internal/ui"
)

func (a *App) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Diagnose the sbctl installation",
		GroupID: groupDiag,
		Long: "Check everything sbctl depends on and report what is wrong and how to fix it.\n\n" +
			"The most valuable check is the sudo one: sbctl's privileged rules are scoped to\n" +
			"exact commands, so an installation left over from an earlier version can stop\n" +
			"matching. Rather than leaving that to surface as a mysterious permission failure,\n" +
			"doctor verifies each required permission directly.",
		Example: "  sbctl doctor\n  sbctl doctor --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := a.runChecks(cmd.Context())

			failed := false
			for _, c := range checks {
				if !c.OK && !c.Warn {
					failed = true
				}
			}

			if a.Format.JSON {
				entries := make([]map[string]any, 0, len(checks))
				for _, c := range checks {
					entries = append(entries, map[string]any{
						"name":    c.Name,
						"ok":      c.OK,
						"warning": c.Warn,
						"detail":  c.Detail,
						"hint":    c.Hint,
					})
				}
				if err := a.emitJSON(map[string]any{"checks": entries, "ok": !failed}); err != nil {
					return err
				}
				if failed {
					return &Error{Code: ExitError, Message: "some checks failed", Reported: true}
				}
				return nil
			}

			a.printf("%s", a.Theme.RenderChecks(checks))
			if failed {
				return &Error{Code: ExitError, Message: "some checks failed"}
			}
			a.println(a.Theme.Okf("everything looks healthy"))
			return nil
		},
	}
}

// runChecks performs every diagnostic and returns the results in report order.
func (a *App) runChecks(ctx context.Context) []ui.Check {
	checks := []ui.Check{a.checkSingBox(ctx)}
	checks = append(checks, a.checkProfilesDir())
	checks = append(checks, a.checkSudoRules(ctx)...)
	checks = append(checks, a.checkService(ctx), a.checkActiveConfig(), a.checkConcurrency())
	return checks
}

func (a *App) checkSingBox(ctx context.Context) ui.Check {
	version, err := a.Checker.Version(ctx)
	if err != nil {
		return ui.Check{
			Name:   "sing-box",
			Detail: "not found on PATH",
			Hint:   "install it with: sudo make install",
		}
	}
	return ui.Check{Name: "sing-box", OK: true, Detail: "version " + version}
}

func (a *App) checkProfilesDir() ui.Check {
	info, err := os.Stat(a.Layout.ProfilesDir)
	if err != nil {
		return ui.Check{
			Name:   "profiles",
			Detail: a.Layout.ProfilesDir + " does not exist",
			Hint:   "create it with: sudo make install",
		}
	}
	if !info.IsDir() {
		return ui.Check{
			Name:   "profiles",
			Detail: a.Layout.ProfilesDir + " is not a directory",
			Hint:   "move it aside and re-run: sudo make install",
		}
	}
	profiles, err := profile.List(a.Layout.ProfilesDir)
	if err != nil {
		return ui.Check{Name: "profiles", Detail: err.Error(), Hint: "repair ownership with: sudo make install"}
	}

	incomplete := 0
	for _, p := range profiles {
		if !p.Ready() {
			incomplete++
		}
	}
	detail := fmt.Sprintf("%d in %s", len(profiles), a.Layout.ProfilesDir)
	if incomplete > 0 {
		return ui.Check{
			Name:   "profiles",
			OK:     true,
			Warn:   true,
			Detail: fmt.Sprintf("%s (%d still have placeholders)", detail, incomplete),
			Hint:   "fill them in with: sbctl edit <name>",
		}
	}
	return ui.Check{Name: "profiles", OK: true, Detail: detail}
}

// checkSudoRules verifies each required privilege individually.
//
// The probe asks whether a command is permitted without running it, so this
// reports precisely which rule is missing. That turns the single most common
// failure mode — a sudoers file from an older install that no longer matches the
// commands this binary issues — from a mystery into a named problem.
func (a *App) checkSudoRules(ctx context.Context) []ui.Check {
	if !a.Layout.UsesSudo() {
		return nil
	}
	probe := a.SudoProbe
	if probe == nil {
		probe = realSudoProbe
	}

	commands := a.Layout.SudoCommands()
	checks := make([]ui.Check, 0, len(commands))
	for _, command := range commands {
		// Substitute the glob with a concrete filename so sudo can evaluate the
		// rule against a real argument.
		argv := make([]string, len(command.Argv))
		for i, arg := range command.Argv {
			argv[i] = strings.ReplaceAll(arg, "*.json", "probe.json")
		}

		if err := probe(ctx, argv); err == nil {
			checks = append(checks, ui.Check{
				Name:   "sudo: " + command.Name,
				OK:     true,
				Detail: "permitted",
			})
			continue
		} else {
			checks = append(checks, ui.Check{
				Name:   "sudo: " + command.Name,
				Detail: summariseSudo(err.Error()),
				Hint:   "reinstall the rules with: sudo make install",
			})
		}
	}
	return checks
}

// realSudoProbe uses `sudo -n -l <command>` which resolves the rule for a
// command without executing it.
func realSudoProbe(ctx context.Context, argv []string) error {
	args := append([]string{"-n", "-l"}, argv...)
	out, err := exec.CommandContext(ctx, "sudo", args...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return err
		}
		return fmt.Errorf("%s", detail)
	}
	return nil
}

func summariseSudo(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "not permitted"
	}
	if service.IsSudoRefusal(trimmed) {
		return "not permitted without a password"
	}
	if i := strings.IndexAny(trimmed, "\r\n"); i >= 0 {
		return trimmed[:i]
	}
	return trimmed
}

func (a *App) checkService(ctx context.Context) ui.Check {
	health, err := a.Manager.Probe(ctx)
	if err != nil {
		return ui.Check{
			Name:   "service",
			Detail: err.Error(),
			Hint:   "register the service with: sudo make install",
		}
	}
	detail := string(health.State)
	if health.PID > 0 {
		detail += fmt.Sprintf(" (pid %d)", health.PID)
	}
	// A stopped service is a normal, chosen state, not a fault.
	return ui.Check{Name: "service", OK: health.State != service.StateError, Detail: detail}
}

func (a *App) checkActiveConfig() ui.Check {
	active, err := a.Activator.ActiveName()
	if err != nil {
		return ui.Check{Name: "active config", Detail: err.Error(), Hint: "re-select a profile with: sbctl use <name>"}
	}
	if active == "" {
		return ui.Check{
			Name:   "active config",
			OK:     true,
			Warn:   true,
			Detail: "no profile selected",
			Hint:   "choose one with: sbctl use <name>",
		}
	}
	profiles, _ := profile.List(a.Layout.ProfilesDir)
	if broken := a.diagnoseActive(active, profiles); broken != "" {
		return ui.Check{Name: "active config", Detail: broken, Hint: "point it somewhere valid with: sbctl use <name>"}
	}
	return ui.Check{Name: "active config", OK: true, Detail: active}
}

// checkConcurrency documents a known limitation rather than leaving it implicit.
func (a *App) checkConcurrency() ui.Check {
	return ui.Check{
		Name:   "concurrency",
		OK:     true,
		Warn:   true,
		Detail: "sbctl assumes one instance at a time; there is no lock",
		Hint:   "avoid running two profile switches simultaneously",
	}
}
