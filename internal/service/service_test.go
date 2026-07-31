package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// realLaunchdPrint is genuine `launchctl print` output, trimmed to the fields
// sbctl reads. Parsing is tested against real text rather than an invented
// format, because the format is the thing most likely to be wrong.
const realLaunchdPrint = `system/app.lexiflix.singbox = {
	active count = 1
	path = /Library/LaunchDaemons/app.lexiflix.singbox.plist
	state = running

	program = /opt/homebrew/bin/sing-box
	runs = 1
	pid = 4711
	immediate reason = speculative
	forks = 0
	execs = 1
	initialized = 1
	trampolined = 0
	started suspended = 0
	proxies suspended = 0
	last exit code = (never exited)
}`

// crashLoopingLaunchdPrint is what a service with a valid-but-broken config
// looks like moments after activation: still "running", but on its eighth run
// and reporting a non-zero exit from the previous one.
const crashLoopingLaunchdPrint = `system/app.lexiflix.singbox = {
	state = running
	runs = 8
	pid = 5120
	last exit code = 1
}`

const stoppedLaunchdPrint = `system/app.lexiflix.singbox = {
	state = waiting
	runs = 3
	last exit code = 2
}`

func TestParseLaunchdPrint(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantState    RunState
		wantPID      int
		wantRestarts int
		wantLastExit string
	}{
		{"healthy", realLaunchdPrint, StateRunning, 4711, 1, ""},
		{"crash looping", crashLoopingLaunchdPrint, StateRunning, 5120, 8, "1"},
		{"waiting after failure", stoppedLaunchdPrint, StateStopped, 0, 3, "2"},
		{"empty", "", StateStopped, 0, -1, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseLaunchdPrint(tc.input)
			if got.State != tc.wantState {
				t.Errorf("State = %q, want %q", got.State, tc.wantState)
			}
			if got.PID != tc.wantPID {
				t.Errorf("PID = %d, want %d", got.PID, tc.wantPID)
			}
			if got.Restarts != tc.wantRestarts {
				t.Errorf("Restarts = %d, want %d", got.Restarts, tc.wantRestarts)
			}
			if got.LastExit != tc.wantLastExit {
				t.Errorf("LastExit = %q, want %q", got.LastExit, tc.wantLastExit)
			}
		})
	}
}

// TestHealthCrashSinceDetectsRestartLoop covers the case that motivated the
// whole Health type: the service reports "running" both before and after, but
// its run counter climbed, which means it died and was restarted in between.
// TestHealthCrashedSince pins the comparison that decides whether an activation
// is rolled back. Both samples are taken after the restart, so anything the
// restart itself caused appears in the baseline and must not count as a failure.
func TestHealthCrashedSince(t *testing.T) {
	tests := []struct {
		name        string
		baseline    Health
		after       Health
		wantCrashed bool
	}{
		{
			name:        "stable",
			baseline:    Health{State: StateRunning, PID: 100, Restarts: 1},
			after:       Health{State: StateRunning, PID: 100, Restarts: 1},
			wantCrashed: false,
		},
		{
			name:        "restart loop while reporting running",
			baseline:    Health{State: StateRunning, PID: 100, Restarts: 1},
			after:       Health{State: StateRunning, PID: 140, Restarts: 6},
			wantCrashed: true,
		},
		{
			name:        "died outright",
			baseline:    Health{State: StateRunning, PID: 100, Restarts: 1},
			after:       Health{State: StateStopped, Restarts: 1},
			wantCrashed: true,
		},
		{
			name:        "non-zero exit recorded",
			baseline:    Health{State: StateRunning, Restarts: 1},
			after:       Health{State: StateRunning, Restarts: 1, LastExit: "1"},
			wantCrashed: true,
		},
		{
			// The process the restart killed leaves its exit code behind. It is
			// present in both samples and is not evidence of a new failure.
			// Treating it as one rolled back every healthy macOS switch.
			name:        "exit code left by the restart itself",
			baseline:    Health{State: StateRunning, PID: 200, Restarts: 2, LastExit: "9"},
			after:       Health{State: StateRunning, PID: 200, Restarts: 2, LastExit: "9"},
			wantCrashed: false,
		},
		{
			name:        "a newly recorded exit is a failure",
			baseline:    Health{State: StateRunning, PID: 200, Restarts: 2, LastExit: "9"},
			after:       Health{State: StateRunning, PID: 200, Restarts: 2, LastExit: "1"},
			wantCrashed: true,
		},
		{
			// Without a restart counter, a changed pid is the only evidence that
			// the supervisor replaced the process underneath us.
			name:        "process replaced with no restart counter available",
			baseline:    Health{State: StateRunning, PID: 100, Restarts: -1},
			after:       Health{State: StateRunning, PID: 200, Restarts: -1},
			wantCrashed: true,
		},
		{
			name: "unknown restart counts are not treated as failure",
			// A platform that cannot report restarts must not cause a healthy
			// activation to be rolled back.
			baseline:    Health{State: StateRunning, Restarts: -1},
			after:       Health{State: StateRunning, Restarts: -1},
			wantCrashed: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			crashed, reason := tc.after.CrashedSince(tc.baseline)
			if crashed != tc.wantCrashed {
				t.Fatalf("CrashedSince = %v (%q), want %v", crashed, reason, tc.wantCrashed)
			}
			if crashed && reason == "" {
				t.Error("a detected crash must come with an explanation")
			}
		})
	}
}

