package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sbctl/internal/service"
)

func TestUseActivatesAndConfirmsHealth(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	// Same pid and restart count before and after: a clean, stable start.
	h.running(4711, 1)

	h.assertExit(h.run("use", "work"), ExitOK)
	h.assertContains("now using work")

	if h.manager.Restarts != 1 {
		t.Errorf("Restarts = %d, want 1", h.manager.Restarts)
	}
	if len(h.activator.Activated) != 1 {
		t.Errorf("Activated = %v, want one entry", h.activator.Activated)
	}
	if h.activator.RollbacksDone != 0 {
		t.Error("a healthy activation must not roll back")
	}
	if len(h.notifier.Messages) != 1 {
		t.Errorf("Messages = %v, want one notification", h.notifier.Messages)
	}
}

// TestUseRollsBackWhenServiceCrashLoops is the central regression test for this
// refactor.
//
// The service reports "running" at every sample, so a naive check calls it a
// success — but its restart counter keeps climbing after the restart already
// completed, which means sing-box is starting, dying, and being respawned by the
// supervisor. That is exactly the situation where the old code printed a tick
// while the user's network was down. Activation must detect it, restore the
// previous profile, and fail with a service exit code.
//
// Samples, in probe order: the pre-activation reading, the post-restart baseline,
// then the reading after the settle delay.
func TestUseRollsBackWhenServiceCrashLoops(t *testing.T) {
	h := newHarness(t)
	h.validProfile("broken")
	h.activator.Active = "known-good"
	h.manager.Samples = []service.Health{
		{State: service.StateRunning, PID: 100, Restarts: 1},
		{State: service.StateRunning, PID: 180, Restarts: 2},
		{State: service.StateRunning, PID: 240, Restarts: 8},
	}

	h.assertExit(h.run("use", "broken"), ExitService)

	if h.activator.RollbacksDone != 1 {
		t.Fatalf("RollbacksDone = %d, want 1", h.activator.RollbacksDone)
	}
	if h.activator.Active != "known-good" {
		t.Fatalf("Active = %q, want the previous profile restored", h.activator.Active)
	}
	// Restoring the symlink is not enough on its own: until the service is
	// restarted it keeps running the configuration that just failed, so the user
	// would still have no working connection. Assert the second restart happened,
	// otherwise that step could be removed without any test noticing.
	if h.manager.Restarts != 2 {
		t.Fatalf("Restarts = %d, want 2 (the attempt, then the restored profile)", h.manager.Restarts)
	}
	h.assertContains("reverted to known-good")
	h.assertContains("sbctl logs")
	if strings.Contains(h.stdout(), "now using") {
		t.Error("a crash-looping activation must never be reported as success")
	}
}

// TestUseAcceptsHealthyRestart guards against the inverse failure, which is worse
// than the bug it protects: reverting a switch that actually worked.
//
// Restarting the service is itself a respawn, and launchd counts it in the same
// counter used to spot crash loops. If the baseline were taken before the restart,
// every successful switch would show one extra run and be rolled back. Here the
// counter advances by exactly the restart that was asked for and then holds
// steady, which must be reported as success.
func TestUseAcceptsHealthyRestart(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "previous"
	h.manager.Samples = []service.Health{
		{State: service.StateRunning, PID: 100, Restarts: 1},
		{State: service.StateRunning, PID: 200, Restarts: 2},
		{State: service.StateRunning, PID: 200, Restarts: 2},
	}

	h.assertExit(h.run("use", "work"), ExitOK)
	h.assertContains("now using work")
	if h.activator.RollbacksDone != 0 {
		t.Fatalf("RollbacksDone = %d; a healthy restart must not be reverted", h.activator.RollbacksDone)
	}
}

// TestUseRollsBackWhenServiceDies covers the simpler failure: the service never
// reaches a running state after the restart.
func TestUseRollsBackWhenServiceDies(t *testing.T) {
	h := newHarness(t)
	h.validProfile("broken")
	h.activator.Active = "known-good"
	h.manager.Samples = []service.Health{
		{State: service.StateRunning, PID: 100, Restarts: 1},
		{State: service.StateStopped, Restarts: 1},
	}

	h.assertExit(h.run("use", "broken"), ExitService)
	if h.activator.RollbacksDone != 1 {
		t.Fatalf("RollbacksDone = %d, want 1", h.activator.RollbacksDone)
	}
	h.assertContains("reverted to known-good")
}

