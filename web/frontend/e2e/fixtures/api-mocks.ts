import { Page } from '@playwright/test';

// ── Seed data ──────────────────────────────────────────────

export const AUDIO_FILES = [
  {
    id: '1',
    title: 'Team Standup March 10',
    status: 'completed',
    created_at: '2026-03-10T09:00:00Z',
    audio_path: '/data/uploads/standup-2026-03-10.wav',
    diarization: true,
    speakers: 3,
    duration: 1823.5,
    folder: 'Work',
  },
  {
    id: '2',
    title: 'Product Review',
    status: 'completed',
    created_at: '2026-03-11T14:00:00Z',
    audio_path: '/data/uploads/product-review.mp3',
    diarization: true,
    speakers: 5,
    duration: 3600.0,
    folder: 'Work/Projects',
  },
  {
    id: '3',
    title: 'Interview with Candidate',
    status: 'processing',
    created_at: '2026-03-12T10:00:00Z',
    audio_path: '/data/uploads/interview-candidate.wav',
    diarization: true,
    speakers: 0,
    duration: 0,
    folder: '',
  },
  {
    id: '4',
    title: 'Client Call Q1 Planning',
    status: 'completed',
    created_at: '2026-03-09T11:00:00Z',
    audio_path: '/data/uploads/client-call-q1.wav',
    diarization: true,
    speakers: 2,
    duration: 2700.0,
    folder: 'Clients',
  },
];

export const TRANSCRIPT_SEGMENTS = [
  { start: 0.0, end: 4.5, text: 'Good morning everyone, let\'s get started with the standup.', speaker: 'SPEAKER_00' },
  { start: 4.8, end: 9.2, text: 'Sure. Yesterday I worked on the API refactor and finished the handler tests.', speaker: 'SPEAKER_01' },
  { start: 9.5, end: 14.1, text: 'Nice. I was debugging the SSE connection drops we saw in production.', speaker: 'SPEAKER_02' },
  { start: 14.5, end: 19.8, text: 'Did you find the root cause? We had a few user reports about that.', speaker: 'SPEAKER_00' },
  { start: 20.1, end: 26.3, text: 'Yes, it was a timeout issue on the reverse proxy. I pushed a config fix.', speaker: 'SPEAKER_02' },
  { start: 26.8, end: 32.0, text: 'Great. Today I\'m going to work on the contacts voice signature feature.', speaker: 'SPEAKER_01' },
  { start: 32.5, end: 37.0, text: 'Sounds good. Let\'s sync again tomorrow. Have a productive day everyone.', speaker: 'SPEAKER_00' },
];

// Generate word-level segments from TRANSCRIPT_SEGMENTS (required for expanded view rendering)
function generateWordSegments(segments: typeof TRANSCRIPT_SEGMENTS) {
  const words: Array<{ start: number; end: number; word: string; score: number; speaker?: string }> = [];
  for (const seg of segments) {
    const segWords = seg.text.split(/\s+/);
    const segDuration = seg.end - seg.start;
    const wordDuration = segDuration / segWords.length;
    segWords.forEach((w, i) => {
      words.push({
        start: parseFloat((seg.start + i * wordDuration).toFixed(2)),
        end: parseFloat((seg.start + (i + 1) * wordDuration).toFixed(2)),
        word: w,
        score: 0.95,
        speaker: seg.speaker,
      });
    });
  }
  return words;
}

export const WORD_SEGMENTS = generateWordSegments(TRANSCRIPT_SEGMENTS);

export const SPEAKER_MAPPINGS = [
  { original_speaker: 'SPEAKER_00', custom_name: 'Alice', confidence_score: 0.92, match_source: 'auto', match_tier: 'auto' },
  { original_speaker: 'SPEAKER_01', custom_name: 'Bob', confidence_score: 0.75, match_source: 'suggestion_promoted', match_tier: 'suggest' },
  { original_speaker: 'SPEAKER_02', custom_name: 'Charlie', confidence_score: 0, match_source: 'manual', match_tier: '' },
];