func TestParseSystemdShow(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantState    RunState
		wantPID      int
		wantRestarts int
		wantLastExit string
	}{
		{
			name: "active",
			input: "ActiveState=active\nSubState=running\nNRestarts=0\n" +
				"ExecMainPID=2201\nExecMainStatus=0\n",
			wantState: StateRunning, wantPID: 2201, wantRestarts: 0,
		},
		{
			name: "crash looping",
			input: "ActiveState=active\nSubState=running\nNRestarts=5\n" +
				"ExecMainPID=2290\nExecMainStatus=1\n",
			wantState: StateRunning, wantPID: 2290, wantRestarts: 5, wantLastExit: "1",
		},
		{
			name: "failed",
			input: "ActiveState=failed\nSubState=failed\nNRestarts=3\n" +
				"ExecMainPID=0\nExecMainStatus=1\n",
			wantState: StateError, wantRestarts: 3, wantLastExit: "1",
		},
		{
			name:      "inactive",
			input:     "ActiveState=inactive\nSubState=dead\nNRestarts=0\nExecMainPID=0\nExecMainStatus=0\n",
			wantState: StateStopped,
		},
		{
			name:      "activating counts as running so a slow start is not a crash",
			input:     "ActiveState=activating\nSubState=start\nNRestarts=0\nExecMainPID=0\nExecMainStatus=0\n",
			wantState: StateRunning,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSystemdShow(tc.input)
			if got.State != tc.wantState {
				t.Errorf("State = %q, want %q", got.State, tc.wantState)
			}
			if got.PID != tc.wantPID {
				t.Errorf("PID = %d, want %d", got.PID, tc.wantPID)
			}
			if got.Restarts != tc.wantRestarts {
				t.Errorf("Restarts = %d, want %d", got.Restarts, tc.wantRestarts)
			}
			if got.LastExit != tc.wantLastExit {
				t.Errorf("LastExit = %q, want %q", got.LastExit, tc.wantLastExit)
			}
		})
	}
}

func TestParseSCQuery(t *testing.T) {
	const running = `SERVICE_NAME: sing-box
        TYPE               : 10  WIN32_OWN_PROCESS
        STATE              : 4  RUNNING
                                (STOPPABLE, NOT_PAUSABLE, ACCEPTS_SHUTDOWN)
        WIN32_EXIT_CODE    : 0  (0x0)
        PID                : 8124
        FLAGS              :`

	const startPending = `SERVICE_NAME: sing-box
        STATE              : 2  START_PENDING
        PID                : 9001`

	const stopPending = `SERVICE_NAME: sing-box
        STATE              : 3  STOP_PENDING
        PID                : 9001`

	const stopped = `SERVICE_NAME: sing-box
        STATE              : 1  STOPPED
        PID                : 0`

	tests := []struct {
		name      string
		input     string
		wantState RunState
		wantPID   int
	}{
		{"running", running, StateRunning, 8124},
		// A service still coming up must not be reported as an error; doing so
		// made a perfectly normal start look like a failure.
		{"start pending", startPending, StateRunning, 9001},
		{"stop pending", stopPending, StateStopped, 9001},
		{"stopped", stopped, StateStopped, 0},
		{"unparseable", "garbage", StateError, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := ParseSCQuery(tc.input)
			if got.State != tc.wantState {
				t.Errorf("State = %q, want %q", got.State, tc.wantState)
			}
			if got.PID != tc.wantPID {
				t.Errorf("PID = %d, want %d", got.PID, tc.wantPID)
			}
		})
	}
}