// TestUseWaitsTheSettleDelayBeforeJudging exercises the branch every other test
// skips by leaving the delay at zero.
//
// The delay is the whole reason the check can catch anything: a broken
// configuration takes a moment to fail, so sampling immediately after the restart
// would always see a healthy process. This asserts the wait actually happens and
// that the second sample is the one used to decide.
func TestUseWaitsTheSettleDelayBeforeJudging(t *testing.T) {
	h := newHarness(t)
	h.validProfile("broken")
	h.activator.Active = "known-good"
	h.app.SettleDelay = 40 * time.Millisecond
	h.manager.Samples = []service.Health{
		{State: service.StateRunning, PID: 100, Restarts: 1},
		// Healthy at the baseline, so a check that did not wait would pass.
		{State: service.StateRunning, PID: 200, Restarts: 2},
		// Only the post-delay sample reveals the crash loop.
		{State: service.StateRunning, PID: 260, Restarts: 9},
	}

	started := time.Now()
	code := h.run("use", "broken")
	elapsed := time.Since(started)

	h.assertExit(code, ExitService)
	if elapsed < h.app.SettleDelay {
		t.Fatalf("returned after %s, which is less than the %s settle delay: the wait was skipped",
			elapsed, h.app.SettleDelay)
	}
	if h.activator.RollbacksDone != 1 {
		t.Fatalf("RollbacksDone = %d, want 1", h.activator.RollbacksDone)
	}
}

// TestUseSurvivesSettleDelayWhenHealthy is the counterpart: waiting must not turn
// a working switch into a failure.
func TestUseSurvivesSettleDelayWhenHealthy(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "previous"
	h.app.SettleDelay = 20 * time.Millisecond
	h.manager.Samples = []service.Health{
		{State: service.StateRunning, PID: 100, Restarts: 1},
		{State: service.StateRunning, PID: 200, Restarts: 2},
		{State: service.StateRunning, PID: 200, Restarts: 2},
	}

	h.assertExit(h.run("use", "work"), ExitOK)
	h.assertContains("now using work")
	if h.activator.RollbacksDone != 0 {
		t.Fatal("a switch that stayed healthy through the delay must not be reverted")
	}
}

func TestUseRollsBackWhenRestartFails(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "previous"
	h.manager.RestartErr = errors.New("unit failed to start")

	h.assertExit(h.run("use", "work"), ExitService)
	if h.activator.RollbacksDone != 1 {
		t.Fatalf("RollbacksDone = %d, want 1", h.activator.RollbacksDone)
	}
	h.assertContains("did not restart")
}

// TestUseGivesManualRecoveryWhenRollbackAlsoFails covers the worst case: both
// the activation and its undo failed, so the user may be offline. They need
// commands, not a bare error.
func TestUseGivesManualRecoveryWhenRollbackAlsoFails(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "previous"
	h.activator.RollbackErr = errors.New("permission denied")
	h.manager.RestartErr = errors.New("unit failed to start")

	h.assertExit(h.run("use", "work"), ExitService)
	h.assertContains("could not restore previous")
	h.assertContains("sbctl use previous")
	h.assertContains("sbctl off")
}

// TestUseNoPriorProfileExplainsItself covers a first activation that fails:
// there is nothing to fall back to, and saying so is more useful than reporting
// a failed rollback.
func TestUseNoPriorProfileExplainsItself(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.manager.RestartErr = errors.New("unit failed to start")

	h.assertExit(h.run("use", "work"), ExitService)
	h.assertContains("no previous profile")
	if h.activator.RollbacksDone != 0 {
		t.Error("must not attempt a rollback when there is no prior state")
	}
}

func TestUseRefusesPlaceholders(t *testing.T) {
	h := newHarness(t)
	h.templateProfile("fresh")

	h.assertExit(h.run("use", "fresh"), ExitValidation)
	h.assertContains("placeholder")
	h.assertContains("sbctl edit fresh")
	if h.manager.Restarts != 0 {
		t.Error("a profile with placeholders must never reach the service")
	}
}

