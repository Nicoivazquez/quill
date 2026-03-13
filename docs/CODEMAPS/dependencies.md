<!-- Generated: 2026-03-13 | Files scanned: 255 | Token estimate: ~650 -->

# Dependencies

## External Cloud Services

| Service | Purpose | SDK | Config Source |
|---------|---------|-----|--------------|
| OpenAI | Whisper transcription + LLM chat/summary | HTTP (custom) | CloudProviderConfig or env `OPENAI_API_KEY` |
| AssemblyAI | Cloud transcription + diarization | assemblyai-go-sdk v1.10 | CloudProviderConfig |
| Deepgram | Cloud transcription + diarization | deepgram-go-sdk v3.5 | CloudProviderConfig |
| HuggingFace | Model downloads | HTTP | env `HF_TOKEN` |

## Local ML Models (Python subprocesses)

| Model | Runtime | Script Location |
|-------|---------|----------------|
| WhisperX | Python (data/whisperx-env) | internal/transcription/adapters/ |
| NVIDIA Parakeet | Python (NeMo) | adapters/py/nvidia/parakeet_transcribe.py |
| NVIDIA Canary | Python (NeMo) | adapters/py/nvidia/canary_transcribe.py |
| NVIDIA Sortformer | Python (NeMo) | adapters/py/nvidia/sortformer_diarize.py |
| Voxtral | Python (Mistral) | adapters/py/voxtral/voxtral_transcribe.py |
| PyAnnote | Python | adapters/py/pyannote/pyannote_diarize.py |
| TitaNet-Large | Python (NeMo) | contacts/py/extract_titanet_embedding.py |

## External Tools (Desktop-bundled)

| Tool | Purpose |
|------|---------|
| FFmpeg | Audio extraction, format conversion, speaker snippet cutting |
| yt-dlp | YouTube audio download |
| uv | Python package manager (virtual env) |

## Go Dependencies (Key)

| Package | Purpose |
|---------|---------|
| gin v1.10 | HTTP framework |
| gorm v1.30 + sqlite | ORM + database |
| golang-jwt/jwt v5 | JWT auth |
| fsnotify v1.9 | File system watching (contacts) |
| cobra v1.10 | CLI framework |
| viper v1.21 | Configuration |
| golang.org/x/crypto | bcrypt password hashing |
| golang.org/x/sync | errgroup, semaphore |
| google/uuid | UUID generation |
| stretchr/testify | Test assertions |
| swaggo/swag | Swagger doc generation |

## Frontend Dependencies (Key)

| Package | Purpose |
|---------|---------|
| react v19 | UI framework |
| react-router-dom v7 | Client routing |
| @tanstack/react-query v5 | Server state + cache |
| zustand v5 | Client state (auth store) |
| wavesurfer.js v7 | Audio waveform player |
| @radix-ui/* | Accessible UI primitives (15 packages) |
| tailwindcss v4 | Utility-first CSS |
| framer-motion v12 | Animations |
| react-markdown | Markdown rendering |
| cmdk | Command palette |
| @playwright/test v1.58 | E2E testing |

## Integration Points

```
OpenAI key sync: CloudProviderConfig ←→ LLMConfig (bidirectional on upsert/delete)
SSE stream: Backend Broadcaster → Frontend useTranscriptionEvents (EventSource)
Vite proxy: /api → localhost:8080 (dev only)
Electron: Embeds Go binary + Python env + FFmpeg + yt-dlp
```

## CI/CD

| File | Trigger | Action |
|------|---------|--------|
| .github/workflows/release.yml | tag v*.*.* | GoReleaser cross-platform build |
| .github/workflows/project-website.yml | push to main | Build + deploy GitHub Pages |
| docker-compose.yml | manual | ghcr.io/rishikanthc/quill:latest on port 8080 |
