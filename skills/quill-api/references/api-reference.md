# Quill API Reference

Complete endpoint catalog for the Quill transcription API. All endpoints are prefixed with `/api/v1/`.

Auth required unless noted. Use JWT (cookie) or API key (`X-API-Key` header).

---

## Health & Setup

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/health` | No | Health check |
| GET | `/api/v1/setup/state` | No | Get setup state, active vault, config |
| POST | `/api/v1/setup/complete` | No | Complete initial setup, create first vault |

## Authentication

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/auth/registration-status` | No | Check if registration is available |
| POST | `/api/v1/auth/register` | No | Register new user (`username`, `password`) |
| POST | `/api/v1/auth/login` | No | Login, returns JWT in Set-Cookie |
| POST | `/api/v1/auth/refresh` | No | Refresh JWT token |
| POST | `/api/v1/auth/logout` | No | Logout, clear session |
| POST | `/api/v1/auth/change-password` | JWT | Change password (`old_password`, `new_password`) |
| POST | `/api/v1/auth/change-username` | JWT | Change username (`new_username`) |

## API Keys

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/api-keys` | JWT | List all API keys |
| POST | `/api/v1/api-keys` | JWT | Create API key (`name`) |
| DELETE | `/api/v1/api-keys/:id` | JWT | Delete API key |

## Transcription - Upload

| Method | Path | Description |
|--------|------|-------------|
| POST | `/upload` | Upload audio file (multipart, field: `file`). Returns job with status `uploaded`. |
| POST | `/upload-video` | Upload video, extracts audio. Returns job. |
| POST | `/upload-multitrack` | Upload multi-track audio archive. Returns job. |
| POST | `/youtube` | Download from YouTube URL (`url` field). Returns job. |
| POST | `/quick` | Upload + start transcription in one request (`file` + `model`). |

## Transcription - Job Control

| Method | Path | Description |
|--------|------|-------------|
| POST | `/submit` | Submit job for transcription queue (`job_id`, `model`). |
| POST | `/:id/start` | Start/restart transcription with model parameters. |
| POST | `/:id/kill` | Cancel running transcription. |
| GET | `/:id/status` | Get job status: `uploaded`, `pending`, `processing`, `completed`, `failed`. |
| GET | `/:id` | Get complete job details. |
| GET | `/:id/transcript` | Get final transcript JSON (segments with timestamps, speakers, text). |
| GET | `/:id/logs` | Get transcription logs. |
| GET | `/:id/execution` | Get execution data (attempts, models used, durations). |
| GET | `/:id/audio` | Stream the audio file. |
| DELETE | `/:id` | Delete job, audio, and all related records. |
| GET | `/list` | List jobs. Query params: `search`, `folder`, `sort`, `order`, `status`, `limit`, `offset`. |
| GET | `/models` | List available transcription models. |
| GET | `/quick/:id` | Get quick-submit job status. |

## Transcription - Metadata

| Method | Path | Description |
|--------|------|-------------|
| PUT | `/:id/title` | Update title (`title`). |
| POST | `/:id/title/auto` | Auto-generate title from transcript using LLM. |
| GET | `/:id/summary` | Get latest summary. |
| POST | `/:id/materialize` | Write transcript artifacts to disk (JSON + Markdown). |

## Transcription - Speakers

| Method | Path | Description |
|--------|------|-------------|
| GET | `/:id/speakers` | Get speaker mappings. Returns array of `{original_speaker, custom_name, confidence_score, match_source, match_tier}`. |
| POST | `/:id/speakers` | Save speaker mappings (`mappings[]`). Triggers contact auto-bootstrap + voice embedding. |
| POST | `/:id/speakers/promote` | Promote a voice suggestion to confirmed mapping (`original_speaker`, `contact_id`, `contact_name`, `score`). |
| GET | `/speakers/distinct` | List all distinct speaker labels across all jobs. |

### Speaker mapping fields

| Field | Type | Description |
|-------|------|-------------|
| `original_speaker` | string | Raw label from diarization (e.g., `SPEAKER_00`) |
| `custom_name` | string | Human-readable name |
| `confidence_score` | float | 0.0-1.0, how confident the voice match is |
| `match_source` | string | `auto` (voice-matched), `manual` (user-set), `suggestion_promoted` (user accepted suggestion) |
| `match_tier` | string | `auto` (>=0.80), `suggest` (0.60-0.79), `unknown` (<0.60) |

## Transcription - Notes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/:id/notes` | Get all notes for transcription. |
| POST | `/:id/notes` | Create note (`content`, optional `start_time`, `end_time`, `word_start_index`, `word_end_index`). |
| GET | `/notes/:note_id` | Get note by ID. |
| PUT | `/notes/:note_id` | Update note content. |
| DELETE | `/notes/:note_id` | Delete note. |

