package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"sbctl/internal/platform"
	"sbctl/internal/profile"
	"sbctl/internal/service"
	"sbctl/internal/ui"
)

// TestMain pins the colour profile so assertions compare text rather than
// escape sequences. Test output is not a terminal, so lipgloss already resolves
// to ASCII; pinning it makes that a guarantee instead of an accident of the
// environment, and keeps results identical under any TERM.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// FakeManager is a service.Manager whose behaviour and observed health are
// scripted by the test.
type FakeManager struct {
	// Samples are returned by successive Probe calls; the last one repeats.
	Samples []service.Health
	// ProbeErr is returned by every Probe when set.
	ProbeErr error
	// RestartErr is returned by Restart when set.
	RestartErr error
	// StopErr is returned by Stop when set.
	StopErr error

	probes   int
	Restarts int
	Stops    int
}

func (m *FakeManager) Probe(context.Context) (service.Health, error) {
	if m.ProbeErr != nil {
		return service.Unknown(service.StateError), m.ProbeErr
	}
	if len(m.Samples) == 0 {
		return service.Unknown(service.StateStopped), nil
	}
	index := m.probes
	if index >= len(m.Samples) {
		index = len(m.Samples) - 1
	}
	m.probes++
	return m.Samples[index], nil
}

func (m *FakeManager) Restart(context.Context) error {
	m.Restarts++
	return m.RestartErr
}

func (m *FakeManager) Stop(context.Context) error {
	m.Stops++
	return m.StopErr
}

// FakeActivator is an in-memory profile.Activator that records rollbacks.
type FakeActivator struct {
	Active      string
	ActiveErr   error
	ActivateErr error
	// RollbackErr makes the undo itself fail, exercising the manual-recovery path.
	RollbackErr error

	Activated     []string
	RollbacksDone int
}

func (a *FakeActivator) ActiveName() (string, error) { return a.Active, a.ActiveErr }

func (a *FakeActivator) ActivePath() (string, error) {
	if a.Active == "" {
		return "", a.ActiveErr
	}
	return "/profiles/" + a.Active + ".json", a.ActiveErr
}

func (a *FakeActivator) Activate(target string) (profile.Rollback, error) {
	if a.ActivateErr != nil {
		return nil, a.ActivateErr
	}
	previous := a.Active
	a.Activated = append(a.Activated, target)
	a.Active = profile.NameFor(target)
	return &fakeRollback{owner: a, previous: previous}, nil
}

type fakeRollback struct {
	owner    *FakeActivator
	previous string
}

func (r *fakeRollback) Known() bool         { return r.previous != "" }
func (r *fakeRollback) Description() string { return r.previous }

func (r *fakeRollback) Rollback() error {
	r.owner.RollbacksDone++
	if r.owner.RollbackErr != nil {
		return r.owner.RollbackErr
	}
	r.owner.Active = r.previous
	return nil
}

// FakeElevator records elevation requests.
type FakeElevator struct {
	Elevated bool
	ExitCode int
	Err      error
	Requests [][]string
}

func (e *FakeElevator) IsElevated() bool { return e.Elevated }

func (e *FakeElevator) RunElevated(args []string) (int, error) {
	e.Requests = append(e.Requests, args)
	return e.ExitCode, e.Err
}

// FakeNotifier records desktop notifications.
type FakeNotifier struct{ Messages []string }

func (n *FakeNotifier) Notify(message string) { n.Messages = append(n.Messages, message) }

// FakeFollower is a platform.LogFollower that reports canned output.
type FakeFollower struct {
	Path     string
	Required bool
	Output   string
	Err      error
	Followed bool
}

func (f *FakeFollower) NeedsFile() (string, bool) { return f.Path, f.Required }

