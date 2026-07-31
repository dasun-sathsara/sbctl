// Package platform describes the host-specific filesystem layout, log sources
// and privilege model that sbctl operates within.
//
// It is deliberately a leaf of the dependency graph: it imports only the
// standard library (plus golang.org/x/sys on Windows) and knows nothing about
// profiles or services. Callers combine a Layout with the profile and service
// packages to build a working command set.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// Layout is a pure value object holding every host-specific path and
// identifier sbctl needs. Constructing one never touches the filesystem, so
// tests can build an arbitrary Layout rooted in a temporary directory.
type Layout struct {
	// OS is the GOOS this layout describes.
	OS string

	// ServiceName is the platform service identifier (systemd unit, Windows
	// service name). Empty on Darwin, which addresses services by label.
	ServiceName string

	// LaunchdLabel is the launchd service target, e.g.
	// "system/app.lexiflix.singbox". Darwin only.
	LaunchdLabel string

	// PlistPath is the LaunchDaemon plist location. Darwin only.
	PlistPath string

	// ProfilesDir holds the user's *.json sing-box profiles.
	ProfilesDir string

	// ActiveConfigPath is the file sing-box actually reads. On Unix it is a
	// symlink into ProfilesDir; on Windows it is a copy.
	ActiveConfigPath string

	// ActiveNamePath records the active profile name on platforms that cannot
	// derive it from a symlink. Windows only; empty elsewhere.
	ActiveNamePath string

	// ErrorLogPath and AccessLogPath are the service's log destinations. They
	// may not exist, and on journald systems are never used.
	ErrorLogPath  string
	AccessLogPath string

	// LnBin, CtlBin are the absolute paths of the privileged helpers sbctl
	// invokes through sudo. Resolved at detection time because /bin vs
	// /usr/bin varies with distribution usr-merge.
	LnBin  string
	CtlBin string
}

// UsesSudo reports whether privileged operations go through sudo(8) rather
// than a Windows-style elevated re-exec.
func (l Layout) UsesSudo() bool { return l.OS == "darwin" || l.OS == "linux" }

// Detect returns the Layout for the current host, or an error on an
// unsupported operating system.
func Detect() (Layout, error) {
	switch runtime.GOOS {
	case "darwin":
		return Darwin(), nil
	case "linux":
		return Linux(), nil
	case "windows":
		return Windows(), nil
	default:
		return Layout{}, &UnsupportedOSError{OS: runtime.GOOS}
	}
}

// UnsupportedOSError reports a host sbctl has no layout for.
type UnsupportedOSError struct{ OS string }

func (e *UnsupportedOSError) Error() string {
	return "sbctl does not support " + e.OS
}

// Darwin returns the macOS layout. Paths are frozen by the installed-base
// compatibility contract and must not be changed casually.
func Darwin() Layout {
	return Layout{
		OS:               "darwin",
		LaunchdLabel:     "system/app.lexiflix.singbox",
		PlistPath:        "/Library/LaunchDaemons/app.lexiflix.singbox.plist",
		ProfilesDir:      "/usr/local/etc/sing-box/profiles",
		ActiveConfigPath: "/usr/local/etc/sing-box/config.json",
		ErrorLogPath:     "/var/log/sing-box/error.log",
		AccessLogPath:    "/var/log/sing-box/access.log",
		LnBin:            lookup("ln", "/bin/ln"),
		CtlBin:           lookup("launchctl", "/bin/launchctl"),
	}
}

// Linux returns the Debian-family layout.
func Linux() Layout {
	return Layout{
		OS:               "linux",
		ServiceName:      "sing-box",
		ProfilesDir:      "/etc/sing-box/profiles",
		ActiveConfigPath: "/etc/sing-box/config.json",
		ErrorLogPath:     "/var/log/sing-box/error.log",
		AccessLogPath:    "/var/log/sing-box/access.log",
		LnBin:            lookup("ln", "/usr/bin/ln"),
		CtlBin:           lookup("systemctl", "/usr/bin/systemctl"),
	}
}

// Windows returns the machine-wide Windows layout, honouring %ProgramData%.
func Windows() Layout {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	singBox := filepath.Join(programData, "sing-box")
	return Layout{
		OS:               "windows",
		ServiceName:      "sing-box",
		ProfilesDir:      filepath.Join(singBox, "profiles"),
		ActiveConfigPath: filepath.Join(singBox, "config.json"),
		ActiveNamePath:   filepath.Join(programData, "sbctl", "active-profile"),
		ErrorLogPath:     filepath.Join(singBox, "logs", "sing-box.err.log"),
		AccessLogPath:    filepath.Join(singBox, "logs", "sing-box.out.log"),
	}
}
