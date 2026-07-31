# sbctl

A cross-platform command-line tool for switching [sing-box](https://sing-box.sagernet.org/)
client profiles and controlling the privileged service that runs sing-box in TUN
mode.

Run it with no arguments to pick a profile interactively; every action is also a
subcommand so it can be scripted.

```
╭────────────────────────────────╮
│  sbctl                         │
│  state    ● running            │
│  profile  work                 │
│  server   vpn.example.com:443  │
│  tun      tun0                 │
│  pid      4711                 │
╰────────────────────────────────╯

  ▸ ● work      active
    ○ home
    ⚠ template  placeholders

    ✗ Turn off

  ↑/↓ move  ·  enter select  ·  q quit
```

## What makes it different

**A switch that fails is reported as a failure.** A sing-box configuration can
pass validation, start cleanly, and then die immediately — an unreachable server,
a stale Reality key, the wrong SNI. Telling the platform to restart the service
succeeds in all of those cases. sbctl samples the service's liveness before and
after activation and compares them, so a process that started and was immediately
restarted by its supervisor is recognised as a crash loop rather than a success.
When that happens it restores the previous profile, restarts on it, and tells you
where to look. An activation that silently takes your network down is the worst
thing this tool could do, so it is the thing it works hardest to avoid.

**Privileges are scoped to individual commands.** sbctl needs root for five
operations on macOS and four on Linux. Its sudo rules name exactly those, with
fixed arguments, rather than granting whole binaries — and `sbctl doctor` checks
each one individually. See [SECURITY.md](SECURITY.md), including an honest account
of what remains unfixed.

**Every failure comes with the command that fixes it.**

```
✗ work still contains placeholder values: TODO_SERVER_IP_OR_HOST and TODO_UUID
  → fill them in with: sbctl edit work
  → every TODO_ marker must be replaced before the profile can be used
```

## Platform support

| Platform            | Service                                        | Active config                                    | Logs                          |
| ------------------- | ---------------------------------------------- | ------------------------------------------------ | ----------------------------- |
| macOS               | launchd system daemon (`app.lexiflix.singbox`) | symlink at `/usr/local/etc/sing-box/config.json` | `/var/log/sing-box/error.log` |
| Debian-family Linux | systemd unit (`sing-box`)                      | symlink at `/etc/sing-box/config.json`           | journald                      |
| Windows             | WinSW service (`sing-box`)                     | copy at `%ProgramData%\sing-box\config.json`     | `%ProgramData%\sing-box\logs` |

## Install

### macOS and Debian-family Linux

```bash
make install
```

This builds the binary, installs it to `/usr/local/bin/sbctl`, installs or
verifies sing-box, creates the profiles directory, registers the service, and
writes `/etc/sudoers.d/sbctl` after validating it with `visudo`. It seeds a
template profile only when none exists, and does not start sing-box while
placeholder values remain.

On Linux the installer also confirms the packaged systemd unit actually reads the
config file sbctl manages, adding a drop-in if it does not — otherwise profile
switches would appear to work while having no effect.

### Windows

From an elevated PowerShell prompt:

```powershell
go build -o bin\sbctl.exe .\cmd\sbctl
.\scripts\install.ps1
```

This installs to `%ProgramFiles%\sbctl`, adds it to the machine PATH, installs or
verifies sing-box, downloads a pinned WinSW release, hardens the ACLs on
`%ProgramData%\sing-box`, and registers the service.

### Upgrading an existing installation

Re-run `make install`. The sudo rules changed in this release, and the new
narrower rules are only installed by the installer. Both upgrade orders are safe
in the meantime — the previous rules are a superset of the commands this build
issues — and `sbctl doctor` reports any mismatch.

## Usage

```bash
sbctl                     # status panel plus an interactive picker
sbctl list                # list profiles
sbctl use work            # switch, verify, roll back on failure
sbctl use                 # pick interactively
sbctl off                 # stop sing-box
sbctl status              # current state
sbctl logs                # follow service output
sbctl add work            # create from the template and edit
sbctl edit work           # edit, validate, reload if it is in service
sbctl rm work             # delete, with confirmation
sbctl check               # validate the active profile
sbctl check work          # validate a specific profile
sbctl ip                  # public IP, network and location
sbctl doctor              # diagnose the installation
sbctl version
sbctl completion zsh      # shell completions
```

### Commands

- `sbctl` renders the status panel and opens the picker. Long lists scroll; a
  filter appears once there are at least eight profiles. Escape or `q` cancels,
  exiting successfully.
- `sbctl use <name>` validates the profile, refuses it if placeholders remain,
  activates it, restarts the service, then confirms it stayed up — rolling back
  to the previous profile if it did not.
- `sbctl off` stops the service. The active profile is remembered.
- `sbctl status` reports run state, profile, server, TUN interface and pid, and
  flags an active config that points at a deleted profile.
- `sbctl logs` follows journald on Linux and the service log file elsewhere.
- `sbctl edit <name>` opens `$VISUAL`/`$EDITOR`, validates, and offers to edit
  again, discard, or keep on failure. Editing the profile in service also reloads
  it. Discarding restores the file's original permissions.
- `sbctl add <name>` creates from the template, then behaves like `edit`.
  Discarding removes the new file rather than leaving a broken profile behind.
- `sbctl rm <name>` deletes after confirmation. Deleting the profile in service
  requires `--force`, which stops sing-box first so it is never left reading a
  file that no longer exists.
- `sbctl check [name]` validates a profile and reports both placeholder markers
  and sing-box's own diagnostics.
- `sbctl doctor` checks sing-box, the profiles directory, each individual sudo
  permission, the service, and the active config.

### Global flags

| Flag            | Effect                                                          |
| --------------- | --------------------------------------------------------------- |
| `--json`        | machine-readable output; every payload carries `"schema": 1`    |
| `--plain`       | ASCII symbols, no borders or colour — the stable text interface |
| `--no-color`    | disable colour, keep Unicode (`NO_COLOR` is also honoured)      |
| `-q, --quiet`   | suppress informational output; errors still print               |
| `-v, --verbose` | print the commands, paths and timings sbctl uses                |

`--json` takes precedence over `--plain` and `--no-color` when combined, since
JSON carries no decoration to degrade.

Colour is disabled automatically when output is not a terminal, so piping is
already safe. Plain mode is selected automatically for `TERM=dumb` and terminals
narrower than 50 columns. Symbols do **not** change based on whether output is
piped: the same command produces the same bytes regardless of destination, so
`sbctl list | grep ●` behaves predictably. For scripting, prefer `--json`.

### Exit codes

| Code | Meaning                                                                     |
| ---- | --------------------------------------------------------------------------- |
| 0    | success — including cancelling an interactive prompt                        |
| 1    | general error: bad usage, unknown profile, I/O, network                     |
| 2    | validation: placeholders remain, or sing-box rejected the config            |
| 3    | service: it would not start or stop, or it failed the post-activation check |
| 4    | permission: sudo rules missing, or elevation declined                       |

## Profile template

`assets/skeleton.json` is the single canonical seed profile, embedded into the
binary and used by the installers. It contains `TODO_` markers which must all be
replaced before a profile can be activated. The check matches the `TODO_` prefix
generically, so adding a field to the template cannot bypass it.

## Uninstall

```bash
make uninstall            # macOS, Linux
.\scripts\uninstall.ps1   # Windows, elevated
```

This removes the service registration, sudo rules, active config, PATH entry
(Windows) and the binary. Profiles and logs are deliberately preserved; the paths
to remove manually are printed.

## Troubleshooting

Start with `sbctl doctor` — it checks each dependency individually and names what
is wrong.

**`sbctl is not allowed to manage the sing-box service without a password`**
The sudo rules are missing or stale. Run `sudo make install`, then `sbctl doctor`
to confirm which permission was not matched.

**`... still contains placeholder values`**
Run `sbctl edit <name>` and replace every `TODO_` marker.

**`started but service restarted N time(s) while starting up`**
The configuration is valid but not working, so sbctl reverted to the previous
profile. Run `sbctl logs` for sing-box's own diagnosis; the usual causes are a
wrong server address, an expired Reality key, or an unreachable SNI.

**`the active profile "x" no longer exists`**
The profile was deleted while still selected. Run `sbctl use <name>` to point the
service somewhere valid.

**A profile switch appears to work but traffic is unchanged (Linux)**
The packaged systemd unit may not read the config sbctl manages. `sudo make
install` installs a drop-in that pins it; `sbctl doctor` reports the mismatch.

## Development

```bash
make check    # gofmt, vet, tests
make test
make cross    # verify all three platforms still compile
make lint     # golangci-lint, if installed
```

Concurrency is not supported: sbctl assumes one instance at a time and takes no
lock. See [SECURITY.md](SECURITY.md) for the threat model and known limitations,
and [docs/manual-verification.md](docs/manual-verification.md) for what must be
checked on a real machine before release.
