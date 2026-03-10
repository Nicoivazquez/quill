<div align="center">
  <img src="logo.svg" height="90" style="vertical-align: middle;" />
  <img src="logo-text.svg" height="80" style="vertical-align: middle;" />
</div>
</br>
</br>
<p align="center">
Desktop-first, local-first, privacy-focused audio transcription. Your recordings never leave your machine.
</p>

<p align="center">
  <a href="https://quill.app">Website</a> •
  <a href="https://quill.app/docs/">Docs</a> •
  <a href="https://quill.app/api">API Reference</a>
</p>

<p align="center">
<a href='https://ko-fi.com/H2H41KQZA3' target='_blank'><img height='36' style='border:0px;height:36px;' src='https://storage.ko-fi.com/cdn/kofi6.png?v=6' border='0' alt='Buy Me a Coffee at ko-fi.com' /></a>
</p>

<div align="center">
  <img src="screenshots/hero.png" alt="Quill Desktop App" width="800" />
</div>

## Introduction

Quill is an open-source audio transcription app that runs entirely on your machine. It ships as a **native desktop app** built on a local-first vault architecture — your recordings, transcripts, and contacts live as plain files in folders you own, much like Obsidian. The database is just a cache; delete it and Quill rebuilds it from your files.

A Docker/server deployment is available for power users who want to self-host on dedicated hardware.

- **Offline transcription** — WhisperX, NVIDIA Parakeet, and Canary models run locally with no cloud dependency
- **Smart speaker detection** — automatic diarization labels who said what
- **Chat with your audio** — connect Ollama or any OpenAI-compatible provider to summarize, ask questions, or converse with your transcripts
- **Vault-based file organization** — Obsidian-like plain Markdown + JSON in folders on your machine
- **Contacts with voice signatures** — associate voice snippets with contacts using NeMo TitaNet embeddings for automatic speaker identification
- **Auto-import folder watching** — drop files into a watched folder and Quill transcribes them automatically
- **Built-in audio recorder & notes** — capture thoughts on the fly and annotate transcripts as you listen
- **PWA + native desktop app** — install as a Progressive Web App on any device, or use the native macOS app

