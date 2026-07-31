package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// LaunchdManager drives a macOS system LaunchDaemon.
type LaunchdManager struct {
	// CtlBin is the absolute launchctl path, matching the installed sudoers rule.
	CtlBin string
	// Label is the service target, e.g. "system/app.lexiflix.singbox".
	Label string
	// PlistPath is the daemon definition used when bootstrapping.
	PlistPath string
	// Runner executes launchctl, normally wrapped in Sudo.
	Runner Runner
}

// launchd reports "not loaded" through several different phrasings depending on
// the macOS release, so all known variants are matched.
var launchdNotLoadedMarkers = []string{
	"could not find service",
	"could not find specified service",
	"no such process",
	"service is not loaded",
	"not find service",
}

func launchdNotLoaded(output string) bool {
	lowered := strings.ToLower(output)
	for _, marker := range launchdNotLoadedMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

var (
	launchdState    = regexp.MustCompile(`(?m)^\s*state\s*=\s*(\S+)`)
	launchdPID      = regexp.MustCompile(`(?m)^\s*pid\s*=\s*(\d+)`)
	launchdRuns     = regexp.MustCompile(`(?m)^\s*runs\s*=\s*(\d+)`)
	launchdLastExit = regexp.MustCompile(`(?m)^\s*last exit (?:code|status)\s*=\s*(.+)$`)
)

// Probe samples the daemon state from `launchctl print`.
func (m LaunchdManager) Probe(ctx context.Context) (Health, error) {
	out, err := m.Runner.Run(ctx, m.CtlBin, "print", m.Label)
	text := string(out)
	if err != nil {
		if errors.Is(err, ErrSudoNotConfigured) {
			return Unknown(StateError), err
		}
		if launchdNotLoaded(text) {
			return Unknown(StateStopped), nil
		}
		return Unknown(StateError), fmt.Errorf("could not read the launchd service state: %w: %s", err, firstLine(strings.TrimSpace(text)))
	}
	return ParseLaunchdPrint(text), nil
}

// ParseLaunchdPrint extracts a Health sample from `launchctl print` output.
//
// The fields it relies on are real: launchctl emits "state", "pid", "runs" and
// "last exit code". "runs" is the key one — with KeepAlive set, a service whose
// config is broken is restarted immediately, so it reports state=running while
// its run counter climbs. Tracking that counter is the only reliable way to tell
// a healthy start from a crash loop.
func ParseLaunchdPrint(text string) Health {
	health := Health{State: StateStopped, Restarts: -1}

	if m := launchdState.FindStringSubmatch(text); m != nil {
		switch strings.ToLower(m[1]) {
		case "running":
			health.State = StateRunning
		case "waiting", "not":
			// "waiting" means loaded but not currently executing. For a
			// KeepAlive daemon that is either "about to start" or "gave up",
			// which the exit code below disambiguates.
			health.State = StateStopped
		default:
			health.State = StateStopped
		}
		health.Detail = "state = " + m[1]
	}
	if m := launchdPID.FindStringSubmatch(text); m != nil {
		if pid, err := strconv.Atoi(m[1]); err == nil {
			health.PID = pid
		}
	}
	if m := launchdRuns.FindStringSubmatch(text); m != nil {
		if runs, err := strconv.Atoi(m[1]); err == nil {
			health.Restarts = runs
		}
	}
	if m := launchdLastExit.FindStringSubmatch(text); m != nil {
		value := strings.TrimSpace(m[1])
		// "(never exited)" is the healthy case and must not be treated as a
		// failure code.
		if !strings.Contains(value, "never exited") && value != "0" {
			health.LastExit = value
		}
	}
	return health
}

// Restart kickstarts the daemon, bootstrapping it first if it is not loaded.
func (m LaunchdManager) Restart(ctx context.Context) error {
	loaded, err := m.isLoaded(ctx)
	if err != nil {
		return err
	}
	if !loaded {
		return m.run(ctx, "bootstrap", "system", m.PlistPath)
	}
	if err := m.run(ctx, "kickstart", "-k", m.Label); err == nil {
		return nil
	} else if fallbackErr := m.reload(ctx); fallbackErr != nil {
		// Report both: the kickstart failure explains why a reload was needed,
		// and the reload failure is what the user must actually fix.
		return fmt.Errorf("%w (reload fallback also failed: %w)", err, fallbackErr)
	}
	return nil
}

// reload boots the daemon out and back in, recovering from a stale definition
// that kickstart cannot refresh.
func (m LaunchdManager) reload(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	return m.run(ctx, "bootstrap", "system", m.PlistPath)
}

// Stop boots the daemon out, treating an already-unloaded service as success.
func (m LaunchdManager) Stop(ctx context.Context) error {
	out, err := m.Runner.Run(ctx, m.CtlBin, "bootout", m.Label)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrSudoNotConfigured) {
		return err
	}
	if launchdNotLoaded(string(out)) {
		return nil
	}
	return fmt.Errorf("could not stop the sing-box service: %w: %s", err, firstLine(strings.TrimSpace(string(out))))
}

func (m LaunchdManager) isLoaded(ctx context.Context) (bool, error) {
	out, err := m.Runner.Run(ctx, m.CtlBin, "print", m.Label)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrSudoNotConfigured) {
		return false, err
	}
	if launchdNotLoaded(string(out)) {
		return false, nil
	}
	return false, fmt.Errorf("could not read the launchd service state: %w: %s", err, firstLine(strings.TrimSpace(string(out))))
}

func (m LaunchdManager) run(ctx context.Context, args ...string) error {
	out, err := m.Runner.Run(ctx, m.CtlBin, args...)
	if err != nil {
		if errors.Is(err, ErrSudoNotConfigured) {
			return err
		}
		return fmt.Errorf("launchctl %s failed: %w: %s", strings.Join(args, " "), err, firstLine(strings.TrimSpace(string(out))))
	}
	return nil
}
