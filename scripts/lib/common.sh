#!/usr/bin/env bash

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
default_profile_name="sg-cloudflare"
sudoers_path="/etc/sudoers.d/sbctl"
user_name="${SUDO_USER:-$(logname 2>/dev/null || whoami)}"

seed_profile() {
  local profiles_dir="$1"
  local group_name="$2"
  local default_profile_path="$profiles_dir/$default_profile_name.json"

  if [[ ! -f "$default_profile_path" ]]; then
    install -o "$user_name" -g "$group_name" -m 644 "$repo_root/assets/skeleton.json" "$default_profile_path"
  fi
}

has_placeholders() {
  local path="$1"
  local data
  data="$(<"$path")"
  [[ "$data" == *"TODO_SERVER_IP_OR_HOST"* || "$data" == *"TODO_UUID"* || "$data" == *"TODO_SNI_HOSTNAME"* || "$data" == *"TODO_REALITY_PUBLIC_KEY"* || "$data" == *"TODO_SHORT_ID"* ]]
}

install_sudoers() {
  local content="$1"
  local group_name="$2"
  local tmp_sudoers
  tmp_sudoers="$(mktemp)"
  printf "%s\n" "$content" > "$tmp_sudoers"

  if [[ ! -f "$sudoers_path" ]] || ! cmp -s "$tmp_sudoers" "$sudoers_path"; then
    if ! visudo -cf "$tmp_sudoers"; then
      rm -f "$tmp_sudoers"
      echo "sudoers validation failed; aborting" >&2
      exit 1
    fi
    install -o root -g "$group_name" -m 440 "$tmp_sudoers" "$sudoers_path"
  fi
  rm -f "$tmp_sudoers"
}
