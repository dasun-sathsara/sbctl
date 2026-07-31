#!/usr/bin/env bash
# Shared helpers for the sbctl installers.
#
# Each library sets its own strict options. Sourcing does inherit the caller's
# `set -e`, but relying on that means a library used from anywhere else silently
# loses its error handling, so it is declared explicitly here.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
default_profile_name="sg-cloudflare"
sudoers_path="/etc/sudoers.d/sbctl"

# The installer runs under sudo, so the account to grant access to is the one
# that invoked it, not root.
user_name="${SUDO_USER:-$(logname 2>/dev/null || whoami)}"

if [[ "$user_name" == "root" ]]; then
  # Granting these rules to root is pointless: root already has the privileges,
  # and a sudoers file naming root suggests the invoking user was misdetected —
  # which would leave the real user unable to switch profiles.
  #
  # Container images legitimately have no non-root account, so the check can be
  # waived deliberately rather than being an unconditional dead end.
  if [[ -n "${SBCTL_ALLOW_ROOT:-}" ]]; then
    echo "installing for root because SBCTL_ALLOW_ROOT is set"
  else
    echo "could not determine which non-root account to grant sbctl access to." >&2
    echo "Run 'make install' as your normal user (it will call sudo itself)," >&2
    echo "or set SBCTL_ALLOW_ROOT=1 if this really is a root-only system." >&2
    exit 1
  fi
fi

# sbctl_bin locates the binary the installer should use as its source of truth.
sbctl_bin() {
  if [[ -x "$repo_root/bin/sbctl" ]]; then
    echo "$repo_root/bin/sbctl"
  elif command -v sbctl >/dev/null 2>&1; then
    command -v sbctl
  else
    echo "sbctl binary not found; run 'make build' first" >&2
    return 1
  fi
}

# curl_download fetches a URL with TLS pinned to a modern protocol and HTTPS
# enforced, so a redirect cannot silently downgrade the transport.
curl_download() {
  local url="$1" dest="$2"
  curl --proto '=https' --tlsv1.2 -fsSL --retry 3 --retry-delay 2 -o "$dest" "$url"
}

# seed_profile installs the bundled template as the default profile, but only
# when no profile of that name already exists, so a user's edits are never lost.
seed_profile() {
  local profiles_dir="$1" group_name="$2"
  local default_profile_path="$profiles_dir/$default_profile_name.json"

  if [[ -f "$default_profile_path" ]]; then
    return
  fi
  install -o "$user_name" -g "$group_name" -m 644 \
    "$repo_root/assets/skeleton.json" "$default_profile_path"
}

# has_placeholders reports whether a profile still contains template markers.
# The prefix is matched generically, mirroring the binary, so adding a marker to
# the template cannot bypass the check here.
has_placeholders() {
  grep -qE 'TODO_[A-Z0-9_]+' "$1"
}

# ensure_managed_symlink points the active config at a profile, preserving any
# pre-existing unmanaged config as a timestamped backup rather than destroying it.
ensure_managed_symlink() {
  local active_link="$1" default_profile_path="$2"

  if [[ -L "$active_link" ]]; then
    return
  fi
  if [[ -e "$active_link" ]]; then
    local backup_path="$active_link.bak.$(date +%Y%m%d%H%M%S)"
    mv "$active_link" "$backup_path"
    echo "existing configuration backed up to $backup_path"
  fi
  ln -sfn "$default_profile_path" "$active_link"
}

# install_sudoers writes the privilege rules, obtaining them from the binary
# itself.
#
# Generating them here in shell would mean maintaining a second copy of the exact
# argument lists the binary uses. Because the rules are narrow, any drift stops
# sudo matching and every privileged operation fails for want of a password —
# a worse outcome than the broad grant this replaced. Asking the binary removes
# that failure mode entirely.
install_sudoers() {
  local group_name="$1"
  local binary tmp_sudoers
  binary="$(sbctl_bin)"

  tmp_sudoers="$(mktemp)"
  # shellcheck disable=SC2064
  trap "rm -f '$tmp_sudoers'" RETURN

  if ! "$binary" print-sudoers --user "$user_name" > "$tmp_sudoers"; then
    echo "could not generate the sudo rules; leaving $sudoers_path unchanged" >&2
    exit 1
  fi

  # An empty file is valid sudoers syntax, so visudo would accept it and we would
  # install a file granting nothing while reporting success — silently removing
  # every permission sbctl needs. Require actual rules before going further.
  if [[ ! -s "$tmp_sudoers" ]]; then
    echo "the generated sudo rules were empty; leaving $sudoers_path unchanged" >&2
    exit 1
  fi
  if ! grep -q 'NOPASSWD:' "$tmp_sudoers"; then
    echo "the generated sudo rules contain no NOPASSWD entries; leaving $sudoers_path unchanged" >&2
    exit 1
  fi

  if ! visudo -cf "$tmp_sudoers" >/dev/null; then
    echo "the generated sudo rules are invalid; leaving $sudoers_path unchanged" >&2
    visudo -cf "$tmp_sudoers" >&2 || true
    exit 1
  fi

  if [[ -f "$sudoers_path" ]] && cmp -s "$tmp_sudoers" "$sudoers_path"; then
    return
  fi
  install -o root -g "$group_name" -m 440 "$tmp_sudoers" "$sudoers_path"
  echo "installed scoped sudo rules to $sudoers_path"
}

# verify_singbox confirms the installed binary actually runs, so a broken or
# partial install fails here rather than at the first profile switch.
verify_singbox() {
  if ! command -v sing-box >/dev/null 2>&1; then
    echo "sing-box is not on PATH after installation" >&2
    return 1
  fi
  if ! sing-box version >/dev/null 2>&1; then
    echo "sing-box is installed but does not run correctly" >&2
    return 1
  fi
  echo "sing-box $(sing-box version | awk 'NR==1 {print $3}') is available"
}
