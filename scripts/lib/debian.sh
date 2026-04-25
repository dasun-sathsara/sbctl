#!/usr/bin/env bash

# shellcheck source=scripts/lib/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

install_debian() {
  local profiles_dir="/etc/sing-box/profiles"
  local active_link="/etc/sing-box/config.json"
  local default_profile_path="$profiles_dir/$default_profile_name.json"
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

  if ! command -v sing-box >/dev/null 2>&1; then
    apt-get update
    apt-get install -y curl gpg
    install -d -m 0755 /etc/apt/keyrings
    curl -fsSL https://sing-box.app/gpg.key | gpg --dearmor -o /etc/apt/keyrings/sagernet.gpg
    chmod a+r /etc/apt/keyrings/sagernet.gpg
    echo "deb [signed-by=/etc/apt/keyrings/sagernet.gpg] https://deb.sagernet.org/ * *" > /etc/apt/sources.list.d/sagernet.list
    apt-get update
    apt-get install -y sing-box
  fi

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

  if [[ ! -e "$active_link" ]]; then
    ln -sfn "$default_profile_path" "$active_link"
  fi

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
