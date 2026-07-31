package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"sbctl/internal/profile"
	"sbctl/internal/service"
)

// StatusView is everything the status surface displays. Commands assemble it;
// rendering never reaches back into the system, which is what makes the visual
// layer testable without a service or a terminal.
type StatusView struct {
	// State is the observed run state.
	State service.RunState

	// Profile is the profile the service is configured to use, whether or not
	// the service is currently running.
	Profile string

	// Running reports whether Profile is actually in service. The distinction
	// matters: previously the profile name was blanked whenever the service was
	// stopped, so a user who had configured a profile and stopped the service
	// could not see which profile would come back.
	Running bool

	// Tun is the configured TUN interface name, empty when sing-box chooses.
	Tun string

	// Server summarises the active profile's outbound endpoint.
	Server string

	// PID is the service process id when known.
	PID int

	// Placeholders lists unreplaced markers in the active profile.
	Placeholders []string

	// Broken describes why the active config is unusable, if it is.
	Broken string
}

// RenderStatus renders the status panel shared by `sbctl status` and the picker
// header.
func (t Theme) RenderStatus(view StatusView) string {
	rows := []KV{{Label: "state", Value: t.StateBadge(string(view.State)), Raw: true}}

	profileValue := ""
	switch {
	case view.Profile == "":
	case view.Running:
		profileValue = t.ValueStyle().Render(t.TruncateName(view.Profile))
	default:
		// Say so explicitly rather than showing nothing.
		profileValue = t.ValueStyle().Render(t.TruncateName(view.Profile)) +
			t.MutedStyle().Render("  (configured, not running)")
	}
	rows = append(rows, KV{Label: "profile", Value: profileValue, Raw: true})

	if view.Server != "" {
		rows = append(rows, KV{Label: "server", Value: view.Server})
	}
	if view.Tun != "" {
		rows = append(rows, KV{Label: "tun", Value: view.Tun})
	}
	if view.PID > 0 && view.State == service.StateRunning {
		rows = append(rows, KV{Label: "pid", Value: fmt.Sprint(view.PID)})
	}

	body := strings.TrimRight(t.KVBlock(rows), "\n")

	if len(view.Placeholders) > 0 {
		body += "\n" + t.Warnf("active profile still has placeholders: %s", strings.Join(view.Placeholders, ", "))
	}
	if view.Broken != "" {
		body += "\n" + t.Failf("%s", view.Broken)
	}

	return t.Panel("sbctl", body)
}

// RenderProfileList renders `sbctl list`.
//
// Each row carries a glyph, the name, and a status word, so the output is
// unambiguous in monochrome and remains greppable.
func (t Theme) RenderProfileList(profiles []profile.Profile, active string, running bool) string {
	if len(profiles) == 0 {
		return t.MutedStyle().Render("no profiles yet") + "\n"
	}

	type row struct {
		glyph string
		name  string
		note  string
		style lipgloss.Style
	}

	rows := make([]row, 0, len(profiles))
	// Column positions are derived from the content rather than a fixed width,
	// so short lists stay compact and the plain-mode badges — which are several
	// characters wide rather than one glyph — still line up.
	glyphWidth, nameWidth := 0, 0

	for _, p := range profiles {
		r := row{glyph: t.Symbols.Inactive, name: t.TruncateName(p.Name), style: t.ValueStyle()}
		switch {
		case !p.Ready():
			r.glyph, r.style, r.note = t.Symbols.Warning, t.WarnStyle(), "placeholders"
		case p.Name == active && running:
			r.glyph, r.style, r.note = t.Symbols.Active, t.ActiveStyle(), "active"
		case p.Name == active:
			r.glyph, r.style, r.note = t.Symbols.Active, t.MutedStyle(), "configured, not running"
		}
		if w := lipgloss.Width(r.glyph); w > glyphWidth {
			glyphWidth = w
		}
		if w := lipgloss.Width(r.name); w > nameWidth {
			nameWidth = w
		}
		rows = append(rows, r)
	}

	var b strings.Builder
	for _, r := range rows {
		glyph := r.glyph + strings.Repeat(" ", glyphWidth-lipgloss.Width(r.glyph))
		line := r.style.Render(glyph) + " " + r.style.Render(r.name)
		if r.note != "" {
			line += strings.Repeat(" ", nameWidth-lipgloss.Width(r.name)+2) + t.MutedStyle().Render(r.note)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// Check is one diagnostic result from `sbctl doctor`.
type Check struct {
	Name   string
	OK     bool
	Detail string
	Hint   string
	// Warn marks a finding that is worth reporting but is not a failure.
	Warn bool
}

// RenderChecks renders the doctor report.
func (t Theme) RenderChecks(checks []Check) string {
	width := 0
	for _, c := range checks {
		if len(c.Name) > width {
			width = len(c.Name)
		}
	}

	var b strings.Builder
	for _, c := range checks {
		var glyph string
		// Warn is checked before OK: a passing-but-noteworthy finding is still a
		// warning, and rendering it as a plain tick would hide it.
		switch {
		case c.Warn:
			glyph = t.WarnStyle().Render(t.Symbols.Warning)
		case c.OK:
			glyph = t.OkStyle().Render(t.Symbols.Success)
		default:
			glyph = t.DangerStyle().Render(t.Symbols.Failure)
		}
		name := c.Name + strings.Repeat(" ", width-len(c.Name))
		fmt.Fprintf(&b, "%s %s  %s\n", glyph, t.LabelStyle().Render(name), t.ValueStyle().Render(c.Detail))
		if c.Hint != "" && !c.OK {
			b.WriteString(t.Hintf("%s", c.Hint) + "\n")
		}
	}
	return b.String()
}
