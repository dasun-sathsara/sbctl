#!/usr/bin/env bash

# shellcheck source=scripts/lib/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

install_darwin() {
  local profiles_dir="/usr/local/etc/sing-box/profiles"
  local active_link="/usr/local/etc/sing-box/config.json"
  local plist_path="/Library/LaunchDaemons/app.lexiflix.singbox.plist"
  local log_dir="/var/log/sing-box"
  local default_profile_path="$profiles_dir/$default_profile_name.json"
  local singbox_bin
  local tmp_plist

  if ! command -v sing-box >/dev/null 2>&1; then
    local brew_bin=""
    if command -v brew >/dev/null 2>&1; then
      brew_bin="$(command -v brew)"
    elif [[ -x /opt/homebrew/bin/brew ]]; then
      brew_bin="/opt/homebrew/bin/brew"
    elif [[ -x /usr/local/bin/brew ]]; then
      brew_bin="/usr/local/bin/brew"
    fi
    if [[ -n "$brew_bin" ]]; then
      sudo -u "$user_name" "$brew_bin" install sing-box
    else
      echo "sing-box is not installed and Homebrew is unavailable; install Homebrew or sing-box, then rerun make install" >&2
      exit 1
    fi
  fi
  singbox_bin="$(command -v sing-box)"

  mkdir -p "$profiles_dir" "$(dirname "$active_link")" "$log_dir"
  chown "$user_name":wheel "$profiles_dir"
  chmod 755 "$profiles_dir"
  chown root:wheel "$log_dir"
  chmod 755 "$log_dir"

  seed_profile "$profiles_dir" wheel

  for profile_path in "$profiles_dir"/*.json; do
    [[ -e "$profile_path" ]] || continue
    if [[ "$(stat -f '%Su' "$profile_path")" == "root" ]]; then
      chown "$user_name":wheel "$profile_path"
    fi
  done

  ensure_managed_symlink "$active_link" "$default_profile_path"

  install_sudoers "$user_name ALL=(root) NOPASSWD: /bin/ln, /bin/launchctl" wheel

  tmp_plist="$(mktemp)"
  trap 'rm -f "$tmp_plist"' RETURN
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
    <string>/usr/local/etc/sing-box/config.json</string>
  </array>
  <key>KeepAlive</key>
  <true/>
  <key>RunAtLoad</key>
  <false/>
  <key>StandardErrorPath</key>
  <string>/var/log/sing-box/error.log</string>
  <key>StandardOutPath</key>
  <string>/var/log/sing-box/access.log</string>
</dict>
</plist>
EOF

  install -o root -g wheel -m 644 "$tmp_plist" "$plist_path"
  if ! plutil -lint "$plist_path"; then
    echo "plist validation failed; aborting" >&2
    exit 1
  fi

  if has_placeholders "$default_profile_path"; then
    echo "installed sbctl system files; edit $default_profile_path before starting sing-box"
    return
  fi

  sing-box check -c "$default_profile_path"
  if launchctl print system/app.lexiflix.singbox >/dev/null 2>&1; then
    if ! launchctl kickstart -k system/app.lexiflix.singbox; then
      launchctl bootout system/app.lexiflix.singbox || true
      launchctl bootstrap system "$plist_path"
    fi
  else
    launchctl bootstrap system "$plist_path"
  fi
  echo "installed sbctl system files"
}
