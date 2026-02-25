#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$ROOT_DIR/dist/desktop-tools"

mkdir -p "$OUT_DIR"
rm -rf "$OUT_DIR"/*

resolve_tool_path() {
  local tool_name="$1"
  local env_var_name="$2"
  local source_path="${!env_var_name:-}"

  if [[ -z "$source_path" ]]; then
    source_path="$(command -v "$tool_name" || true)"
  fi

  if [[ -z "$source_path" ]]; then
    if [[ "$tool_name" == "yt-dlp" ]]; then
      local yt_dlp_url="${SCRIBERR_YTDLP_DOWNLOAD_URL:-https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_macos}"
      if command -v curl >/dev/null 2>&1; then
        echo "yt-dlp not found in PATH; downloading from $yt_dlp_url" >&2
        if ! curl -fsSL "$yt_dlp_url" -o "$OUT_DIR/yt-dlp"; then
          echo "Failed to download yt-dlp automatically. Set SCRIBERR_YTDLP_SOURCE to a local yt-dlp binary path." >&2
          exit 1
        fi
        chmod +x "$OUT_DIR/yt-dlp"
        echo "Bundled yt-dlp from $yt_dlp_url" >&2
        echo "$OUT_DIR/yt-dlp"
        return
      fi
    fi

    echo "Missing required tool '$tool_name'. Install it or set $env_var_name to an absolute path." >&2
    exit 1
  fi

  if [[ ! -f "$source_path" ]]; then
    echo "Tool path for '$tool_name' is not a file: $source_path" >&2
    exit 1
  fi

  echo "$source_path"
}

bundle_tool() {
  local tool_name="$1"
  local source_path="$2"

  cp -L "$source_path" "$OUT_DIR/$tool_name"
  chmod +x "$OUT_DIR/$tool_name"
  chmod u+w "$OUT_DIR/$tool_name"
  echo "Bundled $tool_name from $source_path"
}

is_system_dependency() {
  local dep_path="$1"
  [[ "$dep_path" == /System/Library/* || "$dep_path" == /usr/lib/* ]]
}

resolve_dependency_reference() {
  local dep_ref="$1"
  local parent_file="$2"
  local parent_dir
  parent_dir="$(cd "$(dirname "$parent_file")" && pwd)"

  if [[ "$dep_ref" == @loader_path/* ]]; then
    echo "$parent_dir/${dep_ref#@loader_path/}"
    return
  fi

  if [[ "$dep_ref" == @executable_path/* ]]; then
    echo "$OUT_DIR/${dep_ref#@executable_path/}"
    return
  fi

  if [[ "$dep_ref" == @rpath/* ]]; then
    local dep_base
    dep_base="$(basename "$dep_ref")"
    local candidates=(
      "$parent_dir/$dep_base"
      "$OUT_DIR/lib/$dep_base"
      "/opt/homebrew/lib/$dep_base"
      "/usr/local/lib/$dep_base"
    )
    local candidate
    for candidate in "${candidates[@]}"; do
      if [[ -f "$candidate" ]]; then
        echo "$candidate"
        return
      fi
    done
    echo "$dep_ref"
    return
  fi

  echo "$dep_ref"
}

list_macos_dependencies() {
  local target="$1"
  otool -L "$target" | tail -n +2 | awk '{print $1}'
}

enqueue_dependency() {
  local dep_ref="$1"
  local parent_file="$2"
  local dep_path
  dep_path="$(resolve_dependency_reference "$dep_ref" "$parent_file")"

  # We only bundle resolved file paths.
  if [[ "$dep_path" == @* ]]; then
    return
  fi

  if is_system_dependency "$dep_path"; then
    return
  fi

  if [[ ! -f "$dep_path" ]]; then
    echo "Warning: unresolved dependency $dep_ref referenced by $parent_file" >&2
    return
  fi

  local dep_base
  dep_base="$(basename "$dep_path")"
  if [[ -f "$OUT_DIR/lib/$dep_base" ]]; then
    return
  fi

  if grep -Fxq "$dep_path" "$DEP_QUEUE_FILE"; then
    return
  fi

  echo "$dep_path" >> "$DEP_QUEUE_FILE"
}

patch_install_names() {
  local target="$1"
  local mode="$2" # binary | library
  local dep_ref

  while IFS= read -r dep_ref; do
    [[ -z "$dep_ref" ]] && continue

    local dep_path
    dep_path="$(resolve_dependency_reference "$dep_ref" "$target")"

    if [[ "$dep_path" == @* ]]; then
      continue
    fi
    if is_system_dependency "$dep_path"; then
      continue
    fi

    local dep_base
    dep_base="$(basename "$dep_path")"
    if [[ ! -f "$OUT_DIR/lib/$dep_base" ]]; then
      continue
    fi

    local new_ref
    if [[ "$mode" == "binary" ]]; then
      new_ref="@executable_path/lib/$dep_base"
    else
      new_ref="@loader_path/$dep_base"
    fi

    if [[ "$dep_ref" != "$new_ref" ]]; then
      install_name_tool -change "$dep_ref" "$new_ref" "$target"
    fi
  done < <(list_macos_dependencies "$target")

  if [[ "$mode" == "library" ]]; then
    install_name_tool -id "@loader_path/$(basename "$target")" "$target"
  fi
}

bundle_macos_ffmpeg_runtime() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    return
  fi

  if ! command -v otool >/dev/null 2>&1 || ! command -v install_name_tool >/dev/null 2>&1; then
    echo "Skipping ffmpeg runtime bundling: otool/install_name_tool not available"
    return
  fi

  mkdir -p "$OUT_DIR/lib"
  DEP_QUEUE_FILE="$(mktemp)"
  : > "$DEP_QUEUE_FILE"

  local bin
  for bin in "$OUT_DIR/ffmpeg" "$OUT_DIR/ffprobe"; do
    if [[ ! -f "$bin" ]]; then
      continue
    fi
    while IFS= read -r dep_ref; do
      [[ -z "$dep_ref" ]] && continue
      enqueue_dependency "$dep_ref" "$bin"
    done < <(list_macos_dependencies "$bin")
  done

  while [[ -s "$DEP_QUEUE_FILE" ]]; do
    local dep_path
    dep_path="$(head -n 1 "$DEP_QUEUE_FILE")"
    tail -n +2 "$DEP_QUEUE_FILE" > "$DEP_QUEUE_FILE.next" || true
    mv "$DEP_QUEUE_FILE.next" "$DEP_QUEUE_FILE"

    [[ -z "$dep_path" ]] && continue
    [[ ! -f "$dep_path" ]] && continue

    local dep_base
    dep_base="$(basename "$dep_path")"
    local dep_dest="$OUT_DIR/lib/$dep_base"
    if [[ -f "$dep_dest" ]]; then
      continue
    fi

    cp -L "$dep_path" "$dep_dest"
    chmod u+w "$dep_dest"
    echo "Bundled ffmpeg dependency $dep_base from $dep_path"

    while IFS= read -r child_dep_ref; do
      [[ -z "$child_dep_ref" ]] && continue
      enqueue_dependency "$child_dep_ref" "$dep_dest"
    done < <(list_macos_dependencies "$dep_dest")
  done

  local lib
  for lib in "$OUT_DIR"/lib/*.dylib; do
    [[ -f "$lib" ]] || continue
    patch_install_names "$lib" "library"
  done

  if [[ -f "$OUT_DIR/ffmpeg" ]]; then
    patch_install_names "$OUT_DIR/ffmpeg" "binary"
  fi
  if [[ -f "$OUT_DIR/ffprobe" ]]; then
    patch_install_names "$OUT_DIR/ffprobe" "binary"
  fi

  rm -f "$DEP_QUEUE_FILE"
  echo "Bundled macOS ffmpeg runtime libraries into $OUT_DIR/lib"
}

codesign_macos_ffmpeg_runtime() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    return
  fi
  if ! command -v codesign >/dev/null 2>&1; then
    echo "Skipping ad-hoc signing for ffmpeg runtime: codesign not available"
    return
  fi

  local file
  for file in "$OUT_DIR"/lib/*.dylib "$OUT_DIR/ffmpeg" "$OUT_DIR/ffprobe"; do
    [[ -f "$file" ]] || continue
    codesign --force --sign - "$file" >/dev/null
  done

  echo "Applied ad-hoc signatures to bundled ffmpeg runtime artifacts"
}

uv_source="$(resolve_tool_path "uv" "SCRIBERR_UV_SOURCE")"
ffmpeg_source="$(resolve_tool_path "ffmpeg" "SCRIBERR_FFMPEG_SOURCE")"
ffprobe_source="$(resolve_tool_path "ffprobe" "SCRIBERR_FFPROBE_SOURCE")"
ytdlp_source="$(resolve_tool_path "yt-dlp" "SCRIBERR_YTDLP_SOURCE")"

bundle_tool "uv" "$uv_source"
bundle_tool "ffmpeg" "$ffmpeg_source"
bundle_tool "ffprobe" "$ffprobe_source"
if [[ "$ytdlp_source" != "$OUT_DIR/yt-dlp" ]]; then
  bundle_tool "yt-dlp" "$ytdlp_source"
fi

bundle_macos_ffmpeg_runtime
codesign_macos_ffmpeg_runtime

echo "Desktop tools prepared in $OUT_DIR"
