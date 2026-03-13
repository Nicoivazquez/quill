<!-- Generated: 2026-03-13 | Files scanned: 255 | Token estimate: ~850 -->

# Frontend

## Stack
React 19 + TypeScript + Vite + Tailwind CSS + Radix UI + Zustand + React Query + WaveSurfer.js

## Page Tree (React Router in App.tsx)

```
BrowserRouter
└── ProtectedRoute (auth gate → SetupWizard | Login | Register)
    └── MainLayout (Header + content)
        ├── /                    → Dashboard (AudioFilesTable + upload)
        ├── /audio/:audioId      → AudioDetailView (transcript + player + notes)
        ├── /contacts            → ContactsPage (CRUD + voice signatures)
        ├── /settings            → SettingsPage (8 tabs)
        ├── /settings/cli        → CLISettingsPage
        └── /auth/cli/authorize  → CLIAuthConfirmation
```

## Component Hierarchy

```
App.tsx
├── Header (nav, add-audio menu, theme switcher)
├── Dashboard
│   ├── AudioFilesTable (infinite scroll, search, sort)
│   ├── MultiTrackUploadDialog
│   ├── QuickTranscriptionDialog
│   └── YouTubeDownloadDialog
├── AudioDetailView
│   ├── EmberPlayer (WaveSurfer.js audio player)
│   ├── TranscriptSection → TranscriptView (compact/expanded modes)
│   ├── SpeakerRenameDialog (speaker → contact mapping)
│   ├── NotesSidebar + NoteEditorDialog
│   ├── SummaryDialog (streaming AI summary)
│   ├── DownloadDialog, ExecutionInfoDialog, LogsDialog
│   └── ChatSidePanel → ChatInterface
├── ContactsPage (list, detail, voice snippet/signature upload)
├── SettingsPage
│   ├── ProfileSettings (transcription profiles)
│   ├── AccountSettings
│   ├── APIKeySettings (create/delete API keys)
│   ├── LLMSettings (Ollama/OpenAI config)
│   ├── SummaryTemplatesTable
│   ├── VaultSettingsTab
│   ├── AutoImportSettingsTab
│   └── CloudProviderSettings (OpenAI/AssemblyAI/Deepgram keys)
└── RuntimeWarmupBanner (desktop warmup progress)
```

## State Management

```
Zustand Store (auth-storage → localStorage):
  token, isAuthenticated, isLocalMode, isSetupCompleted, isInitialized

React Query (server cache):
  ['audioFiles']           → paginated list
  ['audio', id]            → single audio detail
  ['transcript', id]       → transcript segments + words
  ['speakerMappings', id]  → speaker labels
  ['notes', id]            → transcript annotations
  ['summaryTemplates']     → summary prompts
  ['contacts']             → contact list/detail/files
  ['runtime-warmup']       → desktop warmup status

Context Providers:
  ThemeContext              → light/dark (localStorage)
  GlobalUploadContext       → upload queue + progress
  ChatEventsContext         → title update events
```

## Key Hooks (features/*/hooks/)

| Hook | Fetches | Mutations |
|------|---------|-----------|
| useAudioFiles | list (infinite), profiles | upload, multitrack, youtube, quick |
| useAudioDetail | detail, transcript, execution, logs | updateTitle |
| useTranscriptionEvents | SSE stream (job_update, speaker_id) | — |
| useTranscriptionSpeakers | speaker mappings | — |
| useTranscriptionNotes | notes | create, update, delete |
| useTranscriptionSummary | templates, existing summary | summarize (streaming) |
| useContacts | list, detail, files | create, update, delete, snippet, signature |
| useAuth | setup state, registration | login, register, refresh, logout |
| useRuntimeWarmup | warmup status | retry |

## API Client
No centralized client — uses native `fetch()` with `useAuth().getAuthHeaders()`.
Auto-refreshes JWT on 401 via fetch wrapper in useAuth hook.
Vite dev proxy: `/api` → `http://localhost:8080`.
