<!-- Generated: 2026-03-13 | Files scanned: 255 | Token estimate: ~950 -->

# Backend

## Entry Point
cmd/server/main.go → config.Load → database.Initialize → repos(13) → services → api.NewHandler → router.Setup

## Middleware Chain
gin.Recovery → logger.GinLogger → CompressionMiddleware → CORS → [per-route: AuthMiddleware | JWTOnlyMiddleware]

## Routes → Handler → Repo/Service

```
# Auth (public)
POST /auth/register       → RegisterHandler         → UserRepo, AuthService
POST /auth/login          → LoginHandler             → UserRepo, AuthService, RefreshTokenRepo
POST /auth/refresh        → RefreshTokenHandler      → RefreshTokenRepo, AuthService
GET  /auth/logout         → LogoutHandler            → RefreshTokenRepo

# Transcription
POST /transcription/upload         → UploadHandler          → FileService, JobRepo
POST /transcription/upload-multitrack → MultiTrackUpload    → MultiTrackProcessor, JobRepo
POST /transcription/submit/:id     → SubmitJob              → JobRepo, TaskQueue
GET  /transcription/list           → ListJobs               → JobRepo (paginated, search, vault-scoped)
GET  /transcription/:id            → GetJob                 → JobRepo
GET  /transcription/:id/transcript → GetTranscript          → JobRepo (reads JSON file)
PUT  /transcription/:id/title      → UpdateTitle            → JobRepo
DELETE /transcription/:id          → DeleteJob              → JobRepo, FileService
GET  /transcription/:id/speakers   → GetSpeakerMappings     → SpeakerMappingRepo
POST /transcription/:id/speakers   → SaveSpeakerMappings    → SpeakerMappingRepo, ContactManager
POST /transcription/youtube        → YouTubeDownload        → FileService (yt-dlp subprocess)
POST /transcription/quick          → QuickTranscription     → QuickTranscriptionService

# Profiles
GET/POST/PUT/DELETE /profiles      → ProfileHandlers        → ProfileRepo

# Contacts
GET/POST /contacts                 → ContactHandlers        → ContactRepo, ContactManager
GET/PUT/DELETE /contacts/:id       → ContactHandlers        → ContactRepo, ContactManager
POST /contacts/:id/snippet         → UploadSnippet          → ContactManager (FFmpeg + TitaNet)
POST /contacts/:id/signature       → UploadSignature        → ContactManager
POST /contacts/:id/signature/extract → ExtractSignature     → ContactManager (EmbeddingWorker)

# Cloud Providers
GET  /cloud-providers              → ListProviders          → CloudProviderRepo
PUT  /cloud-providers/:provider    → UpsertProviderKey      → CloudProviderRepo, LLMConfigRepo (OpenAI sync)
DELETE /cloud-providers/:provider  → DeleteProviderKey      → CloudProviderRepo

# LLM & Summaries
GET/POST /llm/config               → LLMConfigHandlers      → LLMConfigRepo
GET/POST/PUT/DELETE /summaries     → SummaryHandlers        → SummaryRepo
POST /summarize                    → SummarizeHandler       → LLM service (streaming)

# Chat
GET/POST /chat/sessions            → ChatHandlers           → ChatRepo
POST /chat/sessions/:id/messages   → SendMessage            → ChatRepo, LLM service

# SSE
GET /events?job_id=X               → SSEHandler             → Broadcaster (EventSource stream)

# Desktop-specific
GET/POST /runtime/warmup           → WarmupHandlers         → RuntimeWarmupManager
GET/POST/PUT/DELETE /watch-folders  → WatchFolderHandlers    → WatchedFolderRepo, FolderWatchService
GET/POST/PUT/DELETE /vaults        → VaultHandlers          → DB (vaults table)
```

## Key Packages

| Package | Purpose |
|---------|---------|
| internal/api (28 files) | HTTP handlers, router, request types |
| internal/transcription | Unified service, adapter registry, job processor |
| internal/transcription/adapters | 8 model adapters (WhisperX, Parakeet, Canary, Sortformer, Voxtral, OpenAI, AssemblyAI, Deepgram) |
| internal/contacts (18 files) | File-first contacts, TitaNet embeddings, cosine matching |
| internal/repository (6 files) | 13 repo interfaces + GORM implementations |
| internal/models (8 files) | 20 GORM models |
| internal/queue | 2-worker background job queue with auto-scaling |
| internal/sse | Server-Sent Events broadcaster |
| internal/llm | OpenAI/Ollama chat + summarization |
| internal/config | Env var loading |
| internal/auth | JWT + bcrypt |
| pkg/middleware | Auth, compression middleware |
