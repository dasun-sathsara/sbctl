# sbctl Cross-Platform Support Plan

## Objective

Extend `sbctl` from a macOS-only sing-box profile controller into an admin-installed, platform-aware CLI for:

- macOS, preserving the current LaunchDaemon model.
- Debian-family Linux, including Debian, Ubuntu, and other systems with `ID=debian` or `ID_LIKE=debian`.
- Windows, using a machine-wide elevated install and a real service supervisor.

The target user flow is:

```text
git clone <repo>
cd sbctl
make install                  # macOS and Debian-family Linux
.\scripts\install.ps1         # Windows, from elevated PowerShell
```

All supported platforms should install or verify sing-box, install `sbctl`, seed a non-sensitive placeholder profile, configure the privileged service runner, and leave the `sbctl` command usable.

## Resolved Decisions

- Install requires admin privileges on every platform. This is the correct constraint because sing-box TUN mode needs elevated network privileges.
- macOS should auto-install sing-box through Homebrew when missing, and fail with a clear Homebrew requirement if Homebrew is unavailable.
- Linux support should target the Debian family, not only pure Debian.
- Windows should be machine-wide only for now.
- Windows should use WinSW as the service wrapper unless direct testing proves sing-box has reliable native Windows service behavior.
- The seed profile must not include real endpoint values, UUIDs, public keys, or other sensitive config. Seed only the placeholder skeleton and tell the user to edit it before starting.
- `sbctl use <profile>` should validate the selected profile, activate it, and restart/start the managed service on every platform. The command should fail before switching if the profile is still placeholder or invalid.

## Current State

The existing code is small enough to refactor, but macOS assumptions are embedded in runtime commands and install scripts.

- `main.go` hard-codes:
  - `/usr/local/etc/sing-box/profiles`
  - `/usr/local/etc/sing-box/config.json`
  - `/Library/LaunchDaemons/app.lexiflix.singbox.plist`
  - `/var/log/sing-box/error.log`
- `internal/daemon/daemon.go` is launchd-only and shells out to `sudo -n launchctl`.
- Runtime behavior assumes macOS tools:
  - `sudo ln -sfn`
  - `launchctl`
  - `osascript`
  - `tail -f`
- `scripts/install.sh` assumes Darwin utilities:
  - `launchctl`
  - `plutil`
  - `stat -f`
  - `wheel`
  - `/Library/LaunchDaemons`
- `Makefile install` assumes POSIX, sudo, and `/usr/local/bin`.
- `assets/skeleton.json` and the embedded seed profile in `scripts/install.sh` are inconsistent. The installer should seed from one canonical placeholder profile.
- Test coverage is thin. The current tests do not cover platform paths, service manager behavior, activation rollback, installer rendering, or config validation boundaries.

## External Constraints

- Official sing-box package-manager docs show a SagerNet APT repository path for Debian-style systems and `apt-get install sing-box`.
- Official sing-box docs say Linux packages usually include a `sing-box` systemd service.
- Official sing-box docs list Windows package-manager installs through Scoop, Chocolatey, or winget, but do not establish a systemd-equivalent service contract.
- TUN mode requires elevated privileges; a user-local Windows install would create misleading UX because the important path would still require elevation.

Source:

- Official sing-box package-manager docs: <https://sing-box.sagernet.org/installation/package-manager/>

## Architecture

### 1. Add a Platform Runtime Layer

Create a platform package so command code does not scatter `runtime.GOOS` checks.

```text
internal/platform/
  platform.go
  darwin.go
  linux.go
  windows.go
```

Core shape:

```go
type Runtime struct {
    OS               string
    ServiceName      string
    ProfilesDir      string
    ActiveConfigPath string
    ActiveNamePath   string
    ErrorLogPath      string
    AccessLogPath     string
    Manager           daemon.Manager
    Activator         profile.Activator
    Notifier          Notifier
    LogFollower       LogFollower
}
```

Command construction should become:

```go
rt, err := platform.Detect()
root := newRootCmd(rt)
```

Then command implementations use runtime fields rather than package-level constants.

### 2. Split Service Management Behind an Interface

Replace launchd-only daemon code with a platform-neutral interface and implementations.

```go
type Manager interface {
    Restart() error
    Stop() error
    Status() (RunState, error)
}
```

Implementations:

- `internal/daemon/launchd.go`
  - Preserve current behavior.
  - Keep `kickstart -k`.
  - Keep bootstrap fallback after failed restart.
- `internal/daemon/systemd.go`
  - `sudo -n systemctl restart sing-box`
  - `sudo -n systemctl stop sing-box`
  - `sudo -n systemctl is-active --quiet sing-box`
  - Map inactive/not-found states to `stopped`.
  - Map missing sudo rights to a concrete repair message.
