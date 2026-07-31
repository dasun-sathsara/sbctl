# Security

sbctl controls a process that runs as root and captures all network traffic. This
document states plainly what it protects against, what it does not, and why.

## Threat model

sbctl targets a single-user workstation. The person running it is assumed to be
the machine's owner and is already able to become root by other means. The
property worth defending is therefore narrower but still real: **a compromise of
the unprivileged user session should not automatically become persistent root
code execution.**

## Fixed: privilege grants are scoped to exact commands

Previously `/etc/sudoers.d/sbctl` contained:

```
<user> ALL=(root) NOPASSWD: /bin/ln, /bin/launchctl
```

Those are whole-binary grants with unrestricted arguments. `sudo ln -sfn
/anything /etc/anything` as root is enough to replace any file on the system, so
the grant was equivalent to unrestricted root. The Linux equivalent granted all
of `systemctl`, which includes running arbitrary units.

The rules are now scoped to the specific commands sbctl issues, with fixed
arguments:

```
<user> ALL=(root) NOPASSWD: /bin/ln -sfn /usr/local/etc/sing-box/profiles/*.json /usr/local/etc/sing-box/config.json
<user> ALL=(root) NOPASSWD: /bin/launchctl print     system/app.lexiflix.singbox
<user> ALL=(root) NOPASSWD: /bin/launchctl kickstart -k system/app.lexiflix.singbox
<user> ALL=(root) NOPASSWD: /bin/launchctl bootout   system/app.lexiflix.singbox
<user> ALL=(root) NOPASSWD: /bin/launchctl bootstrap system /Library/LaunchDaemons/app.lexiflix.singbox.plist
```

sudo evaluates these globs with `fnmatch` under `FNM_PATHNAME`, so `*` does not
match `/` and a traversal sequence such as `profiles/../../../etc/shadow.json`
cannot satisfy the pattern. `TestSudoersGlobBlocksTraversal` asserts this using
Go's `path.Match`, which has the same semantics.

### Why the rules cannot drift from the code

Narrow rules introduce a failure mode of their own: if the argument list the
binary issues ever stops matching a rule, sudo refuses and *every* privileged
operation fails for want of a password. That is a worse outcome than the broad
grant it replaced.

Two mechanisms prevent it:

- `platform.Layout.SudoCommands()` is the single source of truth. Both the
  installed file and the runtime argument list are derived from it, and the
  installer obtains the file by running `sbctl print-sudoers` rather than
  templating it in shell. There is no second copy to fall out of step.
- `TestSudoersMatchesIssuedCommands` asserts the correspondence for every
  privilege and every profile name the validator accepts.
- `sbctl doctor` re-checks each permission at runtime against the live sudoers
  file, so a stale installation is reported by name instead of surfacing as an
  unexplained permission error.

## Fixed: other hardening in this release

| Issue | Resolution |
|---|---|
| Profile names were used unvalidated as filenames, so `../` escaped the profiles directory | `profile.ValidateName` restricts names to letters, digits, dot, dash and underscore |
| macOS notifications interpolated text into an AppleScript string, so a quote could inject script | `AppleScriptSafe` strips quotes, backslashes and non-printable characters |
| Windows elevation joined arguments with spaces, allowing argument injection | `platform.QuoteArg` implements the documented `CommandLineToArgvW` escaping |
| WinSW was downloaded from a `latest` URL and executed unverified | Pinned to an exact release; SHA-256 enforced when `SBCTL_WINSW_SHA256` is set |
| The WinSW service definition was built by string interpolation | All interpolated paths pass through `SecurityElement::Escape` |
| `%ProgramData%\sing-box` inherited ACLs letting any authenticated user create files read by a LocalSystem service | The installer removes inheritance and grants write access only to Administrators and SYSTEM |
| Downloads did not enforce HTTPS | `curl --proto '=https' --tlsv1.2` |
| `apt-get install ... \|\| true` hid real failures | Failures are reported, and installation is verified by running `sing-box version` |

## Not fixed: user-writable profiles are read by a root process

**This is an outstanding issue and is not resolved by the sudoers change.**

The profiles directory is owned by the invoking user so that profiles can be
edited without `sudo`. A root sing-box then reads whichever profile the active
config points at. Write access to a configuration consumed by a root process
grants more than it appears to:

- `log.output` and `cache_file` direct **root-privileged writes to a path of the
  attacker's choosing**.
- `external_ui_download_url` causes a root process to fetch and unpack a remote
  archive.
- TUN settings can capture, redirect or blackhole all host traffic.
- Because the user owns `profiles/`, they can place a *symlink* there.
  `ln -s /etc/shadow profiles/evil.json` still satisfies the narrowed glob: the
  rule constrains the symlink's **destination**, not what its source resolves to.

To be blunt about the severity: `log.output` alone amounts to an **arbitrary
file write as root** to any path on the system, which is a straightforward route
back to full root. The sudoers change removes arbitrary root *command execution*
via sudo; it does not remove that write primitive. It is a real reduction in
exposure, not a complete fix, and it is described here rather than claimed as
resolved.

Closing it requires a different activation model — root-owned profiles with a
validate-then-copy staging step and no symlink — which changes the on-disk layout
that existing installations depend on. That is deliberately out of scope for this
change.

Until then, treat write access to the profiles directory as equivalent to root on
the machine.

## Not fixed: package authenticity is trust-on-first-use

The SagerNet APT signing key is fetched over HTTPS and trusted on first use. The
`.deb` fallback is downloaded from GitHub without a checksum. No trustworthy pin
can be embedded without shipping a hash table that must be updated for every
upstream release.

Mitigations available now:

- The signing key fingerprint is printed during installation so the trust
  decision is visible.
- Set `SBCTL_SAGERNET_GPG_FPR` to a fingerprint to make a mismatch fatal.
- Set `SBCTL_SINGBOX_DEB_SHA256` to enforce a package checksum.
- Installing sing-box yourself beforehand skips both paths entirely.

## Not fixed: no concurrency lock

There is no inter-process lock. Two simultaneous `sbctl use` runs, or a `sbctl
rm` racing an activation, can leave the active profile in an unexpected state.
The worst outcome is the wrong profile being active, which re-running the command
corrects; no data is lost. `sbctl doctor` states this limitation explicitly.

## Reporting

Open an issue for anything in the "fixed" sections above that appears not to
hold. For a new privilege-escalation path, include the platform, the contents of
`/etc/sudoers.d/sbctl`, and the output of `sbctl doctor`.
