# sbctl

`sbctl` is a small macOS CLI for switching sing-box client profiles, checking status, following logs, and toggling the root LaunchDaemon that runs sing-box in TUN mode.

## Features

- Interactive default TUI picker built with `huh`
- Plain CLI flows for `list`, `use`, `off`, `status`, `logs`, `edit`, `add`, `rm`, `check`, and `version`
- Root-managed LaunchDaemon for sing-box TUN mode
- Profile switching through `/usr/local/etc/sing-box/config.json` symlink updates

## Install

```bash
make install
```

This will:

- build and install `/usr/local/bin/sbctl`
- create `/usr/local/etc/sing-box/profiles`
- install the LaunchDaemon plist at `/Library/LaunchDaemons/app.lexiflix.singbox.plist`
- write the seed profile to `/usr/local/etc/sing-box/profiles/zoom-reality.json`
- point `/usr/local/etc/sing-box/config.json` at the seed profile
- install `/etc/sudoers.d/sbctl` after validating it with `visudo -cf`

## Usage

```bash
sbctl
sbctl list
sbctl use zoom-reality
sbctl off
sbctl status
sbctl logs
sbctl edit zoom-reality
sbctl add work-vpn
sbctl rm work-vpn
sbctl check
sbctl check zoom-reality
sbctl version
```

## Commands

- `sbctl` renders a status panel and opens an interactive picker. Escape cancels.
- `sbctl list` prints profiles and marks the active profile with a green `●`.
- `sbctl use <name>` repoints the active config symlink, restarts the daemon, and posts a macOS notification.
- `sbctl off` stops the daemon and posts a macOS notification.
- `sbctl status` prints a bordered panel with run state, active profile, and configured TUN name.
- `sbctl logs` tails `/var/log/sing-box/error.log` until Ctrl-C, then exits `0`.
- `sbctl edit <name>` opens the profile in `$EDITOR` or `nano`, validates with `sing-box check -c`, and lets you re-edit, revert, or keep a broken file on failure.
- `sbctl add <name>` copies `assets/skeleton.json`, opens it in your editor, and validates it.
- `sbctl rm <name>` deletes a profile after confirmation, refusing the active one unless `--force`.
- `sbctl check [name]` validates a named profile or the active one, and reports a broken active symlink clearly.

## Uninstall

```bash
make uninstall
```

The uninstall script will:

- boot out the LaunchDaemon if loaded
- remove `/Library/LaunchDaemons/app.lexiflix.singbox.plist`
- remove `/etc/sudoers.d/sbctl`
- remove `/usr/local/etc/sing-box/config.json`

It intentionally leaves `/usr/local/etc/sing-box/profiles` and `/var/log/sing-box` in place so you do not destroy user profiles or operational logs by accident. Remove them manually if you want a full cleanup.

## Troubleshooting

- `sudo: a password is required`
  Ensure `/etc/sudoers.d/sbctl` exists, is mode `440`, and validates with `visudo -cf /etc/sudoers.d/sbctl`.
- `launchctl print` shows the daemon is not running
  Check `/var/log/sing-box/error.log`, then run `sbctl check <profile>` on the active profile.
- `ifconfig utun123` does not exist after `sbctl use`
  The profile may be valid JSON but not operational. Inspect `error.log` and confirm your VLESS/Reality values are correct.
- `sbctl edit` fails validation
  Choose revert when prompted, then fix the config incrementally and re-run `sbctl check <name>`.
