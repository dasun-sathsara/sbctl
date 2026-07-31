# sbctl — Refactor & UI/UX Overhaul (FINAL PLAN)

Revision 2. Supersedes revision 1 after two independent adversarial reviews.
Every open question below is resolved. No TBDs.

---

## 0. Verified facts (measured, not assumed)

These were checked against the real tree and the real machine, and they overturn
several revision-1 assumptions.

| # | Claim | Verdict |
|---|-------|---------|
| V1 | `lipgloss` v1.1.0 already strips colour when stdout is not a TTY and when `NO_COLOR` is set | **TRUE.** Empirical: piped render produced `"HELLO"` (no ANSI); `lipgloss.ColorProfile()` resolved to `termenv.Ascii` (3). Root cause of "ANSI when piped" was never a missing detector — the default renderer works. Revision 1's custom `Detect()`/`NoColor` machinery is deleted. |
| V2 | Picker panics with zero profiles | **FALSE.** `main.go:458` returns early before `ui.PickProfile`. Removed from the bug list; a defensive cursor clamp is still added because it costs 3 lines. |
| V3 | `sbctl logs` is broken on Linux | **TRUE.** `main.go:163` stats `rt.ErrorLogPath`; `platform.go:79` sets it to `/var/log/sing-box/error.log`, which does not exist on a journald system, so it returns before ever reaching `journalctl -fu sing-box`. Real, user-visible, total failure of the command. |
| V4 | `/bin/ln` and `/bin/launchctl` exist on modern macOS | **TRUE.** Both present, `root:wheel`. sudo 1.9.17p2. |
| V5 | `launchctl print` exposes liveness fields | **TRUE.** Emits `state = running`, `runs = N`, `pid = N`, `last exit code = (never exited) | N`. This is the mechanism for crash-loop detection. |
| V6 | `platform.RunElevated` is dead code on Unix | **TRUE** (`IsElevated()` hardcodes `true`), but making it an interface purely to inject a Unix no-op is busywork. It becomes an interface only because the Windows path genuinely needs faking. |
| V7 | `PathFor` traversal is CRITICAL | **OVERSTATED.** `filepath.Join` normalises `..`, so `sbctl add ../../x` writes `<parent>/x.json` as the *invoking user*. It is a real input-validation defect (HIGH) but the root-escalation vector is the unrestricted sudoers, not this. Downgraded to HIGH. |
| V8 | `go test` stdout is not a TTY | **TRUE**, therefore golden files are ANSI-free by construction. `TestMain` pins `lipgloss.SetColorProfile(termenv.Ascii)` to make that guarantee explicit rather than incidental. |

---

## 1. Non-negotiable behaviour contract (must not change)

Commands and flags: `list`, `use`, `off`, `status`, `logs`, `edit`, `add`, `rm`,
`check`, `ip`, `version`; `rm --force`.

Paths: `/usr/local/etc/sing-box/{profiles,config.json}`,
`/etc/sing-box/{profiles,config.json}`,
`%ProgramData%\sing-box\{profiles,config.json,logs}`,
`%ProgramData%\sbctl\active-profile`, `/var/log/sing-box/{error,access}.log`,
`/etc/sudoers.d/sbctl`, `/Library/LaunchDaemons/app.lexiflix.singbox.plist`,
`/usr/local/bin/sbctl`.

Identifiers: launchd label `app.lexiflix.singbox`; systemd unit `sing-box`;
Windows service `sing-box`.

Mechanics: symlink activation on Unix, copy activation on Windows.
`make install` / `make uninstall` keep working.

Exit code 0 still means success **and still covers user cancellation.**

---

## 2. Bug triage

### 2.1 Fixed in this refactor

