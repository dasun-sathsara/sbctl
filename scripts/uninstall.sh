#!/usr/bin/env bash
set -euo pipefail

# Profiles and logs are deliberately preserved: an uninstall should remove sbctl,
# not destroy the user's configurations or their operational history. The paths
# to delete manually are printed at the end.

remove_darwin() {
  local label="system/app.lexiflix.singbox"
  local plist_path="/Library/LaunchDaemons/app.lexiflix.singbox.plist"

  if launchctl print "$label" >/dev/null 2>&1; then
    launchctl bootout "$label" || true
  fi
  rm -f "$plist_path"
  rm -f /etc/sudoers.d/sbctl
  rm -f /usr/local/etc/sing-box/config.json

  echo "removed the launchd definition, sudo rules and active configuration"
  echo "profiles kept in /usr/local/etc/sing-box/profiles"
  echo "logs kept in /var/log/sing-box"
}

remove_linux() {
  systemctl stop sing-box || true
  systemctl disable sing-box || true
  rm -f /etc/systemd/system/sing-box.service.d/10-sbctl.conf
  rmdir /etc/systemd/system/sing-box.service.d 2>/dev/null || true
  systemctl daemon-reload || true

  rm -f /etc/sudoers.d/sbctl
  rm -f /etc/sing-box/config.json

  echo "removed the systemd drop-in, sudo rules and active configuration"
  echo "profiles kept in /etc/sing-box/profiles"
}

case "$(uname -s)" in
  Darwin) remove_darwin ;;
  Linux)  remove_linux ;;
  *)
    echo "unsupported platform: $(uname -s)" >&2
    exit 1
    ;;
esac

# Remove the binary here as well as in the Makefile, so running this script
# directly leaves the same state as `make uninstall`.
if [[ -e /usr/local/bin/sbctl ]]; then
  rm -f /usr/local/bin/sbctl
  echo "removed /usr/local/bin/sbctl"
fi
