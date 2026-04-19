#!/usr/bin/env bash
set -euo pipefail

plist_path="/Library/LaunchDaemons/app.lexiflix.singbox.plist"
sudoers_path="/etc/sudoers.d/sbctl"
active_link="/usr/local/etc/sing-box/config.json"

if launchctl print system/app.lexiflix.singbox >/dev/null 2>&1; then
  launchctl bootout system/app.lexiflix.singbox || true
fi

rm -f "$plist_path"
rm -f "$sudoers_path"
rm -f "$active_link"

# Deliberately preserve profiles so uninstall does not destroy user-managed configs.
# Remove /usr/local/etc/sing-box/profiles manually only if you want a full reset.
#
# Deliberately preserve logs so uninstall does not wipe operational history.
# Remove /var/log/sing-box manually if you want a full cleanup.

echo "removed sbctl system files"
