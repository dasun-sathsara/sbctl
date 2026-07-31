# Changelog

## Unreleased

Full refactor and UI overhaul. The command surface, on-disk paths, service
identifiers and launchd label are unchanged; see *Breaking changes* for the two
deliberate exceptions.

### Action required after upgrading

Run `sudo make install`. The sudo rules are now scoped to individual commands
instead of granting whole binaries, and only the installer writes them. Both
upgrade orders are safe in the meantime — the previous rules permit a superset of
what this build issues — and `sbctl doctor` reports any mismatch.

### Fixed

- **An activation that leaves sing-box dead is no longer reported as success.**
  A restart request being accepted was treated as proof the switch worked. A valid
  configuration can start and immediately die on an unreachable server or a stale
  key, and with launchd `KeepAlive` the crash-looping service still reports
  "running". sbctl now samples liveness either side of a settle delay, compares
  pid and restart counts, and on failure restores the previous profile, restarts
  on it, and exits 3.
- **`sbctl logs` was completely broken on Linux.** It checked for
  `/var/log/sing-box/error.log` before following logs, but on journald systems
  that file never exists, so the command failed without ever running
  `journalctl`. Followers now declare whether they depend on a file.
- **Unrestricted root via sudo.** `NOPASSWD: /bin/ln, /bin/launchctl` allowed
  arbitrary arguments, so `sudo ln -sfn` could replace any file on the system.
  Rules are now scoped to exact commands. See [SECURITY.md](SECURITY.md), which
  also documents what this does **not** fix.
- **Profile names were not validated**, so `sbctl add ../../x` wrote outside the
  profiles directory and `sbctl rm` could delete an unrelated file.
- **Windows elevation joined arguments with spaces**, so any profile name
  containing whitespace was split, and a crafted name could inject arguments.
- **The elevated Windows process ran hidden**, making every error it printed
  invisible while the parent reported success regardless.
- **Editing the active profile had no effect** until something else restarted the
  service. On Windows it was not even copied to the active config.
- **`sbctl rm --force` deleted the file a running root service was reading.** It
  now stops the service first.
- **`sbctl add` left broken profiles behind** on validation failure, and offered
  no retry, unlike `edit`. Both now share one loop.
- **`sbctl edit` discarded a file's original permissions** when reverting.
- **`list` and `status` hid the configured profile** whenever the service was
  stopped, so it was impossible to see what would come back.
- **A deleted active profile produced no diagnosis**, only an opaque sing-box
  failure later. It is now reported by `status`, `list`, `check` and `doctor`.
- **`sbctl rm` of a missing profile** surfaced a raw filesystem error after
  prompting for confirmation.
- **Placeholder detection used a hardcoded list of five markers**, so adding one
  to the template silently disabled the guard. It now matches `TODO_` generically.
- **Windows transient service states** (`START_PENDING`, `STOP_PENDING`) were
  reported as errors.
- **launchd `state = waiting` was reported as stopped**, masking a crashed
  service.
- **No subprocess had a timeout**, so a wedged `launchctl` or `systemctl` hung
  sbctl indefinitely.
- **`ERROR_CANCELLED` was detected by substring-matching `"1223"`** in a
  locale-dependent message.
- **`uninstall.ps1` never removed the PATH entry** `install.ps1` added.
- **`uninstall.sh` left `/usr/local/bin/sbctl`** behind; only the Makefile
  removed it.
- **The WinSW service XML was built by raw string interpolation** with no
  escaping.
- **`%ProgramData%\sbctl\active-profile` was written as ASCII** but read as UTF-8.
- **`apt-get install -y sing-box || true`** hid genuine failures.
- **`source /etc/os-release` clobbered script variables** including `ID` and
  `VERSION`.
- **macOS notifications interpolated text into AppleScript**, so a quote could
  inject script.
- **The default editor was hardcoded to `nvim`**, failing confusingly where it
  was not installed.

### Added

- `sbctl doctor` — checks sing-box, the profiles directory, each sudo permission
  individually, the service, the active config, and states the concurrency
  limitation.
- `--json` on every reporting command, with `"schema": 1` on each payload and
  structured errors.
- `--plain`, `--no-color`, `-q/--quiet`, `-v/--verbose`.
- Class-based exit codes: 1 general, 2 validation, 3 service, 4 permission.
  Cancelling remains 0.
- `sbctl use` with no argument opens the picker.
- Shell completions, including real profile names for `use`, `edit`, `rm` and
  `check`.
- `sbctl print-sudoers`, which the installer uses so the rules cannot drift from
  the commands the binary issues.
- Grouped `--help` with descriptions and examples on every command.
- WinSW pinned to an exact release, with SHA-256 enforcement via
  `SBCTL_WINSW_SHA256`.
- GPG fingerprint surfacing, pinnable with `SBCTL_SAGERNET_GPG_FPR`; package
  checksums via `SBCTL_SINGBOX_DEB_SHA256`.
- Windows ACL hardening on `%ProgramData%\sing-box`.
- A systemd drop-in when the packaged unit does not read the managed config,
  which otherwise made profile switches appear to work while doing nothing.
- `SECURITY.md` and `docs/manual-verification.md`.

### Changed

- Rebuilt as `cmd/sbctl` plus `internal/{cli,profile,service,platform,ui}`,
  replacing a 500-line `main.go`. Subprocesses, services, activation, elevation
  and validation are all behind interfaces, so commands are testable without a
  privileged host.
- One coherent visual language: adaptive colours that stay legible on light and
  dark terminals, one aligned key/value renderer shared by `status`, `ip` and
  `doctor`, and prompts themed from the same tokens as the picker.
- The picker scrolls long lists, clamps rather than wraps the cursor, shows a
  spinner during activation without blocking its own event loop, and offers a
  filter once there are at least eight profiles.
- Every user-facing error carries the command that fixes it.
- Tests grew from 4 functions to 125, covering roughly 275 cases once
  table-driven subtests are counted: golden files for every rendered surface,
  tables over captured real platform output, and a bidirectional proof that the
  generated sudo rules authorise exactly the commands the binary issues and
  nothing more.

### Breaking changes

- **Exit codes** are now grouped by cause. `$? -ne 0` is unaffected; only a
  script branching on the specific value `1` needs review.
- **`sbctl ip`** emits aligned columns instead of emoji-prefixed lines. The
  emoji were variable-width and never aligned. Use `--json` for parsing.