| ID | Sev | Defect | Where fixed |
|----|-----|--------|-------------|
| B1 | CRIT | Unrestricted `NOPASSWD: /bin/ln, /bin/launchctl` (and Linux `ln`,`systemctl`) is a local root escalation | Ph6 |
| B2 | CRIT | "Activation succeeded" is reported when the daemon was merely *told* to restart. A config that passes `sing-box check` but is operationally dead takes the user offline and prints `✓`. `KeepAlive=true` means a crash-looping process still reports `state = running`. | Ph2+Ph4 |
| B3 | HIGH | `sbctl logs` totally broken on Linux (V3) | Ph4 |
| B4 | HIGH | Windows `RunElevated` joins args with spaces, unquoted — any profile name with a space breaks, and it is an argument-injection vector | Ph3 |
| B5 | HIGH | Windows elevated child runs `SW_HIDE`, so every error it prints is invisible; parent then prints `✓` regardless | Ph3 |
| B6 | HIGH | No profile-name validation; `PathFor` accepts `../` (V7) | Ph2 |
| B7 | HIGH | `edit` of the *active* profile never restarts the daemon, so the edit silently has no effect (and on Windows is not even copied to the active config) | Ph4 |
| B8 | HIGH | `rm --force` on the active running profile deletes the file out from under a running root daemon | Ph4 |
| B9 | MED | `CopyActivator.ActiveName` returns an error on fresh install where `SymlinkActivator` returns `""` — asymmetric contract | Ph2 |
| B10 | MED | `SymlinkActivator.ActivePath` propagates `ENOENT` as an error instead of "nothing active" | Ph2 |
| B11 | MED | `list`/`status` blank the profile name whenever the daemon is stopped, so a configured-but-stopped profile is invisible | Ph4 |
| B12 | MED | `add` has no re-edit/cleanup on validation failure (unlike `edit`), leaving a broken file behind | Ph4 |
| B13 | MED | `edit --revert` writes `0o644`, discarding the file's original mode | Ph4 |
| B14 | MED | WinSW `Status()` returns `StateError` for transient `START_PENDING`/`STOP_PENDING` | Ph2 |
| B15 | MED | launchd maps `state = waiting` to Stopped, masking a crash-looping service | Ph2 |
| B16 | MED | Windows `ERROR_CANCELLED` detected via `strings.Contains(err, "1223")` | Ph3 |
| B17 | MED | Windows `install.ps1` writes `active-profile` as ASCII; Go reads UTF-8 | Ph6 |
| B18 | MED | WinSW service XML built by raw string interpolation — no XML escaping | Ph6 |
| B19 | MED | `uninstall.ps1` never removes the machine PATH entry it added | Ph6 |
| B20 | MED | No timeout on any subprocess; a wedged `launchctl`/`systemctl` hangs sbctl forever | Ph2 |
| B21 | LOW | `rm` of a nonexistent profile surfaces a raw `os` error | Ph4 |
| B22 | LOW | Deleting the configured profile leaves a dangling active symlink with no diagnosis | Ph4 |
| B23 | LOW | Placeholder detection is a hardcoded 5-marker list; skeleton drift silently disables the guard | Ph2 |
| B24 | LOW | `debian.sh` `apt-get install -y sing-box \|\| true` hides real failures | Ph6 |
| B25 | LOW | `uninstall.sh` leaves `/usr/local/bin/sbctl` (only the Makefile removes it) | Ph6 |
| A1 | HIGH | `main.go` is a 500-line god file; no output injection, so no command is testable | Ph1 |
| A2 | HIGH | `platform.Runtime` is simultaneously a path bag and a service locator; `Detect()` hardcodes production paths | Ph1 |
| A3 | HIGH | `internal/platform` imports `internal/daemon` **and** `internal/profile` — the leaf-named package sits at the top of the graph | Ph1 |
| A4 | HIGH | Daemon managers call `exec.Command` directly and parse English strings; not fakeable | Ph2 |
| A5 | MED | Active-profile resolution is implemented 5 times (`profile.List`, `profile.ActiveName`, both activators, inline in `main.go`) | Ph2 |
| A6 | MED | Sudo-error string sniffing duplicated across `launchd.go`, `systemd.go`, `profile.go` | Ph2 |
| A7 | MED | `appleScriptSafe` in `main.go` is a one-line delegate existing solely to be tested | Ph1 |
| A8 | HIGH | Near-zero test coverage: 0 for `ui`, 0 for `singbox`, 1 trivial test for `daemon`, 0 command tests | Ph2–5 |
| U1 | HIGH | Three unrelated visual languages: bubbletea picker, huh/Catppuccin forms, raw emoji `Printf` | Ph5 |
| U2 | MED | No `tea.WindowSizeMsg` handling — a list longer than the terminal is unusable | Ph5 |
| U3 | MED | No progress feedback across a multi-second restart | Ph5 |
| U4 | MED | Hardcoded ANSI indices (`"13"`,`"10"`,`"9"`) — illegible on light terminals | Ph5 |
| U5 | LOW | Variable-width emoji break the `%-9s` alignment in `writeInfoLine` | Ph5 |
| U6 | LOW | Terse `Short` help, no `Long`, no `Example`, no completions | Ph7 |

### 2.2 Knowingly deferred (documented, not silently dropped)

| ID | Sev | Defect | Why deferred |
|----|-----|--------|--------------|
| D1 | CRIT | **Profile dir is user-owned but read as config by a root process.** Not fixed by B1. | Fixing requires a different activation model (root-owned staging dir + a validating copy step), which changes the on-disk contract this plan promises to preserve. Recorded in `SECURITY.md` as an outstanding critical issue with the concrete attack paths, not hand-waved. See §6.3. |
| D2 | HIGH | `sing-box` `.deb` and SagerNet GPG key fetched without pinned verification | No trustworthy offline pin available. Mitigations shipped instead (§6.4). |
| D3 | HIGH | `%ProgramData%\sing-box` inherits default ACLs allowing any authenticated user to create files | Same root cause as D1, Windows flavour. Documented. |
| D4 | MED | macOS hardcodes `/usr/local/etc` while Apple-Silicon Homebrew is `/opt/homebrew` | Path layout is a compatibility contract (§1). Needs a migration story, which is its own change. Documented. |
| D5 | MED | Distro `sing-box.service` unit is trusted to read `/etc/sing-box/config.json` | Shipping our own unit/drop-in is a packaging decision. `sbctl doctor` now *detects* the mismatch instead. |
| D6 | LOW | No inter-process lock; concurrent `sbctl use` runs race | Single-user tool; worst case is a wrong active profile fixed by re-running. Contract documented in README and `doctor`. |
| D7 | LOW | No `fsync` in `writeFileAtomic` | Only matters on power loss, where the recovery is `sbctl use <name>`. Both reviewers agreed this is not worth the code. Windows `Remove`+`Rename` gap is narrowed by ordering, documented. |

### 2.3 Rejected

