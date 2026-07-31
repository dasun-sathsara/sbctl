package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"sbctl/internal/profile"
	"sbctl/internal/service"
)

// ErrCancelled reports that the user dismissed an interactive surface.
// Cancelling is a normal outcome, not a failure, and callers map it to a
// successful exit.
var ErrCancelled = errors.New("cancelled")

// TurnOffChoice is the sentinel returned when the user selects "turn off".
const TurnOffChoice = "\x00turn-off"

// filterThreshold is the profile count at which a filter becomes worth its
// screen space. Below it, a search box is pure noise on a list the user can read
// at a glance.
const filterThreshold = 8

// minVisibleRows keeps the list usable even in a very short terminal.
const minVisibleRows = 3

// ActivateFunc performs the work the picker triggers. It runs on a background
// goroutine so the spinner keeps animating while a multi-second service restart
// is in flight; running it inline would freeze the whole UI.
type ActivateFunc func(choice string) error

// PickerResult reports what the picker did.
type PickerResult struct {
	// Choice is the selected profile name, or TurnOffChoice.
	Choice string
	// Err is the error returned by the ActivateFunc, if it ran.
	Err error
	// Acted reports whether ActivateFunc was invoked.
	Acted bool
}

type pickerModel struct {
	theme    Theme
	status   StatusView
	profiles []profile.Profile
	activate ActivateFunc

	// visible is the filtered index into profiles.
	visible []int
	cursor  int
	offset  int
	height  int

	filter    textinput.Model
	filtering bool

	spinner spinner.Model
	working bool

	// helpExpanded shows the full keymap rather than the compact footer.
	helpExpanded bool

	choice    string
	actionErr error
	acted     bool
	cancelled bool
	finished  bool
}

type activationDoneMsg struct{ err error }

// NewPicker builds the interactive profile picker.
func NewPicker(theme Theme, status StatusView, profiles []profile.Profile, activate ActivateFunc) tea.Model {
	filter := textinput.New()
	filter.Prompt = "filter: "
	filter.CharLimit = 64

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	if theme.Plain {
		spin.Spinner = spinner.Line
	}
	spin.Style = theme.CursorStyle()

	m := pickerModel{
		theme:    theme,
		status:   status,
		profiles: profiles,
		activate: activate,
		filter:   filter,
		spinner:  spin,
		height:   0,
	}
	m.applyFilter()

	// Start on the profile already in use, so the common "check what is running"
	// case needs no navigation.
	for i, idx := range m.visible {
		if profiles[idx].Name == status.Profile {
			m.cursor = i
			break
		}
	}
	return m
}

// canFilter reports whether the filter affordance is offered at all.
func (m pickerModel) canFilter() bool { return len(m.profiles) >= filterThreshold }

// rowCount is the number of selectable rows, including "turn off" when shown.
func (m pickerModel) rowCount() int {
	n := len(m.visible)
	if m.showTurnOff() {
		n++
	}
	return n
}

// showTurnOff hides the stop action when there is nothing running to stop.
func (m pickerModel) showTurnOff() bool { return m.status.State == service.StateRunning }

// isTurnOffRow reports whether the cursor sits on the turn-off action.
func (m pickerModel) isTurnOffRow() bool {
	return m.showTurnOff() && m.cursor == len(m.visible)
}

func (m *pickerModel) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.visible = m.visible[:0]
	for i, p := range m.profiles {
		if query == "" || strings.Contains(strings.ToLower(p.Name), query) {
			m.visible = append(m.visible, i)
		}
	}
	m.clampCursor()
}

