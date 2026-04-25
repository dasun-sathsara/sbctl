# sbctl

`sbctl` is a platform-aware CLI for switching sing-box client profiles, checking status, following logs, and toggling the privileged service that runs sing-box in TUN mode.

## Features

- Interactive default TUI picker built with `huh`
- Plain CLI flows for `list`, `use`, `off`, `status`, `logs`, `edit`, `add`, `rm`, `check`, and `version`
- macOS LaunchDaemon support
- Debian-family Linux systemd support
- Windows machine-wide WinSW service support
- Placeholder seed profile with activation blocked until real endpoint values are supplied

## Install

### macOS

```bash
make install
```

The installer builds `/usr/local/bin/sbctl`, verifies or installs sing-box with Homebrew, creates `/usr/local/etc/sing-box/profiles`, installs `/Library/LaunchDaemons/app.lexiflix.singbox.plist`, and writes `/etc/sudoers.d/sbctl` after `visudo -cf` validation. It seeds `sg-cloudflare.json` from `assets/skeleton.json` only when absent and does not start sing-box while placeholders remain.

### Debian-Family Linux

```bash
make install
```

The installer accepts systems with `ID=debian` or `ID_LIKE` containing `debian`, installs sing-box through the SagerNet APT repository when missing, creates `/etc/sing-box/profiles`, enables the `sing-box` systemd service, and writes `/etc/sudoers.d/sbctl` after validation. It does not start sing-box while placeholders remain.

### Windows

From elevated PowerShell:

```powershell
go build -ldflags "-X main.version=dev" -o bin\sbctl.exe .
.\scripts\install.ps1
```

The installer copies `sbctl.exe` to `%ProgramFiles%\sbctl`, adds that directory to the machine PATH, verifies or installs sing-box with winget, creates `%ProgramData%\sing-box` and `%ProgramData%\sbctl`, installs WinSW, and configures the `sing-box` service. It does not start sing-box while placeholders remain.

## Usage

```bash
sbctl
sbctl list
sbctl use sg-cloudflare
sbctl off
sbctl status
sbctl logs
sbctl edit sg-cloudflare
sbctl add work-vpn
sbctl rm work-vpn
sbctl check
sbctl check sg-cloudflare
sbctl version
```

## Commands

- `sbctl` renders a status panel and opens an interactive picker. Escape cancels.
- `sbctl list` prints profiles and marks the active profile with a green `●`.
- `sbctl use <name>` validates the profile, blocks placeholder configs, activates it, restarts the service, and rolls back activation if restart fails.
- `sbctl off` stops the managed service.
- `sbctl status` prints a bordered panel with run state, active profile, and configured TUN name.
- `sbctl logs` tails the native log source: macOS file logs, Linux `journalctl -fu sing-box`, or the WinSW error log on Windows.
- `sbctl edit <name>` opens the profile in `$EDITOR` or `nvim`, validates with `sing-box check -c`, and lets you re-edit, revert, or keep a broken file on failure.
- `sbctl add <name>` copies `assets/skeleton.json`, opens it in your editor, and validates it.
- `sbctl rm <name>` deletes a profile after confirmation, refusing the active one unless `--force`.
- `sbctl check [name]` validates a named profile or the active one and reports broken active config state clearly.

## Placeholder Profile

`assets/skeleton.json` is the single canonical seed profile. It intentionally contains:

- `TODO_SERVER_IP_OR_HOST`
- `TODO_UUID`
- `TODO_SNI_HOSTNAME`
- `TODO_REALITY_PUBLIC_KEY`
- `TODO_SHORT_ID`

Replace those values before running `sbctl use <profile>`.

## Uninstall

macOS and Debian-family Linux:

```bash
make uninstall
```

Windows, from elevated PowerShell:

```powershell
.\scripts\uninstall.ps1
```

Uninstall removes service wiring, sudoers entries where applicable, the active config, and the installed CLI. It intentionally leaves profile directories and logs in place so user profiles and operational history are not destroyed by accident. Remove those directories manually for a full reset.

## Troubleshooting

- `sudo: a password is required`
  Ensure `/etc/sudoers.d/sbctl` exists, is mode `440`, and validates with `visudo -cf /etc/sudoers.d/sbctl`.
- `profile ... still contains placeholder values`
  Edit the profile and replace every `TODO_*` marker before activation.
- `systemctl` or `launchctl` shows the service is not running
  Check `sbctl logs`, then run `sbctl check <profile>` on the active profile.
- TUN interface does not exist after `sbctl use`
  The profile may be valid JSON but not operational. Inspect logs and confirm your VLESS/Reality values are correct.
