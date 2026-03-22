---
name: quill-api
description: Operate Quill, a local-first audio transcription app, via its REST API.
  Use when uploading audio, managing transcriptions, renaming speakers, organizing
  folders, searching transcripts, managing contacts with voice signatures, chatting
  with transcripts via LLMs, or syncing to Obsidian. Requires a running Quill instance.
compatibility: Requires curl/httpie and a running Quill instance (default http://localhost:8080)
metadata:
  author: quill
  version: "1.3.0"
---

# Quill API Skill

Quill is a local-first audio transcription app. Everything runs on the user's machine — no cloud. Transcripts, contacts, and audio live as plain files in a vault (like Obsidian). The database is just a cache.

## Connection

Quill runs at `http://localhost:8080` by default. All endpoints are under `/api/v1/`.

## Authentication

Quill uses JWT cookie-based auth. For programmatic access, use API keys.

```bash
# Login (returns JWT in Set-Cookie header)
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"..."}'

# Or use an API key header
curl -H "X-API-Key: your-api-key" http://localhost:8080/api/v1/transcription/list
```

To create an API key: `POST /api/v1/api-keys` (requires JWT auth first).

## Core Workflows

### 1. Upload and Transcribe Audio

```bash
# Step 1: Upload the file
curl -X POST http://localhost:8080/api/v1/transcription/upload \
  -F "file=@recording.mp3" \
  -H "X-API-Key: $KEY"
# Returns: {"id": "job-uuid", "status": "uploaded", ...}

# Step 2: Submit for transcription
curl -X POST http://localhost:8080/api/v1/transcription/submit \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"job_id": "job-uuid", "model": "whisperx"}'

# Step 3: Check status (poll until "completed")
curl http://localhost:8080/api/v1/transcription/job-uuid/status \
  -H "X-API-Key: $KEY"
# Returns: {"status": "completed"} when done

# Step 4: Get the transcript
curl http://localhost:8080/api/v1/transcription/job-uuid/transcript \
  -H "X-API-Key: $KEY"
```

**Quick submit** (upload + transcribe in one request):
```bash
curl -X POST http://localhost:8080/api/v1/transcription/quick \
  -F "file=@recording.mp3" \
  -F "model=whisperx" \
  -H "X-API-Key: $KEY"
```

Available models: `whisperx`, `parakeet`, `canary`, `voxtral`, `assemblyai`, `deepgram`.

### 2. List and Search Transcriptions

```bash
# List all (paginated)
curl "http://localhost:8080/api/v1/transcription/list" -H "X-API-Key: $KEY"

# Search by text (uses FTS5 full-text search)
curl "http://localhost:8080/api/v1/transcription/list?search=quarterly+review" \
  -H "X-API-Key: $KEY"

# Filter by folder
curl "http://localhost:8080/api/v1/transcription/list?folder=Meetings" \
  -H "X-API-Key: $KEY"

# Sort options: created_at, title, duration (append &sort=title&order=asc)
```

### 3. Rename Speakers

After transcription, speakers are labeled SPEAKER_00, SPEAKER_01, etc. Rename them to real names:

```bash
# Get current speaker mappings
curl http://localhost:8080/api/v1/transcription/job-uuid/speakers \
  -H "X-API-Key: $KEY"
# Returns: [{"original_speaker":"SPEAKER_00","custom_name":"SPEAKER_00"}, ...]

# Save new names
curl -X POST http://localhost:8080/api/v1/transcription/job-uuid/speakers \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"mappings": [
    {"original_speaker": "SPEAKER_00", "custom_name": "Alice"},
    {"original_speaker": "SPEAKER_01", "custom_name": "Bob"}
  ]}'
```

When you save speaker mappings, Quill automatically:
- Creates contacts for new names
- Extracts voice snippets from the audio
- Generates voice embeddings (TitaNet 256-dim)
- Uses these to auto-identify speakers in future transcriptions

Speakers with `match_source: "auto"` were auto-identified by voice signature (confidence 80%+). Don't rename these — they're already matched.

### 4. Organize into Folders

```bash
# List folders
curl http://localhost:8080/api/v1/transcription/folders -H "X-API-Key: $KEY"

# Create a folder
curl -X POST http://localhost:8080/api/v1/transcription/folders \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"name": "Meetings/2026"}'

# Move a transcript to a folder
curl -X PUT http://localhost:8080/api/v1/transcription/job-uuid/folder \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"folder": "Meetings/2026"}'

# Bulk move
curl -X POST http://localhost:8080/api/v1/transcription/batch/move \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"ids": ["id1","id2","id3"], "folder": "Archive"}'
```

### 5. Manage Contacts & Voice Signatures

```bash
# List contacts
curl http://localhost:8080/api/v1/contacts -H "X-API-Key: $KEY"

# Create a contact
curl -X POST http://localhost:8080/api/v1/contacts \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"name": "Alice Smith", "email": "alice@example.com"}'

# Upload a voice snippet for speaker identification
curl -X POST http://localhost:8080/api/v1/contacts/1/snippet \
  -F "file=@alice-voice.wav" \
  -H "X-API-Key: $KEY"

# Extract voice signature from snippet
curl -X POST http://localhost:8080/api/v1/contacts/1/signature/extract \
  -H "X-API-Key: $KEY"
```

### 6. Chat with Transcripts

```bash
# Create a chat session for a transcript
curl -X POST http://localhost:8080/api/v1/chat/sessions \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"transcription_id": "job-uuid"}'
# Returns: {"id": "session-uuid", ...}

# Send a message (response streams via SSE)
curl -X POST http://localhost:8080/api/v1/chat/sessions/session-uuid/messages \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"content": "What were the main action items discussed?"}'
```

### 7. Generate Summaries

```bash
# Summarize a transcript (streams response)
curl -X POST http://localhost:8080/api/v1/summarize \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"transcription_id": "job-uuid", "template_id": 1}'
```

### 8. Update Titles

```bash
# Manual title update
curl -X PUT http://localhost:8080/api/v1/transcription/job-uuid/title \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"title": "Q1 Planning Meeting"}'

# Auto-generate title from transcript content using LLM
curl -X POST http://localhost:8080/api/v1/transcription/job-uuid/title/auto \
  -H "X-API-Key: $KEY"
```

### 9. Delete Transcriptions

```bash
# Delete single
curl -X DELETE http://localhost:8080/api/v1/transcription/job-uuid \
  -H "X-API-Key: $KEY"

# Batch delete
curl -X POST http://localhost:8080/api/v1/transcription/batch/delete \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"ids": ["id1","id2"]}'
```

### 10. Sync to Obsidian

```bash
# Set Obsidian vault path
curl -X POST http://localhost:8080/api/v1/obsidian/config \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $KEY" \
  -d '{"vault_path": "/Users/me/ObsidianVault"}'

# Sync a single transcript
curl -X POST http://localhost:8080/api/v1/obsidian/sync/job-uuid \
  -H "X-API-Key: $KEY"

# Sync all completed transcripts
curl -X POST http://localhost:8080/api/v1/obsidian/sync-all \
  -H "X-API-Key: $KEY"
```

## Real-Time Events

Quill pushes real-time updates via Server-Sent Events:

```bash
curl -N http://localhost:8080/api/v1/events -H "X-API-Key: $KEY"
```

Events include transcription progress, completion, speaker identification results, and errors.

## Important Concepts

- **Vault**: A self-contained folder with all recordings, transcripts, and contacts. Users can have multiple vaults.
- **Bundle**: Each transcript is a directory containing audio, JSON, Markdown, notes, and metadata.
- **Speaker confidence**: Scores 0.0-1.0. Auto-assigned at 80%+, suggested at 60-79%, unknown below 60%.
- **Job statuses**: `uploaded` → `pending` → `processing` → `completed` or `failed`.

## Full API Reference

See [references/api-reference.md](references/api-reference.md) for the complete endpoint catalog with all parameters.