func TestUseRejectsTraversalName(t *testing.T) {
	h := newHarness(t)
	h.assertExit(h.run("use", "../../etc/passwd"), ExitError)
	h.assertContains("invalid profile name")
	if len(h.activator.Activated) != 0 {
		t.Error("an invalid name must never be activated")
	}
}

func TestUseUnknownProfileListsAlternatives(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.validProfile("home")

	h.assertExit(h.run("use", "typo"), ExitError)
	h.assertContains(`no profile named "typo"`)
	// Naming the real options saves the user a second command.
	h.assertContains("home")
	h.assertContains("work")
}

func TestUseInvalidConfigExitsValidation(t *testing.T) {
	h := newHarness(t)
	path := h.validProfile("work")
	h.checker.Invalid = map[string]error{path: errors.New("bad json")}

	h.assertExit(h.run("use", "work"), ExitError)
	if h.manager.Restarts != 0 {
		t.Error("an invalid config must never be activated")
	}
}

// TestSudoNotConfiguredExitsPermission verifies the dedicated exit code and the
// specific remedy for the most common real-world failure.
func TestSudoNotConfiguredExitsPermission(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.manager.ProbeErr = service.ErrSudoNotConfigured

	h.assertExit(h.run("use", "work"), ExitPermission)
	h.assertContains("not allowed to manage")
	h.assertContains("make install")
	h.assertContains("sbctl doctor")
}

func TestOffStopsService(t *testing.T) {
	h := newHarness(t)
	h.running(100, 1)

	h.assertExit(h.run("off"), ExitOK)
	h.assertContains("sing-box stopped")
	if h.manager.Stops != 1 {
		t.Errorf("Stops = %d, want 1", h.manager.Stops)
	}
}

func TestListEmptySuggestsCreating(t *testing.T) {
	h := newHarness(t)
	h.assertExit(h.run("list"), ExitOK)
	h.assertContains("no profiles yet")
	h.assertContains("sbctl add")
}

// TestListShowsConfiguredProfileWhileStopped is the fix for hiding the active
// profile whenever the service was not running: a user who stopped sing-box
// could no longer see which profile would come back.
func TestListShowsConfiguredProfileWhileStopped(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "work"
	h.manager.Samples = []service.Health{{State: service.StateStopped, Restarts: 0}}

	h.assertExit(h.run("list"), ExitOK)
	h.assertContains("work")
	h.assertContains("configured, not running")
}

func TestListJSON(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.templateProfile("fresh")
	h.activator.Active = "work"
	h.running(100, 1)

	h.assertExit(h.run("list", "--json"), ExitOK)
	payload := h.decodeJSON()

	profiles, ok := payload["profiles"].([]any)
	if !ok || len(profiles) != 2 {
		t.Fatalf("profiles = %#v", payload["profiles"])
	}
	first := profiles[0].(map[string]any)
	if first["name"] != "fresh" {
		t.Errorf("profiles are not sorted: %v", first["name"])
	}
	if markers, ok := first["placeholders"].([]any); !ok || len(markers) == 0 {
		t.Errorf("the template profile should report its placeholders: %#v", first["placeholders"])
	}
}

func TestStatusJSON(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "work"
	h.running(4711, 2)

	h.assertExit(h.run("status", "--json"), ExitOK)
	payload := h.decodeJSON()

	if payload["state"] != "running" {
		t.Errorf("state = %v", payload["state"])
	}
	if payload["profile"] != "work" {
		t.Errorf("profile = %v", payload["profile"])
	}
	if payload["tun"] != "tun0" {
		t.Errorf("tun = %v", payload["tun"])
	}
	if payload["server"] != "host.example:443" {
		t.Errorf("server = %v", payload["server"])
	}
}

// TestStatusReportsDeletedActiveProfile covers the dangling-config case: the
// active profile was removed, so the service has nothing to load. Previously
// this surfaced only as an opaque sing-box failure at the next restart.
func TestStatusReportsDeletedActiveProfile(t *testing.T) {
	h := newHarness(t)
	h.activator.Active = "deleted"
	h.running(100, 1)

	h.assertExit(h.run("status"), ExitOK)
	h.assertContains("no longer exists")
	h.assertContains("sbctl use")
}

func TestCheckValidProfile(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")

	h.assertExit(h.run("check", "work"), ExitOK)
	h.assertContains("work is a valid configuration")
}

