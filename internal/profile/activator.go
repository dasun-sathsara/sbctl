package profile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Activator reports and changes which profile the service reads.
//
// Both implementations share one contract that the previous code did not:
// ActiveName and ActivePath return ("", nil) when no profile is active. A fresh
// install is a normal state, not an error, and having one activator return an
// error where the other returned an empty string meant every caller needed
// platform-specific handling.
type Activator interface {
	ActiveName() (string, error)
	ActivePath() (string, error)
	Activate(target string) (Rollback, error)
}

// Rollback restores the activation state captured before a change.
type Rollback interface {
	// Rollback restores the previous active profile.
	Rollback() error
	// Description names the profile that Rollback would restore.
	Description() string
	// Known reports whether a previous state was captured. A fresh install has
	// nothing to roll back to.
	Known() bool
}

// SymlinkActivator points a symlink at the chosen profile. Used on macOS and
// Linux, where the service reads a root-owned symlink.
type SymlinkActivator struct {
	// ActiveConfigPath is the symlink the service reads.
	ActiveConfigPath string
	// Link performs the privileged symlink swap. When nil, the swap is
	// performed directly, which only succeeds if the caller can write the
	// parent directory.
	Link func(ctx context.Context, target string) error
}

// ActivePath resolves the symlink target.
//
// A missing symlink and a regular file in its place both mean "sbctl is not
// managing this config", which is reported as an empty path rather than an
// error so that `status` and `list` work on an unconfigured host.
func (a SymlinkActivator) ActivePath() (string, error) {
	target, err := os.Readlink(a.ActiveConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrInvalid) || isNotSymlink(err) {
			return "", nil
		}
		return "", err
	}
	if filepath.IsAbs(target) {
		return target, nil
	}
	return filepath.Clean(filepath.Join(filepath.Dir(a.ActiveConfigPath), target)), nil
}

func (a SymlinkActivator) ActiveName() (string, error) {
	path, err := a.ActivePath()
	if err != nil || path == "" {
		return "", err
	}
	return NameFor(path), nil
}

func (a SymlinkActivator) Activate(target string) (Rollback, error) {
	previous, err := a.ActivePath()
	if err != nil {
		return nil, err
	}
	if err := a.link(target); err != nil {
		return nil, err
	}
	return symlinkRollback{activator: a, previous: previous}, nil
}

func (a SymlinkActivator) link(target string) error {
	if a.Link != nil {
		return a.Link(context.Background(), target)
	}
	if err := os.MkdirAll(filepath.Dir(a.ActiveConfigPath), 0o755); err != nil {
		return err
	}
	// Build the new link beside the destination and rename it over the old one.
	// os.Symlink cannot target an existing path, and removing the live config
	// first would leave a window in which the service has no config at all.
	staging := a.ActiveConfigPath + ".sbctl-staging"
	if err := os.Remove(staging); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(target, staging); err != nil {
		return err
	}
	if err := os.Rename(staging, a.ActiveConfigPath); err != nil {
		_ = os.Remove(staging)
		return err
	}
	return nil
}

type symlinkRollback struct {
	activator SymlinkActivator
	previous  string
}

func (r symlinkRollback) Known() bool         { return r.previous != "" }
func (r symlinkRollback) Description() string { return NameFor(r.previous) }

func (r symlinkRollback) Rollback() error {
	if !r.Known() {
		return fmt.Errorf("there was no previous active profile to restore")
	}
	return r.activator.link(r.previous)
}

// isNotSymlink reports whether a Readlink error means the path exists but is not
// a symlink. The errno differs between Unix (EINVAL) and Windows.
func isNotSymlink(err error) bool {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return false
	}
	msg := strings.ToLower(pathErr.Err.Error())
	return strings.Contains(msg, "invalid argument") ||
		strings.Contains(msg, "the file or directory is not a reparse point") ||
		strings.Contains(msg, "incorrect function")
}

// CopyActivator copies the chosen profile over the active config and records its
// name alongside. Used on Windows, where symlinks need privileges that a service
// account may not grant.
type CopyActivator struct {
	// ActiveConfigPath is the file the service reads.
	ActiveConfigPath string
	// ActiveNamePath records which profile was copied, since a copy carries no
	// reference back to its source.
	ActiveNamePath string
}