| ID | Claim | Why rejected |
|----|-------|--------------|
| R1 | Picker panics on an empty profile list | V2: `runInteractive` returns early. Fabricated. |
| R2 | `NO_COLOR` / piped-ANSI needs a custom detector | V1: lipgloss already does this. Building a detector would have added code and fixed nothing. |
| R3 | Symbol set should switch to ASCII when piped | Actively harmful: it makes the same command emit different bytes depending on destination, so `sbctl list \| grep ●` breaks precisely when scripted. Symbols are now **destination-independent**; only colour degrades. `--plain` and `--json` are the documented stable interfaces. |
| R4 | Exit code 5 for user cancellation | Cancelling is not a failure. Today `ErrCancelled` → `nil` → exit 0. Changing it would break every wrapper script. Cancellation stays 0. |
| R5 | `* *` suite line in `debian.sh` | Correct for SagerNet's flat repo. |
| R6 | armhf → armv7 mapping | Correct. |
| R7 | Empty `interface_name` in `skeleton.json` | Intentional: sing-box auto-names the interface. The status panel simply omits the row. |
| R8 | Alt-screen for the picker | Inline is deliberate — the result stays in scrollback. Alt-screen would erase it. |

---

## 3. Scope decisions forced by review

Both reviewers demanded cuts. Adopted:

**Cut** — `/` filter as an always-on affordance (it is noise at 2–5 profiles): the
keybinding and its hint appear only when `len(profiles) >= 8`, i.e. when a list
plausibly needs it. **Cut** — `e` (edit) and `d` (delete) keys inside the picker:
shelling `nvim` out of a live bubbletea program needs `tea.ExecProcess` and
corrupts the terminal when it goes wrong, and putting a destructive action one
keystroke from a navigation cursor is a regression dressed as a feature.
`sbctl edit` / `sbctl rm` already exist. **Cut** — `CmdRunner`'s 4-value return,
`StateStopping`/`StateStarting` in the public enum, `FakeClock`, `App.Now`,
`App.HTTPGet`, `ui/json.go`, and the 5-file split of `internal/profile`.
**Cut** — the Windows temp-file IPC protocol for elevated output: replaced with
`SW_SHOWNORMAL`, which deletes an entire race-and-cleanup surface and fixes the
invisible-error bug directly. A brief console flash is the accepted cost.

**Kept against one reviewer's objection** — `sbctl doctor`. The other reviewer
independently demanded a runtime detector for sudoers/binary mismatch, `README`
troubleshooting is entirely about diagnosing install state, and D1/D5/D6 are all
"documented" risks that need a place to surface. It is ~100 lines reusing checks
that already exist.

---

## 4. Target tree

```
sbctl/
├── cmd/sbctl/main.go            # 15-line entrypoint: os.Exit(cli.Main())
├── internal/
│   ├── cli/
│   │   ├── app.go               # App (injected deps + output/format flags), Main(), wiring
│   │   ├── root.go              # root cmd, persistent flags, command groups, completions
│   │   ├── errors.go            # Error (exit code + message + hint), exit-code constants
│   │   ├── activate.go          # shared use/off flow: validate→activate→restart→verify→rollback
│   │   ├── use.go off.go list.go status.go logs.go
│   │   ├── edit.go add.go rm.go check.go
│   │   ├── ip.go version.go doctor.go
│   │   ├── editor.go            # $EDITOR launcher (Editor func type + default impl)
│   │   ├── notify.go            # Notifier + AppleScript/no-op impls, AppleScriptSafe
│   │   └── *_test.go            # command tests against fakes, output captured via App.Out
│   ├── profile/
│   │   ├── profile.go           # Profile, List, PathFor, ValidateName, Placeholders, InterfaceName
│   │   ├── activator.go         # Activator/Rollback, Symlink+Copy impls, atomic write helpers
│   │   ├── checker.go           # Checker iface, ExecChecker (sing-box check/version)
│   │   └── *_test.go
│   ├── service/
│   │   ├── service.go           # Manager iface, RunState, Health, sudo-error helpers
│   │   ├── runner.go            # Runner iface + ExecRunner (context + timeout)
│   │   ├── launchd.go systemd.go winsw.go
│   │   └── *_test.go            # table tests via FakeRunner over real captured output
│   ├── platform/
│   │   ├── layout.go            # Layout value object — stdlib only, no internal imports
│   │   ├── detect.go            # Detect() -> Layout per GOOS
│   │   ├── logs.go              # LogFollower iface, tail/journalctl/file-poll impls
│   │   ├── elevation.go         # Elevator iface + arg quoting (portable, testable)
│   │   ├── elevation_windows.go elevation_other.go
│   │   └── *_test.go
│   └── ui/
│       ├── theme.go             # AdaptiveColor tokens, Symbols, Plain mode
│       ├── components.go        # Panel, KV, Badge, Success, Failure, Warn, KeyHints
│       ├── render.go            # RenderStatus/ProfileList/IPInfo/Doctor — pure funcs
│       ├── picker.go            # bubbletea model: viewport, spinner, conditional filter
│       ├── prompts.go           # huh confirm/select, themed from the same tokens
│       ├── testdata/*.golden
│       └── *_test.go
├── assets/skeleton.json
├── scripts/                     # install.sh uninstall.sh install.ps1 uninstall.ps1 lib/{common,darwin,debian}.sh
├── Makefile                     # build vet test lint install uninstall dist
├── .golangci.yml
├── SECURITY.md                  # threat model, fixed vs deferred, D1/D2/D3 in full
├── CHANGELOG.md
├── README.md
└── PLAN.md
```

`internal/singbox` is deleted; it becomes `profile.ExecChecker`.
`internal/daemon` is renamed `internal/service`.
Root `main.go` and the Makefile build target move **in the same change** so the
tree never has two `main` packages or a stale `-o bin/sbctl .`.

Dependency direction, strictly one-way:

```
cmd/sbctl → cli → {profile, service, platform, ui}
ui        → {profile, service}      (types only, for rendering)
profile   → stdlib
service   → stdlib
platform  → stdlib (+ golang.org/x/sys on Windows)
```

`platform` no longer imports `profile` or `service`, fixing A3.

---

## 5. Interfaces (final)

```go
// ---- internal/service ----

type RunState string
const (
    StateRunning RunState = "running"
    StateStopped RunState = "stopped"
    StateError   RunState = "error"
)

// Health is one liveness sample. Fields are best-effort: PID/Restarts are 0/-1
// when the platform cannot report them. Comparing two samples across a delay is
// how a crash-loop is distinguished from a healthy start (B2).
type Health struct {
    State    RunState
    PID      int    // 0 = unknown
    Restarts int    // -1 = unknown
    LastExit string // "" = unknown / never exited
    Detail   string // raw diagnostic line, for --verbose
}

type Manager interface {
    Restart(ctx context.Context) error
    Stop(ctx context.Context) error
    Probe(ctx context.Context) (Health, error)
}

// Runner mirrors exec.Cmd.CombinedOutput, which is the only shape any caller
// needs. err wraps *exec.ExitError so exit codes stay inspectable.
type Runner interface {
    Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ---- internal/profile ----

type Profile struct {
    Name         string
    Path         string
    Placeholders []string // non-empty => not activatable
}

type Activator interface {
    ActiveName() (string, error) // ("", nil) when nothing is managed/active
    ActivePath() (string, error) // ("", nil) when nothing is managed/active
    Activate(target string) (Rollback, error)
}

type Rollback interface {
    Rollback() error
    Description() string
    Known() bool
}

type Checker interface {
    Check(ctx context.Context, path string) error
    Version(ctx context.Context) (string, error)
}

// ---- internal/platform ----

type Layout struct {
    OS, ServiceName                 string
    ProfilesDir, ActiveConfigPath   string
    ActiveNamePath                  string // Windows only
    ErrorLogPath, AccessLogPath     string
    PlistPath                       string // darwin only
    LaunchdLabel                    string // darwin only
}

type Elevator interface {
    IsElevated() bool
    RunElevated(args []string) (exitCode int, err error)
}

type LogFollower interface {
    Follow(ctx context.Context, out io.Writer) error
    // NeedsFile reports whether Follow reads a file that must exist first.
    // journalctl returns false — this is the fix for B3.
    NeedsFile() (path string, required bool)
}

// ---- internal/cli ----

type Editor func(ctx context.Context, path string) error
type Notifier interface{ Notify(message string) }

type Format struct {
    JSON, Plain, Quiet, Verbose bool
}

type App struct {
    Out, Err  io.Writer
    Format    Format
    Layout    platform.Layout
    Manager   service.Manager
    Activator profile.Activator
    Checker   profile.Checker
    Elevator  platform.Elevator
    Follower  platform.LogFollower
    Notifier  Notifier
    Editor    Editor
    Theme     ui.Theme
}
```

---

## 6. Security posture

### 6.1 Fixed: argument-constrained sudoers (B1)

Today any sbctl user can run `sudo ln` and `sudo launchctl` (or `sudo systemctl`)
with *arbitrary arguments* as root. `sudo ln -sfn /anything /etc/anything` is
game over. The grant is narrowed to exactly the commands sbctl issues.

macOS (`/bin/ln`, `/bin/launchctl` verified present — V4):

```
<user> ALL=(root) NOPASSWD: /bin/ln -sfn /usr/local/etc/sing-box/profiles/*.json /usr/local/etc/sing-box/config.json
<user> ALL=(root) NOPASSWD: /bin/launchctl print system/app.lexiflix.singbox
<user> ALL=(root) NOPASSWD: /bin/launchctl kickstart -k system/app.lexiflix.singbox
<user> ALL=(root) NOPASSWD: /bin/launchctl bootout system/app.lexiflix.singbox
<user> ALL=(root) NOPASSWD: /bin/launchctl bootstrap system /Library/LaunchDaemons/app.lexiflix.singbox.plist
```

Linux — binary paths resolved at install time with `command -v` (matching the
existing `debian.sh` idiom) rather than hardcoded, because `/bin` vs `/usr/bin`
varies with usr-merge:

```
<user> ALL=(root) NOPASSWD: <ln> -sfn /etc/sing-box/profiles/*.json /etc/sing-box/config.json
<user> ALL=(root) NOPASSWD: <systemctl> is-active sing-box
<user> ALL=(root) NOPASSWD: <systemctl> show -p ActiveState,SubState,NRestarts,ExecMainPID,ExecMainStatus sing-box
<user> ALL=(root) NOPASSWD: <systemctl> restart sing-box
<user> ALL=(root) NOPASSWD: <systemctl> stop sing-box
```

sudoers globs use `fnmatch` with `FNM_PATHNAME`, so `*` does not match `/` and
`profiles/../../../etc/shadow.json` cannot match the pattern.

### 6.2 Command/sudoers correspondence (mandatory, because a mismatch bricks the tool)

A silently non-matching rule means every operation fails with "a password is
required" — worse than the hole being closed. The exact argv the binary issues:

