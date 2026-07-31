package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// DefaultEditor opens path in the user's editor.
//
// $EDITOR may legitimately contain arguments ("code --wait"), so it is split on
// whitespace rather than treated as a bare executable name. It is deliberately
// not passed through a shell: doing so would make the value of an environment
// variable executable as a command line.
func DefaultEditor(ctx context.Context, path string) error {
	parts := editorCommand()
	if len(parts) == 0 {
		return fmt.Errorf("no editor is configured")
	}

	cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", parts[0], err)
	}
	return nil
}

// editorCommand resolves the editor to use, preferring $VISUAL then $EDITOR,
// then the first platform default that is actually installed.
//
// The previous implementation defaulted to nvim unconditionally, which failed
// with a confusing error on any machine that did not happen to have it.
func editorCommand() []string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(env)); value != "" {
			return strings.Fields(value)
		}
	}
	for _, candidate := range defaultEditors() {
		if _, err := exec.LookPath(candidate); err == nil {
			return []string{candidate}
		}
	}
	return nil
}

func defaultEditors() []string {
	if runtime.GOOS == "windows" {
		return []string{"notepad.exe"}
	}
	return []string{"nvim", "vim", "nano", "vi"}
}
