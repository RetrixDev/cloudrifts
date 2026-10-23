#!/usr/bin/env bash
set -euo pipefail

VERSION="0.1.0-dev"
TAG="v${VERSION}"
ASSET="cloudrifts-panel-${VERSION}-linux-amd64.tar.gz"
RELEASE_URL="${CLOUDRIFTS_RELEASE_URL:-https://github.com/RetrixDev/cloudrifts/releases/download/${TAG}/${ASSET}}"
EXPECTED_SHA256="${CLOUDRIFTS_SHA256:-ed22857fc20ec0377269238e95b2fe12abf4ee7d3ea63c409cd8097fecf0d64d}"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  cat <<HELP
Cloudrifts bootstrap installer

Usage:
  curl -fsSL https://raw.githubusercontent.com/RetrixDev/cloudrifts/main/install.sh | sudo bash
  sudo ./install.sh [bundle installer options]

Environment overrides:
  CLOUDRIFTS_RELEASE_URL   Download a custom release tarball.
  CLOUDRIFTS_SHA256        Expected SHA256 for the tarball.

The installer supports Ubuntu 24.04 x86_64 VPS servers.
HELP
  exit 0
fi

if (( EUID != 0 )); then
  if [[ -f "$0" ]]; then
    exec sudo -E bash "$0" "$@"
  fi
  echo "Run as root, for example: curl -fsSL https://raw.githubusercontent.com/RetrixDev/cloudrifts/main/install.sh | sudo bash" >&2
  exit 1
fi

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "Cloudrifts installs on Ubuntu 24.04 Linux servers only." >&2
  exit 1
fi

if [[ "$(uname -m)" != "x86_64" && "$(uname -m)" != "amd64" ]]; then
  echo "This release is built for Linux x86_64/amd64 servers." >&2
  exit 1
fi

if [[ ! -f /etc/os-release ]]; then
  echo "Unsupported server: missing /etc/os-release." >&2
  exit 1
fi

# shellcheck disable=SC1091
. /etc/os-release
if [[ "${ID:-}" != "ubuntu" || "${VERSION_ID:-}" != "24.04" ]]; then
  echo "Cloudrifts Phase 1 supports Ubuntu 24.04 only. Detected: ${PRETTY_NAME:-unknown}." >&2
  exit 1
fi

for cmd in curl tar sha256sum; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Missing required command: $cmd" >&2
    exit 1
  fi
done

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

echo "Cloudrifts ${VERSION} bootstrap"
echo "Downloading ${ASSET}"
curl -fL "$RELEASE_URL" -o "$tmp_dir/$ASSET"

echo "Verifying SHA256"
(
  cd "$tmp_dir"
  printf '%s  %s\n' "$EXPECTED_SHA256" "$ASSET" | sha256sum -c -
)

echo "Extracting release bundle"
tar -xzf "$tmp_dir/$ASSET" -C "$tmp_dir"

bundle_dir="$tmp_dir/cloudrifts-panel-${VERSION}-linux-amd64"
if [[ ! -x "$bundle_dir/install.sh" ]]; then
  echo "Downloaded bundle is missing install.sh." >&2
  exit 1
fi

echo "Starting Cloudrifts bundle installer"
cd "$bundle_dir"
./install.sh "$@"