| Call site | argv after `sudo -n` | Matching rule |
|---|---|---|
| `SymlinkActivator.activate` | `ln -sfn <profilesDir>/<name>.json <activeConfigPath>` | rule 1 (`<name>` is `ValidateName`-constrained, so no `/`) |
| `LaunchdManager.Probe` | `launchctl print system/app.lexiflix.singbox` | rule 2 |
| `LaunchdManager.Restart` | `launchctl kickstart -k system/…` | rule 3 |
| `LaunchdManager.Stop` | `launchctl bootout system/…` | rule 4 |
| `LaunchdManager` bootstrap | `launchctl bootstrap system /Library/LaunchDaemons/….plist` | rule 5 |
| `SystemdManager.Probe` | `systemctl is-active sing-box` + `systemctl show -p … sing-box` | Linux rules 2,3 |
| `SystemdManager.Restart/Stop` | `systemctl restart\|stop sing-box` | Linux rules 4,5 |

Enforcement, so this cannot silently rot:
- `service` and `profile` route every privileged call through named constructors
  whose emitted argv is asserted by unit tests (`TestSudoArgvMatchesSudoers`)
  against the same template strings the install scripts render.
- `install.sh` still runs `visudo -cf` before installing; a validation failure aborts.
- `sbctl doctor` performs a live `sudo -n` dry-run of each privileged command
  shape and reports which rules are missing, so a stale sudoers file is a
  diagnosis instead of a mystery.

Upgrade safety: the old rules (`/bin/ln, /bin/launchctl`, unrestricted) are a
strict *superset* of the new argv, so a **new binary against an old sudoers still
works**. And the old binary's argv already matches the new narrow rules. Both
upgrade orders are safe; `doctor` covers the residue. CHANGELOG states plainly
that `make install` must be re-run to actually gain the hardening.

### 6.3 Not fixed, and why (D1) — recorded in SECURITY.md

Narrowing sudoers does **not** make the system safe, and this plan will not
pretend otherwise. The profile directory stays owned by the invoking user while a
root sing-box reads what it points at. That leaves, concretely:

- `log.output` and `cache_file` in a profile direct **root-privileged writes to an
  attacker-chosen path**.
- `external_ui_download_url` makes a root process fetch and unpack a remote archive.
- TUN settings can capture or blackhole all host traffic.
- The user owns `profiles/`, so they can place a *symlink* there — `ln -s
  /etc/shadow profiles/evil.json` still satisfies the narrowed glob and points
  root's config at an arbitrary file. The glob restricts the `ln` **destination**,
  not what the source resolves to.

So B1 removes arbitrary `root` command execution via sudo; it does not remove
root-influencing write primitives. `SECURITY.md` states this as an open CRITICAL
with the closing design (root-owned profiles + validate-then-copy staging, no
symlink) written down for the follow-up.

### 6.4 Supply chain (D2 mitigations shipped now)

- WinSW pinned to an exact version tag with a hardcoded SHA-256, verified before
  execution. `releases/latest` is removed.
- `curl --proto '=https' --tlsv1.2` on every download.
- The imported SagerNet GPG key's fingerprint is printed prominently and can be
  pinned via `SBCTL_SAGERNET_GPG_FPR`; when set, a mismatch aborts.
- `.deb` checksum enforced when `SBCTL_SINGBOX_DEB_SHA256` is provided.
- Post-install `sing-box version` must succeed or the install fails loudly
  (replaces `|| true`, B24).

---

## 7. UX design

### 7.1 Colour and degradation — leaning on lipgloss, not reimplementing it

Per V1, detection is already correct. Therefore:

- Tokens are `lipgloss.AdaptiveColor{Light, Dark}`; lipgloss picks per background,
  fixing U4 without any bespoke logic.
- Non-TTY and `NO_COLOR` need **no code at all** — lipgloss resolves `Ascii` and
  `Render` emits bare text.
- `--no-color` calls `lipgloss.SetColorProfile(termenv.Ascii)` — one line.
- `--plain` additionally swaps Unicode symbols for ASCII and drops box borders. It
  is the documented stable-text interface. Auto-enabled when `TERM` is `dumb` or
  empty, or when terminal width < 50 (borders would wrap into garbage).

Palette (`AdaptiveColor{Light, Dark}`): Accent `#7c3aed`/`#c4a7e7`, Success
`#16a34a`/`#a6e3a1`, Warn `#ca8a04`/`#f9e2af`, Danger `#dc2626`/`#f38ba8`, Muted
`#6b7280`/`#6c7086`, Fg `#1f2937`/`#cdd6f4`, Info `#2563eb`/`#89b4fa`.

Symbols are **destination-independent** (R3). Colour degrades; bytes do not.

| Meaning | Default | `--plain` |
|---|---|---|
| active / running | `●` | `*` |
| inactive / stopped | `○` | `-` |
| success | `✓` | `[ok]` |
| failure / error | `✗` | `[!!]` |
| warning, placeholder | `⚠` | `[todo]` |
| cursor | `▸` | `>` |

Every state carries a **symbol and a word**, never colour alone — that is the
accessibility guarantee, and the `⚠` placeholder badge has the explicit `[todo]`
fallback the review asked for.

### 7.2 Non-interactive output

One `KV` renderer for all key/value output, so `status`, `ip`, and `doctor` align
identically. No emoji anywhere in non-interactive output (fixes U5). Profile names
are truncated with `…` at 40 columns.

```
$ sbctl status
  ╭ sbctl ─────────────────────────╮
  │ state    ● running             │
  │ profile  sg-cloudflare         │
  │ tun      tun0                  │
  ╰────────────────────────────────╯

$ sbctl list
  ● sg-cloudflare   active
  ○ work-vpn
  ⚠ new-profile     placeholders

$ sbctl ip
  ip        203.0.113.42
  city      Colombo
  country   LK
  network   AS13335 Cloudflare, Inc.
  timezone  Asia/Colombo
```