## Transcription - Folders

| Method | Path | Description |
|--------|------|-------------|
| GET | `/folders` | List all folders (merged from DB + disk). |
| POST | `/folders` | Create folder (`name`, supports nested paths like `Meetings/2026`). |
| PUT | `/folders/rename` | Rename folder (`old_name`, `new_name`), updates all transcripts inside. |
| DELETE | `/folders?name=X` | Delete empty folder. |
| PUT | `/:id/folder` | Move transcript to folder (`folder`). |

## Transcription - Batch Operations

| Method | Path | Description |
|--------|------|-------------|
| POST | `/batch/delete` | Delete multiple jobs (`ids[]`, max 100). |
| POST | `/batch/move` | Move multiple jobs to folder (`ids[]`, `folder`). |
| POST | `/batch/start` | Start multiple transcriptions (`ids[]` + model params). |

## Contacts & Voice Signatures

| Method | Path | Description |
|--------|------|-------------|
| GET | `/contacts` | List all contacts. |
| POST | `/contacts` | Create contact (`name`, `email`, `phone`, `notes`). |
| GET | `/contacts/:id` | Get contact by ID. |
| PUT | `/contacts/:id` | Update contact. |
| DELETE | `/contacts/:id` | Delete contact. |
| GET | `/contacts/:id/files` | Get contact files (snippets, signatures). |
| GET | `/contacts/:id/snippet` | Get audio snippet file. |
| POST | `/contacts/:id/snippet` | Upload audio snippet (multipart, field: `file`). |
| DELETE | `/contacts/:id/snippet` | Delete snippet. |
| POST | `/contacts/:id/signature` | Upload voice signature (256-dim embedding). |
| DELETE | `/contacts/:id/signature` | Delete voice signature. |
| POST | `/contacts/:id/signature/extract` | Extract voice signature from existing snippet. |
| POST | `/contacts/reindex` | Reindex all contact embeddings from snippets. |

## Chat Sessions

| Method | Path | Description |
|--------|------|-------------|
| GET | `/chat/models` | List available LLM models. |
| POST | `/chat/sessions` | Create chat session (`transcription_id`). |
| GET | `/chat/transcriptions/:id/sessions` | List sessions for a transcript. |
| GET | `/chat/sessions/:id` | Get session with messages. |
| POST | `/chat/sessions/:id/messages` | Send message (`content`). Response streams via SSE. |
| PUT | `/chat/sessions/:id/title` | Update session title. |
| POST | `/chat/sessions/:id/title/auto` | Auto-generate session title from conversation. |
| DELETE | `/chat/sessions/:id` | Delete session and messages. |

## Summarization

| Method | Path | Description |
|--------|------|-------------|
| GET | `/summaries` | List summary templates. |
| POST | `/summaries` | Create template (`name`, `prompt`). |
| GET | `/summaries/:id` | Get template. |
| PUT | `/summaries/:id` | Update template. |
| DELETE | `/summaries/:id` | Delete template. |
| GET | `/summaries/settings` | Get summary settings (default model). |
| POST | `/summaries/settings` | Save summary settings. |
| POST | `/summarize` | Generate summary (`transcription_id`, `template_id`). Streams response. |

