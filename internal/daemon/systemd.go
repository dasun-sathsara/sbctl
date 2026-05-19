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
	cmd := exec.Command("sudo", "-n", "systemctl", "is-active", m.service())
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if strings.Contains(text, "a password is required") {
		return StateError, sudoSetupError()
	}
	if err == nil && text == "active" {
		return StateRunning, nil
	}
	if text == "inactive" || text == "failed" || text == "unknown" || strings.Contains(text, "could not be found") {
		return StateStopped, nil
	}
	if err != nil {
		return StateError, fmt.Errorf("systemctl is-active failed: %w: %s", err, text)
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