export const CONTACTS = [
  {
    id: 1,
    vault_id: 1,
    contact_uid: 'alice-001',
    slug: 'alice-johnson',
    name: 'Alice Johnson',
    email: 'alice@example.com',
    phone: '+1-555-0101',
    notes: 'Engineering manager',
    note_path: 'Contacts/People/alice-johnson--alice-001/contact.md',
    file_mtime_ns: 1709280000000000000,
    voice_snippet_path: '/vault/Contacts/People/alice-johnson--alice-001/snippet.wav',
    signature_embedding_path: '/vault/Contacts/People/alice-johnson--alice-001/signature.json',
    signature_status: 'ready',
    signature_data: '{"source":"extracted"}',
    created_at: '2026-01-15T08:00:00Z',
    updated_at: '2026-03-01T10:00:00Z',
  },
  {
    id: 2,
    vault_id: 1,
    contact_uid: 'bob-002',
    slug: 'bob-smith',
    name: 'Bob Smith',
    email: 'bob@example.com',
    phone: '+1-555-0102',
    notes: 'Senior developer',
    note_path: 'Contacts/People/bob-smith--bob-002/contact.md',
    file_mtime_ns: 1709280000000000000,
    voice_snippet_path: '/vault/Contacts/People/bob-smith--bob-002/snippet.wav',
    signature_embedding_path: null,
    signature_status: 'none',
    signature_data: null,
    created_at: '2026-02-01T08:00:00Z',
    updated_at: '2026-03-05T10:00:00Z',
  },
  {
    id: 3,
    vault_id: 1,
    contact_uid: 'charlie-003',
    slug: 'charlie-davis',
    name: 'Charlie Davis',
    email: 'charlie@example.com',
    phone: '',
    notes: '',
    note_path: 'Contacts/People/charlie-davis--charlie-003/contact.md',
    file_mtime_ns: 1709280000000000000,
    voice_snippet_path: null,
    signature_embedding_path: null,
    signature_status: 'none',
    signature_data: null,
    created_at: '2026-03-01T08:00:00Z',
    updated_at: '2026-03-01T08:00:00Z',
  },
];

export const FOLDERS = ['Work', 'Work/Projects', 'Clients'];

export const CLOUD_PROVIDERS = [
  { provider: 'openai', has_key: true, is_active: true },
  { provider: 'assemblyai', has_key: false, is_active: false },
  { provider: 'deepgram', has_key: false, is_active: false },
];

export const PROFILES = [
  { id: 1, name: 'Default', model: 'whisperx-large-v3', is_default: true, settings: { language: 'en', diarization: true } },
  { id: 2, name: 'Fast', model: 'whisperx-base', is_default: false, settings: { language: 'en', diarization: false } },
];

export const SUMMARY_TEMPLATES = [
  { id: 1, name: 'Meeting Notes', prompt: 'Summarize this meeting transcript into action items and key decisions.', model: 'gpt-4o' },
  { id: 2, name: 'Brief Summary', prompt: 'Provide a 2-3 sentence summary.', model: 'gpt-4o-mini' },
];

// ── Mock installer ─────────────────────────────────────────