const (
	// activeConfigPerm keeps the active config readable only by its owner. A
	// sing-box config carries credentials — VLESS UUIDs, Reality keys — so it is
	// not something to leave world-readable. The service runs elevated and can
	// read it regardless.
	activeConfigPerm os.FileMode = 0o600
	// activeNamePerm applies to the marker file, which holds only a profile name.
	activeNamePerm os.FileMode = 0o644
)

func (a CopyActivator) ActiveName() (string, error) {
	data, err := os.ReadFile(a.ActiveNamePath)
	if err != nil {
		// Matching SymlinkActivator: nothing recorded means nothing active.
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (a CopyActivator) ActivePath() (string, error) {
	name, err := a.ActiveName()
	if err != nil || name == "" {
		return "", err
	}
	return a.ActiveConfigPath, nil
}

func (a CopyActivator) Activate(target string) (Rollback, error) {
	oldConfig, configErr := os.ReadFile(a.ActiveConfigPath)
	if configErr != nil && !errors.Is(configErr, os.ErrNotExist) {
		return nil, configErr
	}
	oldName, nameErr := os.ReadFile(a.ActiveNamePath)
	if nameErr != nil && !errors.Is(nameErr, os.ErrNotExist) {
		return nil, nameErr
	}

	if err := copyFileAtomic(target, a.ActiveConfigPath, activeConfigPerm); err != nil {
		return nil, err
	}
	name := NameFor(target)
	if err := writeFileAtomic(a.ActiveNamePath, []byte(name+"\n"), activeNamePerm); err != nil {
		// The config is already in place but the record of which profile it came
		// from is not. Put the old config back so the service and the reported
		// profile name cannot disagree; a caller that sees an error must be able
		// to trust that nothing changed.
		if configErr == nil {
			_ = writeFileAtomic(a.ActiveConfigPath, oldConfig, activeConfigPerm)
		} else {
			_ = os.Remove(a.ActiveConfigPath)
		}
		return nil, err
	}
	return copyRollback{
		activator: a,
		config:    oldConfig,
		name:      oldName,
		// Having captured the previous config is what makes a rollback possible.
		// The name file is only a label: a config placed by hand has none, and
		// refusing to restore it on that basis would discard the very content
		// the user is relying on.
		known: configErr == nil,
	}, nil
}

// Clear removes the recorded active profile name, marking nothing as active.
func (a CopyActivator) Clear() error {
	if err := os.Remove(a.ActiveNamePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type copyRollback struct {
	activator CopyActivator
	config    []byte
	name      []byte
	known     bool
}

func (r copyRollback) Known() bool { return r.known }

// Description names the profile Rollback would restore, falling back to a phrase
// that still reads correctly when the previous config was not one sbctl had
// recorded a name for.
func (r copyRollback) Description() string {
	if name := strings.TrimSpace(string(r.name)); name != "" {
		return name
	}
	return "the previous configuration"
}

func (r copyRollback) Rollback() error {
	if !r.known {
		return fmt.Errorf("there was no previous active config to restore")
	}
	if err := writeFileAtomic(r.activator.ActiveConfigPath, r.config, activeConfigPerm); err != nil {
		return err
	}
	if len(r.name) == 0 {
		// Nothing was recorded before, so clear the marker rather than leaving
		// the newly activated name pointing at restored old content.
		return r.activator.Clear()
	}
	return writeFileAtomic(r.activator.ActiveNamePath, r.name, activeNamePerm)
}

func copyFileAtomic(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, data, perm)
}

// writeFileAtomic writes via a temporary file in the destination directory and
// renames it into place, so a reader never observes a partially written config.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sbctl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	// The temp file is removed on every path that leaves the destination intact.
	// It is deliberately kept when the destination has already been replaced or
	// removed, because then it holds the only copy of the data — unconditionally
	// deleting it there could leave no config on disk at all.
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	// Flush before the rename so a crash cannot leave a present-but-empty config
	// where a valid one used to be.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err == nil {
		keep = true
		return nil
	}
	// Windows refuses to rename onto an existing file, so fall back to
	// replacing it.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		keep = true
		return fmt.Errorf("could not move the new config into place; it is preserved at %s: %w", tmpName, err)
	}
	keep = true
	return nil
}
