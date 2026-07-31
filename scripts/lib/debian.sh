#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

required_singbox_version="1.13.8"

install_debian() {
  local profiles_dir="/etc/sing-box/profiles"
  local active_link="/etc/sing-box/config.json"
  local default_profile_path="$profiles_dir/$default_profile_name.json"

  require_debian_family
  ensure_singbox_version "$required_singbox_version"
  verify_singbox

  mkdir -p "$profiles_dir" "$(dirname "$active_link")"
  chown "$user_name":"$user_name" "$profiles_dir"
  chmod 755 "$profiles_dir"

  seed_profile "$profiles_dir" "$user_name"

  local profile_path
  for profile_path in "$profiles_dir"/*.json; do
    [[ -e "$profile_path" ]] || continue
    if [[ "$(stat -c '%U' "$profile_path")" == "root" ]]; then
      chown "$user_name":"$user_name" "$profile_path"
    fi
  done

  ensure_managed_symlink "$active_link" "$default_profile_path"
  install_sudoers root
  ensure_unit_reads_active_config "$active_link"

  systemctl enable sing-box

  if has_placeholders "$default_profile_path"; then
    echo
    echo "sbctl is installed."
    echo "Fill in the placeholder values before starting sing-box:"
    echo "  sbctl edit $default_profile_name"
    return
  fi

  sing-box check -c "$default_profile_path"
  systemctl restart sing-box
  echo "sbctl is installed and sing-box is running."
}

require_debian_family() {
  local os_id="" os_like=""
  if [[ -r /etc/os-release ]]; then
    # Read in a subshell so sourcing cannot clobber this script's variables:
    # os-release defines ID and VERSION, which are common names.
    os_id="$(. /etc/os-release && echo "${ID:-}")"
    os_like="$(. /etc/os-release && echo "${ID_LIKE:-}")"
  fi
  if [[ "$os_id" != "debian" && "$os_like" != *"debian"* ]]; then
    echo "unsupported distribution: ID=$os_id ID_LIKE=$os_like" >&2
    echo "sbctl currently supports Debian-family systems only." >&2
    exit 1
  fi
}

# ensure_unit_reads_active_config verifies the packaged unit actually loads the
# config sbctl manages.
#
# The unit ships with the distribution, and different sing-box releases have used
# both `-c <file>` and `-C <directory>`. If it does not read the file sbctl
# switches, profile changes would appear to succeed while having no effect, so a
# drop-in pins it.
ensure_unit_reads_active_config() {
  local active_link="$1"
  local exec_start
  exec_start="$(systemctl show -p ExecStart --value sing-box 2>/dev/null || true)"

  if [[ "$exec_start" == *"$active_link"* ]]; then
    return
  fi

  echo "the packaged sing-box unit does not read $active_link; installing a drop-in to correct it"
  local dropin_dir="/etc/systemd/system/sing-box.service.d"
  mkdir -p "$dropin_dir"
  cat > "$dropin_dir/10-sbctl.conf" <<EOF
# Managed by sbctl.
# Pins the unit to the configuration file sbctl switches, so a profile change
# always takes effect regardless of how the packaged unit was written.
[Service]
ExecStart=
ExecStart=$(command -v sing-box) run -c $active_link
EOF
  systemctl daemon-reload
}

ensure_singbox_version() {
  local required="$1"

  if singbox_version_at_least "$required"; then
    return
  fi

  install_sagernet_apt_source
  # Not silenced: a failure here is meaningful, and the GitHub package below is
  # the deliberate fallback rather than a way to paper over it.
  if ! apt-get install -y sing-box; then
    echo "the SagerNet repository could not supply sing-box; falling back to the release package"
  fi

  if singbox_version_at_least "$required"; then
    return
  fi

  install_singbox_release "$required"

  if ! singbox_version_at_least "$required"; then
    echo "could not install sing-box >= $required (found: $(installed_singbox_version))" >&2
    exit 1
  fi
}

install_sagernet_apt_source() {
  apt-get update
  apt-get install -y ca-certificates curl gpg
  install -d -m 0755 /etc/apt/keyrings

  local tmp_key
  tmp_key="$(mktemp)"
  # shellcheck disable=SC2064
  trap "rm -f '$tmp_key'" RETURN

  curl_download "https://sing-box.app/gpg.key" "$tmp_key"

  # Print the fingerprint so the trust decision is visible and auditable rather
  # than silent. Set SBCTL_SAGERNET_GPG_FPR to enforce a specific key.
  local fingerprint
  fingerprint="$(gpg --show-keys --with-colons --fingerprint "$tmp_key" 2>/dev/null |
    awk -F: '/^fpr:/ { print $10; exit }')"
  echo "SagerNet signing key fingerprint: ${fingerprint:-unknown}"

  if [[ -n "${SBCTL_SAGERNET_GPG_FPR:-}" ]]; then
    if [[ "$fingerprint" != "${SBCTL_SAGERNET_GPG_FPR}" ]]; then
      echo "signing key fingerprint does not match SBCTL_SAGERNET_GPG_FPR; aborting" >&2
      echo "  expected: ${SBCTL_SAGERNET_GPG_FPR}" >&2
      echo "  found:    ${fingerprint:-unknown}" >&2
      exit 1
    fi
    echo "signing key matches the pinned fingerprint"
  else
    echo "note: the key is trusted on first use. Pin it with SBCTL_SAGERNET_GPG_FPR to enforce."
  fi

  gpg --dearmor --yes -o /etc/apt/keyrings/sagernet.gpg < "$tmp_key"
  chmod a+r /etc/apt/keyrings/sagernet.gpg

  echo "deb [signed-by=/etc/apt/keyrings/sagernet.gpg] https://deb.sagernet.org/ * *" \
    > /etc/apt/sources.list.d/sagernet.list
  apt-get update
}

install_singbox_release() {
  local version="$1"
  local arch tmp_deb
  arch="$(singbox_release_arch)"

  tmp_deb="$(mktemp "/tmp/sing-box_${version}_${arch}.XXXXXX.deb")"
  # shellcheck disable=SC2064
  trap "rm -f '$tmp_deb'" RETURN

  curl_download \
    "https://github.com/SagerNet/sing-box/releases/download/v${version}/sing-box_${version}_linux_${arch}.deb" \
    "$tmp_deb"

  # Enforce a checksum when the caller supplies one. No trustworthy value can be
  # embedded here without shipping a hash table that would need updating for
  # every release, so this is opt-in rather than absent.
  if [[ -n "${SBCTL_SINGBOX_DEB_SHA256:-}" ]]; then
    echo "${SBCTL_SINGBOX_DEB_SHA256}  ${tmp_deb}" | sha256sum -c - || {
      echo "downloaded package does not match SBCTL_SINGBOX_DEB_SHA256; aborting" >&2
      exit 1
    }
    echo "package checksum verified"
  else
    echo "note: package checksum not verified. Set SBCTL_SINGBOX_DEB_SHA256 to enforce one."
  fi

  apt-get install -y "$tmp_deb"
}

singbox_release_arch() {
  local arch
  arch="$(dpkg --print-architecture)"
  case "$arch" in
    amd64) echo "amd64" ;;
    arm64) echo "arm64" ;;
    armhf) echo "armv7" ;;
    *)
      echo "no sing-box release package for architecture: $arch" >&2
      exit 1
      ;;
  esac
}

installed_singbox_version() {
  if ! command -v sing-box >/dev/null 2>&1; then
    echo "none"
    return
  fi
  sing-box version | awk 'NR == 1 { print $3 }'
}

singbox_version_at_least() {
  local required="$1" current
  current="$(installed_singbox_version)"

  [[ "$current" != "none" ]] || return 1
  [[ "$(printf '%s\n%s\n' "$required" "$current" | sort -V | head -n1)" == "$required" ]]
}
