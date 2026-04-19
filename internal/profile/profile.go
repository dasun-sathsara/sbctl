package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Profile struct {
	Name string
	Path string
}

func List(profilesDir, activeLink string) ([]Profile, string, error) {
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", err
	}
	active, _ := ActiveName(activeLink)
	var profiles []Profile
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		profiles = append(profiles, Profile{
			Name: name,
			Path: filepath.Join(profilesDir, entry.Name()),
		})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, active, nil
}

func ActivePath(activeLink string) (string, error) {
	target, err := os.Readlink(activeLink)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(target) {
		return target, nil
	}
	return filepath.Clean(filepath.Join(filepath.Dir(activeLink), target)), nil
}

func ActiveName(activeLink string) (string, error) {
	path, err := ActivePath(activeLink)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), nil
}

func PathFor(profilesDir, name string) string {
	return filepath.Join(profilesDir, fmt.Sprintf("%s.json", name))
}

func Activate(activeLink, target string) error {
	if _, err := os.Stat(target); err != nil {
		return err
	}
	dir := filepath.Dir(activeLink)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_ = os.Remove(activeLink)
	return os.Symlink(target, activeLink)
}

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