export async function installApiMocks(page: Page) {
  // Setup state – local mode, completed
  await page.route('**/api/v1/setup/state', (route) =>
    route.fulfill({ json: { completed: true, auth_mode: 'local' } }),
  );

  // Registration status (not needed in local mode, but mock anyway)
  await page.route('**/api/v1/auth/registration-status', (route) =>
    route.fulfill({ json: { is_registered: true, registration_enabled: false } }),
  );

  // User settings
  await page.route('**/api/v1/user/settings', (route) =>
    route.fulfill({ json: { auto_summary_enabled: false, auto_transcription_title_enabled: true } }),
  );

  // Runtime warmup (not running on web)
  await page.route('**/api/v1/runtime/warmup', (route) =>
    route.fulfill({ json: { status: 'idle', steps: [] } }),
  );

  // Profiles
  await page.route('**/api/v1/profiles', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: PROFILES });
    }
    return route.fulfill({ json: PROFILES[0] });
  });

  // Transcription list (frontend expects { jobs, pagination })
  await page.route('**/api/v1/transcription/list*', (route) => {
    const url = new URL(route.request().url());
    const q = url.searchParams.get('q')?.toLowerCase() ?? '';
    const folder = url.searchParams.get('folder');
    let filtered = [...AUDIO_FILES];
    if (q) {
      filtered = filtered.filter(
        (f) =>
          f.title.toLowerCase().includes(q) ||
          f.audio_path.toLowerCase().includes(q),
      );
    }
    if (folder !== null) {
      filtered = filtered.filter((f) => f.folder === folder);
    }
    return route.fulfill({
      json: {
        jobs: filtered,
        pagination: {
          page: 1,
          limit: 20,
          total: filtered.length,
          pages: 1,
        },
      },
    });
  });

  // Single transcription detail
  await page.route(/\/api\/v1\/transcription\/\d+$/, (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    const url = route.request().url();
    const id = url.split('/').pop();
    const file = AUDIO_FILES.find((f) => f.id === id);
    if (file) {
      return route.fulfill({ json: file });
    }
    return route.fulfill({ status: 404, json: { error: 'Not found' } });
  });

  // Transcript segments (frontend expects { transcript: { text, segments, word_segments } })
  await page.route('**/api/v1/transcription/*/transcript', (route) =>
    route.fulfill({
      json: {
        transcript: {
          segments: TRANSCRIPT_SEGMENTS,
          word_segments: WORD_SEGMENTS,
          text: TRANSCRIPT_SEGMENTS.map((s) => s.text).join(' '),
        },
      },
    }),
  );

  // Speaker mappings (frontend expects SpeakerMapping[] directly)
  await page.route('**/api/v1/transcription/*/speakers', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: SPEAKER_MAPPINGS });
    }
    // POST – return updated mappings
    return route.fulfill({
      json: {
        mappings: SPEAKER_MAPPINGS,
        contact_bootstrap: { started_count: 0, created_count: 0, skipped_existing_count: 0 },
      },
    });
  });

  // Summary
  await page.route('**/api/v1/transcription/*/summary', (route) =>
    route.fulfill({ json: { summary: null } }),
  );

  // Execution data
  await page.route('**/api/v1/transcription/*/execution', (route) =>
    route.fulfill({
      json: {
        start_time: '2026-03-10T09:00:00Z',
        end_time: '2026-03-10T09:02:15Z',
        duration: 135.0,
        model: 'whisperx-large-v3',
      },
    }),
  );

  // Logs
  await page.route('**/api/v1/transcription/*/logs', (route) =>
    route.fulfill({ body: '[2026-03-10 09:00:00] Starting transcription...\n[2026-03-10 09:02:15] Completed.' }),
  );

  // Upload
  await page.route('**/api/v1/transcription/upload', (route) =>
    route.fulfill({
      json: {
        id: 5,
        job_id: 'job-005',
        title: 'Uploaded File',
        status: 'uploaded',
        original_filename: 'test-audio.wav',
      },
    }),
  );

  // Submit transcription
  await page.route('**/api/v1/transcription/submit', (route) =>
    route.fulfill({ json: { job_id: 'job-005', status: 'pending' } }),
  );

  // Start transcription
  await page.route('**/api/v1/transcription/*/start', (route) =>
    route.fulfill({ json: { job_id: 'job-005', status: 'processing', progress: 0 } }),
  );

  // Title update
  await page.route('**/api/v1/transcription/*/title', (route) => {
    if (route.request().method() === 'PUT') {
      return route.fulfill({ json: { title: 'Updated Title' } });
    }
    return route.continue();
  });

  // Auto-title
  await page.route('**/api/v1/transcription/*/title/auto', (route) =>
    route.fulfill({ json: { title: 'AI Generated Title' } }),
  );

  // Folder rename (register before broader folders route)
  await page.route('**/api/v1/transcription/folders/rename', (route) => {
    if (route.request().method() === 'PUT') {
      return route.fulfill({ json: { message: 'Folder renamed' } });
    }
    return route.continue();
  });

  // Folders (GET list, POST create, DELETE)
  await page.route(/\/api\/v1\/transcription\/folders(\?.*)?$/, (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: { folders: FOLDERS } });
    }
    if (route.request().method() === 'POST') {
      return route.fulfill({ json: { folder: 'New Folder' } });
    }
    if (route.request().method() === 'DELETE') {
      return route.fulfill({ json: { message: 'Folder deleted' } });
    }
    return route.continue();
  });

  // Move transcript to folder
  await page.route('**/api/v1/transcription/*/folder', (route) => {
    if (route.request().method() === 'PUT') {
      return route.fulfill({ json: { message: 'Moved to folder' } });
    }
    return route.continue();
  });

  // Models
  await page.route('**/api/v1/transcription/models', (route) =>
    route.fulfill({
      json: {
        models: [
          { name: 'whisperx-large-v3', provider: 'local', languages: ['en', 'es', 'fr'] },
          { name: 'parakeet-tdt-0.6b', provider: 'local', languages: ['en'] },
        ],
      },
    }),
  );

  // Notes
  await page.route('**/api/v1/transcription/*/notes', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: [] });
    }
    return route.fulfill({ json: { id: 1, content: 'New note', created_at: new Date().toISOString() } });
  });

  // Contacts (regex to match with or without query params like ?q=...)
  await page.route(/\/api\/v1\/contacts(\?.*)?$/, (route) => {
    if (route.request().method() === 'GET') {
      const url = new URL(route.request().url());
      const q = url.searchParams.get('q')?.toLowerCase() ?? '';
      const filtered = q
        ? CONTACTS.filter((c) => c.name.toLowerCase().includes(q) || c.email.toLowerCase().includes(q))
        : CONTACTS;
      return route.fulfill({ json: { contacts: filtered, vault_id: 1 } });
    }
    // POST – create contact (returns Contact directly)
    return route.fulfill({
      json: {
        id: 99,
        vault_id: 1,
        contact_uid: 'new-099',
        slug: 'new-contact',
        name: 'New Contact',
        email: '',
        phone: '',
        notes: '',
        note_path: 'Contacts/People/new-contact--new-099/contact.md',
        file_mtime_ns: 0,
        voice_snippet_path: null,
        signature_embedding_path: null,
        signature_status: 'none',
        signature_data: null,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      },
    });
  });

  // Single contact (numeric IDs, returns Contact directly)
  await page.route(/\/api\/v1\/contacts\/\d+$/, (route) => {
    const url = route.request().url();
    const id = Number(url.split('/').pop());
    const contact = CONTACTS.find((c) => c.id === id);
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: contact ?? CONTACTS[0] });
    }
    if (route.request().method() === 'PUT') {
      return route.fulfill({ json: { ...(contact ?? CONTACTS[0]), name: 'Updated Name' } });
    }
    if (route.request().method() === 'DELETE') {
      return route.fulfill({ status: 204, body: '' });
    }
    return route.continue();
  });

  // Contact files (returns ContactFilesResponse object)
  await page.route('**/api/v1/contacts/*/files', (route) =>
    route.fulfill({
      json: {
        contact_id: 1,
        vault_id: 1,
        note_path: 'Contacts/People/alice-johnson--alice-001/contact.md',
        note_abs_path: '/Users/test/Quill/Contacts/People/alice-johnson--alice-001/contact.md',
        voice_snippet_path: 'Contacts/People/alice-johnson--alice-001/snippet.wav',
        signature_embedding_path: 'Contacts/People/alice-johnson--alice-001/signature.json',
        signature_status: 'ready',
        sync_error: null,
        file_mtime_ns: 1709280000000000000,
        voice_snippet_abs_path: '/Users/test/Quill/Contacts/People/alice-johnson--alice-001/snippet.wav',
        signature_embedding_abs_path: '/Users/test/Quill/Contacts/People/alice-johnson--alice-001/signature.json',
      },
    }),
  );

  // Contact snippet
  await page.route('**/api/v1/contacts/*/snippet', (route) => {
    if (route.request().method() === 'DELETE') {
      return route.fulfill({ status: 204, body: '' });
    }
    return route.fulfill({ json: { message: 'Snippet uploaded' } });
  });

  // Contact signature
  await page.route('**/api/v1/contacts/*/signature', (route) => {
    if (route.request().method() === 'DELETE') {
      return route.fulfill({ status: 204, body: '' });
    }
    return route.fulfill({ json: { message: 'Signature uploaded' } });
  });

  // Signature extract
  await page.route('**/api/v1/contacts/*/signature/extract', (route) =>
    route.fulfill({ json: { message: 'Extraction started' } }),
  );

  // Reindex
  await page.route('**/api/v1/contacts/reindex', (route) =>
    route.fulfill({ json: { message: 'Reindex complete', indexed_count: 3 } }),
  );

  // Cloud providers
  await page.route('**/api/v1/cloud-providers', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: CLOUD_PROVIDERS });
    }
    return route.continue();
  });

  await page.route(/\/api\/v1\/cloud-providers\/\w+$/, (route) => {
    if (route.request().method() === 'PUT') {
      return route.fulfill({ json: { provider: 'assemblyai', has_key: true, is_active: true } });
    }
    if (route.request().method() === 'DELETE') {
      return route.fulfill({ status: 204, body: '' });
    }
    return route.continue();
  });

  // LLM config
  await page.route('**/api/v1/llm/config', (route) =>
    route.fulfill({ json: { provider: 'openai', model: 'gpt-4o', is_active: true } }),
  );

  // Summary templates
  await page.route('**/api/v1/summaries', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: SUMMARY_TEMPLATES });
    }
    return route.fulfill({ json: SUMMARY_TEMPLATES[0] });
  });

  // Vaults
  await page.route('**/api/v1/vaults', (route) =>
    route.fulfill({
      json: [{ id: 1, name: 'Default Vault', path: '/Users/test/Quill', is_active: true }],
    }),
  );

  // Watch folders
  await page.route('**/api/v1/watch-folders', (route) =>
    route.fulfill({ json: [] }),
  );

  // SSE events – return empty stream that stays open briefly
  await page.route('**/api/v1/events*', (route) =>
    route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
      body: 'data: {"type":"connected"}\n\n',
    }),
  );

  // Audio file (EmberPlayer fetches from /audio endpoint)
  await page.route('**/api/v1/transcription/*/audio', (route) => {
    // Minimal WAV header (44 bytes, no audio data)
    const header = new Uint8Array(44);
    const view = new DataView(header.buffer);
    header.set([0x52, 0x49, 0x46, 0x46], 0);
    view.setUint32(4, 36, true);
    header.set([0x57, 0x41, 0x56, 0x45], 8);
    header.set([0x66, 0x6d, 0x74, 0x20], 12);
    view.setUint32(16, 16, true);
    view.setUint16(20, 1, true);
    view.setUint16(22, 1, true);
    view.setUint32(24, 44100, true);
    view.setUint32(28, 88200, true);
    view.setUint16(32, 2, true);
    view.setUint16(34, 16, true);
    header.set([0x64, 0x61, 0x74, 0x61], 36);
    view.setUint32(40, 0, true);
    return route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'audio/wav' },
      body: Buffer.from(header),
    });
  });

  // Audio stream (legacy endpoint, keep for compatibility)
  await page.route('**/api/v1/transcription/*/stream*', (route) => {
    // Minimal WAV header (44 bytes, no audio data)
    const header = new Uint8Array(44);
    const view = new DataView(header.buffer);
    // "RIFF"
    header.set([0x52, 0x49, 0x46, 0x46], 0);
    view.setUint32(4, 36, true); // file size - 8
    // "WAVE"
    header.set([0x57, 0x41, 0x56, 0x45], 8);
    // "fmt "
    header.set([0x66, 0x6d, 0x74, 0x20], 12);
    view.setUint32(16, 16, true); // fmt chunk size
    view.setUint16(20, 1, true); // PCM
    view.setUint16(22, 1, true); // mono
    view.setUint32(24, 44100, true); // sample rate
    view.setUint32(28, 88200, true); // byte rate
    view.setUint16(32, 2, true); // block align
    view.setUint16(34, 16, true); // bits per sample
    // "data"
    header.set([0x64, 0x61, 0x74, 0x61], 36);
    view.setUint32(40, 0, true); // data size
    return route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'audio/wav' },
      body: Buffer.from(header),
    });
  });

  // Obsidian config
  await page.route('**/api/v1/obsidian/config', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: { vault_path: '', configured: false } });
    }
    // POST – save config
    return route.fulfill({ json: { vault_path: '/Users/test/ObsidianVault', configured: true } });
  });

  // Obsidian sync single transcript
  await page.route('**/api/v1/obsidian/sync/*', (route) =>
    route.fulfill({ json: { synced: true, path: '/Users/test/ObsidianVault/Quill/Test Transcript.md' } }),
  );

  // Obsidian bulk sync
  await page.route('**/api/v1/obsidian/sync-all', (route) =>
    route.fulfill({ json: { synced: 2, failed: 0, total: 2 } }),
  );

  // Batch delete
  await page.route('**/api/v1/transcription/batch/delete', (route) => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON();
      const results = (body.ids || []).map((id: string) => ({ id, success: true }));
      return route.fulfill({ json: { results } });
    }
    return route.continue();
  });

  // Batch move
  await page.route('**/api/v1/transcription/batch/move', (route) => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON();
      const results = (body.ids || []).map((id: string) => ({ id, success: true }));
      return route.fulfill({ json: { results } });
    }
    return route.continue();
  });

  // Batch start
  await page.route('**/api/v1/transcription/batch/start', (route) => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON();
      const results = (body.ids || []).map((id: string) => ({ id, success: true }));
      return route.fulfill({ json: { results } });
    }
    return route.continue();
  });

  // OpenClaw config
  await page.route('**/api/v1/openclaw/config', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: { drop_folder: '', auto_ingest: false, configured: false } });
    }
    return route.fulfill({ json: { drop_folder: '/tmp/openclaw', auto_ingest: true, configured: true } });
  });

  // OpenClaw ingest drop folder
  await page.route('**/api/v1/openclaw/ingest-drop', (route) =>
    route.fulfill({ json: { ingested: 3, failed: 0 } }),
  );

  // OpenClaw jobs list
  await page.route('**/api/v1/openclaw/jobs', (route) =>
    route.fulfill({ json: { jobs: [] } }),
  );

  // Health
  await page.route('**/health', (route) =>
    route.fulfill({ json: { status: 'ok' } }),
  );
}

/**
 * Set localStorage to simulate local-mode auth so the app skips login.
 */
export async function setLocalAuthState(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem(
      'auth-storage',
      JSON.stringify({
        state: { token: null, isLocalMode: true },
        version: 0,
      }),
    );
  });
}
