# Quill Electron Shell

This directory contains the macOS desktop shell for Quill.

## Local development

From repository root:

```bash
# 1) Build backend binary that Electron launches in dev
go build -o quill cmd/server/main.go

# 2) Install desktop shell dependencies
cd desktop/electron
npm install

# 3) Run Electron
npm run dev
```

During startup, Electron shows a built-in initialization screen while the backend starts. In packaged builds, Quill now keeps the DMG small and downloads the local model/runtime assets after launch, with in-app progress surfaced in the header for transcription, diarization, and voice-signature runtime setup.

By default dev mode looks for backend binary at:

```text
/Users/nico/Developer/quill/quill
```

Override backend path if needed:

```bash
QUILL_BACKEND_BIN=/absolute/path/to/quill npm run dev
```

## macOS package (DMG)

From `desktop/electron`:

```bash
npm run dist:mac
```

This runs:
- TypeScript compile for Electron main process.
- Frontend build and embed copy into Go backend.
- Go backend build at `dist/desktop-backend/quill`.
- Tool bundling at `dist/desktop-tools` (`uv`, `ffmpeg`, `ffprobe`, `yt-dlp`, `whisperx/whisperx.zip`).
- `electron-builder` DMG packaging.

## Tool bundling

`scripts/prepare-desktop-tools.sh` resolves tools from local sources and verifies them against pinned checksums in:

`scripts/desktop-tools.lock.env`

If `yt-dlp` is not found, or the copy found on `PATH` does not match the pinned checksum, the script downloads the pinned release (`QUILL_YTDLP_VERSION`) and verifies its SHA-256 before bundling.
If `QUILL_WHISPERX_ZIP_SOURCE` is not set, the script downloads WhisperX source from `QUILL_WHISPERX_ZIP_URL` (or the versioned default URL) and bundles it as `tools/whisperx/whisperx.zip`.

You can override source paths when building:

```bash
QUILL_UV_SOURCE=/absolute/path/to/uv \
QUILL_FFMPEG_SOURCE=/absolute/path/to/ffmpeg \
QUILL_FFPROBE_SOURCE=/absolute/path/to/ffprobe \
QUILL_YTDLP_SOURCE=/absolute/path/to/yt-dlp \
QUILL_WHISPERX_ZIP_SOURCE=/absolute/path/to/whisperx.zip \
npm run dist:mac
```

You can also override pins/checksums at build time:

```bash
QUILL_UV_SHA256=<sha256> \
QUILL_FFMPEG_SHA256=<sha256> \
QUILL_FFPROBE_SHA256=<sha256> \
QUILL_YTDLP_VERSION=<release-tag> \
QUILL_YTDLP_SHA256=<sha256> \
QUILL_WHISPERX_VERSION=<release-tag> \
QUILL_WHISPERX_ZIP_SHA256=<sha256> \
npm run dist:mac
```

## Runtime seed tooling

The runtime seed helper remains available in the repo for future offline-first packaging work, but it is not part of the default `npm run dist:mac` path anymore. The default DMG is intentionally smaller and relies on post-launch runtime/model installation with visible in-app status.