### 7.3 Flags

| Flag | Effect |
|---|---|
| `--json` | machine output; suppresses all decoration, spinners, prompts |
| `--plain` | ASCII symbols, no borders/colour; the stable text interface |
| `--no-color` | colour off, Unicode kept |
| `-q, --quiet` | drop success/informational lines; errors still on stderr |
| `-v, --verbose` | `[debug]` lines on stderr: argv executed, paths, timings, raw probe output |

`--json` payloads carry `"schema": 1`:

```json
{"schema":1,"state":"running","profile":"sg-cloudflare","tun":"tun0","pid":4711}
{"schema":1,"profiles":[{"name":"sg-cloudflare","active":true,"placeholders":[]}]}
{"schema":1,"profile":"sg-cloudflare","valid":true}
{"schema":1,"version":"1.2.3","sing_box":"1.13.8"}
{"schema":1,"checks":[{"name":"sudoers","ok":false,"detail":"…","hint":"…"}],"ok":false}
```

Errors in `--json` mode go to stdout as `{"schema":1,"error":"…","hint":"…","code":2}`.

### 7.4 Exit codes

Cancellation stays 0 (R4). Only genuine failures are non-zero.

| Code | Name | Meaning |
|---|---|---|
| 0 | OK | success, **including user cancellation** |
| 1 | Error | generic: bad usage, missing profile, I/O, network |
| 2 | Validation | placeholders present, or `sing-box check` failed |
| 3 | Service | daemon refused to start/stop, or post-activation health check failed |
| 4 | Permission | elevation missing/denied, sudoers not configured |

### 7.5 Interactive picker

Inline (not alt-screen, R8). Status panel, viewport-clamped list, `Turn off` only
when running, spinner during activation, terminal result left in scrollback.

```
╭ sbctl ──────────────────────────╮
│ state    ● running              │
│ profile  sg-cloudflare          │
│ tun      tun0                   │
╰─────────────────────────────────╯

  ▸ ● sg-cloudflare   active
    ○ work-vpn
    ⚠ new-profile     placeholders

    ✗ Turn off

  ↑/↓ move · enter select · q quit
```

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | move (clamped, no wrap) |
| `g`/`home`, `G`/`end` | first / last |
| `enter`, `space` | activate selection, or turn off |
| `/` | filter — **only bound and hinted when ≥8 profiles** |
| `esc` | clear filter, else cancel (exit 0) |
| `q`, `ctrl+c` | cancel (exit 0) |
| `?` | expand key help |

Selecting a placeholder profile does not activate it; it reports the markers and
the `sbctl edit` command inline.

**Spinner concurrency**, explicitly (this was under-specified before): activation
runs inside a `tea.Cmd` closure returning `activationDoneMsg{err}`; the model
keeps ticking the spinner while that goroutine blocks. A spinner never wraps a
blocking call on the update goroutine.

**Elevation vs rendering**, explicitly: Unix uses `sudo -n` and therefore *never*
prompts — it succeeds or reports "a password is required", so no TUI can be
blocked on hidden stdin. On Windows, `RunElevated` (UAC) must complete **before**
any spinner or bubbletea program starts; elevation is never nested inside a
render loop. Enforced by keeping the elevation branch at the top of the command
handler, ahead of any UI construction.

### 7.6 Subprocess budget

`Probe()` is called **once per user action**, and its result is threaded through
rendering. The spinner does not poll. Post-activation verification is exactly two
extra probes (before/after the settle delay). Concretely: `status`/`list` = 1
probe; `use` = 1 probe + restart + 2 verification probes. `--verbose` prints the
argv of each so this stays auditable.

### 7.7 Post-activation health verification (B2) — the most important behavioural change

`Manager.Restart()` returning nil only means *the request was accepted*. It is no
longer treated as success.

1. Sample `Probe()` → `before`.
2. `Activate(target)`, `Restart()`.
3. Sleep `2500ms` (`healthSettleDelay`).
4. Sample `Probe()` → `after`.
5. Treat as **failed** when: `after.State != StateRunning`; or
   `after.LastExit` is a non-zero exit; or `after.Restarts > before.Restarts`
   (crash-loop under `KeepAlive=true`); or `after.PID == 0` while state claims running.
6. On failure: roll back the activation, restart the service on the previous
   config, and report:
   `✗ sg-cloudflare started then crashed (restarts 3→7); reverted to work-vpn` +
   `→ inspect: sbctl logs` and exit 3.
7. If rollback *itself* fails, print the exact manual recovery commands
   (`sudo ln -sfn <old> <active>` and the platform restart command) rather than a
   bare error.

Platform probe sources, all verified real:
- launchd — `launchctl print`: `state =`, `pid =`, `runs =`, `last exit code =` (V5).
- systemd — `systemctl show -p ActiveState,SubState,NRestarts,ExecMainPID,ExecMainStatus`,
  which is key=value and locale-independent, replacing `is-active` string sniffing.
- WinSW — `sc.exe queryex` for `STATE` + `PID`; `START_PENDING`/`STOP_PENDING`
  handled as transient inside the manager, never leaked as `StateError` (B14).

Connectivity probing (actually reaching the internet through the tunnel) is
explicitly **out of scope**: it needs a network egress policy decision. Process
liveness is mandatory; reachability is not claimed.

---

## 8. Implementation phases

Every phase ends with `go build ./... && go vet ./... && go test ./... && gofmt -l .` clean.

