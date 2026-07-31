package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MaxNameWidth bounds how much horizontal space a profile name may claim before
// it is truncated, so one long name cannot destroy the alignment of a list.
const MaxNameWidth = 40

// KV is one label/value row.
type KV struct {
	Label string
	Value string
	// Raw marks a value that is already styled and must pass through untouched,
	// such as a state badge that carries its own semantic colour.
	Raw bool
}

// Panel wraps body in a titled border.
//
// In plain mode, and on terminals too narrow for borders to survive wrapping, it
// degrades to an indented block with a heading rather than emitting broken box
// characters.
func (t Theme) Panel(title, body string) string {
	if t.Plain || t.Symbols.BorderStyle == (lipgloss.Border{}) {
		var b strings.Builder
		if title != "" {
			b.WriteString(t.TitleStyle().Render(title))
			b.WriteString("\n")
		}
		b.WriteString(body)
		return b.String()
	}

	style := lipgloss.NewStyle().
		Border(t.Symbols.BorderStyle).
		BorderForeground(t.Accent).
		Padding(0, 2)
	if title != "" {
		body = t.TitleStyle().Render(title) + "\n" + body
	}
	return style.Render(body)
}

// KVBlock renders rows with labels padded to a common width, so that status, ip
// and doctor output all align identically instead of each inventing its own
// spacing.
func (t Theme) KVBlock(rows []KV) string {
	width := 0
	for _, row := range rows {
		if n := lipgloss.Width(row.Label); n > width {
			width = n
		}
	}

	var b strings.Builder
	for _, row := range rows {
		label := row.Label + strings.Repeat(" ", width-lipgloss.Width(row.Label))
		value := row.Value
		switch {
		case value == "" && t.Plain:
			value = t.MutedStyle().Render("none")
		case value == "":
			value = t.MutedStyle().Render("—")
		case row.Raw:
			// Already styled; pass through.
		default:
			value = t.ValueStyle().Render(value)
		}
		fmt.Fprintf(&b, "%s  %s\n", t.LabelStyle().Render(label), value)
	}
	return b.String()
}

// Okf renders a confirmation line.
func (t Theme) Okf(format string, args ...any) string {
	return t.OkStyle().Render(t.Symbols.Success) + " " + fmt.Sprintf(format, args...)
}

// Failf renders an error line.
func (t Theme) Failf(format string, args ...any) string {
	return t.DangerStyle().Render(t.Symbols.Failure) + " " + fmt.Sprintf(format, args...)
}

// Warnf renders a caution line.
func (t Theme) Warnf(format string, args ...any) string {
	return t.WarnStyle().Render(t.Symbols.Warning) + " " + fmt.Sprintf(format, args...)
}

// Hintf renders one actionable suggestion beneath a message.
//
// Every user-facing failure is paired with a hint. An error that only says what
// went wrong leaves the user to guess the remedy, which for a tool whose common
// failures are "sudo is not configured" and "the profile still has placeholders"
// is the difference between a dead end and a fix.
func (t Theme) Hintf(format string, args ...any) string {
	return "  " + t.MutedStyle().Render(t.Symbols.ArrowHint+" "+fmt.Sprintf(format, args...))
}

// StateBadge renders a run state as a glyph plus a word, never colour alone.
func (t Theme) StateBadge(state string) string {
	switch state {
	case "running":
		return t.OkStyle().Render(t.Symbols.Active + " running")
	case "error":
		return t.DangerStyle().Render(t.Symbols.Failure + " error")
	case "unknown":
		return t.MutedStyle().Render(t.Symbols.Inactive + " unknown")
	default:
		return t.WarnStyle().Render(t.Symbols.Inactive + " stopped")
	}
}

// KeyHints renders a footer keymap such as "↑/↓ move · enter select".
func (t Theme) KeyHints(pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, t.KeyStyle().Render(pair[0])+" "+t.MutedStyle().Render(pair[1]))
	}
	separator := t.MutedStyle().Render("  " + t.Symbols.Bullet + "  ")
	return strings.Join(parts, separator)
}

// Truncate shortens s to at most width display cells, marking the cut.
func Truncate(s string, width int) string {
	if width <= 1 || lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// TruncateName shortens a profile name for display, using an ASCII marker in
// plain mode so the output stays byte-predictable.
func (t Theme) TruncateName(name string) string {
	if lipgloss.Width(name) <= MaxNameWidth {
		return name
	}
	if t.Plain {
		runes := []rune(name)
		return string(runes[:MaxNameWidth-3]) + "..."
	}
	return Truncate(name, MaxNameWidth)
}
