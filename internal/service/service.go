// Package service controls the privileged process that runs sing-box: a
// launchd daemon on macOS, a systemd unit on Debian-family Linux, and a WinSW
// service on Windows.
//
// Every implementation talks to the host through a Runner, so the parsing logic
// — which is the part that actually breaks — is testable against captured real
// output without a privileged host.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// RunState is the coarse state of the managed service. Transient states such as
// START_PENDING are resolved inside the managers and never surfaced here,
// because no caller has anything useful to do with them.
type RunState string

const (
	StateRunning RunState = "running"
	StateStopped RunState = "stopped"
	StateError   RunState = "error"
)

// String implements fmt.Stringer.
func (s RunState) String() string { return string(s) }

// Health is a single liveness sample.
//
// It exists because a successful Restart only means the platform accepted the
// request. A sing-box config can pass validation, start, and then die
// immediately on a bad server address — and with launchd KeepAlive the service
// is restarted so fast that it still reports "running". Comparing two samples
// taken either side of a settle delay distinguishes a healthy start from a
// crash loop, which is what lets sbctl avoid reporting success while leaving the
// user with no network.
//
// Fields are best-effort: PID is 0 and Restarts is -1 when the platform cannot
// report them.
type Health struct {
	State    RunState
	PID      int
	Restarts int
	LastExit string
	Detail   string
}

// Unknown returns a Health with no liveness detail available.
func Unknown(state RunState) Health {
	return Health{State: state, Restarts: -1}
}

// CrashedSince reports whether this sample shows the service has died or
// restarted since the baseline sample, along with a human explanation.
//
// Both samples must be taken *after* the restart that is being judged, with the
// baseline captured once the service was first observed running. That ordering
// is what makes the comparison meaningful, because the platform counters do not
// share a common meaning: launchd's "runs" counts every spawn, including the
// intentional one sbctl just asked for, while systemd's NRestarts counts only
// the automatic ones. Sampling the baseline before the restart therefore made
// every successful macOS switch look like a crash loop. Sampling it after
// removes the intentional spawn from both sides of every comparison.
//
// It is deliberately conservative: when the platform cannot report restart
// counts or PIDs, only an outright non-running state counts as failure, so
// sbctl never rolls back a working profile on the strength of missing data.
func (h Health) CrashedSince(baseline Health) (bool, string) {
	if h.State != StateRunning {
		return true, fmt.Sprintf("service is %s", h.State)
	}
	if baseline.Restarts >= 0 && h.Restarts > baseline.Restarts {
		return true, fmt.Sprintf("service restarted %d time(s) while starting up", h.Restarts-baseline.Restarts)
	}
	// A different pid for a service that never stopped running means the
	// supervisor replaced the process underneath us, which is a crash loop even
	// on a platform that reports no restart counter at all.
	if baseline.PID > 0 && h.PID > 0 && h.PID != baseline.PID {
		return true, fmt.Sprintf("service was replaced by a new process (pid %d became %d)", baseline.PID, h.PID)
	}
	// Only a *newly* recorded exit indicates a problem. The exit code left
	// behind by the process the restart itself killed is present in both
	// samples and must not be mistaken for a fresh failure.
	if h.LastExit != "" && h.LastExit != baseline.LastExit {
		return true, fmt.Sprintf("service exited with %s", h.LastExit)
	}
	return false, ""
}

// Manager controls the lifecycle of the platform's sing-box service.
type Manager interface {
	// Restart starts the service, or restarts it if already running. A nil
	// error means the request was accepted, not that the service is healthy;
	// confirm with Probe.
	Restart(ctx context.Context) error

	// Stop stops the service. Stopping an already-stopped service is not an
	// error.
	Stop(ctx context.Context) error

	// Probe samples the current service state.
	Probe(ctx context.Context) (Health, error)
}

// ErrSudoNotConfigured reports that sbctl lacks the passwordless sudo rules it
// needs. It is matched with errors.Is so callers can map it to a permission
// exit code and a specific remedy.
var ErrSudoNotConfigured = errors.New("sbctl does not have permission to manage the sing-box service")

// IsSudoRefusal reports whether command output indicates sudo declined for lack
// of a matching NOPASSWD rule. Centralised here because the same check was
// previously duplicated across three files with three slightly different sets
// of match strings.
func IsSudoRefusal(output string) bool {
	lowered := strings.ToLower(output)
	for _, marker := range []string{
		"a password is required",
		"a terminal is required",
		"no tty present",
		"sudo: a password",
		"is not allowed to execute",
		"may not run",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// sudoError wraps a refusal with the output that proved it.
func sudoError(output string) error {
	detail := strings.TrimSpace(output)
	if detail == "" {
		return ErrSudoNotConfigured
	}
	return fmt.Errorf("%w: %s", ErrSudoNotConfigured, firstLine(detail))
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
