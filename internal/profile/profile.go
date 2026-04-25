package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type Profile struct {
	Name string
	Path string
}

type Activator interface {
	ActiveName() (string, error)
	ActivePath() (string, error)
	Activate(target string) (Rollback, error)
}

type Rollback interface {
	Rollback() error
	Description() string
	Known() bool
}

type SymlinkActivator struct {
	ActiveConfigPath string
	UseSudo          bool
}

func (a SymlinkActivator) ActiveName() (string, error) {
	path, err := a.ActivePath()
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), nil
}

func (a SymlinkActivator) ActivePath() (string, error) {
	path, err := ActivePath(a.ActiveConfigPath)
	if errors.Is(err, ErrActiveConfigNotManaged) {
		return "", nil
	}
	return path, err
}

func (a SymlinkActivator) Activate(target string) (Rollback, error) {
	oldTarget, oldTargetErr := os.Readlink(a.ActiveConfigPath)
	oldTargetKnown := oldTargetErr == nil
	if oldTargetErr != nil && !errors.Is(oldTargetErr, os.ErrNotExist) {
		return nil, oldTargetErr
	}
	if err := a.activate(target); err != nil {
		return nil, err
	}
	return symlinkRollback{activeConfigPath: a.ActiveConfigPath, oldTarget: oldTarget, known: oldTargetKnown, useSudo: a.UseSudo}, nil
}

func (a SymlinkActivator) activate(target string) error {
	if !a.UseSudo {
		return Activate(a.ActiveConfigPath, target)
	}
	cmd := exec.Command("sudo", "-n", "ln", "-sfn", target, a.ActiveConfigPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "a password is required") {
			return fmt.Errorf("sudo access is not configured; finish /etc/sudoers.d/sbctl setup and retry")
		}
		return fmt.Errorf("sudo ln -sfn failed: %w: %s", err, text)
	}
	return nil
}

type CopyActivator struct {
	ActiveConfigPath string
	ActiveNamePath   string
}

func (a CopyActivator) ActiveName() (string, error) {
	data, err := os.ReadFile(a.ActiveNamePath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (a CopyActivator) ActivePath() (string, error) {
	return a.ActiveConfigPath, nil
}

func (a CopyActivator) Activate(target string) (Rollback, error) {
	oldConfig, configErr := os.ReadFile(a.ActiveConfigPath)
	oldName, nameErr := os.ReadFile(a.ActiveNamePath)
	known := configErr == nil && nameErr == nil
	if configErr != nil && !errors.Is(configErr, os.ErrNotExist) {
		return nil, configErr
	}
	if nameErr != nil && !errors.Is(nameErr, os.ErrNotExist) {
		return nil, nameErr
	}
	if err := copyFileAtomic(target, a.ActiveConfigPath, 0o644); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(a.ActiveNamePath), 0o755); err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	if err := os.WriteFile(a.ActiveNamePath, []byte(name+"\n"), 0o644); err != nil {
		return nil, err
	}
	return copyRollback{activeConfigPath: a.ActiveConfigPath, activeNamePath: a.ActiveNamePath, oldConfig: oldConfig, oldName: oldName, known: known}, nil
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
		if errors.Is(err, syscall.EINVAL) {
			return "", ErrActiveConfigNotManaged
		}
		return "", err
	}
	if filepath.IsAbs(target) {
		return target, nil
	}
	return filepath.Clean(filepath.Join(filepath.Dir(activeLink), target)), nil
}

var ErrActiveConfigNotManaged = errors.New("active config is not managed by sbctl")

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

func HasPlaceholders(path string) (bool, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil, err
	}
	markers := []string{
		"TODO_SERVER_IP_OR_HOST",
		"TODO_UUID",
		"TODO_SNI_HOSTNAME",
		"TODO_REALITY_PUBLIC_KEY",
		"TODO_SHORT_ID",
	}
	var found []string
	for _, marker := range markers {
		if bytes.Contains(data, []byte(marker)) {
			found = append(found, marker)
		}
	}
	return len(found) > 0, found, nil
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

type symlinkRollback struct {
	activeConfigPath string
	oldTarget        string
	known            bool
	useSudo          bool
}

func (r symlinkRollback) Rollback() error {
	if !r.known {
		return fmt.Errorf("no prior symlink target was set")
	}
	return SymlinkActivator{ActiveConfigPath: r.activeConfigPath, UseSudo: r.useSudo}.activate(r.oldTarget)
}

func (r symlinkRollback) Description() string { return r.oldTarget }
func (r symlinkRollback) Known() bool         { return r.known }

type copyRollback struct {
	activeConfigPath string
	activeNamePath   string
	oldConfig        []byte
	oldName          []byte
	known            bool
}

func (r copyRollback) Rollback() error {
	if !r.known {
		return fmt.Errorf("no prior active config was set")
	}
	if err := writeFileAtomic(r.activeConfigPath, r.oldConfig, 0o644); err != nil {
		return err
	}
	return writeFileAtomic(r.activeNamePath, r.oldName, 0o644)
}

func (r copyRollback) Description() string {
	return strings.TrimSpace(string(r.oldName))
}

func (r copyRollback) Known() bool { return r.known }

func copyFileAtomic(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, in); err != nil {
		return err
	}
	return writeFileAtomic(dst, buf.Bytes(), perm)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sbctl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
