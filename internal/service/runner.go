package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes an external command and returns its combined output.
//
// The signature intentionally mirrors exec.Cmd.CombinedOutput, which is the
// only shape any caller in sbctl needs. err wraps *exec.ExitError so exit codes
// remain inspectable.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// DefaultTimeout bounds every subprocess. Without it a wedged launchctl or
// systemctl call hangs sbctl indefinitely with no output and no way to tell
// what it is waiting for.
const DefaultTimeout = 30 * time.Second

// ExecRunner runs real commands.
type ExecRunner struct {
	// Timeout overrides DefaultTimeout when positive.
	Timeout time.Duration

	// Trace, when non-nil, receives every command before it runs. It backs the
	// --verbose flag so users can see exactly which privileged commands sbctl
	// issues.
	Trace func(name string, args []string)
}

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if r.Trace != nil {
		r.Trace(name, args)
	}

	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	return out, err
}

// Sudo wraps a Runner so that commands are executed through non-interactive
// sudo, translating sudo's refusal into ErrSudoNotConfigured.
//
// -n is essential: it guarantees sbctl never blocks on a hidden password prompt,
// which is what makes it safe to run privileged operations from inside a
// full-screen terminal UI.
type Sudo struct {
	Runner Runner
}

// Run executes name with args under `sudo -n`.
func (s Sudo) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	full := append([]string{"-n", name}, args...)
	out, err := s.Runner.Run(ctx, "sudo", full...)
	if err != nil && IsSudoRefusal(string(out)) {
		return out, sudoError(string(out))
	}
	return out, err
}

// FakeRunner is a Runner for tests. It matches canned responses against the
// command line and records every call so that argv can be asserted.
type FakeRunner struct {
	// Responses maps a "name arg arg" prefix to a canned result. The longest
	// matching prefix wins, so specific cases can override general ones.
	Responses map[string]FakeResult

	// Default is returned when no prefix matches.
	Default FakeResult

	// Calls records every invocation in order as a full argv slice.
	Calls [][]string
}

// FakeResult is a canned command outcome.
type FakeResult struct {
	Output string
	Err    error
}

func (f *FakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	argv := append([]string{name}, args...)
	f.Calls = append(f.Calls, argv)
	line := strings.Join(argv, " ")

	best, bestLen := f.Default, -1
	for prefix, result := range f.Responses {
		if strings.HasPrefix(line, prefix) && len(prefix) > bestLen {
			best, bestLen = result, len(prefix)
		}
	}
	return []byte(best.Output), best.Err
}

// LastCall returns the most recent argv, or nil if nothing has run.
func (f *FakeRunner) LastCall() []string {
	if len(f.Calls) == 0 {
		return nil
	}
	return f.Calls[len(f.Calls)-1]
}

// CallLines renders every recorded call as a space-joined string, for
// assertions and failure messages.
func (f *FakeRunner) CallLines() []string {
	lines := make([]string, 0, len(f.Calls))
	for _, argv := range f.Calls {
		lines = append(lines, strings.Join(argv, " "))
	}
	return lines
}

// ExitError builds an error that reports the given exit status, so fakes can
// reproduce a failing command faithfully.
func ExitError(code int) error { return &fakeExitError{code: code} }

type fakeExitError struct{ code int }

func (e *fakeExitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e *fakeExitError) ExitCode() int { return e.code }
