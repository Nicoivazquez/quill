#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$ROOT_DIR/dist/desktop-runtime-seed"
ENABLE_RUNTIME_SEED="${QUILL_RUNTIME_SEED_ENABLED:-true}"
RUNTIME_SEED_SOURCE="${QUILL_RUNTIME_SEED_SOURCE:-}"
WHISPER_MODELS="${QUILL_RUNTIME_SEED_WHISPER_MODELS:-small}"
INCLUDE_CANARY="${QUILL_RUNTIME_SEED_INCLUDE_CANARY:-false}"
INCLUDE_WHISPERX="${QUILL_RUNTIME_SEED_INCLUDE_WHISPERX:-true}"
INCLUDE_PARAKEET="${QUILL_RUNTIME_SEED_INCLUDE_PARAKEET:-true}"
INCLUDE_SORTFORMER="${QUILL_RUNTIME_SEED_INCLUDE_SORTFORMER:-true}"
INCLUDE_TITANET="${QUILL_RUNTIME_SEED_INCLUDE_TITANET:-true}"
SAMPLE_AUDIO="${QUILL_RUNTIME_SEED_SAMPLE_AUDIO:-$ROOT_DIR/tests/data/AMI-Corpus-IB4002.Mix-Headset-clip.wav}"

mkdir -p "$OUT_DIR"
rm -rf "$OUT_DIR"/*

case "${ENABLE_RUNTIME_SEED,,}" in
  true|1|yes) ;;
  *)
    echo "Desktop runtime seed disabled. Leaving $OUT_DIR empty."
    exit 0
    ;;
esac

if ! command -v go >/dev/null 2>&1; then
  echo "go is required to prepare the desktop runtime seed." >&2
  exit 1
fi

if [[ -n "$RUNTIME_SEED_SOURCE" ]]; then
  if [[ ! -d "$RUNTIME_SEED_SOURCE" ]]; then
    echo "QUILL_RUNTIME_SEED_SOURCE is not a directory: $RUNTIME_SEED_SOURCE" >&2
    exit 1
  fi

  # Resolve to real path to reject symlink tricks
  RESOLVED_SOURCE="$(realpath "$RUNTIME_SEED_SOURCE")"

  # Reject paths matching sensitive system directories
  case "$RESOLVED_SOURCE" in
    /etc/*|/etc|/var/*|/var|/usr/*|/usr|/System/*|/System)
      echo "QUILL_RUNTIME_SEED_SOURCE resolves to a sensitive system path: $RESOLVED_SOURCE" >&2
      exit 1
      ;;
  esac

  if command -v rsync >/dev/null 2>&1; then
    rsync -a "$RESOLVED_SOURCE"/ "$OUT_DIR"/
  else
    cp -R "$RESOLVED_SOURCE"/. "$OUT_DIR"/
  fi

  # Validate WHISPER_MODELS before use in JSON
  if [[ ! "$WHISPER_MODELS" =~ ^[a-zA-Z0-9._,-]+$ ]]; then
    echo "QUILL_RUNTIME_SEED_WHISPER_MODELS contains invalid characters: $WHISPER_MODELS" >&2
    exit 1
  fi
  WHISPER_MODELS_JSON="$(printf '%s' "$WHISPER_MODELS" | awk -F',' '{for(i=1;i<=NF;i++) printf "%s\"%s\"", (i>1?",":""), $i; print ""}')"

  if [[ ! -f "$OUT_DIR/.quill-runtime-seed.json" ]]; then
    cat > "$OUT_DIR/.quill-runtime-seed.json" <<EOF
{
  "version": 1,
  "seed_id": "manual-source;whisper-models=${WHISPER_MODELS};whisperx=${INCLUDE_WHISPERX};parakeet=${INCLUDE_PARAKEET};sortformer=${INCLUDE_SORTFORMER};canary=${INCLUDE_CANARY};titanet=${INCLUDE_TITANET}",
  "prepared_at": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "whisper_models": [${WHISPER_MODELS_JSON}],
  "includes_whisperx": ${INCLUDE_WHISPERX},
  "includes_parakeet": ${INCLUDE_PARAKEET},
  "includes_sortformer": ${INCLUDE_SORTFORMER},
  "includes_canary": ${INCLUDE_CANARY},
  "includes_titanet": ${INCLUDE_TITANET}
}
EOF
  fi

  # Compute and log SHA256 manifest hash of the output
  if command -v sha256sum >/dev/null 2>&1; then
    MANIFEST_HASH="$(find "$OUT_DIR" -type f | sort | xargs sha256sum | sha256sum | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    MANIFEST_HASH="$(find "$OUT_DIR" -type f | sort | xargs shasum -a 256 | shasum -a 256 | awk '{print $1}')"
  else
    MANIFEST_HASH="unavailable"
  fi
  echo "Runtime seed output SHA256 manifest hash: $MANIFEST_HASH"

  echo "Bundled desktop runtime seed from $RUNTIME_SEED_SOURCE"
  exit 0
fi

UV_BIN_PATH="${QUILL_UV_BIN:-$ROOT_DIR/dist/desktop-tools/uv}"
if [[ ! -x "$UV_BIN_PATH" ]]; then
  if command -v uv >/dev/null 2>&1; then
    UV_BIN_PATH="$(command -v uv)"
  else
    echo "uv is required to prepare the desktop runtime seed. Run desktop tool preparation first or install uv." >&2
    exit 1
  fi
fi

TOOL_DIR="$ROOT_DIR/dist/desktop-tools"
if [[ -d "$TOOL_DIR" ]]; then
  export PATH="$TOOL_DIR:$PATH"
fi

export QUILL_UV_BIN="$UV_BIN_PATH"

if [[ -f "$ROOT_DIR/dist/desktop-tools/whisperx/whisperx.zip" ]]; then
  export QUILL_WHISPERX_ZIP_URL="file://$ROOT_DIR/dist/desktop-tools/whisperx/whisperx.zip"
fi
if [[ -f "$ROOT_DIR/dist/desktop-tools/whisperx/whisperx.zip.sha256" ]]; then
  export QUILL_WHISPERX_ZIP_SHA256="$(tr -d '\n' < "$ROOT_DIR/dist/desktop-tools/whisperx/whisperx.zip.sha256")"
fi

cd "$ROOT_DIR"
go run ./cmd/desktop-runtime-seed \
  --output "$OUT_DIR" \
  --whisper-models "$WHISPER_MODELS" \
  --sample-audio "$SAMPLE_AUDIO" \
  --include-whisperx="$INCLUDE_WHISPERX" \
  --include-parakeet="$INCLUDE_PARAKEET" \
  --include-sortformer="$INCLUDE_SORTFORMER" \
  --include-canary="$INCLUDE_CANARY" \
  --include-titanet="$INCLUDE_TITANET"

echo "Desktop runtime seed prepared in $OUT_DIR"