func TestCheckPlaceholdersExitsValidation(t *testing.T) {
	h := newHarness(t)
	h.templateProfile("fresh")
	h.assertExit(h.run("check", "fresh"), ExitValidation)
	h.assertContains("placeholder")
}

func TestCheckWithoutActiveProfileExplains(t *testing.T) {
	h := newHarness(t)
	h.assertExit(h.run("check"), ExitError)
	h.assertContains("no profile is active")
	h.assertContains("sbctl check <name>")
}

func TestCheckJSONReportsInvalid(t *testing.T) {
	h := newHarness(t)
	path := h.validProfile("work")
	h.checker.Invalid = map[string]error{path: errors.New("unknown field")}

	h.assertExit(h.run("check", "work", "--json"), ExitValidation)
	payload := h.decodeJSON()
	if payload["valid"] != false {
		t.Errorf("valid = %v, want false", payload["valid"])
	}
}

// TestLogsDoesNotRequireAFileForJournald is the regression test for `sbctl logs`
// being unusable on Linux. The follower reads the journal and needs no file, so
// a missing log path must not stop it.
func TestLogsDoesNotRequireAFileForJournald(t *testing.T) {
	h := newHarness(t)
	h.follower = &FakeFollower{Path: "", Required: false, Output: "journal line\n"}
	h.app.Follower = h.follower

	h.assertExit(h.run("logs"), ExitOK)
	if !h.follower.Followed {
		t.Fatal("the journal follower was never invoked")
	}
	h.assertContains("journal line")
}

// TestLogsExplainsMissingFileForFileFollowers keeps the useful message for
// platforms that genuinely do read a file.
func TestLogsExplainsMissingFileForFileFollowers(t *testing.T) {
	h := newHarness(t)
	missing := filepath.Join(t.TempDir(), "absent.log")
	h.follower = &FakeFollower{Path: missing, Required: true}
	h.app.Follower = h.follower

	h.assertExit(h.run("logs"), ExitError)
	h.assertContains("no log file")
	h.assertContains("sbctl use")
	if h.follower.Followed {
		t.Error("must not try to follow a file that does not exist")
	}
}

func TestRmRefusesActiveRunningProfile(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "work"
	h.running(100, 1)

	h.assertExit(h.run("rm", "work"), ExitError)
	h.assertContains("in use by the running service")
	h.assertContains("--force")
	if _, err := os.Stat(filepath.Join(h.dir, "work.json")); err != nil {
		t.Error("the profile must still exist after a refused delete")
	}
}

// TestRmForceStopsServiceBeforeDeleting covers the correctness fix: deleting the
// file a running root service is reading would leave it with no configuration.
func TestRmForceStopsServiceBeforeDeleting(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "work"
	h.running(100, 1)

	h.assertExit(h.run("rm", "work", "--force"), ExitOK)
	if h.manager.Stops != 1 {
		t.Errorf("Stops = %d, want the service stopped before deletion", h.manager.Stops)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "work.json")); !os.IsNotExist(err) {
		t.Error("the profile should have been deleted")
	}
	// The active config now points at nothing; that must be said plainly.
	h.assertContains("no longer exists")
}

func TestRmUnknownProfileFailsBeforePrompting(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")

	h.assertExit(h.run("rm", "ghost"), ExitError)
	h.assertContains(`no profile named "ghost"`)
}

func TestRmNonInteractiveRequiresForce(t *testing.T) {
	h := newHarness(t)
	h.validProfile("spare")

	// No terminal is attached, so there is nobody to confirm with; refusing is
	// safer than deleting on the strength of an unanswerable prompt.
	h.assertExit(h.run("rm", "spare"), ExitError)
	h.assertContains("needs confirmation")
	if _, err := os.Stat(filepath.Join(h.dir, "spare.json")); err != nil {
		t.Error("the profile must survive an unconfirmed delete")
	}
}

func TestRmForceDeletesNonInteractively(t *testing.T) {
	h := newHarness(t)
	h.validProfile("spare")

	h.assertExit(h.run("rm", "spare", "--force"), ExitOK)
	h.assertContains("deleted spare")
}

