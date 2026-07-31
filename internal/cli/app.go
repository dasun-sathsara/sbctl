// Package cli implements the sbctl command surface.
//
// Every command handler reads its dependencies from an App rather than reaching
// for package-level state, and writes through App.Out/App.Err rather than
// os.Stdout. That is what makes the commands testable: a test constructs an App
// backed by fakes and a bytes.Buffer, runs the real cobra command, and asserts
// on the captured output and exit code.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"sbctl/assets"
	"sbctl/internal/platform"
	"sbctl/internal/profile"
	"sbctl/internal/service"
	"sbctl/internal/ui"
)

// SchemaVersion identifies the --json output shape. It is emitted with every
// JSON payload so a consumer can detect a format change instead of silently
// misreading new output.
const SchemaVersion = 1

// Format holds the presentation flags shared by every command.
type Format struct {
	JSON    bool
	Plain   bool
	NoColor bool
	Quiet   bool
	Verbose bool
}

// App carries everything a command handler needs.
type App struct {
	Out io.Writer
	Err io.Writer
	In  io.Reader

	Format Format
	Theme  ui.Theme

	Layout    platform.Layout
	Manager   service.Manager
	Activator profile.Activator
	Checker   profile.Checker
	Elevator  platform.Elevator
	Follower  platform.LogFollower
	Notifier  Notifier
	Editor    Editor

	// SudoProbe reports whether a privileged command would be permitted, without
	// running it. Injected so `doctor` can be tested without invoking real sudo.
	SudoProbe func(ctx context.Context, argv []string) error

	// Skeleton is the seed profile content used by `sbctl add`.
	Skeleton string

	// Version is the sbctl build version.
	Version string

	// SettleDelay is how long activation waits before confirming the service
	// stayed up. Tests set it to zero.
	SettleDelay time.Duration

	// StartGrace is how long activation waits for the service to come up at all
	// before declaring that it never started. Tests set it to zero, which makes
	// the wait a single probe.
	StartGrace time.Duration

	// now is the time source, read through clock() so it may be left nil.
	now func() time.Time
}

// HealthSettleDelay is how long sbctl waits after a restart before deciding the
// service is genuinely healthy.
//
// It has to be long enough for a config with an unreachable server to fail and,
// under launchd KeepAlive, be restarted at least once — otherwise the very
// failure this check exists to catch would be sampled before it happens.
const HealthSettleDelay = 2500 * time.Millisecond

// StartGracePeriod is how long sbctl waits for a restarted service to report
// running before giving up on it.
//
// A restart request returns as soon as the supervisor accepts it, which can be
// before the process exists, so activation cannot assume the service is up the
// instant Restart returns.
const StartGracePeriod = 3 * time.Second

// startPollInterval is how often awaitRunning re-probes while waiting for the
// service to come up.
const startPollInterval = 150 * time.Millisecond

// Notifier delivers a desktop notification.
type Notifier interface {
	Notify(message string)
}

// Editor opens a file for interactive editing.
type Editor func(ctx context.Context, path string) error

// Main is the process entry point. It returns the exit code rather than calling
// os.Exit so that the whole flow remains testable.
func Main(version string) int {
	app, err := NewApp(version, os.Stdout, os.Stderr, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitError
	}
	return app.Execute(os.Args[1:])
}

// NewApp wires the production dependencies for the host platform.
func NewApp(version string, out, errOut io.Writer, in io.Reader) (*App, error) {
	layout, err := platform.Detect()
	if err != nil {
		return nil, err
	}

	app := &App{
		Out:         out,
		Err:         errOut,
		In:          in,
		Layout:      layout,
		Elevator:    platform.NewElevator(),
		Follower:    layout.Follower(),
		Checker:     profile.ExecChecker{},
		Editor:      DefaultEditor,
		Notifier:    NewNotifier(layout),
		Version:     resolveVersion(version),
		Skeleton:    assets.Skeleton,
		SettleDelay: HealthSettleDelay,
		StartGrace:  StartGracePeriod,
		now:         time.Now,
	}
	app.Manager, app.Activator = build(layout, func(name string, args []string) {
		// Read the flag lazily. Rebuilding the dependency graph later, once
		// --verbose is known, would discard anything a caller had injected.
		app.debugf("exec: %s %s", name, strings.Join(args, " "))
	})
	app.SudoProbe = realSudoProbe
	return app, nil
}

// build constructs the service manager and activator for a layout.
//
// trace, when non-nil, is called with each subprocess before it runs; it backs
// --verbose so users can see exactly which privileged commands sbctl issues.
func build(layout platform.Layout, trace func(string, []string)) (service.Manager, profile.Activator) {
	return buildWith(layout, service.ExecRunner{Trace: trace})
}

