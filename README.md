## What is Quill?

Quill is an open-source audio transcription app that runs entirely on your machine. It ships as a **native desktop app** built on a local-first vault architecture — your recordings, transcripts, and contacts live as plain files in folders you own, much like Obsidian. The database is just a cache; delete it and Quill rebuilds it from your files.

[Download the latest release](https://github.com/Nicoivazquez/quill/releases)

### Features

- **Offline transcription** — WhisperX, NVIDIA Parakeet, and Canary models run locally with no cloud dependency
- **Smart speaker detection** — automatic diarization labels who said what
- **Voice signatures** — associate voice snippets with contacts using NeMo TitaNet embeddings for automatic speaker identification across recordings
- **Chat with your audio** — connect Ollama or any OpenAI-compatible provider to summarize, ask questions, or converse with your transcripts
- **Vault-based file organization** — plain Markdown + JSON in folders on your machine, Obsidian-style
- **Auto-import folder watching** — drop files into a watched folder and Quill transcribes them automatically
- **Built-in audio recorder & notes** — capture thoughts on the fly and annotate transcripts as you listen
- **AI agent ready** — ships with a [SKILL.md](skills/quill-api/SKILL.md) so AI agents (Claude Code, OpenClaw, Codex, etc.) can operate Quill via natural language — "transcribe this meeting," "find everything with Alice," "move last week's recordings to Archive"
- **PWA + native desktop app** — install as a Progressive Web App on any device, or use the native macOS app

## How to Use

### 1. Install and Launch

Download the DMG from the [releases page](https://github.com/Nicoivazquez/quill/releases), mount it, and drag Quill to Applications. On first launch, Quill will set up a Python environment and download ML models — this requires an internet connection. Subsequent launches are fast and fully offline.

### 2. Set Up Ollama (for AI chat and summaries)

1. Install [Ollama](https://ollama.com): `brew install ollama` or download from [ollama.com](https://ollama.com)
2. Pull a model: `ollama pull qwen2.5:7b`
3. In Quill, go to **Settings > LLMs > Ollama** and set the base URL to `http://localhost:11434`

**Recommended models by Mac:**

| Mac | RAM | Recommended Model |
|:---|:---|:---|
| M1/M2 (base) | 8 GB | `llama3.2:3b` or `qwen2.5:1.5b` |
| M1/M2 Pro | 16 GB | `qwen2.5:7b` or `llama3.1:8b` |
| M1/M2 Max | 32–64 GB | `qwen2.5:14b` or `llama3.3:70b` (64 GB) |
| M3/M4 (base) | 16 GB | `qwen2.5:7b` |
| M3/M4 Pro | 24–48 GB | `qwen2.5:14b` or `phi4:14b` |
| M3/M4/M5 Max | 36–128 GB | `llama3.3:70b` |

### 3. Create a Transcription Profile

Go to **Settings > Transcription** and create a profile with your preferred settings (model, language, diarization, etc.).

**Speaker diarization options:**

- **Sortformer** (recommended) — supports up to 4 speakers. More accurate at detecting the correct number of speakers in a conversation.
- **ONNX** — supports more than 4 speakers, but currently less reliable at determining the correct speaker count. Improving this is active work.

### 4. Transcribe

Upload an audio file or record directly in the app. Select your profile and start transcription. Progress is shown in real-time.

## Philosophy

Quill follows the same principle as Obsidian: everything is stored as plain Markdown and JSON in folders you control. The SQLite database is a read cache — you can delete it and Quill will rehydrate from your vault files on next launch. No cloud accounts, no subscriptions, no data leaving your machine.

```
MyVault/
├── .quill/                                    # Vault metadata
├── Inbox/Media/                               # Uploaded/recorded audio
├── Transcripts/YYYY/MM/<title>-<id>/
│   ├── transcript.md                          # Human-readable transcript
│   └── transcript.json                        # Structured data (timestamps, speakers)
└── Contacts/People/<name>--<uid>/
    ├── contact.md                             # YAML frontmatter + notes
    └── voice-snippet.wav                      # Voice sample for speaker ID
```

You can create multiple vaults and switch between them. Each vault is a self-contained folder that can be moved, backed up, or synced however you like.

## How It Works

Quill is a Go backend with an embedded React frontend. The backend manages audio processing, transcription orchestration, and the vault filesystem. The frontend is a single-page app served by the backend.

**Transcription pipeline:** Audio files are processed through a queue (2 concurrent workers). The system supports multiple ML backends — WhisperX for general transcription, NVIDIA Parakeet/Canary for fast local inference, and Voxtral for multimodal audio understanding. Models are registered at startup via an adapter pattern, so adding new backends is straightforward.

**Speaker identification:** When you save speaker mappings, Quill auto-bootstraps contacts — it creates contact records, extracts voice snippets via FFmpeg, and generates TitaNet 256-dimensional embeddings. On future transcriptions, speakers are matched against known contacts using cosine similarity with configurable confidence thresholds (auto-assign at 80%+, suggest at 60-79%, unknown below 60%).

**Real-time updates:** Transcription progress and speaker identification results are pushed via Server-Sent Events, not polling.

**File-first contacts:** Contacts are stored as vault-scoped Markdown files. The database is a cache. A filesystem watcher detects changes in real-time, so you can edit contact files directly and Quill picks up the changes.

## Getting Started (Development)

### Prerequisites

- Go 1.23+
- Node.js 20+
- Python 3.10+ (for ML models)

### Build the Desktop App

```bash
# Clone the repo
git clone https://github.com/Nicoivazquez/quill.git
cd quill

# Build the macOS DMG
make desktop-dist-mac
```

The DMG is written to `desktop/electron/release/`. Mount it, drag Quill to Applications, and launch.

<details>
<summary>Development mode</summary>

```bash
# Run Electron against a local dev backend (hot-reload)
make desktop-dev

# Or run just the backend + frontend without Electron
make dev
```

</details>

## Configuration

Quill works out of the box with sensible defaults. Customize with environment variables or a `.env` file.

| Variable | Default | Description |
|:---|:---|:---|
| `PORT` | `8080` | Server port |
| `HOST` | `0.0.0.0` | Bind address |
| `APP_ENV` | `development` | Environment (`development` or `production`) |
| `ALLOWED_ORIGINS` | `http://localhost:5173,http://localhost:8080` | CORS allowed origins (comma-separated) |
| `DATABASE_PATH` | `data/quill.db` | SQLite database path |
| `UPLOAD_DIR` | `data/uploads` | Upload storage directory |
| `TRANSCRIPTS_DIR` | `data/transcripts` | Transcript output directory |
| `WHISPERX_ENV` | `data/whisperx-env` | Python environment path for ML models |
| `SECURE_COOKIES` | `true` in production | Set `false` for HTTP-only deployments |
| `AUTH_MODE` | `local` | Authentication mode (`local` for single-user) |
| `OPENAI_API_KEY` | `""` | OpenAI API key for LLM features |
| `TRANSCRIPTION_BACKEND` | `whisperx` | Transcription backend (`whisperx`, `mlx_whisper`, `whisper_cpp`). Auto-detected as `mlx_whisper` on Apple Silicon. |
| `WHISPER_MODEL` | `small` | Whisper model to pre-download during warmup (e.g. `small`, `large-v3-turbo`) |

## License

Open source. See [LICENSE](LICENSE) for details.
