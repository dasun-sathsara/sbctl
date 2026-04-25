#!/usr/bin/env bash

# shellcheck source=scripts/lib/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

install_debian() {
  local profiles_dir="/etc/sing-box/profiles"
  local active_link="/etc/sing-box/config.json"
  local default_profile_path="$profiles_dir/$default_profile_name.json"
  local required_singbox_version="1.13.8"
  local os_id=""
  local os_like=""
  local ln_bin
  local systemctl_bin

  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    source /etc/os-release
    os_id="${ID:-}"
    os_like="${ID_LIKE:-}"
  fi
  if [[ "$os_id" != "debian" && "$os_like" != *"debian"* ]]; then
    echo "unsupported Linux distribution: ID=$os_id ID_LIKE=$os_like; sbctl currently supports Debian-family systems only" >&2
    exit 1
  fi

  ensure_singbox_version "$required_singbox_version"

  mkdir -p "$profiles_dir" "$(dirname "$active_link")"
  chown "$user_name":"$user_name" "$profiles_dir"
  chmod 755 "$profiles_dir"
  seed_profile "$profiles_dir" "$user_name"

  for profile_path in "$profiles_dir"/*.json; do
    [[ -e "$profile_path" ]] || continue
    if [[ "$(stat -c '%U' "$profile_path")" == "root" ]]; then
      chown "$user_name":"$user_name" "$profile_path"
    fi
  done

  ensure_managed_symlink "$active_link" "$default_profile_path"

  ln_bin="$(command -v ln)"
  systemctl_bin="$(command -v systemctl)"
  install_sudoers "$user_name ALL=(root) NOPASSWD: $ln_bin, $systemctl_bin" root

  systemctl enable sing-box
  if has_placeholders "$default_profile_path"; then
    echo "installed sbctl system files; edit $default_profile_path before starting sing-box"
    return
  fi

  sing-box check -c "$default_profile_path"
  systemctl restart sing-box
  echo "installed sbctl system files"
}

ensure_singbox_version() {
  local required_version="$1"
  install_sagernet_apt_source
  apt-get install -y sing-box || true

  if singbox_version_at_least "$required_version"; then
    return
  fi

  install_singbox_deb "$required_version"

  if ! singbox_version_at_least "$required_version"; then
    echo "failed to install sing-box >= $required_version; found $(installed_singbox_version)" >&2
    exit 1
  fi
}

install_sagernet_apt_source() {
  apt-get update
  apt-get install -y ca-certificates curl gpg
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL https://sing-box.app/gpg.key | gpg --dearmor -o /etc/apt/keyrings/sagernet.gpg
  chmod a+r /etc/apt/keyrings/sagernet.gpg
  echo "deb [signed-by=/etc/apt/keyrings/sagernet.gpg] https://deb.sagernet.org/ * *" > /etc/apt/sources.list.d/sagernet.list
  apt-get update
}

install_singbox_deb() {
  local version="$1"
  local deb_arch
  local tmp_deb

  deb_arch="$(singbox_release_arch)"
  tmp_deb="$(mktemp "/tmp/sing-box_${version}_${deb_arch}.XXXXXX.deb")"
  curl -fL -o "$tmp_deb" "https://github.com/SagerNet/sing-box/releases/download/v${version}/sing-box_${version}_linux_${deb_arch}.deb"
  apt-get install -y "$tmp_deb"
  rm -f "$tmp_deb"
}

singbox_release_arch() {
  case "$(dpkg --print-architecture)" in
    amd64)
      echo "amd64"
      ;;
    arm64)
      echo "arm64"
      ;;
    armhf)
      echo "armv7"
      ;;
    *)
      echo "unsupported Debian architecture for sing-box GitHub release fallback: $(dpkg --print-architecture)" >&2
      exit 1
      ;;
  esac
}

installed_singbox_version() {
  if ! command -v sing-box >/dev/null 2>&1; then
    echo "missing"
    return
  fi
  sing-box version | awk 'NR == 1 { print $3 }'
}

singbox_version_at_least() {
  local required_version="$1"
  local current_version
  current_version="$(installed_singbox_version)"

  [[ "$current_version" != "missing" ]] || return 1
  [[ "$(printf "%s\n%s\n" "$required_version" "$current_version" | sort -V | head -n1)" == "$required_version" ]]
}
