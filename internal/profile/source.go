// Remote source tracking for profiles fetched from a URL.
//
// A profile created by `sbctl pull` records where it came from in a sidecar
// file next to the profile itself, so `sbctl sync` can re-fetch it later.
// The sidecar uses its own extension, which keeps it invisible to List: only
// *.json files are treated as profiles.
package profile

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SourceExtension is the sidecar suffix recording a profile's remote URL.
const SourceExtension = ".url"

// SourcePathFor returns the sidecar path for a profile name. The name must
// already have passed ValidateName.
func SourcePathFor(profilesDir, name string) string {
	return filepath.Join(profilesDir, name+SourceExtension)
}

// ReadSource returns the recorded remote URL for a profile, or an empty
// string when the profile was not fetched remotely. A missing sidecar is not
// an error; anything else (permissions, a directory in the way) is.
func ReadSource(profilesDir, name string) (string, error) {
	data, err := os.ReadFile(SourcePathFor(profilesDir, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteSource records the remote URL a profile was fetched from.
func WriteSource(profilesDir, name, url string) error {
	return os.WriteFile(SourcePathFor(profilesDir, name), []byte(url+"\n"), 0o644)
}

// RemoveSource drops the recorded URL, for example when the profile itself is
// deleted. A missing sidecar is not an error.
func RemoveSource(profilesDir, name string) error {
	err := os.Remove(SourcePathFor(profilesDir, name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Sourced returns the names of every profile with a recorded remote URL,
// sorted. Profiles whose sidecar cannot be read are skipped rather than
// failing the whole listing: sync reports per-profile, and one unreadable
// file should not hide the rest.
func Sourced(profilesDir string) ([]string, error) {
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != SourceExtension {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), SourceExtension)
		if ValidateName(name) != nil {
			continue
		}
		if url, err := ReadSource(profilesDir, name); err == nil && url != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}
