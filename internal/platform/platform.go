package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"sbctl/internal/daemon"
	"sbctl/internal/profile"
)

var asciiNotificationFilter = regexp.MustCompile(`[^\x20-\x7E]+`)

type Runtime struct {
	OS               string
	ServiceName      string
	ProfilesDir      string
	ActiveConfigPath string
	ActiveNamePath   string
	ErrorLogPath     string
	AccessLogPath    string
	Manager          daemon.Manager
	Activator        profile.Activator
	Notifier         Notifier
	LogFollower      LogFollower
}

type Notifier interface {
	Notify(message string)
}

type LogFollower interface {
	Follow(ctx context.Context) error
}

func Detect() (Runtime, error) {
	switch runtime.GOOS {
	case "darwin":
		return Darwin(), nil
	case "linux":
		return Linux(), nil
	case "windows":
		return Windows(), nil
	default:
		return Runtime{}, fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}

func Darwin() Runtime {
	active := "/usr/local/etc/sing-box/config.json"
	return Runtime{
		OS:               "darwin",
		ServiceName:      "app.lexiflix.singbox",
		ProfilesDir:      "/usr/local/etc/sing-box/profiles",
		ActiveConfigPath: active,
		ErrorLogPath:     "/var/log/sing-box/error.log",
		AccessLogPath:    "/var/log/sing-box/access.log",
		Manager: daemon.LaunchdManager{
			Label:     "system/app.lexiflix.singbox",
			PlistPath: "/Library/LaunchDaemons/app.lexiflix.singbox.plist",
		},
		Activator:   profile.SymlinkActivator{ActiveConfigPath: active, UseSudo: true},
		Notifier:    AppleScriptNotifier{},
		LogFollower: TailFileFollower{Path: "/var/log/sing-box/error.log"},
	}
}

func Linux() Runtime {
	active := "/etc/sing-box/config.json"
	return Runtime{
		OS:               "linux",
		ServiceName:      "sing-box",
		ProfilesDir:      "/etc/sing-box/profiles",
		ActiveConfigPath: active,
		ErrorLogPath:     "/var/log/sing-box/error.log",
		AccessLogPath:    "/var/log/sing-box/access.log",
		Manager:          daemon.SystemdManager{ServiceName: "sing-box"},
		Activator:        profile.SymlinkActivator{ActiveConfigPath: active, UseSudo: true},
		Notifier:         NoopNotifier{},
		LogFollower:      CommandFollower{Name: "journalctl", Args: []string{"-fu", "sing-box"}},
	}
}

func Windows() Runtime {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	active := filepath.Join(programData, "sing-box", "config.json")
	activeName := filepath.Join(programData, "sbctl", "active-profile")
	errorLog := filepath.Join(programData, "sing-box", "logs", "sing-box.err.log")
	return Runtime{
		OS:               "windows",
		ServiceName:      "sing-box",
		ProfilesDir:      filepath.Join(programData, "sing-box", "profiles"),
		ActiveConfigPath: active,
		ActiveNamePath:   activeName,
		ErrorLogPath:     errorLog,
		AccessLogPath:    filepath.Join(programData, "sing-box", "logs", "sing-box.out.log"),
		Manager:          daemon.WinSWManager{ServiceName: "sing-box"},
		Activator:        profile.CopyActivator{ActiveConfigPath: active, ActiveNamePath: activeName},
		Notifier:         NoopNotifier{},
		LogFollower:      TailFileFollower{Path: errorLog},
	}
}

type NoopNotifier struct{}

func (NoopNotifier) Notify(string) {}

type AppleScriptNotifier struct{}

func (AppleScriptNotifier) Notify(message string) {
	cmd := exec.Command("osascript", "-e", fmt.Sprintf(`display notification "%s" with title "sing-box"`, AppleScriptSafe(message)))
	_ = cmd.Run()
}

type TailFileFollower struct {
	Path string
}

func (f TailFileFollower) Follow(ctx context.Context) error {
	tail := exec.CommandContext(ctx, "tail", "-f", f.Path)
	tail.Stdout = os.Stdout
	tail.Stderr = os.Stderr
	if err := tail.Run(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == -1 {
			return nil
		}
		return err
	}
	return nil
}

type CommandFollower struct {
	Name string
	Args []string
}

func (f CommandFollower) Follow(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, f.Name, f.Args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

func AppleScriptSafe(message string) string {
	safe := asciiNotificationFilter.ReplaceAllString(message, " ")
	safe = strings.ReplaceAll(safe, `"`, `'`)
	safe = strings.Join(strings.Fields(safe), " ")
	return safe
}
