# Manual verification

The automated suite covers parsing, command behaviour, rendering and the
correspondence between the sudo rules and the commands the binary issues. It
cannot cover everything, and this file is explicit about the gap rather than
leaving it implied.

## What the automated tests cannot reach

| Area | Why | Covered instead by |
|---|---|---|
| `ExecRunner`, real `sudo`, real `launchctl`/`systemctl`/`sc.exe` | Requires a privileged host with the service installed | Parsing is tested against captured real output; the runner is a thin `exec` wrapper |
| `ShellExecuteExW` elevation and UAC | Windows-only, needs an interactive consent prompt | `QuoteArg` and the error-code mapping are pure functions and are tested |
| Actual sing-box validation | The binary may be absent on a build machine | `profile.Checker` is an interface; `FakeChecker` drives the logic |
| Linux and Windows runtime behaviour | Development happens on macOS | `GOOS=linux` / `GOOS=windows` compilation plus this checklist |
| Terminal rendering with colour | Test output is not a TTY | Golden files pin the layout with colour disabled |

Run `make cross` to confirm all three platforms still compile before release.

## Per-platform checklist

Run through this on each platform before tagging a release. `sbctl doctor` should
pass at every step unless noted.

### Install and upgrade

- [ ] `make install` on a machine with no prior installation.
- [ ] `sbctl doctor` reports every check passing.
- [ ] `make install` again — it must be idempotent and must not duplicate the
      PATH entry, sudoers file or service registration.
- [ ] Upgrade over an install from the previous release: `git pull && make
      install`. Confirm profiles survive and `sbctl doctor` passes.
- [ ] **Upgrade ordering.** With the *new* binary and the *old* permissive
      sudoers still in place, `sbctl use <name>` must still work; the old rules
      are a superset of the commands issued.
- [ ] `sudo cat /etc/sudoers.d/sbctl` matches `sbctl print-sudoers` byte for byte.

### Core flow

- [ ] `sbctl` with no profiles prints the hint to create one.
- [ ] `sbctl add test` seeds the template, opens `$EDITOR`, and flags the
      remaining placeholders afterwards.
- [ ] `sbctl use test` refuses while placeholders remain, exit code 2.
- [ ] Fill in real values. `sbctl use test` activates, and `sbctl status` shows
      running with the correct pid, server and TUN name.
- [ ] `sbctl ip` shows the tunnel's exit address, not the local one.
- [ ] `sbctl off` stops the service. `sbctl status` still shows `test` as
      configured but not running.
- [ ] `sbctl` opens the picker, arrow keys move, `enter` activates, `q` exits 0.

### Failure handling — the important part

- [ ] Create a profile whose server address is valid syntax but unreachable
      (`203.0.113.1` is reserved for documentation and will not route).
- [ ] `sbctl check` passes on it: the config is genuinely valid.
- [ ] `sbctl use` that profile. It **must** report failure, restore the previous
      profile, restart on it, and exit 3. Confirm network connectivity is intact
      afterwards. This is the behaviour the release exists for.
- [ ] `sbctl logs` shows sing-box's own diagnosis of the failure.
- [ ] Delete the active profile's file by hand. `sbctl status` must report that
      the active profile no longer exists and suggest `sbctl use`.
- [ ] Remove `/etc/sudoers.d/sbctl`. Every privileged command must exit 4 with
      the repair instruction, and `sbctl doctor` must name the missing
      permissions individually.

### Destructive paths

- [ ] `sbctl rm <active-running-profile>` refuses and suggests `--force`.
- [ ] `sbctl rm <active-running-profile> --force` stops sing-box *before*
      deleting, then warns that the active config now refers to a missing file.
- [ ] `sbctl rm` with no terminal attached (`sbctl rm x < /dev/null | cat`)
      refuses rather than deleting unconfirmed.

### Output contracts

- [ ] `sbctl status --json | jq .schema` prints `1`. Same for `list`, `check`,
      `version`, `doctor`, and an error case.
- [ ] `sbctl list | cat` contains no ANSI escape sequences.
- [ ] `sbctl list | cat` and `sbctl list` on a TTY contain the same symbols —
      output must not change shape when piped.
- [ ] `NO_COLOR=1 sbctl status` is uncoloured but still Unicode.
- [ ] `sbctl status --plain` uses ASCII only and draws no borders.
- [ ] `TERM=dumb sbctl status` behaves as `--plain`.
- [ ] Resize the terminal to under 50 columns; `sbctl status` must not emit
      wrapped border fragments.
- [ ] `sbctl --help` shows the Profiles, Service and Diagnostics groups.
- [ ] Completions work: `source <(sbctl completion bash)`, then
      `sbctl use <TAB>` offers real profile names.

### Long and awkward input

- [ ] A profile with a 60-character name lists and activates without breaking
      alignment.
- [ ] With 100 profiles present, the picker scrolls and `/` filters.
- [ ] `sbctl add '../escape'`, `sbctl add 'a b'` and `sbctl add '-x'` are all
      rejected before touching the filesystem.

### macOS specific

- [ ] Works on Apple Silicon, where Homebrew installs to `/opt/homebrew`: the
      generated launchd plist must reference the real sing-box path.
- [ ] A desktop notification appears on a successful switch.
- [ ] `sudo launchctl print system/app.lexiflix.singbox` shows `runs` climbing
      when the config is broken — this is the signal the health check reads.

### Linux specific

- [ ] `sbctl logs` follows journald and does **not** fail because
      `/var/log/sing-box/error.log` is absent. This was previously broken
      outright.
- [ ] If the packaged unit does not reference `/etc/sing-box/config.json`, the
      installer adds `10-sbctl.conf` and `systemctl show -p ExecStart sing-box`
      then references the managed path.
- [ ] Verify on both a merged-`/usr` and, if available, a split-`/usr` system
      that the sudoers binary paths resolve correctly.

### Windows specific

- [ ] Run unelevated: `sbctl use x` raises a UAC prompt, and the elevated child's
      output is **visible** rather than hidden.
- [ ] Decline the UAC prompt: exit code 4 with a clear message.
- [ ] A profile name is passed intact through elevation — verify with a name
      containing a dot and a dash.
- [ ] `%ProgramData%\sing-box` ACLs: a standard user cannot create files there.
- [ ] `%ProgramData%\sbctl\active-profile` is UTF-8 without a BOM.
- [ ] `.\scripts\uninstall.ps1` removes the PATH entry; confirm with
      `[Environment]::GetEnvironmentVariable("Path","Machine")`.
- [ ] Restart while the service is in `STOP_PENDING` succeeds rather than
      reporting an error.
