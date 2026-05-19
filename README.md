# sbctl

A lightweight CLI and system tray app for managing [sing-box](https://sing-box.sagernet.org/) profiles across macOS, Linux, and Windows.

## Features

- **Interactive TUI** — profile picker with status panel
- **System tray app** — menu bar icon that changes color based on proxy state (macOS/Windows)
- **Profile management** — `list`, `use`, `add`, `rm`, `edit`, `check`
- **Service control** — `off`, `status`, `logs`
- **Cross-platform** — macOS (launchd), Linux (systemd), Windows (WinSW)
- **Auto-elevation** — UAC prompt on Windows, passwordless sudo on macOS/Linux

## Install

```bash
# macOS / Linux
make install

# Windows (elevated PowerShell)
go build -ldflags "-X main.version=dev" -o bin\sbctl.exe .
.\scripts\install.ps1
```

## Usage

```bash
sbctl              # interactive profile picker
sbctl list         # show profiles
sbctl use <name>   # switch profile & restart service
sbctl off          # stop sing-box
sbctl status       # show state, active profile, TUN interface
sbctl logs         # tail error logs
sbctl edit <name>  # edit + validate in $EDITOR
sbctl add <name>   # create from skeleton template
sbctl rm <name>    # delete (blocked if active, unless --force)
sbctl check [name] # validate with sing-box check
sbctl ip           # show public IP & location
```

## System Tray App

A lightweight tray icon for quick profile switching without opening a terminal.

```bash
# Build for current platform
make tray

# Windows (no console window)
make tray-windows

# macOS (requires CGo, build on macOS)
make tray-darwin
```

The tray icon is **green** when running, **gray** when stopped, and **red** on error. Click to see profiles and switch instantly.

## Placeholder Profile

New installs seed `assets/skeleton.json` with placeholder values. Replace all `TODO_*` markers before activating:

```
TODO_SERVER_IP_OR_HOST, TODO_UUID, TODO_SNI_HOSTNAME,
TODO_REALITY_PUBLIC_KEY, TODO_SHORT_ID
```

## Uninstall

```bash
# macOS / Linux
make uninstall

# Windows (elevated PowerShell)
.\scripts\uninstall.ps1
```

Profile directories and logs are preserved. Remove them manually for a full reset.

## Troubleshooting

| Problem | Fix |
|---------|-----|
| `sudo: a password is required` | Verify `/etc/sudoers.d/sbctl` exists and passes `visudo -cf` |
| `profile ... still contains placeholder values` | Edit the profile and replace `TODO_*` markers |
| Service not running after `sbctl use` | Run `sbctl logs` and `sbctl check <profile>` |
| Windows: "Access denied" | Run from elevated terminal, or let UAC prompt handle it |
