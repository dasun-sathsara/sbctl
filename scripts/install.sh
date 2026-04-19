#!/usr/bin/env bash
set -euo pipefail

profiles_dir="/usr/local/etc/sing-box/profiles"
active_link="/usr/local/etc/sing-box/config.json"
plist_path="/Library/LaunchDaemons/app.lexiflix.singbox.plist"
log_dir="/var/log/sing-box"
sudoers_path="/etc/sudoers.d/sbctl"
default_profile_name="sg-cloudflare"
default_profile_path="$profiles_dir/$default_profile_name.json"
tmp_sudoers="$(mktemp)"
tmp_plist="$(mktemp)"
tmp_profile="$(mktemp)"
user_name="${SUDO_USER:-$(logname 2>/dev/null || whoami)}"
singbox_bin="$(command -v sing-box)"

cleanup() {
  rm -f "$tmp_sudoers" "$tmp_plist" "$tmp_profile"
}
trap cleanup EXIT

write_seed_profile() {
  cat > "$tmp_profile" <<'JSON'
  {
    "log": {
      "level": "info",
      "timestamp": true
    },
    "dns": {
      "servers": [
        {
          "type": "https",
          "tag": "cloudflare-doh",
          "server": "1.1.1.1",
          "domain_resolver": "local-dns",
          "detour": "proxy"
        },
        {
          "type": "local",
          "tag": "local-dns",
          "detour": "direct"
        }
      ],
      "strategy": "ipv4_only",
      "final": "cloudflare-doh"
    },
    "inbounds": [
      {
        "type": "tun",
        "tag": "tun-in",
        "interface_name": "utun123",
        "address": [
          "172.19.0.1/30"
        ],
        "auto_route": true,
        "strict_route": true,
        "stack": "gvisor",
        "mtu": 1350
      }
    ],
    "outbounds": [
      {
        "type": "vless",
        "tag": "proxy",
        "server": "54.255.210.30",
        "server_port": 443,
        "uuid": "7beb776c-c98f-47f3-a0c5-c2b29b5bba90",
        "flow": "xtls-rprx-vision",
        "tls": {
          "enabled": true,
          "server_name": "www.zoom.us",
          "utls": {
            "enabled": true,
            "fingerprint": "chrome"
          },
          "reality": {
            "enabled": true,
            "public_key": "-uo9S2j_h136hqPobdaURNaSlxylWAGF5fEzHIUMNkA",
            "short_id": "818f5c1f"
          }
        }
      },
      {
        "type": "direct",
        "tag": "direct"
      }
    ],
    "route": {
      "auto_detect_interface": true,
      "default_domain_resolver": {
        "server": "local-dns",
        "strategy": "ipv4_only"
      },
      "rules": [
        {
          "protocol": "dns",
          "action": "hijack-dns"
        },
        {
          "inbound": "tun-in",
          "action": "sniff"
        },
        {
          "ip_is_private": true,
          "outbound": "direct"
        },
        {
          "outbound": "proxy"
        }
      ]
    }
  }
JSON
}

if [[ -z "$singbox_bin" ]]; then
  echo "sing-box is not installed or not in PATH" >&2
  exit 1
fi

mkdir -p "$profiles_dir" "$(dirname "$active_link")" "$log_dir"
chown root:wheel "$log_dir"
chmod 755 "$log_dir"

write_seed_profile

if [[ ! -f "$default_profile_path" ]]; then
  install -o root -g wheel -m 644 "$tmp_profile" "$default_profile_path"
fi

if [[ ! -e "$active_link" ]]; then
  ln -sfn "$default_profile_path" "$active_link"
fi

cat > "$tmp_sudoers" <<EOF
$user_name ALL=(root) NOPASSWD: /bin/ln, /bin/launchctl
EOF

if [[ ! -f "$sudoers_path" ]] || ! cmp -s "$tmp_sudoers" "$sudoers_path"; then
  if ! visudo -cf "$tmp_sudoers"; then
    rm -f "$tmp_sudoers"
    echo "sudoers validation failed; aborting" >&2
    exit 1
  fi

  install -o root -g wheel -m 440 "$tmp_sudoers" "$sudoers_path"
fi

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

if launchctl print system/app.lexiflix.singbox >/dev/null 2>&1; then
  launchctl bootout system/app.lexiflix.singbox || true
fi
launchctl bootstrap system "$plist_path"

sing-box check -c "$default_profile_path"

echo "installed sbctl system files"