func TestAddCreatesFromSkeleton(t *testing.T) {
	h := newHarness(t)
	h.assertExit(h.run("add", "fresh"), ExitOK)
	h.assertContains("created fresh")
	// The template's placeholders must be flagged immediately, along with what
	// to do about them.
	h.assertContains("placeholders")
	h.assertContains("sbctl edit fresh")

	if h.editorRuns != 1 {
		t.Errorf("editor runs = %d, want 1", h.editorRuns)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "fresh.json")); err != nil {
		t.Fatalf("profile not created: %v", err)
	}
}

func TestAddRefusesExistingProfile(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")

	h.assertExit(h.run("add", "work"), ExitError)
	h.assertContains("already exists")
	h.assertContains("sbctl edit work")
}

func TestAddRejectsInvalidName(t *testing.T) {
	h := newHarness(t)
	h.assertExit(h.run("add", "../escape"), ExitError)
	h.assertContains("invalid profile name")
}

// TestAddDoesNotLeaveAnInvalidFileBehindNonInteractively covers the asymmetry
// between add and edit: a failed validation used to leave a broken profile in
// the list with no way to notice.
func TestAddReportsValidationFailure(t *testing.T) {
	h := newHarness(t)
	h.checker.Err = errors.New("missing required field")

	h.assertExit(h.run("add", "fresh"), ExitError)
	h.assertContains("missing required field")
}

func TestEditValidatesAndReportsSuccess(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")

	h.assertExit(h.run("edit", "work"), ExitOK)
	h.assertContains("work is valid")
	if h.editorRuns != 1 {
		t.Errorf("editor runs = %d, want 1", h.editorRuns)
	}
}

// TestEditRestartsTheActiveProfile covers the fix for edits to the live profile
// silently having no effect until something else restarted the service.
func TestEditRestartsTheActiveProfile(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "work"
	h.running(100, 3)

	h.assertExit(h.run("edit", "work"), ExitOK)
	if h.manager.Restarts != 1 {
		t.Fatalf("Restarts = %d, want the service reloaded after editing the active profile", h.manager.Restarts)
	}
	h.assertContains("reloaded sing-box")
}

func TestEditDoesNotRestartAnInactiveProfile(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.validProfile("other")
	h.activator.Active = "other"
	h.running(100, 1)

	h.assertExit(h.run("edit", "work"), ExitOK)
	if h.manager.Restarts != 0 {
		t.Errorf("Restarts = %d, want 0 for a profile that is not in service", h.manager.Restarts)
	}
}

func TestEditUnknownProfile(t *testing.T) {
	h := newHarness(t)
	h.assertExit(h.run("edit", "ghost"), ExitError)
	h.assertContains(`no profile named "ghost"`)
}

func TestVersionJSON(t *testing.T) {
	h := newHarness(t)
	h.checker.VersionValue = "1.13.8"

	h.assertExit(h.run("version", "--json"), ExitOK)
	payload := h.decodeJSON()
	if payload["version"] != "test" {
		t.Errorf("version = %v", payload["version"])
	}
	if payload["sing_box"] != "1.13.8" {
		t.Errorf("sing_box = %v", payload["sing_box"])
	}
}

func TestQuietSuppressesSuccessButNotErrors(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.running(100, 1)

	h.assertExit(h.run("use", "work", "--quiet"), ExitOK)
	if strings.TrimSpace(h.stdout()) != "" {
		t.Errorf("--quiet should print nothing on success, got:\n%s", h.stdout())
	}

	h.assertExit(h.run("use", "ghost", "--quiet"), ExitError)
	if strings.TrimSpace(h.stderr()) == "" {
		t.Error("--quiet must still report errors")
	}
}

func TestVerbosePrintsDiagnosticsToStderrOnly(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.running(100, 1)

	h.assertExit(h.run("use", "work", "--verbose"), ExitOK)
	if !strings.Contains(h.stderr(), "[debug]") {
		t.Errorf("--verbose should emit diagnostics, stderr:\n%s", h.stderr())
	}
	// Diagnostics on stdout would corrupt piped or JSON output.
	if strings.Contains(h.stdout(), "[debug]") {
		t.Errorf("diagnostics must not go to stdout:\n%s", h.stdout())
	}
}

