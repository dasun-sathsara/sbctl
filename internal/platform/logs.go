package platform

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// LogFollower streams the service's log output until the context is cancelled.
type LogFollower interface {
	// Follow writes log lines to out until ctx is done. A context
	// cancellation is a normal termination and must not be reported as an
	// error.
	Follow(ctx context.Context, out io.Writer) error

	// NeedsFile reports the file Follow depends on, and whether that file must
	// already exist for Follow to work.
	//
	// This exists because the previous implementation unconditionally stat'ed
	// the configured error-log path before following logs. On journald systems
	// that file never exists, so `sbctl logs` failed outright even though the
	// follower was going to run `journalctl` and did not need the file at all.
	// Command-based followers return required=false.
	NeedsFile() (path string, required bool)
}

// followingEnded reports whether a failed log command simply stopped rather than
// went wrong.
//
// Following logs is ended by the user pressing Ctrl-C, which cancels the context
// and signals the child. Both a cancelled context and the exit code -1 that a
// signalled process reports are normal terminations, not faults to be surfaced.
func followingEnded(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == -1
}

// CommandFollower streams logs by running an external command such as
// `journalctl -fu sing-box`. It depends on no file.
type CommandFollower struct {
	Name string
	Args []string
}

// NeedsFile reports no file dependency.
func (f CommandFollower) NeedsFile() (string, bool) { return "", false }

func (f CommandFollower) Follow(ctx context.Context, out io.Writer) error {
	cmd := exec.CommandContext(ctx, f.Name, f.Args...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil && !followingEnded(ctx, err) {
		return fmt.Errorf("%s: %w", f.Name, err)
	}
	return nil
}

// TailFollower streams a log file using tail(1), which handles rotation and
// truncation better than a hand-rolled poller on Unix.
type TailFollower struct {
	Path  string
	Lines int
}

// NeedsFile reports the log file as required: tail cannot follow what is not
// there, so the caller can produce a better message than tail's own.
func (f TailFollower) NeedsFile() (string, bool) { return f.Path, true }

func (f TailFollower) Follow(ctx context.Context, out io.Writer) error {
	lines := f.Lines
	if lines <= 0 {
		lines = 80
	}
	cmd := exec.CommandContext(ctx, "tail", "-n", fmt.Sprint(lines), "-f", f.Path)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil && !followingEnded(ctx, err) {
		return fmt.Errorf("tail: %w", err)
	}
	return nil
}

// PollingFollower streams a log file by polling for appended data. It is used
// on Windows, which has no tail(1).
type PollingFollower struct {
	Path         string
	Lines        int
	PollInterval time.Duration
}

// NeedsFile reports the log file as required.
func (f PollingFollower) NeedsFile() (string, bool) { return f.Path, true }

func (f PollingFollower) Follow(ctx context.Context, out io.Writer) error {
	lines := f.Lines
	if lines <= 0 {
		lines = 80
	}
	interval := f.PollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}

	file, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	offset, err := LastLinesOffset(file, lines)
	if err != nil {
		return err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if _, writeErr := io.WriteString(out, line); writeErr != nil {
				return writeErr
			}
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
			reader.Reset(file)
		}
	}
}

// LastLinesOffset returns the byte offset at which the final n lines of file
// begin, scanning backwards so that large logs are not read in full.
func LastLinesOffset(file *os.File, lines int) (int64, error) {
	if lines <= 0 {
		return 0, nil
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}

	var count int
	buf := make([]byte, 4096)
	for pos := size; pos > 0; {
		readSize := int64(len(buf))
		if pos < readSize {
			readSize = pos
		}
		pos -= readSize
		if _, err := file.ReadAt(buf[:readSize], pos); err != nil {
			return 0, err
		}
		for i := readSize - 1; i >= 0; i-- {
			if buf[i] != '\n' {
				continue
			}
			count++
			if count > lines {
				return pos + i + 1, nil
			}
		}
	}
	return 0, nil
}

// Follower returns the log follower appropriate for this layout.
func (l Layout) Follower() LogFollower {
	switch l.OS {
	case "linux":
		// journald owns the logs; the configured error-log path is unused.
		return CommandFollower{Name: "journalctl", Args: []string{"-fu", l.ServiceName}}
	case "windows":
		return PollingFollower{Path: l.ErrorLogPath, Lines: 80}
	default:
		return TailFollower{Path: l.ErrorLogPath, Lines: 80}
	}
}
