package ui

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"sbctl/internal/profile"
	"sbctl/internal/service"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestMain pins the colour profile to ASCII.
//
// Test output is not a terminal, so lipgloss already resolves to ASCII and
// golden files contain no escape sequences. Pinning it makes that a guarantee
// rather than an accident of the environment, so the files stay identical across
// developer machines, CI runners and any value of TERM.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}

// golden compares got against the recorded file, or rewrites it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run: go test ./internal/ui -update)\ngot:\n%s", path, got)
	}
	if got != string(want) {
		t.Errorf("output does not match %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func testProfiles() []profile.Profile {
	return []profile.Profile{
		{Name: "home", Path: "/p/home.json"},
		{Name: "template", Path: "/p/template.json", Placeholders: []string{"TODO_UUID"}},
		{Name: "work", Path: "/p/work.json"},
	}
}

func TestRenderStatusRunning(t *testing.T) {
	theme := NewTheme(nil, Options{})
	got := theme.RenderStatus(StatusView{
		State:   service.StateRunning,
		Profile: "work",
		Running: true,
		Tun:     "tun0",
		Server:  "vpn.example.com:443",
		PID:     4711,
	})
	golden(t, "status_running", got)
}

// TestRenderStatusConfiguredButStopped covers the distinction that used to be
// lost: a profile is selected, but the service is not running. Blanking the name
// in that state left the user unable to see what would come back.
func TestRenderStatusConfiguredButStopped(t *testing.T) {
	theme := NewTheme(nil, Options{})
	got := theme.RenderStatus(StatusView{
		State:   service.StateStopped,
		Profile: "work",
		Running: false,
	})
	golden(t, "status_stopped_configured", got)

	if !strings.Contains(got, "work") {
		t.Error("the configured profile must remain visible while stopped")
	}
	if !strings.Contains(got, "configured") {
		t.Error("the stopped-but-configured state must be labelled")
	}
}

func TestRenderStatusBroken(t *testing.T) {
	theme := NewTheme(nil, Options{})
	got := theme.RenderStatus(StatusView{
		State:   service.StateError,
		Profile: "gone",
		Broken:  `the active profile "gone" no longer exists`,
	})
	golden(t, "status_broken", got)
}

func TestRenderStatusPlain(t *testing.T) {
	theme := NewTheme(nil, Options{Plain: true})
	got := theme.RenderStatus(StatusView{
		State:   service.StateRunning,
		Profile: "work",
		Running: true,
		Tun:     "tun0",
	})
	golden(t, "status_running_plain", got)

	// Plain mode exists for terminals that cannot render box drawing or the
	// Unicode state glyphs.
	for _, glyph := range []string{"●", "○", "✓", "✗", "⚠", "╭", "─"} {
		if strings.Contains(got, glyph) {
			t.Errorf("plain output still contains %q:\n%s", glyph, got)
		}
	}
}

func TestRenderProfileList(t *testing.T) {
	theme := NewTheme(nil, Options{})
	golden(t, "list_running", theme.RenderProfileList(testProfiles(), "work", true))
}

func TestRenderProfileListStopped(t *testing.T) {
	theme := NewTheme(nil, Options{})
	golden(t, "list_stopped", theme.RenderProfileList(testProfiles(), "work", false))
}

func TestRenderProfileListPlain(t *testing.T) {
	theme := NewTheme(nil, Options{Plain: true})
	got := theme.RenderProfileList(testProfiles(), "work", true)
	golden(t, "list_plain", got)

	// The placeholder badge must survive plain mode with a readable label, so
	// the warning is not conveyed by colour alone.
	if !strings.Contains(got, "[todo]") {
		t.Errorf("plain output lost the placeholder marker:\n%s", got)
	}
}

func TestRenderProfileListEmpty(t *testing.T) {
	theme := NewTheme(nil, Options{})
	got := theme.RenderProfileList(nil, "", false)
	if !strings.Contains(got, "no profiles") {
		t.Fatalf("empty list should say so, got %q", got)
	}
}

func TestKVBlockAligns(t *testing.T) {
	theme := NewTheme(nil, Options{})
	rows := []KV{
		{Label: "ip", Value: "203.0.113.42"},
		{Label: "network", Value: "AS13335 Cloudflare, Inc."},
		{Label: "timezone", Value: "Asia/Colombo"},
	}
	got := theme.KVBlock(append(rows, KV{Label: "postal", Value: ""}))
	golden(t, "kv_ip", got)

	// Alignment is the whole point of having one shared renderer; the previous
	// emoji-prefixed layout used variable-width glyphs and never lined up. Each
	// value must begin in the same column regardless of its label's length.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	column := -1
	for i, row := range rows {
		at := strings.Index(lines[i], row.Value)
		if at < 0 {
			t.Fatalf("row %d does not contain its value %q:\n%s", i, row.Value, got)
		}
		if column == -1 {
			column = at
			continue
		}
		if at != column {
			t.Errorf("row %d starts its value at column %d, expected %d:\n%s", i, at, column, got)
		}
	}
}

func TestRenderChecks(t *testing.T) {
	theme := NewTheme(nil, Options{})
	got := theme.RenderChecks([]Check{
		{Name: "sing-box", OK: true, Detail: "version 1.13.8"},
		{Name: "sudo: activate", Detail: "not permitted without a password", Hint: "reinstall the rules with: sudo make install"},
		{Name: "active config", OK: true, Warn: true, Detail: "no profile selected", Hint: "choose one with: sbctl use <name>"},
	})
	golden(t, "doctor_checks", got)
}

func TestTruncateName(t *testing.T) {
	theme := NewTheme(nil, Options{})
	long := strings.Repeat("a", 60)
	got := theme.TruncateName(long)
	if lipgloss.Width(got) > MaxNameWidth {
		t.Fatalf("truncated name is %d cells wide, want at most %d", lipgloss.Width(got), MaxNameWidth)
	}
	if theme.TruncateName("short") != "short" {
		t.Error("a short name must not be altered")
	}

	plain := NewTheme(nil, Options{Plain: true})
	if strings.Contains(plain.TruncateName(long), "…") {
		t.Error("plain mode must use ASCII for the truncation marker")
	}
}

func TestNewThemePlainWhenTerminalIsDumb(t *testing.T) {
	t.Setenv("TERM", "dumb")
	if !NewTheme(nil, Options{}).Plain {
		t.Error("a dumb terminal cannot render decoration and must fall back to plain")
	}

	t.Setenv("TERM", "xterm-256color")
	if NewTheme(nil, Options{}).Plain {
		t.Error("a capable terminal should not be forced into plain mode")
	}
}

// ---------------------------------------------------------------------------
// Picker model
// ---------------------------------------------------------------------------

// drive feeds key presses to the model and returns the final state.
func drive(t *testing.T, model tea.Model, keys ...string) pickerModel {
	t.Helper()
	for _, key := range keys {
		var msg tea.Msg
		switch key {
		case "enter", "up", "down", "esc", "tab", "home", "end":
			msg = tea.KeyMsg{Type: keyType(key)}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}
		model, _ = model.Update(msg)
	}
	final, ok := model.(pickerModel)
	if !ok {
		t.Fatalf("unexpected model %T", model)
	}
	return final
}

func keyType(name string) tea.KeyType {
	switch name {
	case "enter":
		return tea.KeyEnter
	case "up":
		return tea.KeyUp
	case "down":
		return tea.KeyDown
	case "esc":
		return tea.KeyEsc
	case "tab":
		return tea.KeyTab
	case "home":
		return tea.KeyHome
	case "end":
		return tea.KeyEnd
	}
	return tea.KeyRunes
}

func newTestPicker(profiles []profile.Profile, state service.RunState, active string) tea.Model {
	return NewPicker(
		NewTheme(nil, Options{}),
		StatusView{State: state, Profile: active, Running: state == service.StateRunning},
		profiles,
		nil, // no activation function: tests assert on the selection
	)
}

func TestPickerStartsOnTheActiveProfile(t *testing.T) {
	model := newTestPicker(testProfiles(), service.StateRunning, "work")
	final := drive(t, model)
	// "work" sorts last in the fixture, so a non-zero cursor proves the picker
	// opened on the profile already in use rather than at the top.
	if final.cursor != 2 {
		t.Fatalf("cursor = %d, want it on the active profile", final.cursor)
	}
}

func TestPickerSelectsProfile(t *testing.T) {
	model := newTestPicker(testProfiles(), service.StateStopped, "")
	final := drive(t, model, "enter")
	if final.choice != "home" {
		t.Fatalf("choice = %q, want home", final.choice)
	}
}

// TestPickerCursorIsClampedNotWrapped covers the navigation change: wrapping
// made it possible to derive an index from an empty range, and jumping from the
// last row to the first is rarely what "down" was meant to do.
func TestPickerCursorIsClampedNotWrapped(t *testing.T) {
	model := newTestPicker(testProfiles(), service.StateStopped, "")

	atTop := drive(t, model, "up", "up", "up", "up")
	if atTop.cursor != 0 {
		t.Fatalf("cursor = %d after pressing up at the top, want 0", atTop.cursor)
	}

	atBottom := drive(t, model, "down", "down", "down", "down", "down")
	if atBottom.cursor != len(testProfiles())-1 {
		t.Fatalf("cursor = %d after pressing down at the bottom, want %d", atBottom.cursor, len(testProfiles())-1)
	}
}

// TestPickerHandlesEmptyProfileListWithoutPanicking is a defensive guard: the
// caller avoids this case, but a selectable range of zero must not index a
// slice.
func TestPickerHandlesEmptyProfileListWithoutPanicking(t *testing.T) {
	model := newTestPicker(nil, service.StateStopped, "")
	final := drive(t, model, "down", "up", "enter", "end", "home")
	if final.choice != "" {
		t.Fatalf("choice = %q, want empty", final.choice)
	}
	if view := final.View(); view == "" {
		t.Error("the picker should still render something for an empty list")
	}
}

// TestPickerOffersTurnOffOnlyWhenRunning keeps the action list honest: there is
// nothing to stop when the service is already stopped.
func TestPickerOffersTurnOffOnlyWhenRunning(t *testing.T) {
	running := newTestPicker(testProfiles(), service.StateRunning, "work")
	final := drive(t, running, "end", "enter")
	if final.choice != TurnOffChoice {
		t.Fatalf("choice = %q, want the turn-off action", final.choice)
	}

	stopped := newTestPicker(testProfiles(), service.StateStopped, "")
	finalStopped := drive(t, stopped, "end", "enter")
	if finalStopped.choice == TurnOffChoice {
		t.Fatal("turn off must not be offered while the service is stopped")
	}
}

// TestPickerRefusesPlaceholderProfile checks the guard fires at selection time
// with an actionable message, rather than failing deep inside activation.
func TestPickerRefusesPlaceholderProfile(t *testing.T) {
	model := newTestPicker(testProfiles(), service.StateStopped, "")
	// "template" is the middle entry and carries a placeholder.
	final := drive(t, model, "down", "enter")

	if final.actionErr == nil {
		t.Fatal("selecting a profile with placeholders should be refused")
	}
	if !strings.Contains(final.actionErr.Error(), "sbctl edit template") {
		t.Errorf("the refusal should say how to fix it, got: %v", final.actionErr)
	}
}

func TestPickerQuitCancels(t *testing.T) {
	for _, key := range []string{"q", "esc"} {
		final := drive(t, newTestPicker(testProfiles(), service.StateRunning, "work"), key)
		if !final.cancelled {
			t.Errorf("%q should cancel the picker", key)
		}
		if final.choice != "" {
			t.Errorf("%q should not select anything, got %q", key, final.choice)
		}
	}
}

// TestPickerFilterAppearsOnlyForLongLists covers the deliberate restraint: a
// search box is noise on the two-to-five profiles a typical install has.
func TestPickerFilterAppearsOnlyForLongLists(t *testing.T) {
	small := drive(t, newTestPicker(testProfiles(), service.StateStopped, ""), "/")
	if small.filtering {
		t.Error("a short list should not offer filtering")
	}
	if strings.Contains(small.View(), "filter") {
		t.Error("a short list should not advertise a filter")
	}

	many := make([]profile.Profile, 0, filterThreshold)
	for i := 0; i < filterThreshold; i++ {
		many = append(many, profile.Profile{Name: string(rune('a'+i)) + "-profile"})
	}
	large := drive(t, newTestPicker(many, service.StateStopped, ""), "/")
	if !large.filtering {
		t.Error("a long list should offer filtering")
	}
}

func TestPickerFilterNarrowsSelection(t *testing.T) {
	many := []profile.Profile{
		{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"}, {Name: "delta"},
		{Name: "epsilon"}, {Name: "zeta"}, {Name: "eta"}, {Name: "theta"},
	}
	final := drive(t, newTestPicker(many, service.StateStopped, ""), "/", "z", "e", "t")
	if len(final.visible) != 1 {
		t.Fatalf("visible = %d rows, want 1 match for \"zet\"", len(final.visible))
	}
	if many[final.visible[0]].Name != "zeta" {
		t.Fatalf("matched %q, want zeta", many[final.visible[0]].Name)
	}

	// Escape clears the filter rather than quitting, so a mistyped query is
	// recoverable without losing the picker.
	cleared := drive(t, final, "esc")
	if cleared.cancelled {
		t.Error("escape should clear the filter, not cancel the picker")
	}
	if len(cleared.visible) != len(many) {
		t.Fatalf("visible = %d after clearing, want all %d", len(cleared.visible), len(many))
	}
}

// TestPickerViewportScrollsForLongLists covers terminal-height handling. Without
// it, a list longer than the window ran off the bottom with no way to reach the
// hidden entries.
func TestPickerViewportScrollsForLongLists(t *testing.T) {
	many := make([]profile.Profile, 0, 40)
	for i := 0; i < 40; i++ {
		many = append(many, profile.Profile{Name: "profile-" + string(rune('a'+i%26)) + string(rune('0'+i/26))})
	}

	model := newTestPicker(many, service.StateStopped, "")
	sized, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	final := drive(t, sized, "end")
	if final.offset == 0 {
		t.Fatal("the viewport should have scrolled to reveal the last entry")
	}
	rows := final.visibleRows()
	if final.cursor < final.offset || final.cursor >= final.offset+rows {
		t.Fatalf("cursor %d is outside the visible window [%d,%d)", final.cursor, final.offset, final.offset+rows)
	}
	if view := final.View(); !strings.Contains(view, "above") {
		t.Error("a scrolled list should indicate that entries are hidden above")
	}
}

// TestPickerRunsActivationAndReportsResult exercises the path every other picker
// test skips by passing a nil activation function.
//
// Selecting a profile hands the work to a tea.Cmd so the spinner keeps animating
// during a multi-second restart. That indirection is exactly where a hang or a
// dropped error would hide, so the command is executed here and its outcome
// followed all the way back into the model.
func TestPickerRunsActivationAndReportsResult(t *testing.T) {
	var activated string
	model := NewPicker(
		NewTheme(nil, Options{}),
		StatusView{State: service.StateStopped},
		testProfiles(),
		func(choice string) error {
			activated = choice
			return nil
		},
	)

	// Selecting returns a command rather than finishing, because the work has
	// not run yet.
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	picker, ok := next.(pickerModel)
	if !ok {
		t.Fatalf("unexpected model %T", next)
	}
	if !picker.working {
		t.Fatal("the picker should show itself as working while activation runs")
	}
	if cmd == nil {
		t.Fatal("selecting a profile must return a command that performs the activation")
	}
	// While working, the view must still render (that is what keeps the spinner
	// visible) and input must not select something else underneath the user.
	if picker.View() == "" {
		t.Error("the picker should keep rendering while activation is in flight")
	}
	ignored, _ := picker.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ignored.(pickerModel).cursor != picker.cursor {
		t.Error("input must be ignored once activation has been committed")
	}

	// Run the command as the bubbletea runtime would, then deliver its message.
	msg := drainCmd(t, cmd)
	if activated != "home" {
		t.Fatalf("activation ran with %q, want home", activated)
	}
	done, ok := msg.(activationDoneMsg)
	if !ok {
		t.Fatalf("expected an activationDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("activation reported %v", done.err)
	}

	final, _ := picker.Update(done)
	result := final.(pickerModel)
	if !result.acted {
		t.Error("the model should record that the action ran")
	}
	if !result.finished {
		t.Error("the model should be finished once activation completes")
	}
	if result.View() != "" {
		t.Error("the picker should collapse after acting")
	}
}

// TestPickerSurfacesActivationFailure confirms an error from the activation
// function reaches the caller rather than being swallowed by the goroutine.
func TestPickerSurfacesActivationFailure(t *testing.T) {
	boom := errors.New("service would not start")
	model := NewPicker(
		NewTheme(nil, Options{}),
		StatusView{State: service.StateStopped},
		testProfiles(),
		func(string) error { return boom },
	)

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := drainCmd(t, cmd)
	done, ok := msg.(activationDoneMsg)
	if !ok {
		t.Fatalf("expected an activationDoneMsg, got %T", msg)
	}
	if !errors.Is(done.err, boom) {
		t.Fatalf("err = %v, want the activation failure", done.err)
	}
}

// drainCmd runs a tea.Cmd, following batches, and returns the first non-tick
// message it produces.
func drainCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	msg := cmd()
	// Selecting batches the spinner tick with the activation, and batch order is
	// not guaranteed, so search the results for the message that matters.
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, inner := range batch {
			if inner == nil {
				continue
			}
			if found, ok := inner().(activationDoneMsg); ok {
				return found
			}
		}
		t.Fatal("no activationDoneMsg was produced by the batch")
	}
	return msg
}

func TestPickerViewCollapsesWhenFinished(t *testing.T) {
	final := drive(t, newTestPicker(testProfiles(), service.StateStopped, ""), "enter")
	if final.View() != "" {
		t.Error("the picker should collapse on exit so it does not crowd the command's own output")
	}
}

// TestPickerHelpToggleActuallyToggles guards against the key being accepted but
// doing nothing, which is worse than not binding it at all.
func TestPickerHelpToggleActuallyToggles(t *testing.T) {
	compact := drive(t, newTestPicker(testProfiles(), service.StateStopped, ""))
	expanded := drive(t, newTestPicker(testProfiles(), service.StateStopped, ""), "?")

	if !expanded.helpExpanded {
		t.Fatal("? should expand the help")
	}
	compactView, expandedView := compact.View(), expanded.View()
	if expandedView == compactView {
		t.Fatal("expanding the help must change what is rendered")
	}
	if !strings.Contains(expandedView, "quit without changing anything") {
		t.Errorf("expanded help should describe the bindings in full:\n%s", expandedView)
	}

	// Pressing it again must collapse back, not latch on.
	collapsed := drive(t, expanded, "?")
	if collapsed.helpExpanded {
		t.Error("? should collapse the help again")
	}
	if collapsed.View() != compactView {
		t.Error("collapsing should restore the compact footer exactly")
	}
}

// TestPickerAdvertisesTheHelpKey checks the toggle is discoverable; a hidden
// binding may as well not exist.
func TestPickerAdvertisesTheHelpKey(t *testing.T) {
	view := drive(t, newTestPicker(testProfiles(), service.StateStopped, "")).View()
	if !strings.Contains(view, "?") {
		t.Errorf("the compact footer should mention the help key:\n%s", view)
	}
}
