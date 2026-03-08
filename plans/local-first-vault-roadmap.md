# Local-First Vault Roadmap (Obsidian + OpenClaw Bridge)

## Product Direction
- JSON is canonical for diarization/AI fidelity.
- Markdown is always generated as a companion artifact for human reading and external integrations.
- Desktop-first mode is local/no-login by default.
- Server auth remains available behind advanced setup.
- This app is the durable transcription + diarization system of record.
- OpenClaw and Obsidian are downstream integration targets.

## Phase 1: Local Runtime + Setup
- Add `AUTH_MODE` with `local` as the default runtime mode.
- Remove login gating in local mode.
- Add setup flow for:
  - vault path
  - vault name
  - auth mode (advanced)
  - optional Obsidian vault path
  - optional OpenClaw drop path

### Completion Criteria
- App can boot without login in local mode.
- Setup state is persisted and queryable.

## Phase 2: Vault Core + App Shell
- Multi-vault model and APIs:
  - create/list/update/delete vault
  - activate vault
- Active vault is used by local artifact/output behavior.
- Obsidian/Calibre-style shell baseline:
  - left vault pane/library context
  - center content pane for jobs/transcripts

### Completion Criteria
- Active vault can be switched in UI.
- Vault context is visible and persistent.

## Phase 3: Storage + Transcript Artifacts
- Canonical artifact per job folder: `transcript.json`.
- Companion artifact per job folder: `transcript.md`.
- Markdown includes frontmatter IDs and readable speaker/timestamp blocks.
- Vault-first structure:
  - `Inbox/`
  - `Media/`
  - `Transcripts/`
  - `Contacts/Snippets/`
  - `.scriber/`

### Completion Criteria
- Completed jobs materialize both JSON and Markdown artifacts.
- Reprocessing keeps artifacts deterministic and updatable.

## Phase 4: Obsidian + OpenClaw Bridge (Before Voice Signatures)

### Obsidian (one-way publish)
- Publish/update transcript Markdown into configured Obsidian vault path.
- Use stable file naming + frontmatter ID for deterministic updates.
- Include links/paths to media and canonical transcript artifacts.

### OpenClaw Interoperability
- Ingest contract:
  - API upload endpoint for recordings
  - optional drop-folder ingest endpoint using configured local directory
- Retrieval contract:
  - job status endpoint
  - transcript JSON endpoint
  - transcript Markdown endpoint
  - artifact path metadata endpoint
- Local mode: no-login path via local auth mode.
- Server mode: token/JWT auth remains available.

### Positioning
- This app is the durable transcription/diarization dashboard + artifact store.
- OpenClaw automates downstream workflows using stable local contracts.

### Completion Criteria
- OpenClaw can submit/drop recordings and fetch artifacts/status end-to-end.
- Obsidian note publish can create and deterministically update existing notes.

## Phase 5: Contacts + Voice Signature Scaffold
- Contact CRUD fields:
  - name
  - phone
  - email
  - notes
- Voice snippet upload and vault storage.
- Signature metadata scaffold:
  - status lifecycle: `none` -> `processing` -> `ready|failed`
  - signature metadata payload slot for later matcher pipeline
- Full voice matching deferred to a later phase.

### Completion Criteria
- Contacts and snippets are persisted.
- Signature status can be tracked independently of matching.

## Public Interfaces / Contracts
- Setup APIs:
  - setup state
  - setup completion
- Vault APIs:
  - create/list/update/delete/activate
- Obsidian APIs/jobs:
  - config get/save
  - publish transcript markdown
- OpenClaw APIs:
  - ingest upload
  - ingest from configured drop folder
  - list jobs/status
  - fetch transcript JSON
  - fetch transcript Markdown
- Contact APIs:
  - CRUD
  - snippet upload
  - signature status scaffolding

## Test Plan
- Local mode boots and works without login.
- Server mode auth remains functional.
- Multi-vault switching updates active vault behavior.
- JSON and Markdown artifacts remain consistent after updates.
- Obsidian publish:
  - creates new note
  - deterministic update for same transcript ID
- OpenClaw flow:
  - ingest (upload or drop folder)
  - process
  - retrieve status + JSON + Markdown
- Contact scaffold:
  - CRUD works
  - snippets stored in vault paths
  - status transitions validated

## Implementation Notes
- Keep JSON as source-of-truth for diarization and AI workflows.
- Keep Markdown predictable and plugin-friendly for Obsidian integration.
- Phase 5 intentionally stays one-way for Obsidian sync (no bi-directional merge logic yet).
