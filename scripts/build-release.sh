#!/usr/bin/env bash
set -euo pipefail
export COPYFILE_DISABLE=1

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${CLOUDRIFTS_VERSION:-0.1.0-dev}"
GOOS_TARGET="${GOOS_TARGET:-linux}"
GOARCH_TARGET="${GOARCH_TARGET:-amd64}"
OUT_DIR="$ROOT_DIR/build/release/cloudrifts-panel-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}"
TARBALL="$OUT_DIR.tar.gz"

cd "$ROOT_DIR"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1"
    exit 1
  fi
}

need_cmd go
need_cmd npm
need_cmd tar

rm -rf "$OUT_DIR" "$TARBALL"
mkdir -p "$OUT_DIR/bin" "$OUT_DIR/web" "$OUT_DIR/systemd"

echo "Building web app..."
npm ci --prefix "$ROOT_DIR/apps/web"
npm run build --prefix "$ROOT_DIR/apps/web"

echo "Building ${GOOS_TARGET}/${GOARCH_TARGET} binaries..."
CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/bin/cloudriftsd" ./cmd/cloudriftsd
CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/bin/cloudrifts-agent" ./cmd/cloudrifts-agent
CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/bin/cloudrifts-filed" ./cmd/cloudrifts-filed
CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/bin/cloudrifts" ./cmd/cloudriftsctl
CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/bin/cloudriftsctl" ./cmd/cloudriftsctl

COPYFILE_DISABLE=1 cp -R "$ROOT_DIR/apps/web/dist"/. "$OUT_DIR/web"/
cp "$ROOT_DIR/packaging/systemd/cloudriftsd.service" "$OUT_DIR/systemd/cloudriftsd.service"
cp "$ROOT_DIR/packaging/bundle/install.sh" "$OUT_DIR/install.sh"
cp "$ROOT_DIR/packaging/bundle/uninstall.sh" "$OUT_DIR/uninstall.sh"
cat > "$OUT_DIR/manifest.json" <<MANIFEST
{
  "name": "cloudrifts-panel",
  "version": "$VERSION",
  "target": "$GOOS_TARGET/$GOARCH_TARGET",
  "built_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "phase": "phase-1-test-installer"
}
MANIFEST
chmod +x "$OUT_DIR/install.sh" "$OUT_DIR/uninstall.sh" "$OUT_DIR/bin/"*

if command -v xattr >/dev/null 2>&1; then
  xattr -cr "$OUT_DIR" 2>/dev/null || true
fi

COPYFILE_DISABLE=1 tar --format ustar -C "$(dirname "$OUT_DIR")" -czf "$TARBALL" "$(basename "$OUT_DIR")"

echo "Release bundle ready: $TARBALL"
echo "Install on Ubuntu 24.04 VPS:"
echo "  tar -xzf $(basename "$TARBALL")"
echo "  cd $(basename "$OUT_DIR")"
echo "  sudo ./install.sh"