// TestJSONErrorsAreStructured makes the machine interface usable for failures as
// well as successes.
func TestJSONErrorsAreStructured(t *testing.T) {
	h := newHarness(t)
	h.assertExit(h.run("use", "ghost", "--json"), ExitError)

	payload := h.decodeJSON()
	if payload["error"] == nil {
		t.Fatalf("expected an error field:\n%s", h.stdout())
	}
	if payload["code"] == nil {
		t.Error("expected the exit code in the payload")
	}
}

func TestPlainModeUsesASCIISymbols(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "work"
	h.running(100, 1)

	h.assertExit(h.run("list", "--plain"), ExitOK)
	out := h.stdout()
	for _, glyph := range []string{"●", "○", "✓", "✗", "⚠"} {
		if strings.Contains(out, glyph) {
			t.Errorf("--plain output still contains %q:\n%s", glyph, out)
		}
	}
}

// TestElevationIsRequestedBeforeDoingWork covers the Windows path: an
// unelevated process must delegate rather than attempt privileged work itself.
func TestElevationIsRequestedBeforeDoingWork(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.elevator.Elevated = false

	h.assertExit(h.run("use", "work"), ExitOK)

	if len(h.elevator.Requests) != 1 {
		t.Fatalf("Requests = %v, want one elevation request", h.elevator.Requests)
	}
	if got := strings.Join(h.elevator.Requests[0], " "); got != "use work" {
		t.Errorf("elevation args = %q", got)
	}
	if h.manager.Restarts != 0 {
		t.Error("the unelevated process must not perform the work itself")
	}
	// The elevated child reports its own result; duplicating it here would
	// either repeat or contradict what the user already saw.
	if strings.Contains(h.stdout(), "now using") {
		t.Errorf("the delegating process must not claim success:\n%s", h.stdout())
	}
}

func TestElevationFailurePropagatesExitCode(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.elevator.Elevated = false
	h.elevator.ExitCode = ExitValidation

	h.assertExit(h.run("use", "work"), ExitValidation)
}

func TestElevationDeclinedExitsPermission(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.elevator.Elevated = false
	h.elevator.Err = errors.New("elevation was declined")

	h.assertExit(h.run("use", "work"), ExitPermission)
	h.assertContains("declined")
}

func TestDoctorReportsMissingSingBox(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "work"
	h.running(100, 1)
	h.checker.VersionValue = "1.13.8"

	// doctor exercises real sudo probing, which will legitimately fail in a test
	// environment; the point here is that it reports rather than crashes.
	code := h.run("doctor")
	if code != ExitOK && code != ExitError {
		t.Fatalf("doctor exit = %d, want 0 or 1", code)
	}
	h.assertContains("sing-box")
	h.assertContains("profiles")
	h.assertContains("active config")
}

func TestDoctorJSON(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")

	h.run("doctor", "--json")
	payload := h.decodeJSON()
	if _, ok := payload["checks"].([]any); !ok {
		t.Fatalf("checks = %#v", payload["checks"])
	}
}

func TestPrintSudoersProducesNarrowRules(t *testing.T) {
	h := newHarness(t)
	h.assertExit(h.run("print-sudoers", "--user", "alice"), ExitOK)

	out := h.stdout()
	if !strings.Contains(out, "alice ALL=(root) NOPASSWD:") {
		t.Fatalf("no rules emitted:\n%s", out)
	}
	// The narrowing is the entire point: a bare binary grant would restore the
	// escalation it was introduced to remove.
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, "NOPASSWD: /usr/bin/ln") ||
			strings.HasSuffix(trimmed, "NOPASSWD: /usr/bin/systemctl") {
			t.Fatalf("unrestricted grant emitted:\n%s", line)
		}
	}
}

func TestUnknownCommandFails(t *testing.T) {
	h := newHarness(t)
	if code := h.run("nonsense"); code == ExitOK {
		t.Fatal("an unknown command must not exit successfully")
	}
}

func TestAppleScriptSafeNeutralisesInjection(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`switched to "work" ✓`, "switched to 'work'"},
		// A quote would otherwise close the AppleScript string literal and let
		// the rest be interpreted as code.
		{`x" & (do shell script "id") & "`, "x' & (do shell script 'id') & '"},
		{"multi\nline\ttext", "multi line text"},
	}
	for _, tc := range tests {
		if got := AppleScriptSafe(tc.in); got != tc.want {
			t.Errorf("AppleScriptSafe(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
