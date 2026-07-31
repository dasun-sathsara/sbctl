package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SystemdManager drives a systemd unit.
type SystemdManager struct {
	// CtlBin is the absolute systemctl path, matching the installed sudoers rule.
	CtlBin string
	// Unit is the service name, e.g. "sing-box".
	Unit string
	// ShowProperties is the property list passed to `systemctl show -p`. It must
	// match the installed sudoers rule exactly.
	ShowProperties string
	// Runner executes systemctl, normally wrapped in Sudo.
	Runner Runner
}

// Probe samples unit state via `systemctl show -p`.
//
// This replaces parsing the prose output of `systemctl is-active`. show emits
// key=value pairs that are stable across locales and systemd versions, and it
// carries NRestarts, which is what makes crash-loop detection possible.
func (m SystemdManager) Probe(ctx context.Context) (Health, error) {
	out, err := m.Runner.Run(ctx, m.CtlBin, "show", "-p", m.ShowProperties, m.Unit)
	text := string(out)
	if err != nil {
		if errors.Is(err, ErrSudoNotConfigured) {
			return Unknown(StateError), err
		}
		if systemdMissing(text) {
			return Unknown(StateStopped), nil
		}
		return Unknown(StateError), fmt.Errorf("could not read the systemd unit state: %w: %s", err, firstLine(strings.TrimSpace(text)))
	}
	health := ParseSystemdShow(text)
	if health.State == StateStopped && strings.TrimSpace(text) == "" {
		// An empty response means systemd knows nothing about the unit.
		return Unknown(StateStopped), nil
	}
	return health, nil
}

// ParseSystemdShow extracts a Health sample from `systemctl show -p` output.
func ParseSystemdShow(text string) Health {
	health := Health{State: StateStopped, Restarts: -1}
	var active, sub, mainStatus string

	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "ActiveState":
			active = value
		case "SubState":
			sub = value
		case "NRestarts":
			if n, err := strconv.Atoi(value); err == nil {
				health.Restarts = n
			}
		case "ExecMainPID":
			if pid, err := strconv.Atoi(value); err == nil {
				health.PID = pid
			}
		case "ExecMainStatus":
			mainStatus = value
		}
	}

	switch active {
	case "active", "reloading":
		health.State = StateRunning
	case "activating":
		// Still coming up; treat as running so a slow start is not mistaken for
		// a crash. The settle delay in the activation flow re-samples anyway.
		health.State = StateRunning
	case "failed":
		health.State = StateError
	default:
		health.State = StateStopped
	}

	if mainStatus != "" && mainStatus != "0" {
		health.LastExit = mainStatus
	}
	health.Detail = strings.TrimSpace("ActiveState=" + active + " SubState=" + sub)
	return health
}

func systemdMissing(output string) bool {
	lowered := strings.ToLower(output)
	for _, marker := range []string{"could not be found", "not loaded", "no such file"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// Restart restarts the unit.
func (m SystemdManager) Restart(ctx context.Context) error {
	out, err := m.Runner.Run(ctx, m.CtlBin, "restart", m.Unit)
	if err != nil {
		if errors.Is(err, ErrSudoNotConfigured) {
			return err
		}
		return fmt.Errorf("could not restart the sing-box service: %w: %s", err, firstLine(strings.TrimSpace(string(out))))
	}
	return nil
}

// Stop stops the unit, treating an absent or already-inactive unit as success.
func (m SystemdManager) Stop(ctx context.Context) error {
	out, err := m.Runner.Run(ctx, m.CtlBin, "stop", m.Unit)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrSudoNotConfigured) {
		return err
	}
	if systemdMissing(string(out)) {
		return nil
	}
	return fmt.Errorf("could not stop the sing-box service: %w: %s", err, firstLine(strings.TrimSpace(string(out))))
}