func TestIsSudoRefusal(t *testing.T) {
	refusals := []string{
		"sudo: a password is required",
		"sudo: no tty present and no askpass program specified",
		"Sorry, user alice is not allowed to execute '/bin/ln -sfn x y' as root",
		"sudo: a terminal is required to read the password",
	}
	for _, text := range refusals {
		if !IsSudoRefusal(text) {
			t.Errorf("IsSudoRefusal(%q) = false, want true", text)
		}
	}

	notRefusals := []string{
		"Could not find service in domain for system",
		"Unit sing-box.service could not be found.",
		"",
	}
	for _, text := range notRefusals {
		if IsSudoRefusal(text) {
			t.Errorf("IsSudoRefusal(%q) = true, want false", text)
		}
	}
}

// TestSudoTranslatesRefusal confirms a sudo refusal becomes a typed error the
// CLI can map to a permission exit code, rather than an opaque exit status.
func TestSudoTranslatesRefusal(t *testing.T) {
	fake := &FakeRunner{Default: FakeResult{Output: "sudo: a password is required", Err: ExitError(1)}}
	sudo := Sudo{Runner: fake}

	_, err := sudo.Run(context.Background(), "/bin/ln", "-sfn", "a", "b")
	if !errors.Is(err, ErrSudoNotConfigured) {
		t.Fatalf("err = %v, want ErrSudoNotConfigured", err)
	}
	if got := fake.CallLines()[0]; got != "sudo -n /bin/ln -sfn a b" {
		t.Fatalf("argv = %q", got)
	}
}

func TestLaunchdProbe(t *testing.T) {
	tests := []struct {
		name      string
		result    FakeResult
		wantState RunState
		wantErr   bool
	}{
		{"running", FakeResult{Output: realLaunchdPrint}, StateRunning, false},
		{
			name:      "not loaded is stopped, not an error",
			result:    FakeResult{Output: "Could not find service \"app.lexiflix.singbox\"", Err: ExitError(113)},
			wantState: StateStopped,
		},
		{
			name:      "sudo refusal surfaces as an error",
			result:    FakeResult{Output: "sudo: a password is required", Err: ExitError(1)},
			wantState: StateError,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &FakeRunner{Default: tc.result}
			m := LaunchdManager{
				CtlBin:    "/bin/launchctl",
				Label:     "system/app.lexiflix.singbox",
				PlistPath: "/Library/LaunchDaemons/app.lexiflix.singbox.plist",
				Runner:    Sudo{Runner: fake},
			}
			health, err := m.Probe(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if health.State != tc.wantState {
				t.Fatalf("State = %q, want %q", health.State, tc.wantState)
			}
		})
	}
}

func TestLaunchdRestartBootstrapsWhenNotLoaded(t *testing.T) {
	fake := &FakeRunner{
		Responses: map[string]FakeResult{
			"sudo -n /bin/launchctl print":     {Output: "Could not find service", Err: ExitError(113)},
			"sudo -n /bin/launchctl bootstrap": {Output: ""},
		},
	}
	m := LaunchdManager{
		CtlBin:    "/bin/launchctl",
		Label:     "system/app.lexiflix.singbox",
		PlistPath: "/Library/LaunchDaemons/app.lexiflix.singbox.plist",
		Runner:    Sudo{Runner: fake},
	}
	if err := m.Restart(context.Background()); err != nil {
		t.Fatalf("Restart() = %v", err)
	}

	joined := strings.Join(fake.CallLines(), "\n")
	if !strings.Contains(joined, "bootstrap system /Library/LaunchDaemons/app.lexiflix.singbox.plist") {
		t.Fatalf("expected a bootstrap call, got:\n%s", joined)
	}
	if strings.Contains(joined, "kickstart") {
		t.Fatalf("must not kickstart a service that is not loaded, got:\n%s", joined)
	}
}