- `internal/daemon/winsw.go`
  - Manage a WinSW-backed `sing-box` service.
  - Use PowerShell or `sc.exe query/start/stop` only as control tools, not as the service wrapper.
  - Report service missing, stopped, running, and failed states clearly.

### 3. Split Profile Activation by Platform

macOS and Linux can keep symlink activation:

```text
active config path -> selected profile JSON
```

Windows should use an atomic file copy into the active config path:

```text
C:\ProgramData\sing-box\profiles\<name>.json
C:\ProgramData\sing-box\config.json
C:\ProgramData\sbctl\active-profile
```

Reasoning:

- Windows symlinks are fragile across privilege modes and Developer Mode settings.
- A copied active config is boring but reliable.
- `active-profile` metadata preserves the current UX for `list`, `status`, and interactive mode.

### 4. Normalize Paths

| Platform | CLI path | Profiles dir | Active config | Logs |
|---|---|---|---|---|
| macOS | `/usr/local/bin/sbctl` | `/usr/local/etc/sing-box/profiles` | `/usr/local/etc/sing-box/config.json` | `/var/log/sing-box/*.log` |
| Debian family | `/usr/local/bin/sbctl` | `/etc/sing-box/profiles` | `/etc/sing-box/config.json` | `journalctl -u sing-box`, optionally `/var/log/sing-box/*.log` |
| Windows | `%ProgramFiles%\sbctl\sbctl.exe` | `%ProgramData%\sing-box\profiles` | `%ProgramData%\sing-box\config.json` | `%ProgramData%\sing-box\logs\*.log` through WinSW |

Use `/etc/sing-box` on Linux because it matches package and service expectations better than `/usr/local/etc/sing-box`.

### 5. Keep the Runtime UX Stable

The visible command surface should remain:

```text
sbctl
sbctl list
sbctl use <name>
sbctl off
sbctl status
sbctl logs
sbctl edit <name>
sbctl add <name>
sbctl rm <name>
sbctl check [name]
sbctl version
```

Behavioral rules:

- `sbctl use <name>` validates first, activates second, restarts/starts third.
- If restart fails, POSIX platforms should roll back the active symlink.
- Windows should roll back by restoring the previous active config copy and active-profile metadata.
- `sbctl off` stops the service on every platform.
- `sbctl logs` should use the native log source:
  - macOS: `/var/log/sing-box/error.log`
  - Linux: `journalctl -fu sing-box`
  - Windows: WinSW log file under `%ProgramData%\sing-box\logs`
- Notifications:
  - macOS keeps `osascript`.
  - Linux and Windows start as no-op notifications unless a native notification path is trivial and reliable.

## Installer Plan

### POSIX Installer

Keep `make install` as the primary command for macOS and Debian-family Linux.

`Makefile` should:

```text
build sbctl
sudo scripts/install.sh
```

`scripts/install.sh` should become a dispatcher:

```text
scripts/
  install.sh
  uninstall.sh
  lib/
    common.sh
    darwin.sh
    debian.sh
```

Do not grow the current installer into one large conditional shell file. The current macOS script is already dense.

#### macOS Installer

Responsibilities:

1. Detect Darwin with `uname`.
2. Install `/usr/local/bin/sbctl`.
3. If `sing-box` is missing:
   - install with `brew install sing-box` when Homebrew exists;
   - fail with a precise message when Homebrew is absent.
4. Create `/usr/local/etc/sing-box/profiles`.
5. Seed the placeholder profile only if absent.
6. Keep profile directory and profile JSON files owned by the invoking user.
7. Install `/Library/LaunchDaemons/app.lexiflix.singbox.plist`.
8. Validate plist with `plutil -lint`.
9. Install `/etc/sudoers.d/sbctl` only after `visudo -cf`.
10. Keep sudoers scope narrow.
11. Do not start sing-box automatically if the active profile still contains placeholders.

Preserve existing safety rules:

- Never edit `/etc/sudoers` directly.
- Never run the install flow as `sudo make install`; use `make install`.
- Preserve user-created profiles and logs on uninstall.
- Do not overwrite an existing profile.

#### Debian-Family Installer

Responsibilities:

1. Detect Linux with `uname`.
2. Read `/etc/os-release`.
3. Accept `ID=debian` or `ID_LIKE` containing `debian`.
4. Refuse unsupported Linux distributions with a precise error.
5. Install `sbctl` to `/usr/local/bin/sbctl`.
6. If `sing-box` is missing:
   - add the official SagerNet APT source;
   - install with `apt-get install sing-box`.
7. Create `/etc/sing-box/profiles`.
8. Seed the placeholder profile only if absent.
9. Ensure profile directory and profile JSON files are owned by the invoking user.
10. Point `/etc/sing-box/config.json` at the chosen profile only after it passes `sing-box check`.
11. Install `/etc/sudoers.d/sbctl` only after `visudo -cf`.
12. Grant only the runtime commands needed by `sbctl`, likely:
    - `/bin/ln` or `/usr/bin/ln`
    - `/bin/systemctl` or `/usr/bin/systemctl`
