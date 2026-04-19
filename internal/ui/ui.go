package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"sbctl/internal/daemon"
	"sbctl/internal/profile"
)

var (
	ErrCancelled = errors.New("cancelled")

	// Palette
	cAccent = lipgloss.Color("13") // bright magenta
	cGood   = lipgloss.Color("10") // bright green
	cWarn   = lipgloss.Color("11") // bright yellow
	cBad    = lipgloss.Color("9")  // bright red
	cDim    = lipgloss.Color("8")  // dim gray
	cFg     = lipgloss.Color("15") // bright white
	cInfo   = lipgloss.Color("12") // bright blue

	titleStyle  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	subStyle    = lipgloss.NewStyle().Foreground(cDim).Italic(true)
	runStyle    = lipgloss.NewStyle().Foreground(cGood).Bold(true)
	stopStyle   = lipgloss.NewStyle().Foreground(cWarn).Bold(true)
	errStyle    = lipgloss.NewStyle().Foreground(cBad).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(cDim)
	labelStyle  = lipgloss.NewStyle().Foreground(cDim)
	valueStyle  = lipgloss.NewStyle().Foreground(cFg)
	activeStyle = lipgloss.NewStyle().Foreground(cGood).Bold(true)
	cursorStyle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	offStyle    = lipgloss.NewStyle().Foreground(cBad)
	keyStyle    = lipgloss.NewStyle().Foreground(cInfo).Bold(true)
	hintStyle   = lipgloss.NewStyle().Foreground(cDim)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cAccent).
			Padding(0, 2)
)

const TurnOffChoice = "__turn_off__"

const (
	EditChoiceReedit     = "reedit"
	EditChoiceRevert     = "revert"
	EditChoiceKeepBroken = "keep_broken"
)

// RenderProfileList is used by the non-interactive `sbctl list` command.
func RenderProfileList(profiles []profile.Profile, active string) string {
	if len(profiles) == 0 {
		return dimStyle.Render("(no profiles)") + "\n"
	}
	var b strings.Builder
	for _, p := range profiles {
		if p.Name == active {
			fmt.Fprintf(&b, "%s %s  %s\n",
				runStyle.Render("●"),
				activeStyle.Render(p.Name),
				dimStyle.Render("(active)"),
			)
		} else {
			fmt.Fprintf(&b, "%s %s\n",
				dimStyle.Render("○"),
				valueStyle.Render(p.Name),
			)
		}
	}
	return b.String()
}

// RenderStatus renders the bordered status panel used by `sbctl status` and
// embedded at the top of the interactive picker.
func RenderStatus(state daemon.RunState, active, tun string) string {
	return panelStyle.Render(statusBody(state, active, tun))
}

func statusBody(state daemon.RunState, active, tun string) string {
	stateText, style := stateVisual(state)

	header := titleStyle.Render("sbctl") + "  " + subStyle.Render("sing-box controller")

	rows := []string{
		header,
		"",
		fmt.Sprintf("%s  %s", labelStyle.Render("state  "), style.Render(stateText)),
	}
	if active == "" {
		rows = append(rows, fmt.Sprintf("%s  %s",
			labelStyle.Render("profile"),
			dimStyle.Render("—"),
		))
	} else {
		rows = append(rows, fmt.Sprintf("%s  %s",
			labelStyle.Render("profile"),
			valueStyle.Render(active),
		))
	}
	if state == daemon.StateRunning && tun != "" {
		rows = append(rows, fmt.Sprintf("%s  %s",
			labelStyle.Render("tun    "),
			valueStyle.Render(tun),
		))
	}
	return strings.Join(rows, "\n")
}

func stateVisual(state daemon.RunState) (string, lipgloss.Style) {
	switch state {
	case daemon.StateRunning:
		return "● running", runStyle
	case daemon.StateError:
		return "✗ error", errStyle
	default:
		return "○ stopped", stopStyle
	}
}

// ---------------------------------------------------------------------------
// Interactive picker (bubbletea)
// ---------------------------------------------------------------------------

