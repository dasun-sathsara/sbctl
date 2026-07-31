package cli

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"sbctl/internal/platform"
)

// NewNotifier returns the desktop notifier for a platform, or a no-op where
// there is no established mechanism.
func NewNotifier(layout platform.Layout) Notifier {
	if layout.OS == "darwin" {
		return AppleScriptNotifier{}
	}
	return NoopNotifier{}
}

// NoopNotifier discards notifications.
type NoopNotifier struct{}

func (NoopNotifier) Notify(string) {}

// AppleScriptNotifier posts a macOS notification via osascript.
type AppleScriptNotifier struct{}

// Notify posts message. Failures are ignored: a missing notification is not
// worth failing a successful profile switch over.
func (AppleScriptNotifier) Notify(message string) {
	script := fmt.Sprintf(`display notification "%s" with title "sing-box"`, AppleScriptSafe(message))
	_ = exec.Command("osascript", "-e", script).Run()
}

// nonPrintableASCII matches anything outside the printable ASCII range.
var nonPrintableASCII = regexp.MustCompile(`[^\x20-\x7E]+`)

// AppleScriptSafe reduces message to text that can be embedded in an AppleScript
// string literal.
//
// The message is interpolated into a script that osascript executes, so quotes
// must not survive: a profile name containing one would otherwise terminate the
// string literal early and let the remainder be interpreted as AppleScript.
func AppleScriptSafe(message string) string {
	safe := nonPrintableASCII.ReplaceAllString(message, " ")
	safe = strings.ReplaceAll(safe, `\`, "")
	safe = strings.ReplaceAll(safe, `"`, "'")
	return strings.Join(strings.Fields(safe), " ")
}
