# Task: Fix bugs and polish in the `sbctl` repo

You previously built `sbctl` (a Go CLI for managing sing-box profiles on macOS) in `~/code/sbctl`. A code review found one critical bug and several issues. Fix them end-to-end: edit files, rebuild, reinstall, and verify. Proceed autonomously; only stop if you hit a truly ambiguous decision.

## Context

Repo root: `~/code/sbctl`. The previous build is installed at `/usr/local/bin/sbctl` with supporting files at `/Library/LaunchDaemons/app.lexiflix.singbox.plist`, `/etc/sudoers.d/sbctl`, and `/usr/local/etc/sing-box/`. Preserve any user-created profile files in `/usr/local/etc/sing-box/profiles/` — never overwrite them.

## Fixes (in order)

### 1. CRITICAL: Corrupted `server_name` in `scripts/install.sh`

The heredoc-embedded seed profile has `"server_name": "[www.zoom.us](http://www.zoom.us)"` which is invalid. Change to `"server_name": "www.zoom.us"`.

### 2. Make `scripts/install.sh` idempotent and non-destructive

- Only write `sg-cloudflare.json` if it does **not** already exist:

```bash
if [[ ! -f "$profiles_dir/sg-cloudflare.json" ]]; then

cat > "$profiles_dir/sg-cloudflare.json" <<'JSON'

...

JSON

fi
```

- Only create the symlink if it's missing or broken.
- After writing the plist, validate it with `plutil -lint "$plist_path"` and abort if invalid.
- If the sudoers file already exists with identical content, skip rewriting it (compare with `cmp -s`).

### 3. Narrow the sudoers grant

The CLI binary only needs `/bin/ln` and `/bin/launchctl` at runtime. Rewrite `/etc/sudoers.d/sbctl` (via the same temp-file + `visudo -cf` pattern) to:

```bash
<USER> ALL=(root) NOPASSWD: /bin/ln, /bin/launchctl
```

`install.sh` already runs under `sudo` and doesn't need passwordless access for its own helpers. Update the generated sudoers file accordingly.

### 6. Fix `go.mod` directive

Change `go 1.25.0` to `go 1.25` (drop the patch version — that's the canonical form). Run `go mod tidy` afterward.

### 7. `useProfile` — safer ordering and clearer failure message

In `main.go`'s `useProfile`:

- Run `singbox.Check(path)` first (already done — good).
- Capture the old symlink target before overwriting.
- If `daemon.Restart` fails after the symlink was swapped, attempt to restore the old symlink and print:

```
✗ failed to restart sing-box; reverted active profile to <old>
```

Then return the error. If even the revert fails, print both errors clearly.

### 8. Simplify `daemon.Restart`

Currently it does `bootout` → `bootstrap` → `kickstart -k`. Replace with: if the daemon is already loaded (check via `launchctl print`), just `kickstart -k`. Otherwise `bootstrap` then rely on `KeepAlive=true` to start it. Drop the unconditional `bootout`. Retain the current behavior as a fallback if `kickstart -k` returns an error.

### 9. Empty-profiles UX in `runInteractive`

If `profile.List` returns zero profiles, print a helpful message and exit 0 instead of showing an empty picker:

```
No profiles found in /usr/local/etc/sing-box/profiles/.
Create one with:  sbctl add <name>
```

### 10. `check [name]` with broken symlink

When invoked with no args and the active symlink target doesn't exist, print:

```
✗ active config symlink is broken: /usr/local/etc/sing-box/config.json → <dangling>
```

and exit 1 instead of propagating the raw `os.Readlink` error.

### 11. `edit` — offer re-edit loop on validation failure

In `newEditCmd`, when `sing-box check` fails, prompt a three-way choice (via `huh.NewSelect`): **Re-edit**, **Revert**, **Keep broken file**. "Re-edit" re-opens `$EDITOR` on the same file and re-runs `sing-box check`; loop until success or user picks one of the other options.

### 12. `notify()` — safer AppleScript escaping

Replace the `%q`-based osascript call with an explicit JXA-style or properly AppleScript-escaped string. Simplest fix: strip non-ASCII-safe characters and replace any `"` in the message with `'` before interpolating. Verify by testing with a message containing a `"` character.

### 13. Documentation in `uninstall.sh`

Add comments (not interactivity) explaining that `/usr/local/etc/sing-box/profiles/` and `/var/log/sing-box/` are deliberately preserved to avoid destroying user data. Mention this in the README's Uninstall section too.

## After the fixes

1. `cd ~/code/sbctl && make fmt && make vet && make build` — all clean.
2. `make install` — runs idempotently, does not clobber the existing `sg-cloudflare.json` (verify by checking file hash before/after).
3. `sbctl check sg-cloudflare` — passes.
4. `sbctl use sg-cloudflare` — prints `✓ switched to sg-cloudflare`; `sudo launchctl print system/app.lexiflix.singbox` shows `state = running`; `ifconfig utun123` exists.
5. `curl -s https://api.ipify.org` returns the server's public IP (not the LAN IP).
6. `sbctl edit sg-cloudflare` — induce a JSON error (e.g., add a trailing comma), see the 3-way prompt; test Re-edit, fix the error, see success.
7. `sbctl off` — stops cleanly; `ifconfig utun123` gone.
8. `git status` — no untracked `*.json` profile files surfaced (gitignore working).

## Report format

Final markdown summary with:

- ✅/❌ per fix, per acceptance test
- Diff-style summary of changes per file
- Contents of the rewritten `/etc/sudoers.d/sbctl`
- Output of `plutil -lint` on the plist
- Any deviation with justification

## Guardrails

- Never overwrite a user-created profile JSON in `/usr/local/etc/sing-box/profiles/`.
- Never edit `/etc/sudoers`; only `/etc/sudoers.d/sbctl` via the temp-file + `visudo -cf` pattern.
- Do not leave the machine in a broken network state. If any step fails mid-way, run `sbctl off` before aborting.
- Preserve all commit history; make fixes in new commits with clear messages, not a rewrite.

Proceed.
