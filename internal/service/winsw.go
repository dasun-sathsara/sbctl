package service

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WinSWManager drives a Windows service, normally one installed by WinSW.
type WinSWManager struct {
	// Name is the Windows service name.
	Name string
	// Runner executes sc.exe. No sudo wrapper is used; the process is already
	// elevated by the time these calls are made.
	Runner Runner
	// StopTimeout bounds how long Restart waits for a stop to complete.
	StopTimeout time.Duration
	// Sleep is injected so tests do not actually wait.
	Sleep func(time.Duration)
}

// winState is the raw service state reported by sc.exe, including the transient
// values that must not be mistaken for errors.
type winState string

const (
	winRunning      winState = "RUNNING"
	winStopped      winState = "STOPPED"
	winStartPending winState = "START_PENDING"
	winStopPending  winState = "STOP_PENDING"
	winUnknown      winState = ""
)

var (
	scState = regexp.MustCompile(`STATE\s*:\s*\d+\s+(\S+)`)
	scPID   = regexp.MustCompile(`PID\s*:\s*(\d+)`)
)

func scMissing(output string) bool {
	lowered := strings.ToLower(output)
	return strings.Contains(lowered, "does not exist") ||
		strings.Contains(lowered, "1060")
}

// Probe samples the service state via `sc.exe queryex`, which unlike plain
// query also reports the PID needed for liveness comparison.
func (m WinSWManager) Probe(ctx context.Context) (Health, error) {
	out, err := m.Runner.Run(ctx, "sc.exe", "queryex", m.service())
	text := string(out)
	if err != nil {
		if scMissing(text) {
			return Unknown(StateStopped), nil
		}
		return Unknown(StateError), fmt.Errorf("could not read the Windows service state: %w: %s", err, firstLine(strings.TrimSpace(text)))
	}
	health, _ := ParseSCQuery(text)
	return health, nil
}

// ParseSCQuery extracts a Health sample and the raw transient state from
// `sc.exe query`/`queryex` output.
//
// START_PENDING and STOP_PENDING are reported as running and stopped
// respectively rather than as errors. The previous implementation returned an
// error state for anything that was not exactly RUNNING or STOPPED, so a
// perfectly normal service still coming up looked like a failure.
func ParseSCQuery(text string) (Health, winState) {
	health := Health{State: StateStopped, Restarts: -1}
	raw := winUnknown

	if m := scState.FindStringSubmatch(text); m != nil {
		raw = winState(strings.ToUpper(m[1]))
		health.Detail = "STATE = " + m[1]
	}
	switch raw {
	case winRunning, winStartPending:
		health.State = StateRunning
	case winStopped, winStopPending:
		health.State = StateStopped
	case winUnknown:
		health.State = StateError
	default:
		health.State = StateStopped
	}

	if m := scPID.FindStringSubmatch(text); m != nil {
		if pid, err := strconv.Atoi(m[1]); err == nil {
			health.PID = pid
		}
	}
	// A PID of 0 alongside RUNNING is contradictory; report stopped.
	if raw == winRunning && health.PID == 0 {
		health.State = StateStopped
	}
	return health, raw
}

// Restart stops the service, waits for the stop to complete, then starts it.
func (m WinSWManager) Restart(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	if err := m.waitForStop(ctx); err != nil {
		return err
	}
	out, err := m.Runner.Run(ctx, "sc.exe", "start", m.service())
	if err != nil {
		return fmt.Errorf("could not start the sing-box service: %w: %s", err, firstLine(strings.TrimSpace(string(out))))
	}
	return nil
}

// Stop stops the service, treating an absent or already-stopped service as
// success.
func (m WinSWManager) Stop(ctx context.Context) error {
	out, err := m.Runner.Run(ctx, "sc.exe", "stop", m.service())
	if err == nil {
		return nil
	}
	text := strings.ToLower(string(out))
	if scMissing(text) ||
		strings.Contains(text, "has not been started") ||
		strings.Contains(text, "1062") {
		return nil
	}
	return fmt.Errorf("could not stop the sing-box service: %w: %s", err, firstLine(strings.TrimSpace(string(out))))
}

// waitForStop polls until the service reports STOPPED, so that a start issued
// while a stop is still pending does not fail.
func (m WinSWManager) waitForStop(ctx context.Context) error {
	timeout := m.StopTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	sleep := m.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	const interval = 250 * time.Millisecond
	attempts := int(timeout / interval)
	if attempts < 1 {
		attempts = 1
	}

	for i := 0; i < attempts; i++ {
		out, err := m.Runner.Run(ctx, "sc.exe", "queryex", m.service())
		if err != nil {
			if scMissing(string(out)) {
				return nil
			}
			return fmt.Errorf("could not confirm the sing-box service stopped: %w", err)
		}
		if _, raw := ParseSCQuery(string(out)); raw == winStopped || raw == winUnknown {
			return nil
		}
		sleep(interval)
	}
	return fmt.Errorf("the sing-box service did not stop within %s", timeout)
}

func (m WinSWManager) service() string {
	if m.Name == "" {
		return "sing-box"
	}
	return m.Name
}
