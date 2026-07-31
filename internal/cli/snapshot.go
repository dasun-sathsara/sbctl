package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"sbctl/internal/profile"
	"sbctl/internal/service"
	"sbctl/internal/ui"
)

// snapshot is one consistent reading of system state.
//
// It exists so that every surface — status, list, the picker header, doctor —
// derives from the same single sample. Each of those previously performed its
// own probe and its own active-profile lookup, which is both slower (each probe
// is a sudo subprocess) and capable of disagreeing with itself.
type snapshot struct {
	Health   service.Health
	Active   string
	Running  bool
	Profiles []profile.Profile

	// Broken explains why the active configuration is unusable, if it is.
	Broken string
}

// snapshot samples the service and profile state exactly once.
func (a *App) snapshot(ctx context.Context) (snapshot, error) {
	var snap snapshot

	health, err := a.Manager.Probe(ctx)
	if err != nil {
		if errors.Is(err, service.ErrSudoNotConfigured) {
			return snap, err
		}
		// A probe that cannot be read is reported as an error state rather than
		// aborting: the user still deserves to see their profile list.
		a.debugf("could not read service state: %v", err)
		health = service.Unknown(service.StateError)
	}
	snap.Health = health
	snap.Running = health.State == service.StateRunning

	active, activeErr := a.Activator.ActiveName()
	if activeErr != nil {
		a.debugf("could not read the active profile: %v", activeErr)
	}
	snap.Active = active

	profiles, listErr := profile.List(a.Layout.ProfilesDir)
	if listErr != nil {
		return snap, a.storeError(listErr, "read")
	}
	snap.Profiles = profiles

	if active != "" {
		snap.Broken = a.diagnoseActive(active, profiles)
	}
	return snap, nil
}

// diagnoseActive reports why the active configuration cannot be used.
//
// The case that matters is a deleted profile: on Unix the active config is a
// symlink into the profiles directory, so removing the profile leaves the
// service pointed at nothing. That used to surface as a cryptic sing-box error
// at the next restart, with no indication of the cause.
func (a *App) diagnoseActive(active string, profiles []profile.Profile) string {
	if _, ok := profile.Find(profiles, active); !ok {
		return fmt.Sprintf("the active profile %q no longer exists; the service has no configuration to load", active)
	}
	if _, err := os.Stat(a.Layout.ActiveConfigPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Sprintf("%s is missing or points at a deleted file", a.Layout.ActiveConfigPath)
		}
		return fmt.Sprintf("%s cannot be read: %v", a.Layout.ActiveConfigPath, err)
	}
	return ""
}

// statusView converts a snapshot into the renderable status model.
func (a *App) statusView(snap snapshot) ui.StatusView {
	view := ui.StatusView{
		State:   snap.Health.State,
		Profile: snap.Active,
		Running: snap.Running,
		PID:     snap.Health.PID,
		Broken:  snap.Broken,
	}
	if snap.Active == "" {
		return view
	}
	if active, ok := profile.Find(snap.Profiles, snap.Active); ok {
		view.Placeholders = active.Placeholders
		if tun, err := profile.InterfaceName(active.Path); err == nil {
			view.Tun = tun
		}
		if server, err := profile.Server(active.Path); err == nil {
			view.Server = server
		}
	}
	return view
}
