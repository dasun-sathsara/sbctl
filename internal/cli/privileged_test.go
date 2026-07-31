package cli

import (
	"context"
	"strings"
	"testing"

	"sbctl/internal/platform"
	"sbctl/internal/profile"
	"sbctl/internal/service"
)

// TestEveryPrivilegedCommandIsAuthorised closes the gap left by the
// platform-level sudoers test, which only checks the symlink command.
//
// It drives the real managers and the real activator through a recording Runner,
// collects every argv they actually hand to sudo, and requires each one to be
// authorised by a rule in the generated sudoers file. That matters because the
// rules are narrow: an argv with no matching rule does not degrade gracefully, it
// makes the operation fail with "a password is required". Deriving both sides
// from platform.Layout makes them agree by construction, and this asserts the
// construction actually holds for every code path rather than just the one.
func TestEveryPrivilegedCommandIsAuthorised(t *testing.T) {
	for _, layout := range []platform.Layout{platform.Darwin(), platform.Linux()} {
		t.Run(layout.OS, func(t *testing.T) {
			issued := collectPrivilegedArgv(t, layout)
			if len(issued) == 0 {
				t.Fatal("no privileged commands were recorded; the harness is not exercising them")
			}

			rules := layout.SudoCommands()
			for _, argv := range issued {
				if !authorised(rules, argv) {
					t.Errorf("this command is issued but no sudo rule permits it:\n  %s\nrules:\n%s",
						strings.Join(argv, " "), renderRules(rules))
				}
			}

			// The converse: a rule with no corresponding command is a privilege
			// granted for nothing, which widens the grant without cause.
			for _, rule := range rules {
				used := false
				for _, argv := range issued {
					if rule.Matches(argv) {
						used = true
						break
					}
				}
				if !used {
					t.Errorf("sudo rule %q is granted but no code path issues it:\n  %s", rule.Name, rule)
				}
			}
		})
	}
}

// collectPrivilegedArgv exercises every privileged operation and returns the argv
// each one passed to sudo, with the "sudo -n" prefix removed.
func collectPrivilegedArgv(t *testing.T, layout platform.Layout) [][]string {
	t.Helper()
	ctx := context.Background()

	// Responses are shaped so that every branch runs: the service is reported
	// loaded and running, so probe, restart and stop all proceed.
	recorder := &service.FakeRunner{
		Default: service.FakeResult{Output: launchdRunningFixture + systemdRunningFixture},
	}

	manager, activator := buildForTest(layout, recorder)

	if _, err := manager.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if err := manager.Restart(ctx); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Bootstrap is only reached when the daemon is not already loaded, so drive
	// that branch separately rather than leaving the rule unexercised.
	notLoaded := &service.FakeRunner{
		Default: service.FakeResult{Output: "Could not find service", Err: service.ExitError(113)},
		Responses: map[string]service.FakeResult{
			"sudo -n " + layout.CtlBin + " bootstrap": {Output: ""},
		},
	}
	bootstrapManager, _ := buildForTest(layout, notLoaded)
	_ = bootstrapManager.Restart(ctx)

	if _, err := activator.Activate(profilePathFor(layout, "work")); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	var argvs [][]string
	for _, call := range append(recorder.Calls, notLoaded.Calls...) {
		argvs = append(argvs, stripSudo(call))
	}
	return argvs
}

// stripSudo removes the "sudo -n" prefix so the remainder can be matched against
// a sudoers rule, which names the target command rather than sudo itself.
func stripSudo(argv []string) []string {
	if len(argv) >= 2 && argv[0] == "sudo" && argv[1] == "-n" {
		return argv[2:]
	}
	return argv
}

func authorised(rules []platform.SudoCommand, argv []string) bool {
	for _, rule := range rules {
		if rule.Matches(argv) {
			return true
		}
	}
	return false
}

func renderRules(rules []platform.SudoCommand) string {
	var b strings.Builder
	for _, rule := range rules {
		b.WriteString("  " + rule.String() + "\n")
	}
	return b.String()
}

// Fixtures that report a loaded, running service on either platform, so a single
// canned response satisfies both managers.
const launchdRunningFixture = "state = running\nruns = 1\npid = 4711\nlast exit code = (never exited)\n"

const systemdRunningFixture = "ActiveState=active\nSubState=running\nNRestarts=0\nExecMainPID=4711\nExecMainStatus=0\n"

// buildForTest wires the real managers and activator for a layout with a
// recording runner in place of the real one.
func buildForTest(layout platform.Layout, runner service.Runner) (service.Manager, profile.Activator) {
	return buildWith(layout, runner)
}

// profilePathFor builds a valid profile path within a layout's profiles dir.
func profilePathFor(layout platform.Layout, name string) string {
	return profile.PathFor(layout.ProfilesDir, name)
}
