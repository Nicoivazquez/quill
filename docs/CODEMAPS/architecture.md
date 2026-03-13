<!-- Generated: 2026-03-13 | Files scanned: 255 | Token estimate: ~900 -->

# Architecture

## System Diagram

```
                      +-----------+
                      | Electron  |  (macOS DMG)
                      | desktop/  |
                      +-----+-----+
                            |
                            v
+-------------+     +------+-------+     +----------------+
| React SPA   |<--->| Go Backend   |<--->| SQLite (GORM)  |
| web/frontend|     | cmd/server   |     | data/quill.db  |
| :5173 (dev) |     | :8080        |     +----------------+
+-------------+     +------+-------+
       ^                   |
       | SSE               +------+--------+--------+
       |                   |      |        |        |
                      +----v-+ +--v---+ +--v---+ +--v-------+
                      |Queue | | SSE  | | LLM  | | Contacts |
                      |2 wrk | | Bcast| |OpenAI| | TitaNet  |
                      +--+---+ +------+ |Ollama| | Matcher  |
                         |              +------+ +----------+
                    +----v----+
                    | Adapters|
                    +---------+
                    | WhisperX (local)
                    | Parakeet (local, NVIDIA)
                    | Canary   (local, NVIDIA)
                    | Voxtral  (local, Mistral)
                    | OpenAI   (cloud API)
                    | AssemblyAI (cloud API)
                    | Deepgram (cloud API)
                    +---------+
```

## Data Flow

```
Upload → FileService → JobRepo(status:uploaded)
       → TaskQueue(status:pending)
       → UnifiedJobProcessor(status:processing)
         → AdapterRegistry → Model Adapter → Transcribe
         → Diarization Adapter → Speaker segments
         → Postprocessors → JSON/Markdown
       → JobRepo(status:completed) + SSE broadcast
       → Auto-title (LLM) + Auto-label (TitaNet cosine match)
```

## Service Boundaries

| Boundary | Owns | Communicates Via |
|----------|------|-----------------|
| Frontend (React) | UI state, query cache | REST + SSE |
| Backend (Go) | Business logic, auth, files | Gin HTTP, GORM |
| Queue (internal) | Job scheduling, 2 workers | Channel-based |
| SSE (internal) | Real-time push | EventSource stream |
| Transcription | ML model execution | Subprocess (Python) |
| Contacts | Voice signatures, file sync | fsnotify + TitaNet |
| LLM | Chat, summarization | OpenAI/Ollama API |
| Desktop (Electron) | OS integration, packaging | Embedded Go binary |

## Vault Architecture (Local-First)

```
Vault Root/
├── Contacts/People/<slug>--<uid>/contact.md   (file-first, DB is cache)
├── data/quill.db                               (SQLite, regenerable)
├── data/uploads/                               (audio files)
├── data/transcripts/                           (output JSON/Markdown)
└── data/temp/                                  (working directory)
```
