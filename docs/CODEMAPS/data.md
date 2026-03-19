<!-- Generated: 2026-03-19 | Files scanned: 270 | Token estimate: ~750 -->

# Data

## Database
SQLite + GORM | WAL mode | 10 max connections | Auto-migrate on startup

## Tables (20 models, internal/models/)

### Core Transcription
| Table | Key Fields | Relationships |
|-------|-----------|---------------|
| transcription_jobs | id(UUID), title, status, audio_path, vault_id, folder, transcript, whisperx_params(embedded) | has_many: multi_track_files, speaker_mappings, chat_sessions |
| transcription_job_executions | id, job_id, started_at, completed_at, processing_duration | belongs_to: job |
| speaker_mappings | id, job_id, original_speaker, custom_name | unique(job_id, original_speaker) |
| multi_track_files | id, job_id, file_name, file_path, track_index, offset, gain | belongs_to: job |

### Auth & Users
| Table | Key Fields | Relationships |
|-------|-----------|---------------|
| users | id, username, password(bcrypt), default_profile_id | has_many: api_keys, refresh_tokens |
| api_keys | id, key(UUID), name, is_active, last_used | belongs_to: user |
| refresh_tokens | id, user_id, hashed, expires_at, revoked | belongs_to: user |

### Chat & Summaries
| Table | Key Fields | Relationships |
|-------|-----------|---------------|
| chat_sessions | id(UUID), job_id, title, model, provider, message_count | has_many: messages, belongs_to: job |
| chat_messages | id, session_id, role(user/assistant), content, tokens_used | belongs_to: session |
| summaries | id(UUID), transcription_id, template_id, model, content | belongs_to: job |
| summary_templates | id(UUID), name, prompt, model, include_speaker_info | — |
| summary_settings | id, default_model | singleton |

### Contacts & Voice
| Table | Key Fields | Relationships |
|-------|-----------|---------------|
| contacts | id, vault_id, contact_uid, slug, name, note_path, voice_snippet_path, signature_embedding_path, signature_status | vault-scoped |
| notes | id(UUID), transcription_id, start/end_word_index, start/end_time, quote, content | belongs_to: job |

### Configuration
| Table | Key Fields |
|-------|-----------|
| transcription_profiles | id(UUID), name, is_default, whisperx_params(embedded) |
| llm_configs | id, provider(ollama/openai), base_url, api_key, is_active |
| cloud_provider_configs | id, provider(assemblyai/deepgram/openai), api_key, is_active |
| vaults | id, name, path(unique), is_active |
| app_setups | id, completed, auth_mode, active_vault_id |
| watched_folders | id, user_id, path, recursive, enabled |

## Job Status Lifecycle
```
uploaded → pending → processing → completed
                               → failed
```

## File Storage (Vault-Scoped)
```
Transcripts/<title>/           → self-contained bundles (audio, transcript, notes, metadata.json)
Transcripts/<folder>/<title>/  → folder-organized bundles
data/uploads/                  → original audio files
data/transcripts/              → legacy output JSON + Markdown
data/temp/                     → working directory
Contacts/People/<slug>--<uid>/contact.md → contact files (source of truth)
```
