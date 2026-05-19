package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/energye/systray"

	"sbctl/cmd/sbctl-tray/icons"
	"sbctl/internal/daemon"
	"sbctl/internal/platform"
	"sbctl/internal/profile"
	"sbctl/internal/singbox"
)

const pollInterval = 3 * time.Second

// trayApp holds the state for the system tray application.
type trayApp struct {
	rt           platform.Runtime
	mu           sync.Mutex
	lastState    daemon.RunState
	lastActive   string
	profileItems []*systray.MenuItem
	mTurnOff     *systray.MenuItem
}

func onReady() {
	rt, err := platform.Detect()
	if err != nil {
		systray.SetTooltip("sbctl - unsupported platform")
		systray.SetIcon(icons.Error)
		mQuit := systray.AddMenuItem("Quit", "Quit sbctl-tray")
		mQuit.Click(func() { systray.Quit() })
		return
	}

	app := &trayApp{
		rt:        rt,
		lastState: daemon.StateStopped,
	}

	// Initial icon
	systray.SetIcon(icons.Inactive)
	systray.SetTooltip("sbctl - stopped")

	// Build the menu
	app.buildMenu()

	// Start background polling
	go app.pollLoop()
}

func (app *trayApp) buildMenu() {
	// Header
	mHeader := systray.AddMenuItem("sbctl", "sing-box controller")
	mHeader.Disable()

	systray.AddSeparator()

	// Profile items (built dynamically)
	app.rebuildProfileMenu()

	systray.AddSeparator()

	// Turn off
	app.mTurnOff = systray.AddMenuItem("Turn off", "Stop sing-box proxy")
	app.mTurnOff.Disable()
	app.mTurnOff.Click(func() {
		go app.handleTurnOff()
	})

	systray.AddSeparator()

	// Quit
	mQuit := systray.AddMenuItem("Quit", "Quit sbctl-tray")
	mQuit.Click(func() { systray.Quit() })
}

func (app *trayApp) rebuildProfileMenu() {
	app.mu.Lock()
	defer app.mu.Unlock()

	// Hide old profile menu items
	for _, item := range app.profileItems {
		item.Hide()
	}
	app.profileItems = nil

	// Load profiles
	profiles, _, _ := profile.List(app.rt.ProfilesDir, app.rt.ActiveConfigPath)
	activeName, _ := app.rt.Activator.ActiveName()

	// Only show active marker if daemon is running
	if app.lastState != daemon.StateRunning {
		activeName = ""
	}

	for _, p := range profiles {
		name := p.Name
		label := "○  " + name
		if name == activeName {
			label = "●  " + name
		}

		item := systray.AddMenuItem(label, fmt.Sprintf("Switch to %s", name))
		if name == activeName && app.lastState == daemon.StateRunning {
			item.Check()
		}
		app.profileItems = append(app.profileItems, item)

		// Capture for closure
		profileName := name
		item.Click(func() {
			go app.handleProfileSwitch(profileName)
		})
	}

	if len(profiles) == 0 {
		item := systray.AddMenuItem("No profiles", "Create one with: sbctl add <name>")
		item.Disable()
		app.profileItems = append(app.profileItems, item)
	}
}

func (app *trayApp) pollLoop() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Immediate first check
	app.updateStatus()

	for range ticker.C {
		app.updateStatus()
	}
}

func (app *trayApp) updateStatus() {
	state, err := app.rt.Manager.Status()
	if err != nil {
		state = daemon.StateError
	}

	activeName, _ := app.rt.Activator.ActiveName()
	if state != daemon.StateRunning {
		activeName = ""
	}

	app.mu.Lock()
	changed := state != app.lastState || activeName != app.lastActive
	app.lastState = state
	app.lastActive = activeName
	app.mu.Unlock()

	if !changed {
		return
	}

	// Update icon and tooltip
	switch state {
	case daemon.StateRunning:
		systray.SetIcon(icons.Active)
		if activeName != "" {
			systray.SetTooltip(fmt.Sprintf("sbctl - running (%s)", activeName))
		} else {
			systray.SetTooltip("sbctl - running")
		}
		app.mTurnOff.Enable()
	case daemon.StateError:
		systray.SetIcon(icons.Error)
		systray.SetTooltip("sbctl - error")
		app.mTurnOff.Disable()
	default:
		systray.SetIcon(icons.Inactive)
		systray.SetTooltip("sbctl - stopped")
		app.mTurnOff.Disable()
	}

	// Update profile checkmarks
	app.mu.Lock()
	profiles, _, _ := profile.List(app.rt.ProfilesDir, app.rt.ActiveConfigPath)
	for i, item := range app.profileItems {
		if i < len(profiles) {
			name := profiles[i].Name
			if name == activeName {
				item.SetTitle("●  " + name)
				item.Check()
			} else {
				item.SetTitle("○  " + name)
				item.Uncheck()
			}
		}
	}
	app.mu.Unlock()
}

func (app *trayApp) handleProfileSwitch(name string) {
	path := profile.PathFor(app.rt.ProfilesDir, name)

	// Validate before switching
	if err := singbox.Check(path); err != nil {
		app.rt.Notifier.Notify(fmt.Sprintf("profile %s failed validation", name))
		return
	}

	if !platform.IsElevated() {
		exitCode, err := platform.RunElevated([]string{"use", name})
		if err != nil {
			app.rt.Notifier.Notify(fmt.Sprintf("failed to switch: %v", err))
			return
		}
		if exitCode != 0 {
			app.rt.Notifier.Notify("failed to switch profile")
			return
		}
		app.rt.Notifier.Notify(fmt.Sprintf("switched to %s", name))
		app.updateStatus()
		return
	}

	// Already elevated — do it directly
	rollback, err := app.rt.Activator.Activate(path)
	if err != nil {
		app.rt.Notifier.Notify(fmt.Sprintf("activation failed: %v", err))
		return
	}
	if err := app.rt.Manager.Restart(); err != nil {
		if rollback.Known() {
			_ = rollback.Rollback()
		}
		app.rt.Notifier.Notify(fmt.Sprintf("restart failed: %v", err))
		return
	}
	app.rt.Notifier.Notify(fmt.Sprintf("switched to %s", name))
	app.updateStatus()
}

func (app *trayApp) handleTurnOff() {
	if !platform.IsElevated() {
		exitCode, err := platform.RunElevated([]string{"off"})
		if err != nil {
			app.rt.Notifier.Notify(fmt.Sprintf("failed to stop: %v", err))
			return
		}
		if exitCode != 0 {
			app.rt.Notifier.Notify("failed to stop sing-box")
			return
		}
		app.rt.Notifier.Notify("sing-box stopped")
		app.updateStatus()
		return
	}

	if err := app.rt.Manager.Stop(); err != nil {
		app.rt.Notifier.Notify(fmt.Sprintf("stop failed: %v", err))
		return
	}
	app.rt.Notifier.Notify("sing-box stopped")
	app.updateStatus()
}