type pickerModel struct {
	profiles []profile.Profile
	active   string
	state    daemon.RunState
	tun      string
	cursor   int
	choice   string
	quitted  bool
	done     bool
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	total := len(m.profiles) + 1 // profiles + turn-off

	switch km.String() {
	case "ctrl+c", "esc", "q":
		m.quitted = true
		m.done = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor == 0 {
			m.cursor = total - 1
		} else {
			m.cursor--
		}
	case "down", "j", "tab":
		if m.cursor == total-1 {
			m.cursor = 0
		} else {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = total - 1
	case "enter", " ":
		if m.cursor == len(m.profiles) {
			m.choice = TurnOffChoice
		} else {
			m.choice = m.profiles[m.cursor].Name
		}
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m pickerModel) View() string {
	// On quit, collapse the UI so subsequent CLI output isn't crowded.
	if m.done {
		return ""
	}

	var b strings.Builder
	b.WriteString(RenderStatus(m.state, m.active, m.tun))
	b.WriteString("\n\n")
	b.WriteString(subStyle.Render("  Select a profile"))
	b.WriteString("\n\n")

	for i, p := range m.profiles {
		b.WriteString(renderProfileRow(i == m.cursor, p.Name == m.active, p.Name))
	}

	// Spacer and separator before the destructive action.
	b.WriteString("\n")
	b.WriteString(renderOffRow(m.cursor == len(m.profiles)))

	b.WriteString("\n")
	b.WriteString(renderHelp())
	b.WriteString("\n")
	return b.String()
}

func renderProfileRow(isCursor, isActive bool, name string) string {
	pointer := "  "
	if isCursor {
		pointer = cursorStyle.Render("▸ ")
	}

	var marker string
	if isActive {
		marker = runStyle.Render("●")
	} else {
		marker = dimStyle.Render("○")
	}

	nameStyle := valueStyle
	if isActive {
		nameStyle = activeStyle
	}
	if isCursor {
		nameStyle = nameStyle.Underline(true)
	}

	suffix := ""
	if isActive {
		suffix = "  " + dimStyle.Render("(active)")
	}

	return fmt.Sprintf("%s%s  %s%s\n", pointer, marker, nameStyle.Render(name), suffix)
}

func renderOffRow(isCursor bool) string {
	pointer := "  "
	if isCursor {
		pointer = cursorStyle.Render("▸ ")
	}
	label := offStyle.Render("Turn off")
	if isCursor {
		label = offStyle.Underline(true).Bold(true).Render("Turn off")
	}
	return fmt.Sprintf("%s%s  %s\n", pointer, errStyle.Render("✗"), label)
}

func renderHelp() string {
	parts := []string{
		keyStyle.Render("↑/↓") + " " + hintStyle.Render("navigate"),
		keyStyle.Render("enter") + " " + hintStyle.Render("select"),
		keyStyle.Render("q") + " " + hintStyle.Render("quit"),
	}
	return hintStyle.Render("  ") + strings.Join(parts, hintStyle.Render("   ·   "))
}

// PickProfile renders a single-screen, in-place picker with status header,
// profile list, and turn-off action. No separator option (that was eating
// the cursor position), no huh viewport (that was hiding rows).
func PickProfile(profiles []profile.Profile, active string, state daemon.RunState, tun string) (string, error) {
	// Default cursor to the currently active profile if present.
	initial := 0
	for i, p := range profiles {
		if p.Name == active {
			initial = i
			break
		}
	}
	m := pickerModel{
		profiles: profiles,
		active:   active,
		state:    state,
		tun:      tun,
		cursor:   initial,
	}
	prog := tea.NewProgram(m)
	final, err := prog.Run()
	if err != nil {
		return "", err
	}
	fm, ok := final.(pickerModel)
	if !ok {
		return "", fmt.Errorf("unexpected final model %T", final)
	}
	if fm.quitted {
		return "", ErrCancelled
	}
	return fm.choice, nil
}

// ---------------------------------------------------------------------------
// Single-shot prompts (still use huh; they work fine inline)
// ---------------------------------------------------------------------------

func ConfirmDelete(name string) (bool, error) {
	var ok bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete profile %q?", name)).
				Description(dimStyle.Render("This cannot be undone.")).
				Affirmative("Delete").
				Negative("Cancel").
				Value(&ok),
		),
	).WithTheme(huh.ThemeCatppuccin())
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, ErrCancelled
		}
		return false, err
	}
	return ok, nil
}

func ResolveValidationFailure() (string, error) {
	var choice string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Validation failed").
				Description(dimStyle.Render("What should happen next?")).
				Options(
					huh.NewOption("Re-edit", EditChoiceReedit),
					huh.NewOption("Revert to previous contents", EditChoiceRevert),
					huh.NewOption("Keep broken file", EditChoiceKeepBroken),
				).
				Value(&choice),
		),
	).WithTheme(huh.ThemeCatppuccin())
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", ErrCancelled
		}
		return "", err
	}
	return choice, nil
}
