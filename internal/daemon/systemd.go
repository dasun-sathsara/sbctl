package daemon

import (
	"fmt"
	"os/exec"
	"strings"
)

type SystemdManager struct {
	ServiceName string
}

func (m SystemdManager) Restart() error {
	return m.runSystemctl("restart", m.service())
}

func (m SystemdManager) Stop() error {
	err := m.runSystemctl("stop", m.service())
	if err == nil || isSystemdMissingOrInactive(err.Error()) {
		return nil
	}
	return err
}

func (m SystemdManager) Status() (RunState, error) {
	cmd := exec.Command("sudo", "-n", "systemctl", "is-active", "--quiet", m.service())
	out, err := cmd.CombinedOutput()
	if err == nil {
		return StateRunning, nil
	}
	text := strings.TrimSpace(string(out))
	if strings.Contains(text, "a password is required") {
		return StateError, sudoSetupError()
	}
	stateCmd := exec.Command("sudo", "-n", "systemctl", "is-active", m.service())
	stateOut, stateErr := stateCmd.CombinedOutput()
	stateText := strings.TrimSpace(string(stateOut))
	if stateErr != nil {
		if strings.Contains(stateText, "a password is required") {
			return StateError, sudoSetupError()
		}
		if stateText == "inactive" || stateText == "failed" || stateText == "unknown" || strings.Contains(stateText, "could not be found") {
			return StateStopped, nil
		}
		return StateError, fmt.Errorf("systemctl is-active failed: %w: %s", stateErr, stateText)
	}
	if stateText == "active" {
		return StateRunning, nil
	}
	return StateStopped, nil
}

func (m SystemdManager) runSystemctl(args ...string) error {
	cmd := exec.Command("sudo", append([]string{"-n", "systemctl"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "a password is required") {
			return sudoSetupError()
		}
		return fmt.Errorf("systemctl %s failed: %w: %s", strings.Join(args, " "), err, text)
	}
	return nil
}

func (m SystemdManager) service() string {
	if m.ServiceName == "" {
		return "sing-box"
	}
	return m.ServiceName
}

func isSystemdMissingOrInactive(msg string) bool {
	return strings.Contains(msg, "not loaded") || strings.Contains(msg, "could not be found") || strings.Contains(msg, "inactive")
}
