package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"sbctl/internal/profile"
	"sbctl/internal/service"
)

// activate switches the service to the named profile and confirms it stayed up.
//
// The important property here is that a successful Restart is not treated as a
// successful activation. Restart only means the platform accepted the request; a
// configuration can pass sing-box's own validation and still be operationally
// dead — an unreachable server, a stale Reality key, a wrong SNI. Under launchd
// KeepAlive such a service is restarted so quickly that a naive status check
// reports "running" while the user has, in fact, just lost all connectivity and
// been told everything worked.
//
// So activation waits for the service to come up, samples its health, waits out
// a settle delay, and samples again. Success is reported only when the same
// process is still there. Otherwise it rolls back to the previous profile and
// says what happened.
func (a *App) activate(ctx context.Context, name string) error {
	if err := profile.ValidateName(name); err != nil {
		return err
	}

	profiles, err := profile.List(a.Layout.ProfilesDir)
	if err != nil {
		return a.storeError(err, "read")
	}
	target, ok := profile.Find(profiles, name)
	if !ok {
		return notFoundError(name, profile.Names(profiles))
	}
	if !target.Ready() {
		return placeholderError(target.Name, target.Placeholders)
	}

	a.debugf("validating %s", target.Path)
	checkStarted := a.clock()
	if err := a.Checker.Check(ctx, target.Path); err != nil {
		return err
	}
	a.debugf("validated in %s", a.since(checkStarted))

	// Probe before touching anything so that a missing sudo rule fails the
	// command before it has changed the active config, rather than halfway
	// through. The sample itself is not used to judge health; see verifyHealthy.
	before, beforeErr := a.Manager.Probe(ctx)
	if beforeErr != nil && errors.Is(beforeErr, service.ErrSudoNotConfigured) {
		return beforeErr
	}
	a.debugf("state before activation: %s (pid=%d restarts=%d)", before.State, before.PID, before.Restarts)

	rollback, err := a.Activator.Activate(target.Path)
	if err != nil {
		return a.storeError(err, "update the active configuration for")
	}

	restartStarted := a.clock()
	if err := a.Manager.Restart(ctx); err != nil {
		return a.recover(ctx, rollback, err, "the sing-box service did not restart")
	}
	a.debugf("restart request accepted in %s", a.since(restartStarted))

	if problem := a.verifyHealthy(ctx); problem != "" {
		return a.recover(ctx, rollback, nil,
			fmt.Sprintf("%s started but %s", target.Name, problem))
	}

	a.success("now using %s", target.Name)
	a.Notifier.Notify("sing-box switched to " + target.Name)
	return nil
}

// verifyHealthy confirms the freshly restarted service is genuinely up, and
// reports why it is not, or an empty string if it is fine.
//
// Both liveness samples are taken after the restart: the first as soon as the
// service is observed running, the second after the settle delay. Comparing two
// post-restart samples is what keeps the intentional restart out of the
// platform's restart counters — see Health.CrashedSince.
func (a *App) verifyHealthy(ctx context.Context) string {
	verifyStarted := a.clock()
	baseline, ok := a.awaitRunning(ctx)
	if !ok {
		return "did not stay up"
	}
	a.debugf("baseline after restart: %s (pid=%d restarts=%d lastExit=%q)",
		baseline.State, baseline.PID, baseline.Restarts, baseline.LastExit)

	if a.SettleDelay > 0 {
		a.debugf("waiting %s to confirm the service stays up", a.SettleDelay)
		if !sleepCtx(ctx, a.SettleDelay) {
			// Interrupted. The user asked us to stop waiting, not to roll back.
			return ""
		}
	}

	after, err := a.Manager.Probe(ctx)
	if err != nil {
		// A probe failure is not proof of a crash, and rolling back a working
		// profile because we could not read its state would be worse than the
		// uncertainty. Report it and accept the activation.
		a.debugf("could not confirm service health: %v", err)
		return ""
	}
	a.debugf("state after settling: %s (pid=%d restarts=%d lastExit=%q)",
		after.State, after.PID, after.Restarts, after.LastExit)

	crashed, reason := after.CrashedSince(baseline)
	if crashed {
		return reason
	}
	a.debugf("health confirmed in %s", a.since(verifyStarted))
	return ""
}