[View full list of features →](https://quill.app/docs/features)

## Philosophy: Your Data, Your Files

Quill follows the same principle as Obsidian: everything is stored as plain Markdown and JSON in folders you control. The SQLite database is a read cache — you can delete it and Quill will rehydrate from your vault files on next launch.

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

## Contacts & Voice Signatures

Quill stores contacts as folder-per-person structures inside your vault. Each contact has a Markdown file with YAML frontmatter (name, email, organization, tags) and an optional voice snippet.

**How voice signatures work:**

1. Upload a short audio clip of someone speaking to their contact profile
2. Quill extracts a voice embedding using NVIDIA NeMo TitaNet
3. When transcribing new audio, Quill compares speaker segments against known voice embeddings
4. Matched speakers are automatically labeled with contact names in the transcript

Contacts sync bidirectionally — edit the Markdown files directly or use the app UI. Changes are picked up automatically via filesystem watching.

## Installation

### Desktop App (Recommended)

Download the latest macOS DMG from [GitHub Releases](https://github.com/rishikanthc/quill/releases).

On first launch, Quill will:
1. Initialize a Python environment for ML models
2. Download transcription models (WhisperX, PyAnnote, NVIDIA NeMo)
3. Set up the database

This initial setup requires an internet connection. Subsequent launches are fast and fully offline.

The desktop app bundles **ffmpeg**, **uv** (Python package manager), and **yt-dlp**.

### Homebrew (macOS & Linux)

```bash
# Add the Quill tap
brew tap rishikanthc/quill

# Install Quill (automatically installs uv and ffmpeg)
brew install quill

# Start the server
quill
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

### Docker (Advanced)

For a containerized setup on dedicated hardware. We provide images for CPU, CUDA GPUs, and Blackwell-generation GPUs.

> [!IMPORTANT]
> **Permissions:** Set `PUID` and `PGID` to your host user's UID/GID (typically `1000` on Linux) to avoid SQLite permission errors. Run `id` on your host to check.
>
> **HTTP vs HTTPS:** Quill enables Secure Cookies in production by default. If accessing via plain HTTP, set `SECURE_COOKIES=false` or you will get "Unable to load audio stream" errors.

#### CPU

```yaml
services:
  quill:
    image: ghcr.io/rishikanthc/quill:v1.2.0
    ports:
      - "8080:8080"
    volumes:
      - quill_data:/app/data
      - env_data:/app/whisperx-env
    environment:
      - PUID=${PUID:-1000}
      - PGID=${PGID:-1000}
      - APP_ENV=production
      # - ALLOWED_ORIGINS=https://your-domain.com
      # - SECURE_COOKIES=false  # Uncomment for HTTP-only access
    restart: unless-stopped

volumes:
  quill_data: {}
  env_data: {}
```

```bash
docker compose up -d
```

#### NVIDIA GPU (CUDA)

Requires the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html).

```yaml
services:
  quill:
    image: ghcr.io/rishikanthc/quill-cuda:v1.2.0
    ports:
      - "8080:8080"
    volumes:
      - quill_data:/app/data
      - env_data:/app/whisperx-env
    restart: unless-stopped
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities:
                - gpu
    environment:
      - NVIDIA_VISIBLE_DEVICES=all
      - NVIDIA_DRIVER_CAPABILITIES=compute,utility
      - PUID=${PUID:-1000}
      - PGID=${PGID:-1000}
      - APP_ENV=production
      # - ALLOWED_ORIGINS=https://your-domain.com
      # - SECURE_COOKIES=false  # Uncomment for HTTP-only access

volumes:
  quill_data: {}
  env_data: {}
```

```bash
docker compose -f docker-compose.cuda.yml up -d
```

#### Blackwell (RTX 50-series)

RTX 5080/5090 users must use the Blackwell-specific image due to CUDA/PyTorch compatibility:

```bash
docker compose -f docker-compose.blackwell.yml up -d
```

#### GPU Compatibility

| GPU Generation | Compute Capability | Docker Image | Docker Compose File |
|:---|:---|:---|:---|
| GTX 10-series (Pascal) | sm_61 | `quill-cuda` | `docker-compose.cuda.yml` |
| RTX 20-series (Turing) | sm_75 | `quill-cuda` | `docker-compose.cuda.yml` |
| RTX 30-series (Ampere) | sm_86 | `quill-cuda` | `docker-compose.cuda.yml` |
| RTX 40-series (Ada Lovelace) | sm_89 | `quill-cuda` | `docker-compose.cuda.yml` |
| **RTX 50-series (Blackwell)** | sm_120 | `quill-cuda-blackwell` | `docker-compose.blackwell.yml` |

### First Run

When you run Quill for the first time, it needs to initialize Python environments and download ML models. You will know it's ready when you see:

```
msg="Quill is ready" url=http://0.0.0.0:8080
```

Subsequent launches are fast because models and environments are persisted.

## Configuration

Quill works out of the box. For Homebrew or manual installations, you can customize behavior with environment variables or a `.env` file in the working directory.

Docker users can set these in the `environment` section of their compose file.

| Variable | Default | Description |
|:---|:---|:---|
| `PORT` | `8080` | Server port |
| `HOST` | `0.0.0.0` | Bind address |
| `APP_ENV` | `development` | Environment (`development` or `production`) |
| `ALLOWED_ORIGINS` | `http://localhost:5173,http://localhost:8080` | CORS allowed origins (comma-separated) |
| `DATABASE_PATH` | `data/quill.db` | SQLite database path |
| `JWT_SECRET` | Auto-generated | JWT signing secret (auto-generated and persisted if not set) |
| `JWT_SECRET_FILE` | `data/jwt_secret` | Path to persist the auto-generated JWT secret |
| `UPLOAD_DIR` | `data/uploads` | Upload storage directory |
| `TRANSCRIPTS_DIR` | `data/transcripts` | Transcript output directory |
| `TEMP_DIR` | `data/temp` | Temporary processing files |
| `WHISPERX_ENV` | `data/whisperx-env` | Python environment path for ML models |
| `QUILL_WHISPERX_ZIP_URL` | GitHub release URL | WhisperX source archive URL |
| `QUILL_WHISPERX_ZIP_SHA256` | `""` | Optional SHA-256 checksum for the archive |
| `SECURE_COOKIES` | `true` in production | Set `false` for HTTP-only deployments |
| `AUTH_MODE` | `local` | Authentication mode (`local` for single-user) |
| `OPENAI_API_KEY` | `""` | OpenAI API key for LLM features |
| `HF_TOKEN` | `""` | HuggingFace token for model downloads |
| `QUILL_DEFER_MODEL_INIT` | `false` | Defer ML model download to first use (desktop builds) |

For packaged desktop builds, `QUILL_WHISPERX_ZIP_URL` is automatically overridden to a bundled local archive when available.

## Setting Up Ollama (Local LLMs)

Quill can use local LLMs through [Ollama](https://ollama.com) for chat, summaries, and Q&A over your transcripts.

**Setup:**

1. Install Ollama: `brew install ollama` or download from [ollama.com](https://ollama.com)
2. Pull a model: `ollama pull qwen2.5:7b`
3. In Quill, go to **Settings > LLMs > Ollama** and set the base URL to `http://localhost:11434`

### Recommended Models by Mac

Pick a model that fits comfortably in your available RAM. Use `Q4_K_M` quantization for the best speed/quality balance.

| Mac | RAM | Recommended Model | Notes |
|:---|:---|:---|:---|
| M1 / M2 (base) | 8 GB | `llama3.2:3b` or `qwen2.5:1.5b` | Lightweight only; ~18–22 tok/s |
| M1/M2 Pro | 16 GB | `qwen2.5:7b` or `llama3.1:8b` | Best balance of quality and speed |
| M1/M2 Max | 32–64 GB | `qwen2.5:14b` or `llama3.3:70b` (64 GB) | Larger models viable |
| M3 (base) | 8–16 GB | `qwen2.5:7b` (16 GB) or `llama3.2:3b` (8 GB) | ~25 tok/s at 7B |
| M3 Pro | 18–36 GB | `qwen2.5:14b` or `mistral-nemo:12b` | Sweet spot for 14B |
| M3 Max | 36–128 GB | `llama3.3:70b` or `qwen2.5:14b` | 70B comfortable at 64 GB+ |
| M4 (base) | 16–32 GB | `qwen2.5:7b` or `gemma2:9b` | ~30 tok/s at 7B |
| M4 Pro | 24–48 GB | `qwen2.5:14b` or `phi4:14b` | Great 14B performance |
| M4 Max | 36–128 GB | `llama3.3:70b` | 70B runs well |
| M5 (base) | 16–32 GB | `qwen2.5:7b` or `gemma2:9b` | ~30% faster than M4 |
| M5 Pro | 24–64 GB | `qwen2.5:14b` or `phi4:14b` | Up to 307 GB/s bandwidth |
| M5 Max | 36–128 GB | `llama3.3:70b` | Up to 614 GB/s bandwidth |

## HuggingFace Token (Speaker Diarization)

Speaker diarization (detecting who said what) uses PyAnnote models hosted on HuggingFace. These require a free access token.

**How to get a token:**

1. Sign up at [huggingface.co](https://huggingface.co)
2. Create a read token at [huggingface.co/settings/tokens](https://huggingface.co/settings/tokens)
3. Accept the model license agreements:
   - [pyannote/speaker-diarization-3.1](https://huggingface.co/pyannote/speaker-diarization-3.1)
   - [pyannote/segmentation-3.0](https://huggingface.co/pyannote/segmentation-3.0)

**How to provide the token:**

- **Desktop app:** Enter it per-job in **Settings > Transcription**
- **Docker:** Set `HF_TOKEN` in your docker-compose environment

**Alternative:** Use NVIDIA NeMo Sortformer diarization, which does not require a HuggingFace token (though PyAnnote generally provides higher accuracy).

## App Settings Overview

- **Transcription** — Transcription profiles, auto-transcribe on upload, auto-summary generation, auto-title extraction
- **Account** — User management
- **API Keys** — Create and revoke programmatic access keys for the REST API
- **LLMs** — Configure Ollama or OpenAI-compatible provider (base URL, API key, model selection)
- **Summary Templates** — Custom summary prompts, per-template model selection, speaker info toggle
- **Auto Import** *(Desktop)* — Watch folders for automatic file ingestion and transcription
- **Vaults** *(Desktop)* — Create, connect, and switch vaults; rehydrate database from vault files

## Screenshots

<details>
  <summary>Click to expand</summary>

  <p align="center">
    <img alt="Transcript view" src="screenshots/transcript-light.png" width="720" />
  </p>
  <p align="center"><em>Transcript reader with playback follow-along and seek-from-text.</em></p>

  <p align="center">
    <img alt="Chat with Audio" src="screenshots/chat.png" width="720" />
  </p>
  <p align="center"><em>Chat with your transcripts using local LLMs or OpenAI.</em></p>

  <p align="center">
    <img alt="Notes and Highlights" src="screenshots/notes.png" width="720" />
  </p>
  <p align="center"><em>Highlight key moments and take notes while listening.</em></p>

  <p align="center">
    <img alt="AI Summaries" src="screenshots/ai-summary.png" width="720" />
  </p>
  <p align="center"><em>Generate comprehensive summaries of your recordings.</em></p>

  <p align="center">
    <strong style="font-size: 1.2em;">Dark Mode</strong>
  </p>

  <p align="center">
    <img alt="Homepage Dark Mode" src="screenshots/homepage-dark.png" width="720" />
  </p>
  <p align="center"><em>Homepage in Dark Mode.</em></p>

  <p align="center">
    <img alt="Transcript Dark Mode" src="screenshots/transcript-dark.png" width="720" />
  </p>
  <p align="center"><em>Transcript view in Dark Mode.</em></p>

  ### Mobile

  <p align="center">
    <img alt="Mobile Homepage" src="screenshots/homepage-mobile.PNG" width="300" />
    <img alt="Mobile Homepage Dark" src="screenshots/homepage-mobile-dark.PNG" width="300" />
  </p>
  <p align="center"><em>PWA mobile app (Light & Dark).</em></p>

  <p align="center">
    <img alt="Mobile Transcript" src="screenshots/transcript-mobile.PNG" width="300" />
    <img alt="Mobile Transcript Dark" src="screenshots/transcript-mobile-dark.PNG" width="300" />
  </p>
  <p align="center"><em>Mobile transcript reading experience.</em></p>

</details>

## Why I Built This

The inspiration for Quill was born out of privacy paranoia and not wanting to pay for a subscription.
About a year ago, I purchased a [Plaud Note](https://www.plaud.ai/) for recording voice memos. I loved the device itself; the form factor, microphone quality, and workflow were excellent.

However, transcription was done on their cloud servers. As someone who is paranoid about privacy I wasn't comfortable with uploading my recordings to a third party provider.
Moreover I was hit with subscription costs: $100 a year for 20 hours of transcription per month, or $240 a year for unlimited access. As an avid self-hoster with a background in ML and AI, it felt wrong to pay such a premium for a service I knew I could engineer myself.

I decided to build Quill to bridge that gap, creating a powerful, private, and free alternative for everyone.

## Sponsors

![recall.ai-logo](https://cdn.prod.website-files.com/620d732b1f1f7b244ac89f0e/66b294e51ee15f18dd2b171e_recall-logo.svg) Meeting Transcription API
If you're looking for a transcription API for meetings, consider checking out [Recall.ai](https://www.recall.ai/?utm_source=github&utm_medium=sponsorship&utm_campaign=rishikanthc-quill), an API that works with Zoom, Google Meet, Microsoft Teams, and more.
Recall.ai diarizes by pulling the speaker data and separate audio streams from the meeting platforms, which means 100% accurate speaker diarization with actual speaker names.

## Donating

<a href='https://ko-fi.com/H2H41KQZA3' target='_blank'><img height='36' style='border:0px;height:36px;' src='https://storage.ko-fi.com/cdn/kofi6.png?v=6' border='0' alt='Buy Me a Coffee at ko-fi.com' /></a>
