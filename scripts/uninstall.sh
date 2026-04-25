#!/usr/bin/env bash
set -euo pipefail

case "$(uname -s)" in
  Darwin)
    plist_path="/Library/LaunchDaemons/app.lexiflix.singbox.plist"
    sudoers_path="/etc/sudoers.d/sbctl"
    active_link="/usr/local/etc/sing-box/config.json"

    if launchctl print system/app.lexiflix.singbox >/dev/null 2>&1; then
      launchctl bootout system/app.lexiflix.singbox || true
    fi

    rm -f "$plist_path" "$sudoers_path" "$active_link"
    echo "removed sbctl macOS system files"
    ;;
  Linux)
    if [[ -r /etc/os-release ]]; then
      # shellcheck disable=SC1091
      source /etc/os-release
    fi
    if [[ "${ID:-}" != "debian" && "${ID_LIKE:-}" != *"debian"* ]]; then
      echo "unsupported Linux distribution: ID=${ID:-} ID_LIKE=${ID_LIKE:-}" >&2
      exit 1
    fi
    systemctl stop sing-box || true
    rm -f /etc/sudoers.d/sbctl /etc/sing-box/config.json
    echo "removed sbctl Debian-family system files"
    ;;
  *)
    echo "unsupported platform: $(uname -s)" >&2
    exit 1
    ;;
esac

# Deliberately preserve profiles so uninstall does not destroy user-managed configs.
# Remove the platform profile directory manually only if you want a full reset.
#
# Deliberately preserve logs so uninstall does not wipe operational history.
# Remove the platform log directory manually if you want a full cleanup.