// buildWith is build with the command runner supplied, so tests can record the
// exact privileged commands each platform's manager and activator issue and
// check them against the installed sudo rules.
func buildWith(layout platform.Layout, runner service.Runner) (service.Manager, profile.Activator) {
	switch layout.OS {
	case "darwin":
		sudo := service.Sudo{Runner: runner}
		return service.LaunchdManager{
			CtlBin:    layout.CtlBin,
			Label:     layout.LaunchdLabel,
			PlistPath: layout.PlistPath,
			Runner:    sudo,
		}, sudoSymlinkActivator(layout, sudo)

	case "linux":
		sudo := service.Sudo{Runner: runner}
		return service.SystemdManager{
			CtlBin:         layout.CtlBin,
			Unit:           layout.ServiceName,
			ShowProperties: platform.SystemdShowProperties,
			Runner:         sudo,
		}, sudoSymlinkActivator(layout, sudo)

	default:
		return service.WinSWManager{Name: layout.ServiceName, Runner: runner},
			profile.CopyActivator{
				ActiveConfigPath: layout.ActiveConfigPath,
				ActiveNamePath:   layout.ActiveNamePath,
			}
	}
}

// sudoSymlinkActivator builds an activator whose symlink swap runs through the
// single narrow sudo rule installed for it.
func sudoSymlinkActivator(layout platform.Layout, sudo service.Sudo) profile.Activator {
	return profile.SymlinkActivator{
		ActiveConfigPath: layout.ActiveConfigPath,
		Link: func(ctx context.Context, target string) error {
			argv := layout.ActivateArgv(target)
			_, err := sudo.Run(ctx, argv[0], argv[1:]...)
			return err
		},
	}
}

// resolveVersion falls back to the module build metadata when no version was
// stamped in at link time, so a `go install`ed binary still reports something
// meaningful instead of "dev".
func resolveVersion(version string) string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && len(setting.Value) >= 12 {
				return "dev+" + setting.Value[:12]
			}
		}
	}
	return "dev"
}

// ---------------------------------------------------------------------------
// Output helpers
// ---------------------------------------------------------------------------

// clock reads the current time. It defaults to the real clock so an App built
// directly, as tests do, never has to supply one.
func (a *App) clock() time.Time {
	if a.now == nil {
		return time.Now()
	}
	return a.now()
}

// since reports how long has passed, rounded to keep --verbose traces readable.
func (a *App) since(start time.Time) time.Duration {
	return a.clock().Sub(start).Round(time.Millisecond)
}

// interactive reports whether prompts and animations are appropriate.
//
// It is false for JSON, quiet and non-terminal output, which is what keeps sbctl
// usable in a pipeline: a command must never wait on input that a script cannot
// supply.
func (a *App) interactive() bool {
	return !a.Format.JSON && !a.Format.Quiet && ui.IsTerminal(a.Out) && ui.IsTerminal(a.In)
}

// printf writes informational output, suppressed by --quiet and --json.
func (a *App) printf(format string, args ...any) {
	if a.Format.Quiet || a.Format.JSON {
		return
	}
	fmt.Fprintf(a.Out, format, args...)
}

// println writes an informational line, suppressed by --quiet and --json.
func (a *App) println(line string) {
	if a.Format.Quiet || a.Format.JSON {
		return
	}
	fmt.Fprintln(a.Out, line)
}

// success reports a completed action.
func (a *App) success(format string, args ...any) {
	a.println(a.Theme.Okf(format, args...))
}

// warn reports a non-fatal concern on stderr, so it never contaminates piped
// stdout.
func (a *App) warn(format string, args ...any) {
	if a.Format.Quiet || a.Format.JSON {
		return
	}
	fmt.Fprintln(a.Err, a.Theme.Warnf(format, args...))
}

// debugf writes diagnostics when --verbose is set. Always stderr, so it cannot
// corrupt machine-readable stdout.
func (a *App) debugf(format string, args ...any) {
	if !a.Format.Verbose {
		return
	}
	fmt.Fprintf(a.Err, "%s %s\n", a.Theme.MutedStyle().Render("[debug]"), fmt.Sprintf(format, args...))
}

// emitJSON writes a payload with the schema version attached.
func (a *App) emitJSON(payload map[string]any) error {
	full := make(map[string]any, len(payload)+1)
	full["schema"] = SchemaVersion
	for k, v := range payload {
		full[k] = v
	}
	encoder := json.NewEncoder(a.Out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(full)
}

// reportError renders a failure and returns its exit code.
func (a *App) reportError(err error) int {
	failure := classify(err)
	if failure == nil {
		return ExitOK
	}

	// The handler already rendered the details; only the exit code is left.
	if failure.Reported {
		return failure.ExitCode()
	}

	if a.Format.JSON {
		payload := map[string]any{
			"error": failure.Message,
			"code":  failure.ExitCode(),
		}
		if len(failure.Hints) > 0 {
			payload["hints"] = failure.Hints
		}
		_ = a.emitJSON(payload)
		return failure.ExitCode()
	}

	fmt.Fprintln(a.Err, a.Theme.Failf("%s", failure.Message))
	for _, hint := range failure.Hints {
		fmt.Fprintln(a.Err, a.Theme.Hintf("%s", hint))
	}
	if a.Format.Verbose && failure.Err != nil {
		a.debugf("cause: %v", failure.Err)
	}
	return failure.ExitCode()
}
