package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"sbctl/internal/profile"
)

// maxRemoteProfileBytes bounds a downloaded profile so a misbehaving URL
// cannot fill the disk. Sing-box configurations are small JSON documents; 8
// MiB is generous.
const maxRemoteProfileBytes = 8 << 20

// fetchTimeout bounds the whole download, not just the connection setup.
const fetchTimeout = 30 * time.Second

func (a *App) pullCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "pull <name> <url>",
		Short:   "Download a profile from a URL",
		GroupID: groupProfiles,
		Long: "Fetch a sing-box configuration from a remote URL (a subscription link,\n" +
			"a raw file such as a GitHub Gist) and save it as a local profile.\n\n" +
			"The download is validated with sing-box before it is kept, and the source\n" +
			"URL is recorded so `sbctl sync` can re-fetch it later.",
		Example: "  sbctl pull gcp-sg https://example.com/singbox.json",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.pullAction(cmd.Context(), args[0], args[1])
		},
	}
}

func (a *App) syncCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "sync [name]",
		Short:   "Re-fetch remote profiles",
		GroupID: groupProfiles,
		Long: "Re-download profiles previously fetched with `sbctl pull` and replace the\n" +
			"local copies after validating them with sing-box.\n\n" +
			"With no name, every profile with a recorded source is synced. A synced\n" +
			"profile that is currently in service is reloaded through the same verified\n" +
			"activation as `sbctl use`, so a broken remote update restores the previous\n" +
			"configuration instead of taking the network down. Downloaded content that\n" +
			"fails validation never touches the stored profile.",
		Example: "  sbctl sync\n  sbctl sync gcp-sg",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return a.syncAllAction(cmd.Context())
			}
			return a.syncOneAction(cmd.Context(), args[0])
		},
	}
}

// pullAction fetches url and stores it as a new profile called name.
func (a *App) pullAction(ctx context.Context, name, rawURL string) error {
	if err := profile.ValidateName(name); err != nil {
		return err
	}
	if _, err := normaliseProfileURL(rawURL); err != nil {
		return err
	}
	if err := os.MkdirAll(a.Layout.ProfilesDir, 0o755); err != nil {
		return a.storeError(err, "create")
	}

	path := profile.PathFor(a.Layout.ProfilesDir, name)
	if _, err := os.Stat(path); err == nil {
		return (&Error{Code: ExitError, Message: fmt.Sprintf("a profile named %q already exists", name)}).
			withHints(
				fmt.Sprintf("refresh it from its source with: sbctl sync %s", name),
				fmt.Sprintf("or delete it first with: sbctl rm %s", name),
			)
	}

	body, err := fetchRemoteProfile(ctx, rawURL)
	if err != nil {
		return err
	}
	if err := a.checkRemoteBody(ctx, body); err != nil {
		return err
	}

	// O_EXCL so a concurrent pull can never silently overwrite.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return (&Error{Code: ExitError, Message: fmt.Sprintf("a profile named %q already exists", name)}).
				withHints(fmt.Sprintf("refresh it from its source with: sbctl sync %s", name))
		}
		return a.storeError(err, "write")
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return a.storeError(err, "write")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return a.storeError(err, "finish writing")
	}
	if err := profile.WriteSource(a.Layout.ProfilesDir, name, strings.TrimSpace(rawURL)); err != nil {
		_ = os.Remove(path)
		return a.storeError(err, "record the source URL for")
	}

	a.success("pulled %s", name)
	if markers, err := profile.Placeholders(path); err == nil && len(markers) > 0 {
		a.println(a.Theme.Warnf("it still has placeholders: %s", joinAnd(markers)))
		a.println(a.Theme.Hintf("fill them in with: sbctl edit %s", name))
	} else {
		a.println(a.Theme.Hintf("activate it with: sbctl use %s", name))
	}
	return nil
}

// syncOneAction re-fetches a single profile from its recorded source.
func (a *App) syncOneAction(ctx context.Context, name string) error {
	if err := profile.ValidateName(name); err != nil {
		return err
	}
	rawURL, err := profile.ReadSource(a.Layout.ProfilesDir, name)
	if err != nil {
		return a.storeError(err, "read")
	}
	if rawURL == "" {
		return (&Error{Code: ExitError, Message: fmt.Sprintf("%q has no recorded source URL", name)}).
			withHints(
				fmt.Sprintf("fetch it from a URL first with: sbctl pull %s <url>", name),
				"sync without a name refreshes every profile that does have one",
			)
	}
	if err := a.refreshProfile(ctx, name, rawURL); err != nil {
		return err
	}
	return a.reloadIfActive(ctx, name)
}

// syncAllAction re-fetches every profile with a recorded source. Inactive
// profiles are replaced in place; the active one, if any, goes last through
// the verified activation path so there is at most one service restart.
func (a *App) syncAllAction(ctx context.Context) error {
	names, err := profile.Sourced(a.Layout.ProfilesDir)
	if err != nil {
		return a.storeError(err, "read")
	}
	if len(names) == 0 {
		a.println(a.Theme.MutedStyle().Render("No profiles have a recorded source URL."))
		a.println(a.Theme.Hintf("fetch one first with: sbctl pull <name> <url>"))
		return nil
	}

	active, _ := a.Activator.ActiveName()
	var failed []string
	synced := 0
	for _, name := range names {
		if name == active {
			continue
		}
		rawURL, err := profile.ReadSource(a.Layout.ProfilesDir, name)
		if err != nil || rawURL == "" {
			failed = append(failed, name)
			continue
		}
		if err := a.refreshProfile(ctx, name, rawURL); err != nil {
			a.warn("could not sync %s: %v", name, err)
			failed = append(failed, name)
			continue
		}
		synced++
		a.success("synced %s", name)
	}
	if active != "" {
		if _, ok := profile.Find(mustListProfiles(a, ctx), active); ok {
			if rawURL, _ := profile.ReadSource(a.Layout.ProfilesDir, active); rawURL != "" {
				if err := a.refreshProfile(ctx, active, rawURL); err != nil {
					a.warn("could not sync %s: %v", active, err)
					failed = append(failed, active)
				} else if err := a.reloadIfActive(ctx, active); err != nil {
					return err
				} else {
					synced++
				}
			}
		}
	}

	if len(failed) > 0 {
		return (&Error{Code: ExitError, Message: fmt.Sprintf("synced %d profile(s), %d failed: %s", synced, len(failed), strings.Join(failed, ", "))}).
			withHints("inspect the service output with: sbctl logs")
	}
	return nil
}

