// Package profile models sing-box configuration profiles on disk and controls
// which one the service currently reads.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Extension is the required profile file suffix.
const Extension = ".json"

// Profile is one named configuration file.
type Profile struct {
	// Name is the filename without its extension.
	Name string
	// Path is the absolute file location.
	Path string
	// Placeholders lists unreplaced template markers. A profile with any
	// placeholders is not activatable.
	Placeholders []string
}

// Ready reports whether the profile has no outstanding placeholders.
func (p Profile) Ready() bool { return len(p.Placeholders) == 0 }

// ErrInvalidName reports a profile name that cannot be used as a filename.
var ErrInvalidName = errors.New("invalid profile name")

// maxNameLength keeps names within every supported filesystem's limits while
// leaving room for the extension.
const maxNameLength = 64

// windowsReservedNames are the DOS device names Windows still resolves in any
// directory. A profile called "nul" would become NUL.json, which silently
// discards everything written to it, and "con" would block waiting on the
// console. They are rejected on every platform so that a profile directory stays
// portable and a name means the same thing everywhere.
var windowsReservedNames = map[string]struct{}{
	"con": {}, "prn": {}, "aux": {}, "nul": {},
	"com1": {}, "com2": {}, "com3": {}, "com4": {}, "com5": {},
	"com6": {}, "com7": {}, "com8": {}, "com9": {},
	"lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {}, "lpt5": {},
	"lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
}

// ValidateName rejects any name that would escape the profiles directory or
// produce a surprising filename.
//
// Without this, a name is joined straight onto the profiles directory, so
// `sbctl add ../../thing` writes outside the intended tree and `sbctl rm` can
// delete an unrelated file. It also guarantees a profile path can never contain
// a separator, which is what keeps the narrow sudoers glob effective.
func ValidateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: name must not be empty", ErrInvalidName)
	case len(name) > maxNameLength:
		return fmt.Errorf("%w: %q is longer than %d characters", ErrInvalidName, name, maxNameLength)
	case name == "." || name == "..":
		return fmt.Errorf("%w: %q is a directory reference", ErrInvalidName, name)
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("%w: %q must not start with a dash, which would be read as a flag", ErrInvalidName, name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("%w: %q must not start with a dot", ErrInvalidName, name)
	case strings.HasSuffix(name, "."):
		// Windows silently strips trailing dots, so "work." and "work" would be
		// the same file while sbctl treated them as two separate profiles.
		return fmt.Errorf("%w: %q must not end with a dot", ErrInvalidName, name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("%w: %q contains %q; use letters, digits, dot, dash or underscore", ErrInvalidName, name, r)
		}
	}
	// Windows resolves device names from the stem, so "nul.json" is still NUL.
	stem, _, _ := strings.Cut(strings.ToLower(name), ".")
	if _, reserved := windowsReservedNames[stem]; reserved {
		return fmt.Errorf("%w: %q is a reserved device name on Windows", ErrInvalidName, name)
	}
	return nil
}

// PathFor returns the file path for a profile name. The name must already have
// passed ValidateName.
func PathFor(profilesDir, name string) string {
	return filepath.Join(profilesDir, name+Extension)
}

// NameFor returns the profile name for a config file path.
func NameFor(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// List returns every profile in profilesDir, sorted by name. A missing
// directory yields no profiles rather than an error, so a fresh install reports
// "no profiles" instead of a filesystem error.
func List(profilesDir string) ([]Profile, error) {
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	profiles := make([]Profile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), Extension) {
			continue
		}
		path := filepath.Join(profilesDir, entry.Name())
		markers, err := Placeholders(path)
		if err != nil {
			// An unreadable profile is still worth listing; the specific error
			// surfaces when the user tries to use it.
			markers = nil
		}
		profiles = append(profiles, Profile{
			Name:         NameFor(entry.Name()),
			Path:         path,
			Placeholders: markers,
		})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

// Find returns the named profile from a slice, reporting whether it exists.
func Find(profiles []Profile, name string) (Profile, bool) {
	for _, p := range profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// Names returns the profile names in order.
func Names(profiles []Profile) []string {
	names := make([]string, len(profiles))
	for i, p := range profiles {
		names[i] = p.Name
	}
	return names
}

// placeholderPattern matches any template marker in the seed profile.
//
// Matching the TODO_ prefix generically, rather than a hardcoded list of the
// five markers that happened to exist, means adding a field to the skeleton
// cannot silently disable the guard that stops a template being activated.
var placeholderPattern = regexp.MustCompile(`TODO_[A-Z0-9_]+`)

// Placeholders returns the sorted, deduplicated template markers still present
// in a profile.
func Placeholders(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return PlaceholdersIn(data), nil
}

// PlaceholdersIn returns the markers present in profile content.
func PlaceholdersIn(data []byte) []string {
	matches := placeholderPattern.FindAll(data, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	found := make([]string, 0, len(matches))
	for _, m := range matches {
		marker := string(m)
		if _, ok := seen[marker]; ok {
			continue
		}
		seen[marker] = struct{}{}
		found = append(found, marker)
	}
	sort.Strings(found)
	return found
}

// InterfaceName returns the TUN interface name declared by a profile, or an
// empty string when the profile leaves it for sing-box to choose.
func InterfaceName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var payload struct {
		Inbounds []struct {
			Type          string `json:"type"`
			InterfaceName string `json:"interface_name"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	for _, inbound := range payload.Inbounds {
		if inbound.Type == "tun" && inbound.InterfaceName != "" {
			return inbound.InterfaceName, nil
		}
	}
	return "", nil
}

// Server returns a short "host:port" summary of the first non-direct outbound,
// used to give the profile list some context beyond a bare name.
func Server(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var payload struct {
		Outbounds []struct {
			Type       string `json:"type"`
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	for _, out := range payload.Outbounds {
		if out.Server == "" || out.Type == "direct" || out.Type == "block" {
			continue
		}
		if out.ServerPort > 0 {
			return fmt.Sprintf("%s:%d", out.Server, out.ServerPort), nil
		}
		return out.Server, nil
	}
	return "", nil
}
