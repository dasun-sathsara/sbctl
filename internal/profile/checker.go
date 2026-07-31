package profile

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Checker validates a profile and reports the validating tool's version.
type Checker interface {
	// Check returns nil when the profile is a valid sing-box configuration.
	Check(ctx context.Context, path string) error
	// Version returns the sing-box version string.
	Version(ctx context.Context) (string, error)
}

// ErrSingBoxMissing reports that the sing-box binary is not installed.
var ErrSingBoxMissing = errors.New("sing-box was not found on PATH")

// InvalidConfigError reports a configuration rejected by sing-box, preserving
// the tool's own diagnostics, which are far more useful than a wrapper message.
type InvalidConfigError struct {
	Path   string
	Output string
	Err    error
}

func (e *InvalidConfigError) Error() string {
	detail := strings.TrimSpace(e.Output)
	if detail == "" {
		return fmt.Sprintf("sing-box rejected %s: %v", e.Path, e.Err)
	}
	return fmt.Sprintf("sing-box rejected %s:\n%s", e.Path, indent(detail, "  "))
}

func (e *InvalidConfigError) Unwrap() error { return e.Err }

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// ExecChecker validates configurations by running the real sing-box binary.
type ExecChecker struct {
	// Binary overrides the executable name; empty means look up "sing-box".
	Binary string
	// Timeout bounds validation. Zero uses a 20s default.
	Timeout time.Duration
}

func (c ExecChecker) binary() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "sing-box"
}

func (c ExecChecker) resolve() (string, error) {
	path, err := exec.LookPath(c.binary())
	if err != nil {
		return "", ErrSingBoxMissing
	}
	return path, nil
}

func (c ExecChecker) run(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := c.resolve()
	if err != nil {
		return nil, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(ctx, bin, args...).CombinedOutput()
}

func (c ExecChecker) Check(ctx context.Context, path string) error {
	out, err := c.run(ctx, "check", "-c", path)
	if err != nil {
		if errors.Is(err, ErrSingBoxMissing) {
			return err
		}
		return &InvalidConfigError{Path: path, Output: string(out), Err: err}
	}
	return nil
}

func (c ExecChecker) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "version")
	if err != nil {
		return "", err
	}
	// sing-box prints "sing-box version 1.13.8" followed by build details.
	first := strings.TrimSpace(firstLine(string(out)))
	if fields := strings.Fields(first); len(fields) >= 3 && fields[1] == "version" {
		return fields[2], nil
	}
	return first, nil
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// FakeChecker is a Checker for tests.
type FakeChecker struct {
	// Invalid maps a path to the error Check should return.
	Invalid map[string]error
	// Err is returned for every path when set.
	Err error
	// VersionValue is returned by Version.
	VersionValue string
	// Checked records every validated path in order.
	Checked []string
}

func (c *FakeChecker) Check(_ context.Context, path string) error {
	c.Checked = append(c.Checked, path)
	if c.Err != nil {
		return c.Err
	}
	return c.Invalid[path]
}

func (c *FakeChecker) Version(context.Context) (string, error) {
	if c.VersionValue == "" {
		return "0.0.0-fake", nil
	}
	return c.VersionValue, nil
}
