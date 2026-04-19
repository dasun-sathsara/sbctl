package singbox

import (
	"fmt"
	"os/exec"
	"strings"
)

func Check(path string) error {
	bin, err := exec.LookPath("sing-box")
	if err != nil {
		return fmt.Errorf("sing-box not found in PATH")
	}
	cmd := exec.Command(bin, "check", "-c", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func Version() (string, error) {
	bin, err := exec.LookPath("sing-box")
	if err != nil {
		return "", err
	}
	cmd := exec.Command(bin, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