// awaitRunning polls until the service reports running and returns that sample,
// or reports false if it never got there within the start grace period.
//
// This is what lets the settle comparison start from a known-running process. A
// restart request returns as soon as the supervisor accepts it, which on launchd
// can be before the new process exists; baselining that would compare a sample
// taken before the spawn against one taken after and read the difference as a
// crash.
func (a *App) awaitRunning(ctx context.Context) (service.Health, bool) {
	deadline := a.clock().Add(a.StartGrace)
	for attempt := 0; ; attempt++ {
		health, err := a.Manager.Probe(ctx)
		if err != nil {
			// Unreadable state is not proof of failure; treat it as "up" and let
			// the settle comparison decide with whatever it can read.
			a.debugf("could not read service state while waiting for it to start: %v", err)
			return health, true
		}
		if health.State == service.StateRunning {
			return health, true
		}
		if attempt > 0 && !a.clock().Before(deadline) {
			a.debugf("service still %s after %s", health.State, a.StartGrace)
			return health, false
		}
		if !sleepCtx(ctx, startPollInterval) {
			return health, false
		}
	}
}

// sleepCtx waits for d, reporting false if the context was cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// recover restores the previous profile after a failed activation and returns an
// error describing both the failure and the recovery outcome.
func (a *App) recover(ctx context.Context, rollback profile.Rollback, cause error, summary string) error {
	failure := (&Error{Code: ExitService, Message: summary}).wrap(cause)

	if !rollback.Known() {
		return failure.withHints(
			"there was no previous profile to fall back to",
			"inspect the service output with: sbctl logs",
			"stop sing-box with: sbctl off",
		)
	}

	previous := rollback.Description()
	if err := rollback.Rollback(); err != nil {
		// Both the activation and its undo failed. Give the exact manual
		// commands rather than an error the user cannot act on, because at this
		// point their network may be down.
		return failure.withHints(
			fmt.Sprintf("could not restore %s automatically: %v", previous, err),
			fmt.Sprintf("restore it manually with: sbctl use %s", previous),
			"or stop sing-box entirely with: sbctl off",
		)
	}

	// Bring the restored config back into service; a rolled-back symlink means
	// nothing until the daemon rereads it.
	restartErr := a.Manager.Restart(ctx)
	if restartErr != nil {
		return failure.withHints(
			fmt.Sprintf("reverted to %s, but the service did not restart: %v", previous, restartErr),
			"inspect the service output with: sbctl logs",
		)
	}

	failure.Message = summary + "; reverted to " + previous
	return failure.withHints("inspect the service output with: sbctl logs")
}

// stop stops the service.
func (a *App) stop(ctx context.Context) error {
	if err := a.Manager.Stop(ctx); err != nil {
		return err
	}
	a.success("sing-box stopped")
	a.Notifier.Notify("sing-box stopped")
	return nil
}

// elevateAndRun re-runs sbctl elevated when the current process cannot perform
// privileged work, and reports whether it handled the request.
//
// Elevation deliberately happens before any spinner or interactive surface is
// drawn. On Windows the consent dialog takes focus and the elevated child owns
// its own console; starting an animation first would leave the user watching a
// spinner attached to a process that is no longer doing the work.
func (a *App) elevateAndRun(args ...string) (handled bool, err error) {
	if a.Elevator.IsElevated() {
		return false, nil
	}
	a.debugf("requesting elevation for: %v", args)

	code, err := a.Elevator.RunElevated(args)
	if err != nil {
		return true, (&Error{Code: ExitPermission, Message: err.Error()}).
			withHints("accept the Windows elevation prompt, or run sbctl from an elevated terminal")
	}
	if code != ExitOK {
		// The elevated child printed its own diagnostics to its own console.
		// Re-announcing success or inventing a message here would either
		// duplicate or contradict it.
		return true, &Error{Code: code, Message: "the elevated sbctl process reported a failure"}
	}
	return true, nil
}

// storeError adds a recovery hint to profile-directory permission failures,
// which almost always mean the install left the directory owned by someone else.
func (a *App) storeError(err error, action string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) {
		return (&Error{
			Code:    ExitPermission,
			Message: fmt.Sprintf("not allowed to %s profile data in %s", action, a.Layout.ProfilesDir),
			Err:     err,
		}).withHints("repair ownership with: sudo make install")
	}
	return err
}
