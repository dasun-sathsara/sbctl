package platform

import "strings"

// Elevator abstracts privilege escalation so command handlers can be tested
// without a real UAC prompt.
//
// On Unix, escalation is per-operation via sudo inside the service and
// activator layers, so IsElevated always reports true and RunElevated is never
// reached. On Windows the whole process must be re-launched elevated.
type Elevator interface {
	// IsElevated reports whether the current process can perform privileged
	// operations directly.
	IsElevated() bool

	// RunElevated re-launches sbctl with args under elevation and returns the
	// child's exit code. The child writes its own output to its own console;
	// callers must not duplicate success messages.
	RunElevated(args []string) (exitCode int, err error)
}

// QuoteArg escapes one argument for a Windows command line so that
// CommandLineToArgvW reconstructs it exactly.
//
// The previous implementation joined arguments with plain spaces, so any
// profile name containing a space was silently split into two arguments, and a
// crafted name could inject additional ones. The rules implemented here are
// the documented inverse of CommandLineToArgvW: backslashes are only special
// when they immediately precede a quote, so runs of them must be doubled in
// that position and left alone everywhere else.
//
// It lives in a platform-neutral file so it is unit-testable on any host.
func QuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\v\"") {
		return arg
	}

	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for i := 0; i < len(arg); i++ {
		switch c := arg[i]; c {
		case '\\':
			backslashes++
		case '"':
			// Double the pending backslashes, then escape the quote itself.
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			backslashes = 0
			b.WriteByte('"')
		default:
			b.WriteString(strings.Repeat(`\`, backslashes))
			backslashes = 0
			b.WriteByte(c)
		}
	}
	// Trailing backslashes precede the closing quote, so they must be doubled.
	b.WriteString(strings.Repeat(`\`, backslashes*2))
	b.WriteByte('"')
	return b.String()
}

// QuoteArgs joins args into a single Windows command-line string.
func QuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = QuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}