func TestLaunchdRestartKickstartsWhenLoaded(t *testing.T) {
	fake := &FakeRunner{
		Responses: map[string]FakeResult{
			"sudo -n /bin/launchctl print":     {Output: realLaunchdPrint},
			"sudo -n /bin/launchctl kickstart": {Output: ""},
		},
	}
	m := LaunchdManager{CtlBin: "/bin/launchctl", Label: "system/app.lexiflix.singbox", Runner: Sudo{Runner: fake}}
	if err := m.Restart(context.Background()); err != nil {
		t.Fatalf("Restart() = %v", err)
	}
	if joined := strings.Join(fake.CallLines(), "\n"); !strings.Contains(joined, "kickstart -k") {
		t.Fatalf("expected kickstart, got:\n%s", joined)
	}
}

func TestLaunchdStopIgnoresNotLoaded(t *testing.T) {
	fake := &FakeRunner{Default: FakeResult{Output: "Boot-out failed: No such process", Err: ExitError(3)}}
	m := LaunchdManager{CtlBin: "/bin/launchctl", Label: "system/x", Runner: Sudo{Runner: fake}}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("stopping an unloaded service should succeed, got %v", err)
	}
}

func TestSystemdStopIgnoresMissingUnit(t *testing.T) {
	fake := &FakeRunner{Default: FakeResult{Output: "Failed to stop sing-box.service: Unit sing-box.service could not be found.", Err: ExitError(5)}}
	m := SystemdManager{CtlBin: "/usr/bin/systemctl", Unit: "sing-box", Runner: Sudo{Runner: fake}}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("stopping a missing unit should succeed, got %v", err)
	}
}

func TestSystemdProbeUsesShowNotIsActive(t *testing.T) {
	fake := &FakeRunner{Default: FakeResult{Output: "ActiveState=active\nSubState=running\nNRestarts=0\nExecMainPID=42\nExecMainStatus=0\n"}}
	m := SystemdManager{
		CtlBin:         "/usr/bin/systemctl",
		Unit:           "sing-box",
		ShowProperties: "ActiveState,SubState,NRestarts,ExecMainPID,ExecMainStatus",
		Runner:         Sudo{Runner: fake},
	}
	health, err := m.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe() = %v", err)
	}
	if health.State != StateRunning || health.PID != 42 {
		t.Fatalf("health = %+v", health)
	}
	// show is used because its key=value output is locale-independent, unlike
	// the prose that is-active prints.
	if got := fake.CallLines()[0]; !strings.Contains(got, "show -p ActiveState") {
		t.Fatalf("argv = %q, want a systemctl show call", got)
	}
}

// TestWinSWRestartWaitsForPendingStop covers the transient-state handling: a
// start issued while the service is still stopping fails, so Restart must wait.
func TestWinSWRestartWaitsForPendingStop(t *testing.T) {
	queries := 0
	fake := &FakeRunner{}
	fake.Responses = map[string]FakeResult{
		"sc.exe stop":  {Output: ""},
		"sc.exe start": {Output: ""},
	}

	runner := &sequencedRunner{inner: fake, onQuery: func() string {
		queries++
		if queries < 3 {
			return "STATE              : 3  STOP_PENDING\nPID                : 10"
		}
		return "STATE              : 1  STOPPED\nPID                : 0"
	}}

	m := WinSWManager{Name: "sing-box", Runner: runner, Sleep: func(time.Duration) {}}
	if err := m.Restart(context.Background()); err != nil {
		t.Fatalf("Restart() = %v", err)
	}
	if queries < 3 {
		t.Fatalf("expected repeated status polling while stopping, saw %d queries", queries)
	}
	if joined := strings.Join(runner.inner.CallLines(), "\n"); !strings.Contains(joined, "sc.exe start sing-box") {
		t.Fatalf("expected a start after the stop completed, got:\n%s", joined)
	}
}

// sequencedRunner returns changing output for repeated queries, so polling loops
// can be tested without real timing.
type sequencedRunner struct {
	inner   *FakeRunner
	onQuery func() string
}

func (r *sequencedRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "queryex" {
		r.inner.Calls = append(r.inner.Calls, append([]string{name}, args...))
		return []byte(r.onQuery()), nil
	}
	return r.inner.Run(ctx, name, args...)
}

func TestExecRunnerTimesOut(t *testing.T) {
	runner := ExecRunner{Timeout: 50 * time.Millisecond}
	_, err := runner.Run(context.Background(), "sleep", "5")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}
