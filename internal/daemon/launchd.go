package daemon

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

type LaunchdManager struct {
	Label     string
	PlistPath string
}

func (m LaunchdManager) Restart() error {
	loaded, err := m.isLoaded()
	if err != nil {
		return err
	}
	if loaded {
		if err := m.runLaunchctl("kickstart", "-k", m.Label); err == nil {
			return nil
		} else if fallbackErr := m.restartWithFallback(); fallbackErr != nil {
			return fmt.Errorf("%v; fallback restart also failed: %w", err, fallbackErr)
		}
		return nil
	}
	return m.runLaunchctl("bootstrap", "system", m.PlistPath)
}

func (m LaunchdManager) Stop() error {
	err := m.runLaunchctl("bootout", m.Label)
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "No such process") || strings.Contains(msg, "service is not loaded") || strings.Contains(msg, "Could not find specified service") {
		return nil
	}
	return err
}

func (m LaunchdManager) Status() (RunState, error) {
	cmd := exec.Command("sudo", "-n", "launchctl", "print", m.Label)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := string(out)
		switch {
		case strings.Contains(text, "a password is required"):
			return StateError, sudoSetupError()
		case strings.Contains(text, "Could not find service"), strings.Contains(text, "Could not find specified service"), strings.Contains(text, "No such process"):
			return StateStopped, nil
		default:
			return StateError, fmt.Errorf("launchctl print failed: %w: %s", err, strings.TrimSpace(text))
		}
	}
	if bytes.Contains(out, []byte("state = running")) {
		return StateRunning, nil
	}
	if bytes.Contains(out, []byte("state = waiting")) {
		return StateStopped, nil
	}
	return StateStopped, nil
}

func (m LaunchdManager) runLaunchctl(args ...string) error {
	cmd := exec.Command("sudo", append([]string{"-n", "launchctl"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "a password is required") {
			return sudoSetupError()
		}
		return fmt.Errorf("launchctl %s failed: %w: %s", strings.Join(args, " "), err, text)
	}
	return nil
}

func (m LaunchdManager) isLoaded() (bool, error) {
	cmd := exec.Command("sudo", "-n", "launchctl", "print", m.Label)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	text := strings.TrimSpace(string(out))
	switch {
	case strings.Contains(text, "a password is required"):
		return false, sudoSetupError()
	case strings.Contains(text, "Could not find service"), strings.Contains(text, "Could not find specified service"), strings.Contains(text, "No such process"), strings.Contains(text, "service is not loaded"):
		return false, nil
	default:
		return false, fmt.Errorf("launchctl print failed: %w: %s", err, text)
	}
}

func (m LaunchdManager) restartWithFallback() error {
	if err := m.Stop(); err != nil {
		return err
	}
	if err := m.runLaunchctl("bootstrap", "system", m.PlistPath); err != nil {
		return err
	}
	return m.runLaunchctl("kickstart", "-k", m.Label)
}

func sudoSetupError() error {
	return fmt.Errorf("sudo access is not configured; finish /etc/sudoers.d/sbctl setup and retry")
}