**Ph1 — restructure, behaviour-preserving.** Create `cmd/sbctl`, `internal/cli`
skeleton with `App`; split commands one per file; `Layout` value object; move
service construction into `cli`; rename `daemon`→`service`; fold `singbox` into
`profile.ExecChecker`; delete root `main.go` **and** update the Makefile target in
the same change; move `appleScriptSafe` into `cli/notify.go` (A7); add `test`/`lint`
targets. All output through `App.Out`/`App.Err`.

**Ph2 — core correctness + injectable subprocess.** `Runner`/`ExecRunner` with a
30s default timeout (B20); managers take a `Runner`; `Probe`+`Health` (B2 groundwork);
launchd `runs`/`pid`/`last exit code` parsing (B15); systemd `show -p` parsing;
WinSW `queryex` transient handling (B14); `ValidateName` (B6); symmetric
`("", nil)` activator contract (B9, B10); single active-name resolution path
(A5); one `IsSudoError` (A6); regex `TODO_[A-Z0-9_]+` placeholder scan (B23);
`TestSudoArgvMatchesSudoers`.

**Ph3 — Windows elevation.** Portable `QuoteArg` per `CommandLineToArgvW` rules
(B4, unit-tested on macOS); `SW_SHOWNORMAL` so child output is visible and the
parent stops double-printing (B5); numeric `ERROR_CANCELLED` via `syscall.Errno`
(B16); `GetExitCodeProcess` error checked. Verified by `GOOS=windows go build` and
pure-function tests.

**Ph4 — command semantics.** Health-verified activation with rollback (B2);
`logs` uses `Follower.NeedsFile()` so journalctl is never gated on a file (B3);
`edit` restarts when editing the active profile — via `Manager.Restart()`, which
already handles elevation internally through sudo, and re-copies on Windows (B7);
`edit` revert preserves the original file mode (B13); `add` gets `edit`'s
re-edit/revert loop, deleting the file on revert (B12); `rm --force` stops the
daemon before deleting (B8); `rm` reports nonexistent profiles usefully (B21);
dangling active config diagnosed by `status`/`list`/`check`/`doctor` (B22) —
**no new sudo right is introduced to unlink the root-owned symlink**, which was
the unresolved gap in revision 1; `list`/`status` distinguish
`active` from `configured (stopped)` (B11).

**Ph5 — UI.** `theme.go` (AdaptiveColor + Symbols + Plain), `components.go`,
pure `render.go`; picker viewport, clamped cursor, conditional filter, spinner via
`tea.Cmd`; huh prompts themed from the same tokens (U1); `--json/--plain/--no-color/-q/-v`;
`Error{code,msg,hint}` with hints on every user-facing failure; golden tests with
`lipgloss.SetColorProfile(termenv.Ascii)` pinned in `TestMain` (V8).

**Ph6 — install/scripts/security.** Constrained sudoers with `command -v`-resolved
paths (B1); `set -euo pipefail` asserted in every lib script; WinSW pinned
version+SHA256; GPG fingerprint surfacing; `--proto '=https' --tlsv1.2`; remove
`|| true` (B24); `SecurityElement::Escape` in the service XML (B18); UTF-8
`active-profile` (B17); PATH cleanup on Windows uninstall (B19); binary removal in
`uninstall.sh` (B25); `SECURITY.md`.

**Ph7 — surface polish.** `doctor` (sudoers dry-run, sing-box presence/version,
unit config path D5, dangling active config, ownership, concurrency note);
`Long`+`Example` on every command, grouped help, completions with profile-name
`ValidArgsFunction` (U6); `debug.BuildInfo` version fallback; `.golangci.yml`;
`CHANGELOG.md`; README rewrite against real behaviour.

---

## 9. Test plan

Fakes: `FakeRunner` (canned output per argv, records calls), `FakeManager`,
`FakeActivator`, `FakeChecker`, `FakeElevator`, `FakeNotifier`, `FakeFollower`,
`FakeEditor`. No `FakeClock` — timeouts are tested with `context.WithTimeout`.

- `service` — table tests over **real captured** `launchctl print` /
  `systemctl show` / `sc queryex` output, including crash-loop and PENDING cases;
  restart/stop paths; sudo-error mapping; argv-vs-sudoers correspondence.
- `profile` — `ValidateName` traversal corpus; placeholder regex; `InterfaceName`;
  both activators' activate/rollback and fresh-install `("", nil)` in `t.TempDir()`.
- `platform` — `Layout` per GOOS; `QuoteArg` edge cases; `NeedsFile()` semantics.
- `cli` — command tests with a fully faked `App`, asserting captured stdout/stderr
  and exit codes: happy paths, placeholder block (2), traversal reject (1), rollback
  on restart failure (3), **health-check failure triggers rollback** (3), sudo missing
  (4), `--json` shape for every supporting command, cancel = 0.
- `ui` — golden files for status/list/ip/doctor in default and `--plain`; picker
  model driven by synthetic `tea.KeyMsg`/`tea.WindowSizeMsg` sequences asserting
  cursor/choice/filter state without a TTY.

Honest limits, stated rather than papered over: `ExecRunner`, `ShellExecuteExW`,
`sc.exe`, and anything requiring real `sudo` or a real `sing-box` binary are not
unit-testable. Windows and Linux runtime behaviour cannot be verified from this
macOS machine — only `GOOS=windows`/`GOOS=linux` compilation plus fakes.
`docs/manual-verification.md` therefore ships a per-platform checklist (install,
use, off, status, logs, edit, add, rm, check, doctor, upgrade-over-existing-install)
that a human must run before release.

