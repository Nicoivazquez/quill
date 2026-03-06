# Scriberr Electron Shell

This directory contains the macOS desktop shell for Scriberr.

## Local development

From repository root:

```bash
# 1) Build backend binary that Electron launches in dev
go build -o scriberr cmd/server/main.go

# 2) Install desktop shell dependencies
cd desktop/electron
npm install

# 3) Run Electron
npm run dev
```

During startup, Electron now shows a built-in initialization screen while the backend prepares model environments and downloads first-run assets.

By default dev mode looks for backend binary at:

```text
/Users/nico/Developer/quill/scriberr
```

Override backend path if needed:

```bash
SCRIBERR_BACKEND_BIN=/absolute/path/to/scriberr npm run dev
```

## macOS package (DMG)

From `desktop/electron`:

```bash
npm run dist:mac
```

This runs:
- TypeScript compile for Electron main process.
- Frontend build and embed copy into Go backend.
- Go backend build at `dist/desktop-backend/scriberr`.
- Tool bundling at `dist/desktop-tools` (`uv`, `ffmpeg`, `ffprobe`, `yt-dlp`).
- `electron-builder` DMG packaging.

## Tool bundling

`scripts/prepare-desktop-tools.sh` resolves tools from local sources and verifies them against pinned checksums in:

`scripts/desktop-tools.lock.env`

If `yt-dlp` is not found, the script downloads a pinned release (`SCRIBERR_YTDLP_VERSION`) and verifies its SHA-256 before bundling.

You can override source paths when building:

```bash
SCRIBERR_UV_SOURCE=/absolute/path/to/uv \
SCRIBERR_FFMPEG_SOURCE=/absolute/path/to/ffmpeg \
SCRIBERR_FFPROBE_SOURCE=/absolute/path/to/ffprobe \
SCRIBERR_YTDLP_SOURCE=/absolute/path/to/yt-dlp \
npm run dist:mac
```

You can also override pins/checksums at build time:

```bash
SCRIBERR_UV_SHA256=<sha256> \
SCRIBERR_FFMPEG_SHA256=<sha256> \
SCRIBERR_FFPROBE_SHA256=<sha256> \
SCRIBERR_YTDLP_VERSION=<release-tag> \
SCRIBERR_YTDLP_SHA256=<sha256> \
npm run dist:mac
```