13. Enable the `sing-box` service, but do not start it while the profile still contains placeholders.

Linux installer validation:

```text
bash -n scripts/install.sh scripts/uninstall.sh scripts/lib/*.sh
go test ./...
go vet ./...
```

### Windows Installer

Add:

```text
scripts/install.ps1
scripts/uninstall.ps1
```

Primary install command:

```powershell
.\scripts\install.ps1
```

The script should require elevated PowerShell and fail early if not elevated.

Responsibilities:

1. Install `sbctl.exe` to `%ProgramFiles%\sbctl\sbctl.exe`.
2. Add `%ProgramFiles%\sbctl` to machine PATH.
3. Install or verify sing-box:
   - prefer `winget` when available;
   - fail with a clear manual install message if no supported package manager exists.
4. Create:
   - `%ProgramData%\sing-box\profiles`
   - `%ProgramData%\sing-box\logs`
   - `%ProgramData%\sbctl`
5. Seed the placeholder profile only if absent.
6. Install WinSW and configure a `sing-box` service running:

```text
sing-box.exe run -c C:\ProgramData\sing-box\config.json
```

7. Do not start the service until the active config passes `sing-box check` and placeholders have been replaced.
8. Preserve profiles and logs on uninstall.

## Placeholder Profile Policy

The repository should ship exactly one canonical placeholder profile, likely `assets/skeleton.json`.

Rules:

- No real server IPs.
- No real UUIDs.
- No real Reality public keys or short IDs.
- No production SNI or sensitive endpoint data.
- The installer seeds this as the default profile only if no profile exists.
- `sbctl use` and installer startup should fail clearly if required placeholder tokens remain.

Add validation for known placeholder markers such as:

```text
TODO_SERVER_IP_OR_HOST
TODO_UUID
TODO_SNI_HOSTNAME
TODO_REALITY_PUBLIC_KEY
TODO_SHORT_ID
```

This avoids the worst failure mode: installing a service that starts but can never work because the default config was still a template.

## Implementation Sequence

1. Normalize the seed profile.
   - Remove the real config from `scripts/install.sh`.
   - Use `assets/skeleton.json` as the single source of truth.
   - Add placeholder detection before `use` and before service start.
2. Add the platform runtime package.
   - Inject runtime config into the Cobra command tree.
   - Keep macOS paths and behavior identical through the new abstraction.
3. Split daemon management.
   - Preserve launchd behavior.
   - Add systemd manager.
   - Add WinSW-backed manager after Windows installer shape is in place.
4. Split profile activation.
   - POSIX symlink activator.
   - Windows atomic-copy activator.
5. Refactor logs and notifications behind small interfaces.
6. Split `scripts/install.sh` into dispatcher plus Darwin/Debian libraries.
7. Add Debian-family install and uninstall behavior.
8. Add Windows PowerShell install and uninstall behavior.
9. Update README with platform-specific install sections.
10. Add tests for:
    - platform path selection;
    - placeholder detection;
    - POSIX activation rollback;
    - Windows activation rollback;
    - service manager status mapping;
    - command construction using injected runtime config.
11. Validate locally:
    - `go test ./...`
    - `go vet ./...`
    - `bash -n scripts/install.sh scripts/uninstall.sh scripts/lib/*.sh`
12. Validate on real systems:
    - macOS host;
    - Debian or Ubuntu VM with systemd;
    - Windows VM with elevated PowerShell.

## Risks and Mitigations

- **Windows service behavior:** WinSW adds one managed dependency, but it avoids the unreliable assumption that a console binary can be registered directly as a Windows service.
- **Debian-family spread:** Ubuntu and Debian-like systems can differ in package manager defaults. Detect Debian-family systems, but keep failure messages precise and avoid claiming broad Linux support.
- **Package source trust:** When adding the SagerNet APT source, print exactly what is being added and where the keyring is installed.
- **Placeholder configs:** A placeholder seed is safer than shipping secrets, but it means first install cannot immediately start a working tunnel. Make this explicit in install output.
- **Privilege UX:** Since every supported platform is admin-installed, the tool should fail early with clear elevation errors rather than partially configuring user-local files.
- **Systemd in containers:** Do not use a normal container as proof of Linux support. Validate in a VM or systemd-enabled environment.

## Final Recommendation

Prioritize the work in this order:

1. Remove sensitive seed data and add placeholder validation.
2. Introduce platform runtime abstractions while preserving macOS behavior.
3. Add Debian-family systemd support.
4. Add Windows with elevated PowerShell plus WinSW.

This order reduces the highest current risk first: the repo should not ship real connection details while expanding automated install paths.
