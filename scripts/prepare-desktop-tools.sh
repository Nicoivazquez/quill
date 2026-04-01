#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$ROOT_DIR/dist/desktop-tools"
LOCK_FILE="$ROOT_DIR/scripts/desktop-tools.lock.env"
DEFAULT_YTDLP_VERSION="2026.02.21"
DEFAULT_YTDLP_SHA256="13dc66e13e87c187e16bf0def71b35f118bc06145907739d5549d213a9e3b9e5"
DEFAULT_WHISPERX_VERSION="3.8.0"

if [[ -f "$LOCK_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$LOCK_FILE"
fi

: "${QUILL_YTDLP_VERSION:=$DEFAULT_YTDLP_VERSION}"
: "${QUILL_YTDLP_SHA256:=$DEFAULT_YTDLP_SHA256}"
: "${QUILL_WHISPERX_VERSION:=$DEFAULT_WHISPERX_VERSION}"

mkdir -p "$OUT_DIR"
rm -rf "$OUT_DIR"/*

WHISPERX_OUT_DIR="$OUT_DIR/whisperx"
WHISPERX_BUNDLE_PATH="$WHISPERX_OUT_DIR/whisperx.zip"
WHISPERX_BUNDLE_SHA_PATH="${WHISPERX_BUNDLE_PATH}.sha256"
mkdir -p "$WHISPERX_OUT_DIR"

sha256_file() {
  local target="$1"

  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$target" | awk '{print $1}'
    return
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$target" | awk '{print $1}'
    return
  fi

  echo "Unable to compute SHA-256 for $target (missing shasum/sha256sum)." >&2
  exit 1
}

verify_checksum() {
  local tool_name="$1"
  local target="$2"
  local expected="$3"

  if [[ -z "$expected" ]]; then
    return
  fi

  local actual
  actual="$(sha256_file "$target")"

  if [[ "$actual" != "$expected" ]]; then
    echo "Checksum mismatch for $tool_name." >&2
    echo "Expected: $expected" >&2
    echo "Actual:   $actual" >&2
    echo "Source:   $target" >&2
    exit 1
  fi
}

download_pinned_ytdlp() {
  local yt_dlp_url="${QUILL_YTDLP_DOWNLOAD_URL:-https://github.com/yt-dlp/yt-dlp/releases/download/${QUILL_YTDLP_VERSION}/yt-dlp_macos}"

  if ! command -v curl >/dev/null 2>&1; then
    echo "curl is required to download pinned yt-dlp. Set QUILL_YTDLP_SOURCE to a matching local binary path." >&2
    exit 1
  fi

  echo "Downloading pinned yt-dlp from $yt_dlp_url" >&2
  if ! curl -fsSL "$yt_dlp_url" -o "$OUT_DIR/yt-dlp"; then
    echo "Failed to download pinned yt-dlp automatically. Set QUILL_YTDLP_SOURCE to a local yt-dlp binary path." >&2
    exit 1
  fi

  chmod +x "$OUT_DIR/yt-dlp"
  verify_checksum "yt-dlp" "$OUT_DIR/yt-dlp" "${QUILL_YTDLP_SHA256:-}"
  echo "Bundled pinned yt-dlp from $yt_dlp_url" >&2
  echo "$OUT_DIR/yt-dlp"
}

resolve_tool_path() {
  local tool_name="$1"
  local env_var_name="$2"
  local source_path="${!env_var_name:-}"
  local source_from_env="false"

  if [[ -n "$source_path" ]]; then
    source_from_env="true"
  fi

  if [[ -z "$source_path" ]]; then
    source_path="$(command -v "$tool_name" || true)"
  fi

  if [[ -z "$source_path" ]]; then
    if [[ "$tool_name" == "yt-dlp" ]]; then
      download_pinned_ytdlp
      return
    fi

    echo "Missing required tool '$tool_name'. Install it or set $env_var_name to an absolute path." >&2
    exit 1
  fi

  if [[ ! -f "$source_path" ]]; then
    echo "Tool path for '$tool_name' is not a file: $source_path" >&2
    exit 1
  fi

  if [[ "$tool_name" == "yt-dlp" && "$source_from_env" != "true" && -n "${QUILL_YTDLP_SHA256:-}" ]]; then
    local actual_checksum
    actual_checksum="$(sha256_file "$source_path")"
    if [[ "$actual_checksum" != "${QUILL_YTDLP_SHA256:-}" ]]; then
      echo "Local yt-dlp does not match pinned checksum; ignoring $source_path and downloading pinned release instead." >&2
      download_pinned_ytdlp
      return
    fi
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

resolve_whisperx_archive() {
  local source_path="${QUILL_WHISPERX_ZIP_SOURCE:-}"

  if [[ -n "$source_path" ]]; then
    if [[ ! -f "$source_path" ]]; then
      echo "WhisperX source archive path is not a file: $source_path" >&2
      exit 1
    fi
    echo "$source_path"
    return
  fi

  local whisperx_url="${QUILL_WHISPERX_ZIP_URL:-https://github.com/m-bain/WhisperX/archive/refs/tags/v${QUILL_WHISPERX_VERSION}.zip}"
  if ! command -v curl >/dev/null 2>&1; then
    echo "curl is required to download WhisperX source archive. Set QUILL_WHISPERX_ZIP_SOURCE to a local zip file." >&2
    exit 1
  fi

  echo "Downloading WhisperX source archive from $whisperx_url" >&2
  if ! curl -fsSL "$whisperx_url" -o "$WHISPERX_BUNDLE_PATH"; then
    echo "Failed to download WhisperX source archive. Set QUILL_WHISPERX_ZIP_SOURCE to a local zip file." >&2
    exit 1
  fi

  echo "$WHISPERX_BUNDLE_PATH"
}

bundle_whisperx_archive() {
  local source_path="$1"

  if [[ "$source_path" != "$WHISPERX_BUNDLE_PATH" ]]; then
    cp -L "$source_path" "$WHISPERX_BUNDLE_PATH"
  fi

  chmod u+w "$WHISPERX_BUNDLE_PATH"

  local sha
  sha="$(sha256_file "$WHISPERX_BUNDLE_PATH")"
  printf '%s\n' "$sha" > "$WHISPERX_BUNDLE_SHA_PATH"

  echo "Bundled whisperx source archive from $source_path"
  echo "WhisperX archive SHA-256: $sha"
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

bundle_macos_sherpa_onnx_runtime() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    return
  fi

  if ! command -v go >/dev/null 2>&1; then
    echo "Skipping sherpa-onnx runtime bundling: go not available"
    return
  fi

  if ! command -v otool >/dev/null 2>&1 || ! command -v install_name_tool >/dev/null 2>&1; then
    echo "Skipping sherpa-onnx runtime bundling: otool/install_name_tool not available"
    return
  fi

  local mod_dir
  mod_dir="$(cd "$ROOT_DIR" && go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-macos 2>/dev/null || true)"

  if [[ -z "$mod_dir" ]]; then
    echo "Skipping sherpa-onnx runtime bundling: module not found"
    return
  fi

  local arch_dir=""
  if [[ -d "$mod_dir/lib/aarch64-apple-darwin" ]]; then
    arch_dir="$mod_dir/lib/aarch64-apple-darwin"
  elif [[ -d "$mod_dir/lib/x86_64-apple-darwin" ]]; then
    arch_dir="$mod_dir/lib/x86_64-apple-darwin"
  else
    echo "Skipping sherpa-onnx runtime bundling: no Darwin lib directory found in $mod_dir/lib/"
    return
  fi

  mkdir -p "$OUT_DIR/lib"

  local dylib
  for dylib in "$arch_dir"/*.dylib; do
    [[ -f "$dylib" ]] || continue
    local base
    base="$(basename "$dylib")"
    if [[ -f "$OUT_DIR/lib/$base" ]]; then
      continue
    fi
    cp -L "$dylib" "$OUT_DIR/lib/$base"
    chmod u+w "$OUT_DIR/lib/$base"
    echo "Bundled sherpa-onnx dependency $base from $dylib"
  done

  local lib
  for lib in "$OUT_DIR"/lib/libsherpa-onnx*.dylib "$OUT_DIR"/lib/libonnxruntime*.dylib; do
    [[ -f "$lib" ]] || continue
    patch_install_names "$lib" "library"
  done

  echo "Bundled macOS sherpa-onnx runtime libraries into $OUT_DIR/lib"
}

codesign_macos_runtime() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    return
  fi
  if ! command -v codesign >/dev/null 2>&1; then
    echo "Skipping ad-hoc signing for runtime libraries: codesign not available"
    return
  fi

  local file
  for file in "$OUT_DIR"/lib/*.dylib "$OUT_DIR/ffmpeg" "$OUT_DIR/ffprobe"; do
    [[ -f "$file" ]] || continue
    codesign --force --sign - "$file" >/dev/null
  done

  echo "Applied ad-hoc signatures to bundled runtime artifacts"
}

uv_source="$(resolve_tool_path "uv" "QUILL_UV_SOURCE")"
ffmpeg_source="$(resolve_tool_path "ffmpeg" "QUILL_FFMPEG_SOURCE")"
ffprobe_source="$(resolve_tool_path "ffprobe" "QUILL_FFPROBE_SOURCE")"
ytdlp_source="$(resolve_tool_path "yt-dlp" "QUILL_YTDLP_SOURCE")"
whisperx_source="$(resolve_whisperx_archive)"

# whisper-cpp is optional — only bundled if available on PATH or explicitly set
whisper_cpp_source="${QUILL_WHISPER_CPP_SOURCE:-$(command -v whisper-cpp || true)}"

verify_checksum "uv" "$uv_source" "${QUILL_UV_SHA256:-}"
verify_checksum "ffmpeg" "$ffmpeg_source" "${QUILL_FFMPEG_SHA256:-}"
verify_checksum "ffprobe" "$ffprobe_source" "${QUILL_FFPROBE_SHA256:-}"
verify_checksum "yt-dlp" "$ytdlp_source" "${QUILL_YTDLP_SHA256:-}"
verify_checksum "whisperx.zip" "$whisperx_source" "${QUILL_WHISPERX_ZIP_SHA256:-}"

bundle_tool "uv" "$uv_source"
bundle_tool "ffmpeg" "$ffmpeg_source"
bundle_tool "ffprobe" "$ffprobe_source"
if [[ "$ytdlp_source" != "$OUT_DIR/yt-dlp" ]]; then
  bundle_tool "yt-dlp" "$ytdlp_source"
fi
bundle_whisperx_archive "$whisperx_source"

if [[ -n "$whisper_cpp_source" && -f "$whisper_cpp_source" ]]; then
  verify_checksum "whisper-cpp" "$whisper_cpp_source" "${QUILL_WHISPER_CPP_SHA256:-}"
  bundle_tool "whisper-cpp" "$whisper_cpp_source"
else
  echo "whisper-cpp not found — skipping (set QUILL_WHISPER_CPP_SOURCE to bundle it)"
fi

bundle_macos_ffmpeg_runtime
bundle_macos_sherpa_onnx_runtime
codesign_macos_runtime

echo "Desktop tools prepared in $OUT_DIR"