func (f *FakeFollower) Follow(_ context.Context, out io.Writer) error {
	f.Followed = true
	if f.Err != nil {
		return f.Err
	}
	_, err := io.WriteString(out, f.Output)
	return err
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// harness is a fully faked App plus the buffers its output was captured into.
type harness struct {
	t         *testing.T
	app       *App
	out       *bytes.Buffer
	err       *bytes.Buffer
	dir       string
	manager   *FakeManager
	activator *FakeActivator
	checker   *profile.FakeChecker
	elevator  *FakeElevator
	notifier  *FakeNotifier
	follower  *FakeFollower
	// EditorFunc replaces the file content when the editor is "opened".
	EditorFunc func(path string) error
	editorRuns int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	h := &harness{
		t:         t,
		out:       &bytes.Buffer{},
		err:       &bytes.Buffer{},
		dir:       profilesDir,
		manager:   &FakeManager{},
		activator: &FakeActivator{},
		checker:   &profile.FakeChecker{},
		elevator:  &FakeElevator{Elevated: true},
		notifier:  &FakeNotifier{},
		follower:  &FakeFollower{},
	}

	h.app = &App{
		Out: h.out,
		Err: h.err,
		In:  strings.NewReader(""),
		Layout: platform.Layout{
			OS:               "linux",
			ServiceName:      "sing-box",
			ProfilesDir:      profilesDir,
			ActiveConfigPath: filepath.Join(dir, "config.json"),
			ErrorLogPath:     filepath.Join(dir, "error.log"),
			LnBin:            "/usr/bin/ln",
			CtlBin:           "/usr/bin/systemctl",
		},
		Manager:   h.manager,
		Activator: h.activator,
		Checker:   h.checker,
		Elevator:  h.elevator,
		Notifier:  h.notifier,
		Follower:  h.follower,
		Skeleton:  `{"outbounds":[{"server":"TODO_SERVER_IP_OR_HOST","uuid":"TODO_UUID"}]}`,
		Version:   "test",
		// Zero by default so tests do not pay the settle delay; the tests that
		// specifically exercise that wait set it themselves. Note that
		// awaitRunning still polls with a real interval, so a case scripting a
		// non-running service does spend a little wall-clock time.
		SettleDelay: 0,
		// Permit everything by default: tests exercise command behaviour, not
		// the host's real sudo configuration.
		SudoProbe: func(context.Context, []string) error { return nil },
	}
	h.app.Editor = func(_ context.Context, path string) error {
		h.editorRuns++
		if h.EditorFunc != nil {
			return h.EditorFunc(path)
		}
		return nil
	}
	h.app.Theme = ui.NewTheme(io.Discard, ui.Options{})
	return h
}

// writeProfile creates a profile file with the given content.
func (h *harness) writeProfile(name, content string) string {
	h.t.Helper()
	path := filepath.Join(h.dir, name+".json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return path
}

// validProfile creates a placeholder-free profile.
func (h *harness) validProfile(name string) string {
	return h.writeProfile(name, `{"inbounds":[{"type":"tun","interface_name":"tun0"}],"outbounds":[{"type":"vless","server":"host.example","server_port":443}]}`)
}

// templateProfile creates a profile that still has placeholders.
func (h *harness) templateProfile(name string) string {
	return h.writeProfile(name, `{"outbounds":[{"server":"TODO_SERVER_IP_OR_HOST","uuid":"TODO_UUID"}]}`)
}

// run executes sbctl with args and returns the exit code.
func (h *harness) run(args ...string) int {
	h.t.Helper()
	h.out.Reset()
	h.err.Reset()
	return h.app.Execute(args)
}

// running marks the service as healthy and stable across probes.
func (h *harness) running(pid, restarts int) {
	h.manager.Samples = []service.Health{{State: service.StateRunning, PID: pid, Restarts: restarts}}
}

func (h *harness) stdout() string { return h.out.String() }
func (h *harness) stderr() string { return h.err.String() }

// assertExit fails unless the code matches.
func (h *harness) assertExit(got, want int) {
	h.t.Helper()
	if got != want {
		h.t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", got, want, h.stdout(), h.stderr())
	}
}

// assertContains fails unless text appears in the combined output.
func (h *harness) assertContains(want string) {
	h.t.Helper()
	combined := h.stdout() + h.stderr()
	if !strings.Contains(combined, want) {
		h.t.Fatalf("output does not contain %q\nstdout:\n%s\nstderr:\n%s", want, h.stdout(), h.stderr())
	}
}

// decodeJSON parses stdout as a JSON object.
func (h *harness) decodeJSON() map[string]any {
	h.t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &payload); err != nil {
		h.t.Fatalf("stdout is not valid JSON (%v):\n%s", err, h.stdout())
	}
	if version, ok := payload["schema"]; !ok {
		h.t.Error("every JSON payload must carry a schema version so consumers can detect changes")
	} else if fmt.Sprint(version) != fmt.Sprint(SchemaVersion) {
		h.t.Errorf("schema = %v, want %d", version, SchemaVersion)
	}
	return payload
}
