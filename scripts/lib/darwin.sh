#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

install_darwin() {
  local profiles_dir="/usr/local/etc/sing-box/profiles"
  local active_link="/usr/local/etc/sing-box/config.json"
  local plist_path="/Library/LaunchDaemons/app.lexiflix.singbox.plist"
  local log_dir="/var/log/sing-box"
  local default_profile_path="$profiles_dir/$default_profile_name.json"

  ensure_singbox_darwin
  verify_singbox

  # Resolve the real binary path. Homebrew installs to /opt/homebrew on Apple
  # Silicon and /usr/local on Intel, so the launchd definition must record
  # whatever is actually present rather than assuming either.
  local singbox_bin
  singbox_bin="$(command -v sing-box)"

  mkdir -p "$profiles_dir" "$(dirname "$active_link")" "$log_dir"
  chown "$user_name":wheel "$profiles_dir"
  chmod 755 "$profiles_dir"
  chown root:wheel "$log_dir"
  chmod 755 "$log_dir"

  seed_profile "$profiles_dir" wheel

  # Profiles must stay editable by their owner; an install run under sudo can
  # otherwise leave root-owned files the user can no longer edit.
  local profile_path
  for profile_path in "$profiles_dir"/*.json; do
    [[ -e "$profile_path" ]] || continue
    if [[ "$(stat -f '%Su' "$profile_path")" == "root" ]]; then
      chown "$user_name":wheel "$profile_path"
    fi
  done

  ensure_managed_symlink "$active_link" "$default_profile_path"
  install_sudoers wheel
  install_launch_daemon "$plist_path" "$singbox_bin" "$active_link" "$log_dir"

  if has_placeholders "$default_profile_path"; then
    echo
    echo "sbctl is installed."
    echo "Fill in the placeholder values before starting sing-box:"
    echo "  sbctl edit $default_profile_name"
    return
  fi

  sing-box check -c "$default_profile_path"
  start_launch_daemon "$plist_path"
  echo "sbctl is installed and sing-box is running."
}

ensure_singbox_darwin() {
  if command -v sing-box >/dev/null 2>&1; then
    return
  fi

  local brew_bin=""
  if command -v brew >/dev/null 2>&1; then
    brew_bin="$(command -v brew)"
  elif [[ -x /opt/homebrew/bin/brew ]]; then
    brew_bin="/opt/homebrew/bin/brew"
  elif [[ -x /usr/local/bin/brew ]]; then
    brew_bin="/usr/local/bin/brew"
  fi

  if [[ -z "$brew_bin" ]]; then
    echo "sing-box is not installed and Homebrew was not found." >&2
    echo "Install Homebrew or sing-box, then run 'make install' again." >&2
    exit 1
  fi

  # Homebrew refuses to run as root, so drop back to the invoking user.
  sudo -u "$user_name" "$brew_bin" install sing-box

  # brew may install outside the current PATH; make the new binary findable.
  local brew_prefix
  brew_prefix="$(sudo -u "$user_name" "$brew_bin" --prefix)"
  export PATH="$brew_prefix/bin:$PATH"
}

install_launch_daemon() {
  local plist_path="$1" singbox_bin="$2" active_link="$3" log_dir="$4"
  local tmp_plist
  tmp_plist="$(mktemp)"
  # shellcheck disable=SC2064
  trap "rm -f '$tmp_plist'" RETURN

  cat > "$tmp_plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>app.lexiflix.singbox</string>
  <key>ProgramArguments</key>
  <array>
    <string>$singbox_bin</string>
    <string>run</string>
    <string>-c</string>
    <string>$active_link</string>
  </array>
  <key>KeepAlive</key>
  <true/>
  <key>RunAtLoad</key>
  <false/>
  <key>StandardErrorPath</key>
  <string>$log_dir/error.log</string>
  <key>StandardOutPath</key>
  <string>$log_dir/access.log</string>
</dict>
</plist>
EOF

  if ! plutil -lint "$tmp_plist" >/dev/null; then
    echo "generated launchd definition is invalid; aborting without changing $plist_path" >&2
    exit 1
  fi
  install -o root -g wheel -m 644 "$tmp_plist" "$plist_path"
}

start_launch_daemon() {
  local plist_path="$1"
  local label="system/app.lexiflix.singbox"

  if launchctl print "$label" >/dev/null 2>&1; then
    if launchctl kickstart -k "$label"; then
      return
    fi
    launchctl bootout "$label" || true
  fi
  launchctl bootstrap system "$plist_path"
}
