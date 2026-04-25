#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

case "$(uname -s)" in
  Darwin)
    # shellcheck source=scripts/lib/darwin.sh
    source "$script_dir/lib/darwin.sh"
    install_darwin
    ;;
  Linux)
    # shellcheck source=scripts/lib/debian.sh
    source "$script_dir/lib/debian.sh"
    install_debian
    ;;
  *)
    echo "unsupported platform: $(uname -s)" >&2
    exit 1
    ;;
esac
