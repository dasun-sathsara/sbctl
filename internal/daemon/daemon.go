package daemon

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

type RunState string

const (
	StateRunning RunState = "running"
	StateStopped RunState = "stopped"
	StateError   RunState = "error"
)

func Restart(label, plistPath string) error {
	loaded, err := isLoaded(label)
	if err != nil {
		return err
	}
	if loaded {
		if err := runLaunchctl("kickstart", "-k", label); err == nil {
			return nil
		} else if fallbackErr := restartWithFallback(label, plistPath); fallbackErr != nil {
			return fmt.Errorf("%v; fallback restart also failed: %w", err, fallbackErr)
		}
		return nil
	}
	return runLaunchctl("bootstrap", "system", plistPath)
}

func Stop(label string) error {
	err := runLaunchctl("bootout", label)
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "No such process") || strings.Contains(msg, "service is not loaded") || strings.Contains(msg, "Could not find specified service") {
		return nil
	}
	return err
}

func Status(label string) (RunState, error) {
	cmd := exec.Command("sudo", "-n", "launchctl", "print", label)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := string(out)
		switch {
		case strings.Contains(text, "a password is required"):
			return StateError, fmt.Errorf("sudo access is not configured; finish /etc/sudoers.d/sbctl setup and retry")
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

func runLaunchctl(args ...string) error {
	cmd := exec.Command("sudo", append([]string{"-n", "launchctl"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "a password is required") {
			return fmt.Errorf("sudo access is not configured; finish /etc/sudoers.d/sbctl setup and retry")
		}
		return fmt.Errorf("launchctl %s failed: %w: %s", strings.Join(args, " "), err, text)
	}
	return nil
}

func isLoaded(label string) (bool, error) {
	cmd := exec.Command("sudo", "-n", "launchctl", "print", label)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	text := strings.TrimSpace(string(out))
	switch {
	case strings.Contains(text, "a password is required"):
		return false, fmt.Errorf("sudo access is not configured; finish /etc/sudoers.d/sbctl setup and retry")
	case strings.Contains(text, "Could not find service"), strings.Contains(text, "Could not find specified service"), strings.Contains(text, "No such process"), strings.Contains(text, "service is not loaded"):
		return false, nil
	default:
		return false, fmt.Errorf("launchctl print failed: %w: %s", err, text)
	}
}

func restartWithFallback(label, plistPath string) error {
	if err := Stop(label); err != nil {
		return err
	}
	if err := runLaunchctl("bootstrap", "system", plistPath); err != nil {
		return err
	}
	return runLaunchctl("kickstart", "-k", label)
}
