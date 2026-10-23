#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA_DIR="${CLOUDRIFTS_DATA_DIR:-$ROOT_DIR/.local/data}"
mkdir -p "$DATA_DIR"

cleanup() {
  if [[ -n "${API_PID:-}" ]]; then kill "$API_PID" 2>/dev/null || true; fi
  if [[ -n "${WEB_PID:-}" ]]; then kill "$WEB_PID" 2>/dev/null || true; fi
}
trap cleanup EXIT INT TERM

cd "$ROOT_DIR"
CLOUDRIFTS_DATA_DIR="$DATA_DIR" CLOUDRIFTS_HTTP_ADDR="${CLOUDRIFTS_HTTP_ADDR:-:8080}" go run ./cmd/cloudriftsd &
API_PID=$!

cd "$ROOT_DIR/apps/web"
VITE_API_BASE="${VITE_API_BASE:-http://localhost:8080}" npm run dev &
WEB_PID=$!

cat <<MSG
Cloudrifts dev is starting.

API:  http://localhost:8080/api/health
PWA:  http://localhost:5173
Data: $DATA_DIR

Press Ctrl+C to stop.
MSG

wait
