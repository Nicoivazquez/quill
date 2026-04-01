#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v npm >/dev/null 2>&1; then
  echo "npm is required but was not found in PATH." >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is required but was not found in PATH." >&2
  exit 1
fi

echo "Building frontend for embed..."
cd "$ROOT_DIR/web/frontend"
npm ci
npm run build

echo "Copying frontend assets into Go embed path..."
cd "$ROOT_DIR"
rm -rf internal/web/dist
cp -r web/frontend/dist internal/web/

echo "Building backend binary for Electron packaging..."
mkdir -p dist/desktop-backend
go build -o dist/desktop-backend/quill cmd/server/main.go
chmod +x dist/desktop-backend/quill

# Patch sherpa-onnx rpath so the binary finds dylibs in the bundled tools/lib
# directory instead of the build machine's Go module cache.
if [[ "$(uname -s)" == "Darwin" ]] && command -v install_name_tool >/dev/null 2>&1; then
  BINARY="$ROOT_DIR/dist/desktop-backend/quill"
  # Remove all existing LC_RPATH entries (they point to the build machine's paths)
  while IFS= read -r old_rpath; do
    [[ -z "$old_rpath" ]] && continue
    install_name_tool -delete_rpath "$old_rpath" "$BINARY" 2>/dev/null || true
  done < <(otool -l "$BINARY" | awk '/cmd LC_RPATH/{found=1} found && /path /{print $2; found=0}')

  # Add rpath pointing to the bundled tools/lib directory relative to the binary.
  # In the packaged app: binary is at Resources/backend/quill,
  # dylibs are at Resources/tools/lib/
  install_name_tool -add_rpath "@executable_path/../tools/lib" "$BINARY"
  echo "Patched backend binary rpath → @executable_path/../tools/lib"
fi

echo "Desktop backend prepared at dist/desktop-backend/quill"
