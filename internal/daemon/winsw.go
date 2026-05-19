package daemon

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type WinSWManager struct {
	ServiceName string
}

func (m WinSWManager) Restart() error {
	if err := m.runSC("stop", m.service()); err != nil && !strings.Contains(err.Error(), "service has not been started") {
		return err
	}
	// Wait for service to fully stop before starting again.
	for i := 0; i < 20; i++ {
		state, _ := m.Status()
		if state == StateStopped {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	return m.runSC("start", m.service())
}

func (m WinSWManager) Stop() error {
	err := m.runSC("stop", m.service())
	if err == nil || strings.Contains(err.Error(), "service has not been started") {
		return nil
	}
	return err
}

func (m WinSWManager) Status() (RunState, error) {
	cmd := exec.Command("sc.exe", "query", m.service())
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		if strings.Contains(text, "does not exist") || strings.Contains(text, "OpenService FAILED 1060") {
			return StateStopped, nil
		}
		return StateError, fmt.Errorf("sc.exe query failed: %w: %s", err, strings.TrimSpace(text))
	}
	if strings.Contains(text, "RUNNING") {
		return StateRunning, nil
	}
	if strings.Contains(text, "STOPPED") {
		return StateStopped, nil
	}
	return StateError, nil
}

func (m WinSWManager) runSC(args ...string) error {
	cmd := exec.Command("sc.exe", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc.exe %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m WinSWManager) service() string {
	if m.ServiceName == "" {
		return "sing-box"
	}
	return m.ServiceName
}