// refreshProfile downloads rawURL, validates it, and replaces the stored
// profile. The stored file is only touched after the download passes
// validation, and its permissions are preserved.
func (a *App) refreshProfile(ctx context.Context, name, rawURL string) error {
	path := profile.PathFor(a.Layout.ProfilesDir, name)
	if _, err := os.Stat(path); err != nil {
		return (&Error{Code: ExitError, Message: fmt.Sprintf("profile %q is gone; its source URL is orphaned", name)}).
			withHints(fmt.Sprintf("fetch it again with: sbctl pull %s %s", name, rawURL))
	}

	body, err := fetchRemoteProfile(ctx, rawURL)
	if err != nil {
		return err
	}
	if err := a.checkRemoteBody(ctx, body); err != nil {
		return err
	}

	mode := fs.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(a.Layout.ProfilesDir, name+".sync-*")
	if err != nil {
		return a.storeError(err, "write")
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return a.storeError(err, "write")
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return a.storeError(err, "finish writing")
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		_ = os.Remove(tmpName)
		return a.storeError(err, "write")
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return a.storeError(err, "replace")
	}
	return nil
}

// reloadIfActive runs the verified activation when name is the profile in
// service, so a refreshed remote update gets the same crash-loop protection
// as `sbctl use`. Anything else is already in effect on disk.
func (a *App) reloadIfActive(ctx context.Context, name string) error {
	active, err := a.Activator.ActiveName()
	if err != nil {
		// Without knowing which profile is in service there is nothing to
		// reload. The sync itself succeeded, so report that rather than
		// failing a refresh that is already safely on disk.
		a.debugf("could not determine the active profile: %v", err)
		a.success("synced %s", name)
		return nil
	}
	if active != name {
		a.success("synced %s", name)
		return nil
	}
	return a.useAction(ctx, name)
}

// checkRemoteBody validates downloaded bytes with sing-box without touching
// any stored file.
func (a *App) checkRemoteBody(ctx context.Context, body []byte) error {
	tmp, err := os.CreateTemp("", "sbctl-remote-*.json")
	if err != nil {
		return a.storeError(err, "write")
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return a.storeError(err, "write")
	}
	if err := tmp.Close(); err != nil {
		return a.storeError(err, "finish writing")
	}
	// A download sing-box rejects must never reach the stored profiles, and
	// reports as a validation failure rather than a network error: the fetch
	// worked, the content is wrong.
	if err := a.Checker.Check(ctx, tmpName); err != nil {
		return (&Error{Code: ExitValidation, Message: "the downloaded file is not a valid sing-box configuration"}).
			wrap(err).
			withHints("check the URL points at the raw file, not a preview page")
	}
	return nil
}

// normaliseProfileURL rejects anything that is not an absolute http(s) URL.
// The restriction is deliberate: a file:// or custom-scheme source would turn
// `sbctl pull` into a confusing local-copy command with its own path rules.
func normaliseProfileURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", (&Error{Code: ExitError, Message: fmt.Sprintf("%q is not a valid http(s) profile URL", rawURL)}).
			withHints("use the full https:// address of the raw configuration file")
	}
	return u.String(), nil
}

// fetchRemoteProfile downloads a remote profile, bounding both time and size.
func fetchRemoteProfile(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := normaliseProfileURL(rawURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, failf("could not fetch %s: %v", u, err)
	}
	req.Header.Set("User-Agent", "sbctl")
	req.Header.Set("Accept", "application/json, */*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, (&Error{Code: ExitError, Message: fmt.Sprintf("could not download %s: %v", u, err)}).
			withHints("check the URL and that this machine is online")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, (&Error{Code: ExitError, Message: fmt.Sprintf("could not download %s: server replied %s", u, resp.Status)}).
			withHints("check the URL points at the raw file, not a preview page")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteProfileBytes+1))
	if err != nil {
		return nil, (&Error{Code: ExitError, Message: fmt.Sprintf("could not download %s: %v", u, err)}).
			withHints("check the URL and that this machine is online")
	}
	if len(body) > maxRemoteProfileBytes {
		return nil, (&Error{Code: ExitError, Message: fmt.Sprintf("refusing %s: larger than %d MiB, not a sing-box profile", u, maxRemoteProfileBytes>>20)}).
			withHints("check the URL points at the raw file, not a preview page")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, (&Error{Code: ExitError, Message: fmt.Sprintf("refusing %s: the server returned an empty file", u)}).
			withHints("check the URL points at the raw file, not a preview page")
	}
	return body, nil
}

// mustListProfiles lists profiles for the sync-all active check. A listing
// failure there must not hide sync results, so it degrades to empty.
func mustListProfiles(a *App, _ context.Context) []profile.Profile {
	profiles, err := profile.List(a.Layout.ProfilesDir)
	if err != nil {
		return nil
	}
	return profiles
}