## Transcription Profiles

| Method | Path | Description |
|--------|------|-------------|
| GET | `/profiles` | List transcription profiles. |
| POST | `/profiles` | Create profile (model, language, settings). |
| GET | `/profiles/:id` | Get profile. |
| PUT | `/profiles/:id` | Update profile. |
| DELETE | `/profiles/:id` | Delete profile. |
| POST | `/profiles/:id/set-default` | Set as default. |

## User Settings

| Method | Path | Description |
|--------|------|-------------|
| GET | `/user/default-profile` | Get default profile. |
| POST | `/user/default-profile` | Set default profile. |
| GET | `/user/settings` | Get user settings. |
| PUT | `/user/settings` | Update user settings. |

## Vault Management

| Method | Path | Description |
|--------|------|-------------|
| GET | `/vaults` | List all vaults. |
| POST | `/vaults` | Create vault (`name`, `path`). |
| PUT | `/vaults/:id` | Update vault. |
| DELETE | `/vaults/:id` | Delete vault. |
| POST | `/vaults/:id/activate` | Set as active vault. |
| POST | `/vaults/:id/rehydrate` | Rebuild DB from vault files. |

## Obsidian Bridge

| Method | Path | Description |
|--------|------|-------------|
| GET | `/obsidian/config` | Get Obsidian vault path. |
| POST | `/obsidian/config` | Set Obsidian vault path (`vault_path`). |
| POST | `/obsidian/sync/:id` | Publish single transcript to Obsidian. |
| POST | `/obsidian/sync-all` | Publish all completed transcripts. |

## OpenClaw Integration

| Method | Path | Description |
|--------|------|-------------|
| GET | `/openclaw/config` | Get drop folder path. |
| POST | `/openclaw/config` | Set drop folder path (`drop_dir`). |
| POST | `/openclaw/ingest` | Ingest single recording. |
| POST | `/openclaw/ingest-drop` | Consume files from drop folder (`limit`, `consume`). |
| GET | `/openclaw/jobs` | List jobs for OpenClaw. |
| GET | `/openclaw/jobs/:id` | Get job details. |
| GET | `/openclaw/jobs/:id/transcript.json` | Get transcript JSON. |
| GET | `/openclaw/jobs/:id/transcript.md` | Get transcript Markdown. |

## Search

| Method | Path | Description |
|--------|------|-------------|
| POST | `/search/reindex` | Rebuild FTS5 full-text search index. |

## Cloud Providers (External API Keys)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/cloud-providers` | List providers with `has_key` flags. |
| PUT | `/cloud-providers/:provider` | Set API key for provider (`assemblyai`, `deepgram`, `openai`). |
| DELETE | `/cloud-providers/:provider` | Remove API key. |

## LLM Configuration

| Method | Path | Description |
|--------|------|-------------|
| GET | `/llm/config` | Get LLM config (provider, model, API key status). |
| POST | `/llm/config` | Save LLM config (`provider`: `openai` or `ollama`, `base_url`, `api_key`, `model`). |
| POST | `/config/openai/validate` | Validate OpenAI key, list available models. |

## Watch Folders (Auto-Import)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/watch-folders` | List watched folders. |
| POST | `/watch-folders` | Create watch folder (`path`, `recursive`). |
| PUT | `/watch-folders/:id` | Update (enable/disable). |
| DELETE | `/watch-folders/:id` | Delete watch folder. |

## Desktop Runtime

| Method | Path | Description |
|--------|------|-------------|
| GET | `/runtime/warmup` | Get warmup status (model downloads, setup). |
| POST | `/runtime/warmup/retry` | Retry failed warmup. |

## Admin

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/queue/stats` | Queue stats (enqueued, processing, completed, failed). |

## Server-Sent Events

| Method | Path | Description |
|--------|------|-------------|
| GET | `/events` | SSE stream for real-time updates (transcription progress, speaker ID results, errors). |