// clampCursor keeps the cursor inside the selectable range. The cursor is
// clamped rather than wrapped: wrapping made it possible to land on an index
// derived from an empty list, and jumping from the last row to the first is
// rarely what someone reaching for "down" intended.
func (m *pickerModel) clampCursor() {
	max := m.rowCount() - 1
	if max < 0 {
		m.cursor = 0
		m.offset = 0
		return
	}
	if m.cursor > max {
		m.cursor = max
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.scrollIntoView()
}

// scrollIntoView adjusts the viewport window so the cursor stays visible. Long
// profile lists previously ran off the bottom of the terminal with no way to
// reach the hidden entries.
func (m *pickerModel) scrollIntoView() {
	rows := m.visibleRows()
	if rows <= 0 || len(m.visible) <= rows {
		m.offset = 0
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if maxOffset := len(m.visible) - rows; m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// visibleRows is how many profile rows fit, reserving space for the status
// panel, the action row and the footer.
func (m pickerModel) visibleRows() int {
	if m.height <= 0 {
		return len(m.visible)
	}
	const chrome = 12
	rows := m.height - chrome
	if rows < minVisibleRows {
		rows = minVisibleRows
	}
	if rows > len(m.visible) {
		rows = len(m.visible)
	}
	return rows
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.clampCursor()
		return m, nil

	case spinner.TickMsg:
		if !m.working {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case activationDoneMsg:
		m.working = false
		m.acted = true
		m.actionErr = msg.err
		m.finished = true
		return m, tea.Quit

	case tea.KeyMsg:
		if m.working {
			// Ignore input while an activation is in flight; the operation is
			// already committed and cannot be usefully interrupted here.
			return m, nil
		}
		if m.filtering {
			return m.updateFiltering(msg)
		}
		return m.updateBrowsing(msg)
	}
	return m, nil
}

func (m pickerModel) updateFiltering(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter.SetValue("")
		m.filter.Blur()
		m.applyFilter()
		return m, nil
	case "enter":
		m.filtering = false
		m.filter.Blur()
		return m, nil
	case "ctrl+c":
		m.cancelled = true
		m.finished = true
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.applyFilter()
	return m, cmd
}

func (m pickerModel) updateBrowsing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.cancelled = true
		m.finished = true
		return m, tea.Quit

	case "up", "k":
		m.cursor--
		m.clampCursor()

	case "down", "j", "tab":
		m.cursor++
		m.clampCursor()

	case "home", "g":
		m.cursor = 0
		m.clampCursor()

	case "end", "G":
		m.cursor = m.rowCount() - 1
		m.clampCursor()

	case "/":
		if m.canFilter() {
			m.filtering = true
			m.filter.Focus()
		}

	case "?":
		// The footer lists only the common keys so it stays one line. Keys that
		// are discoverable but rarely needed live behind this toggle rather than
		// being invisible.
		m.helpExpanded = !m.helpExpanded

	case "enter", " ":
		return m.commit()
	}
	return m, nil
}

func (m pickerModel) commit() (tea.Model, tea.Cmd) {
	if m.rowCount() == 0 {
		return m, nil
	}
	if m.isTurnOffRow() {
		m.choice = TurnOffChoice
	} else {
		if m.cursor >= len(m.visible) {
			return m, nil
		}
		selected := m.profiles[m.visible[m.cursor]]
		if !selected.Ready() {
			// Refuse rather than fail deep inside activation, and say what to do.
			m.actionErr = fmt.Errorf("%s still has placeholder values (%s); edit it with: sbctl edit %s",
				selected.Name, strings.Join(selected.Placeholders, ", "), selected.Name)
			m.finished = true
			return m, tea.Quit
		}
		m.choice = selected.Name
	}

	if m.activate == nil {
		m.finished = true
		return m, tea.Quit
	}

	m.working = true
	choice := m.choice
	activate := m.activate
	// The work runs in a tea.Cmd so the update loop stays free to service
	// spinner ticks for the duration of the restart.
	return m, tea.Batch(
		m.spinner.Tick,
		func() tea.Msg { return activationDoneMsg{err: activate(choice)} },
	)
}

func (m pickerModel) View() string {
	// Collapse on exit so the command's own output is not competing with a
	// stale menu. Rendering stays inline rather than using the alternate screen
	// so the result remains in scrollback afterwards.
	if m.finished {
		return ""
	}

	t := m.theme
	var b strings.Builder

	status := m.status
	if m.working {
		b.WriteString(t.Panel("sbctl", strings.TrimRight(t.KVBlock([]KV{
			{Label: "state", Value: m.spinner.View() + " " + t.MutedStyle().Render(m.workingVerb()), Raw: true},
			{Label: "profile", Value: t.ValueStyle().Render(t.TruncateName(m.targetLabel())), Raw: true},
		}), "\n")))
		b.WriteString("\n")
		return b.String()
	}

	b.WriteString(t.RenderStatus(status))
	b.WriteString("\n\n")

	if m.canFilter() {
		if m.filtering {
			b.WriteString("  " + m.filter.View() + "\n\n")
		} else if v := m.filter.Value(); v != "" {
			b.WriteString("  " + t.MutedStyle().Render("filter: "+v) + "\n\n")
		}
	}

	if len(m.visible) == 0 {
		b.WriteString("  " + t.MutedStyle().Render("no profiles match") + "\n")
	}

	rows := m.visibleRows()
	end := m.offset + rows
	if end > len(m.visible) {
		end = len(m.visible)
	}
	if m.offset > 0 {
		b.WriteString("  " + t.MutedStyle().Render(fmt.Sprintf("… %d above", m.offset)) + "\n")
	}
	for i := m.offset; i < end; i++ {
		b.WriteString(m.renderRow(i))
	}
	if end < len(m.visible) {
		b.WriteString("  " + t.MutedStyle().Render(fmt.Sprintf("… %d below", len(m.visible)-end)) + "\n")
	}

	if m.showTurnOff() {
		b.WriteString("\n")
		b.WriteString(m.renderTurnOff())
	}

	b.WriteString("\n")
	if m.helpExpanded {
		b.WriteString(m.expandedHelp())
	} else {
		b.WriteString("  " + m.helpLine() + "\n")
	}
	return b.String()
}

// expandedHelp lists every binding, including the ones the compact footer omits.
func (m pickerModel) expandedHelp() string {
	t := m.theme
	rows := [][2]string{
		{"↑/k, ↓/j", "move the cursor"},
		{"g/home, G/end", "jump to first or last"},
		{"enter, space", "activate the selection"},
	}
	if m.canFilter() {
		rows = append(rows, [2]string{"/", "filter by name"})
	}
	rows = append(rows,
		[2]string{"?", "hide this help"},
		[2]string{"q, esc", "quit without changing anything"},
	)
	if t.Plain {
		rows[0] = [2]string{"up/k, down/j", "move the cursor"}
	}

	width := 0
	for _, row := range rows {
		if w := lipgloss.Width(row[0]); w > width {
			width = w
		}
	}
	var b strings.Builder
	for _, row := range rows {
		key := row[0] + strings.Repeat(" ", width-lipgloss.Width(row[0]))
		b.WriteString("  " + t.KeyStyle().Render(key) + "  " + t.MutedStyle().Render(row[1]) + "\n")
	}
	return b.String()
}

func (m pickerModel) workingVerb() string {
	if m.choice == TurnOffChoice {
		return "stopping…"
	}
	return "activating…"
}

func (m pickerModel) targetLabel() string {
	if m.choice == TurnOffChoice {
		return m.status.Profile
	}
	return m.choice
}

func (m pickerModel) renderRow(i int) string {
	t := m.theme
	p := m.profiles[m.visible[i]]
	isCursor := i == m.cursor
	isActive := p.Name == m.status.Profile && m.status.State == service.StateRunning

	pointer := strings.Repeat(" ", lipgloss.Width(t.Symbols.Cursor)+1)
	if isCursor {
		pointer = t.CursorStyle().Render(t.Symbols.Cursor) + " "
	}

	glyph, style, note := t.Symbols.Inactive, t.ValueStyle(), ""
	switch {
	case !p.Ready():
		glyph, style, note = t.Symbols.Warning, t.WarnStyle(), "placeholders"
	case isActive:
		glyph, style, note = t.Symbols.Active, t.ActiveStyle(), "active"
	case p.Name == m.status.Profile:
		glyph, style, note = t.Symbols.Active, t.MutedStyle(), "configured"
	}

	name := t.TruncateName(p.Name)
	if isCursor {
		style = style.Underline(true)
	}
	// Pad from the widest glyph and name actually present so the note column
	// lines up, including in plain mode where the badges are multi-character.
	glyph += strings.Repeat(" ", m.glyphWidth()-lipgloss.Width(glyph))
	line := pointer + style.Render(glyph) + " " + style.Render(name)
	if note != "" {
		line += strings.Repeat(" ", m.nameWidth()-lipgloss.Width(name)+2) + t.MutedStyle().Render(note)
	}
	return line + "\n"
}

// glyphWidth is the widest state glyph in use, which differs between the Unicode
// and plain symbol sets.
func (m pickerModel) glyphWidth() int {
	widest := 0
	for _, glyph := range []string{m.theme.Symbols.Active, m.theme.Symbols.Inactive, m.theme.Symbols.Warning} {
		if w := lipgloss.Width(glyph); w > widest {
			widest = w
		}
	}
	return widest
}

// nameWidth is the widest displayed profile name among the visible rows.
func (m pickerModel) nameWidth() int {
	widest := 0
	for _, index := range m.visible {
		if w := lipgloss.Width(m.theme.TruncateName(m.profiles[index].Name)); w > widest {
			widest = w
		}
	}
	return widest
}

func (m pickerModel) renderTurnOff() string {
	t := m.theme
	pointer := strings.Repeat(" ", lipgloss.Width(t.Symbols.Cursor)+1)
	style := t.DangerStyle()
	if m.isTurnOffRow() {
		pointer = t.CursorStyle().Render(t.Symbols.Cursor) + " "
		style = style.Underline(true).Bold(true)
	}
	return fmt.Sprintf("%s%s %s\n", pointer, style.Render(t.Symbols.Failure), style.Render("Turn off"))
}

func (m pickerModel) helpLine() string {
	hints := [][2]string{
		{"↑/↓", "move"},
		{"enter", "select"},
	}
	if m.theme.Plain {
		hints[0] = [2]string{"up/down", "move"}
	}
	if m.canFilter() {
		hints = append(hints, [2]string{"/", "filter"})
	}
	hints = append(hints, [2]string{"?", "keys"}, [2]string{"q", "quit"})
	return m.theme.KeyHints(hints...)
}

// RunPicker runs the picker and reports the outcome.
//
// A cancelled picker returns ErrCancelled, which callers translate into a
// successful exit.
func RunPicker(theme Theme, status StatusView, profiles []profile.Profile, activate ActivateFunc) (PickerResult, error) {
	final, err := tea.NewProgram(NewPicker(theme, status, profiles, activate)).Run()
	if err != nil {
		return PickerResult{}, err
	}
	model, ok := final.(pickerModel)
	if !ok {
		return PickerResult{}, fmt.Errorf("unexpected picker model %T", final)
	}
	if model.cancelled {
		return PickerResult{}, ErrCancelled
	}
	return PickerResult{Choice: model.choice, Err: model.actionErr, Acted: model.acted}, nil
}