---

## 10. Non-goals

Secrets management for profiles; multi-user ACLs; remote profile sync/URL import;
GUI or tray; config generation/templating; sing-box upgrade management; profile
rename; connectivity probing; `slog`; plugins; `fsync` (D7); inter-process
locking (D6); the D1 activation-model rewrite.

---

## 11. Critique resolutions

Reviewer A = principal-engineer review. Reviewer B = security/UX review.

| # | Amendment | Disposition |
|---|---|---|
| A1 | Remove fabricated zero-profile panic | **Accepted** — R1; defensive clamp kept as it is trivial |
| A2 | lipgloss already handles NO_COLOR/TTY; drop custom detection | **Accepted** — verified V1; §7.1 rewritten, detector deleted |
| A3 | Fully specify or drop Windows temp-file IPC | **Accepted (drop)** — `SW_SHOWNORMAL`, §3 |
| A4 | `rm --force` has no sudoers right to unlink the root symlink | **Accepted** — no new right; dangling config is detected and reported instead (Ph4/B22) |
| A5 | Makefile update atomic with `main.go` deletion | **Accepted** — Ph1 |
| A6 | Resolve Linux sudoers binary paths with `command -v` | **Accepted** — §6.1 |
| A7 | Specify launchd crash parsing exactly | **Accepted** — `state`/`pid`/`runs`/`last exit code`, verified V5 |
| A8 | Specify spinner architecture | **Accepted** — `tea.Cmd` + `activationDoneMsg`, §7.5 |
| A9 | Cut scope | **Partially accepted** — cut filter-always-on, picker `e`/`d`, temp-file IPC, `ui/json.go`, `FakeClock`, `App.Now`/`HTTPGet`, 4-value Runner, transient states, 5-file profile split. `doctor`, completions and lint kept, with reasons in §3 |
| A10 | Drop `StateStopping`/`StateStarting` | **Accepted** |
| A11 | Simplify Runner to `([]byte, error)` | **Accepted** |
| A12 | Remove `FakeClock` | **Accepted** |
| A13 | Collapse `profile` to 3 files | **Accepted** |
| A14 | Kill `ui/json.go` | **Accepted** |
| A15 | Remove `App.Now`/`App.HTTPGet` | **Accepted** |
| A16 | Manual verification checklist for Linux/Windows | **Accepted** — `docs/manual-verification.md`, §9 |
| A17 | Admit the narrowed glob still permits a symlink inside `profiles/` | **Accepted** — §6.3, stated explicitly |
| A18 | Fix golden-file strategy | **Accepted** — `termenv.Ascii` pinned in `TestMain`; V8 shows non-TTY test output is already ANSI-free |
| A19 | Downgrade path traversal from CRITICAL | **Accepted** — HIGH, V7/B6 |
| A20 | State that edit-restart reuses `Manager.Restart()`'s internal sudo | **Accepted** — Ph4 |
| B1 | Post-activation health check with rollback | **Accepted** — §7.7; the single most valuable finding of the review |
| B2 | Prove sudoers/argv correspondence; add runtime mismatch detection | **Accepted** — §6.2 table + `TestSudoArgvMatchesSudoers` + `doctor` dry-run + CHANGELOG |
| B3 | Document residual root-write risk honestly | **Accepted** — §6.3 + `SECURITY.md`; D1 stays CRITICAL and open |
| B4 | Pin WinSW hash, surface GPG fingerprint | **Accepted** — §6.4 |
| B5 | Remove `e`/`d` from the picker | **Accepted** — §3 |
| B6 | Make the filter conditional | **Accepted** — bound only at ≥8 profiles |
| B7 | Elevation before any spinner/TUI on Windows | **Accepted** — §7.5 |
| B8 | Do not change symbols based on pipe-ness | **Accepted** — R3; symbols destination-independent, `--plain`/`--json` are the stable interfaces |
| B9 | Keep cancellation at exit 0 | **Accepted** — R4; `ExitCancelled` deleted |
| B10 | Narrow-terminal and `TERM=dumb` handling | **Accepted** — auto-`--plain` under 50 cols or dumb/empty `TERM`; 40-char name truncation |
| B11 | Plain-mode placeholder badge | **Accepted** — `[todo]`, §7.1 |
| B12 | One `Probe()` per invocation, no polling | **Accepted** — §7.6 with counts |
| B13 | Document the concurrency contract | **Accepted** — D6, README + `doctor` |
| B14 | List the `ip` output change as a break | **Accepted** — §12 |
| B15 | Version the JSON schema | **Accepted** — `"schema": 1` on every payload |

---

## 12. Intentional breaks

| Change | Impact & migration |
|---|---|
| Exit codes 1 → {1,2,3,4} by class | `$? -ne 0` unaffected. Only a script branching on the *specific* value 1 needs review. Cancellation stays 0. |
| `sbctl ip` loses emoji, gains aligned columns | Anyone scraping `📍 IP:` must switch to `--json` (now the supported interface). `main_test.go`'s emoji assertions are rewritten. |
| Sudoers content narrows | Both upgrade orders keep working (§6.2); `make install` must be re-run to gain the hardening. `doctor` reports the mismatch. |
| `sbctl use` with no args opens the picker instead of erroring | Additive for humans; scripts always pass a name. |
| Windows `active-profile` written UTF-8 not ASCII | Identical bytes for the ASCII-only names `ValidateName` permits. |
| New flags `--json --plain --no-color -q -v`; new `doctor`, `completion` | Additive. |
