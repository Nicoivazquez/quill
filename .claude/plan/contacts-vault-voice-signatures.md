# Implementation Plan: Contacts File-First + Voice Signatures (NeMo TitaNet)

## Summary

Implement vault-scoped, file-canonical contacts with bidirectional sync and hard-delete semantics. Keep DB as cache/index. Add voice snippet + TitaNet embedding artifacts now; defer speaker matching logic.

---

## Key Interface Changes

1. Extend `Contact` with `vault_id`, `contact_uid`, `slug`, `note_path`, `file_mtime_ns`, `sync_error`, `signature_embedding_path`
2. Scope all `/api/v1/contacts` operations to active vault
3. Add `POST /api/v1/contacts/reindex` and `GET /api/v1/contacts/:id/files`
4. Keep `POST /api/v1/contacts/:id/snippet`, but run async embedding extraction and return status/path fields

---

## Phase 5B.1: Data + Migration Foundation

### Step 1.1: Update Contact Model
**File:** `internal/models/vault.go`

```go
type Contact struct {
    ID        uint   `json:"id" gorm:"primaryKey"`
    VaultID   uint   `json:"vault_id" gorm:"not null;index"`
    ContactUID string `json:"contact_uid" gorm:"type:varchar(36);not null;uniqueIndex"`
    Slug       string `json:"slug" gorm:"type:varchar(255);not null"`

    // Cached from file
    Name   string  `json:"name" gorm:"type:varchar(255);not null"`
    Phone  *string `json:"phone,omitempty" gorm:"type:varchar(64)"`
    Email  *string `json:"email,omitempty" gorm:"type:varchar(255)"`
    Notes  *string `json:"notes,omitempty" gorm:"type:text"`

    // File tracking
    NotePath    string  `json:"note_path" gorm:"type:text"`
    FileMtimeNs int64   `json:"file_mtime_ns"`  // UnixNano precision
    SyncError   *string `json:"sync_error,omitempty" gorm:"type:text"`

    // Voice signature
    VoiceSnippetPath       *string `json:"voice_snippet_path,omitempty" gorm:"type:text"`
    SignatureEmbeddingPath *string `json:"signature_embedding_path,omitempty" gorm:"type:text"`
    SignatureStatus        string  `json:"signature_status" gorm:"type:varchar(32);not null;default:'none'"`

    CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
    UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

    Vault Vault `json:"-" gorm:"foreignKey:VaultID;constraint:OnDelete:CASCADE"`
}
```

### Step 1.2: Create Vault-Scoped Contact Repository
**File:** `internal/repository/contact_repository.go`

```go
type ContactRepository interface {
    Create(ctx context.Context, contact *models.Contact) error
    Update(ctx context.Context, contact *models.Contact) error
    Delete(ctx context.Context, id uint) error
    GetByID(ctx context.Context, vaultID, id uint) (*models.Contact, error)
    GetByUID(ctx context.Context, vaultID uint, uid string) (*models.Contact, error)
    ListByVault(ctx context.Context, vaultID uint) ([]models.Contact, error)
    Search(ctx context.Context, vaultID uint, query string) ([]models.Contact, error)
    ListBySignatureStatus(ctx context.Context, vaultID uint, status string) ([]models.Contact, error)
}
```

### Step 1.3: Startup Backfill
**File:** `internal/database/database.go` (in existing init flow)

```go
func backfillContacts(db *gorm.DB) error {
    var vault models.Vault
    if err := db.Where("is_active = ?", true).First(&vault).Error; err != nil {
        return nil // No active vault, skip
    }

    var contacts []models.Contact
    db.Where("contact_uid = '' OR contact_uid IS NULL").Find(&contacts)

    for _, c := range contacts {
        c.ContactUID = uuid.New().String()
        c.VaultID = vault.ID
        c.Slug = generateSlug(c.Name)
        c.NotePath = fmt.Sprintf("Contacts/People/%s--%s/contact.md", c.Slug, c.ContactUID)
        db.Save(&c)
    }
    return nil
}
```

**Deliverables:**
- [x] Contact model with new fields
- [x] Vault-scoped repository
- [x] Startup backfill (idempotent)

---

## Phase 5B.2: File Contract + CRUD Rewrite

### Step 2.1: Contact File Service
**File:** `internal/contacts/file_service.go`

```go
type ContactFileService struct {
    vaultPath string
}

// Folder format: Contacts/People/<slug>--<uid>/contact.md
func (s *ContactFileService) GetContactFolder(contact *models.Contact) string
func (s *ContactFileService) WriteContact(contact *models.Contact) error
func (s *ContactFileService) ReadContact(folderPath string) (*models.Contact, error)
func (s *ContactFileService) DeleteContactFolder(contact *models.Contact) error
func (s *ContactFileService) ScanAllContacts() ([]ContactFileInfo, error)
func (s *ContactFileService) GetFileMtimeNs(path string) (int64, error)

// Helpers
func generateSlug(name string) string
func parseContactUID(folderName string) (slug, uid string)
```

### Step 2.2: Contact Markdown Format
**File:** `<vault>/Contacts/People/<slug>--<uid>/contact.md`

```markdown
---
format: contact-note-v1
contact_uid: "c1a2b3c4-5678-90ab-cdef-1234567890ab"
name: "John Doe"
phone: "+1-555-123-4567"
email: "john@example.com"
signature_status: "ready"
voice_snippet: "voice-snippet.wav"
voice_signature: "voice-signature.embedding.json"
updated_at: "2026-03-10T12:00:00Z"
---

Notes and context about this contact...
```

### Step 2.3: Rewrite CRUD Handlers
**File:** `internal/api/contact_handlers.go`

```go
// All scoped to active vault
POST   /api/v1/contacts        → CreateContact  // Write DB + file atomically
GET    /api/v1/contacts        → ListContacts   // Read from DB cache
GET    /api/v1/contacts/:id    → GetContact
PUT    /api/v1/contacts/:id    → UpdateContact  // Write DB + file atomically
DELETE /api/v1/contacts/:id    → DeleteContact  // Hard-delete: remove DB row + folder
```

**Hard-delete behavior:**
```go
func (h *Handler) DeleteContact(c *gin.Context) {
    // 1. Get contact from DB (vault-scoped)
    // 2. Delete contact folder (including snippets/embeddings)
    // 3. Delete DB row
    // 4. Return 204
}
```

**Deliverables:**
- [x] Contact file service with folder-per-contact format
- [x] `contact.md` as canonical source
- [x] CRUD handlers write DB + file atomically
- [x] Hard-delete removes folder and DB row

---

## Phase 5B.3: Bidirectional Sync + Watcher

### Step 3.1: Contact Sync Service
**File:** `internal/contacts/sync_service.go`

```go
type ContactSyncService struct {
    fileService   *ContactFileService
    repo          ContactRepository
    vaultID       uint
    vaultPath     string
    selfWrites    map[string]int64  // path → mtime_ns for suppression
    mu            sync.RWMutex
}

func (s *ContactSyncService) FullReindex() error
func (s *ContactSyncService) SyncFolder(folderPath string) error
func (s *ContactSyncService) MarkSelfWrite(path string, mtimeNs int64)
func (s *ContactSyncService) IsSelfWrite(path string, mtimeNs int64) bool
```

**Full reindex algorithm:**
```go
func (s *ContactSyncService) FullReindex() error {
    folders := s.fileService.ScanAllContacts()
    seenUIDs := map[string]bool{}

    for _, f := range folders {
        seenUIDs[f.ContactUID] = true
        existing, _ := s.repo.GetByUID(ctx, s.vaultID, f.ContactUID)

        if existing == nil {
            // Import new contact
            contact := s.fileService.ReadContact(f.Path)
            contact.VaultID = s.vaultID
            contact.FileMtimeNs = f.MtimeNs
            s.repo.Create(ctx, contact)
        } else if f.MtimeNs > existing.FileMtimeNs && !s.IsSelfWrite(f.Path, f.MtimeNs) {
            // External edit → update DB
            contact := s.fileService.ReadContact(f.Path)
            contact.ID = existing.ID
            contact.FileMtimeNs = f.MtimeNs
            s.repo.Update(ctx, contact)
        }
    }

    // Hard-delete orphaned DB rows
    dbContacts := s.repo.ListByVault(ctx, s.vaultID)
    for _, c := range dbContacts {
        if !seenUIDs[c.ContactUID] {
            s.repo.Delete(ctx, c.ID)
        }
    }
    return nil
}
```

### Step 3.2: Fsnotify Watcher
**File:** `internal/contacts/watcher.go`

```go
type ContactWatcher struct {
    watcher     *fsnotify.Watcher
    syncService *ContactSyncService
    debounceMs  int  // 500ms default
    stopCh      chan struct{}
}

func (w *ContactWatcher) Start(vaultPath string) error
func (w *ContactWatcher) Stop() error
```

### Step 3.3: Wire Watcher Lifecycle
**File:** `internal/api/vault_setup_handlers.go`

Wire watcher into:
- `CompleteSetup` (start watcher for new/linked vault)
- `CreateVault` (start watcher if activated)
- `ActivateVault` (stop old watcher, start new, reindex)
- `RehydrateVault` (reindex)
- Server startup (start watcher for active vault)

### Step 3.4: Manual Reindex Endpoint
```go
POST /api/v1/contacts/reindex → TriggerFullReindex
```

**Deliverables:**
- [x] Sync service with full reindex and single-folder sync
- [x] Fsnotify watcher with debounce + self-write suppression
- [x] Watcher lifecycle wired into vault operations
- [x] Manual reindex endpoint

---

## Phase 5B.4: Voice Snippet + TitaNet Embedding Pipeline

### Step 4.1: Save Snippets in Contact Folder
**File:** `internal/contacts/voice_service.go`

```go
func (s *VoiceService) SaveSnippet(contact *models.Contact, data io.Reader, ext string) (string, error)
// Saves to: Contacts/People/<slug>--<uid>/voice-snippet.<ext>

func (s *VoiceService) DeleteSnippet(contact *models.Contact) error
func (s *VoiceService) GetSnippetPath(contact *models.Contact) string
```

### Step 4.2: Dedicated Contacts Background Worker
**File:** `internal/contacts/worker.go`

```go
type ContactWorker struct {
    jobs     chan ContactJob
    repo     ContactRepository
    embedSvc *EmbeddingService
    workers  int  // Default: 1
}

type ContactJob struct {
    Type      string  // "extract_embedding"
    ContactID uint
    VaultID   uint
}

func (w *ContactWorker) Start()
func (w *ContactWorker) Stop()
func (w *ContactWorker) Enqueue(job ContactJob)
```

**Do not reuse transcription queue internals.**

### Step 4.3: TitaNet Embedding Extraction
**File:** `internal/contacts/embedding_service.go`

```go
type EmbeddingService struct {
    vaultPath string
}

func (s *EmbeddingService) ExtractEmbedding(contact *models.Contact) error
func (s *EmbeddingService) LoadEmbedding(contact *models.Contact) (*EmbeddingData, error)
func (s *EmbeddingService) DeleteEmbedding(contact *models.Contact) error
```

**Python script:** `internal/contacts/py/extract_titanet_embedding.py`

```python
# Uses NeMo TitaNet for speaker embedding
# Input: voice-snippet path
# Output: voice-signature.embedding.json

{
  "version": 1,
  "model": "nvidia/speakerverification_en_titanet_large",
  "dimension": 192,
  "vector": [0.123, -0.456, ...],
  "source_sha256": "abc123...",
  "created_at": "2026-03-10T12:00:00Z"
}
```

**No HF token required for TitaNet.**

### Step 4.4: Status Lifecycle
```
none → processing → ready | failed
```

- On snippet upload: set `signature_status = "processing"`, enqueue extraction
- On success: set `signature_status = "ready"`, update `signature_embedding_path`
- On failure: set `signature_status = "failed"`, store reason in `sync_error`

**Graceful failure:** If NeMo runtime unavailable, set `failed` with actionable error message.

**Deliverables:**
- [x] Snippet storage in contact folder
- [x] Dedicated contacts background worker
- [x] TitaNet embedding extraction script
- [x] Status lifecycle with failure handling

---

## Phase 5B.5: Hardening + Roadmap Alignment

### Step 5.1: Diagnostics Endpoint
```go
GET /api/v1/contacts/:id/files → GetContactFiles

{
  "note_path": "Contacts/People/john-doe--abc123/contact.md",
  "voice_snippet_path": "Contacts/People/john-doe--abc123/voice-snippet.wav",
  "signature_embedding_path": "Contacts/People/john-doe--abc123/voice-signature.embedding.json",
  "file_mtime_ns": 1710072000000000000,
  "sync_error": null,
  "signature_status": "ready"
}
```

### Step 5.2: Tests

| Category | Tests |
|----------|-------|
| Migration | Backfill correctness, idempotency |
| Roundtrip | DB→file, file→DB, external edits |
| Watcher | Debounce, self-write loop prevention |
| Vault isolation | List/get/update/delete/reindex scoped to active vault |
| Snippet/embedding | Upload, status transitions, failure handling |
| Hard-delete | Folder + DB row removed |

### Step 5.3: Roadmap Update
**File:** `plans/local-first-vault-roadmap.md`

```markdown
- Phase 5A (done): Contacts scaffold (CRUD + snippet + status scaffold)
- Phase 5B (in progress): Contacts File-First + Voice Signatures
- Phase 6 (deferred): Speaker Identity Matching
```

**Deliverables:**
- [x] Diagnostics endpoint
- [ ] Test coverage for all categories (partial coverage added for migration/sync/watcher/hard-delete)
- [x] Roadmap updated

---

## Key Files

| File | Operation | Description |
|------|-----------|-------------|
| `internal/models/vault.go` | Modify | Add vault_id, contact_uid, file_mtime_ns, etc. |
| `internal/repository/contact_repository.go` | Create | Vault-scoped queries |
| `internal/database/database.go` | Modify | Add startup backfill |
| `internal/contacts/file_service.go` | Create | Folder-per-contact read/write |
| `internal/contacts/sync_service.go` | Create | Bidirectional sync |
| `internal/contacts/watcher.go` | Create | Fsnotify with debounce |
| `internal/contacts/voice_service.go` | Create | Snippet storage |
| `internal/contacts/worker.go` | Create | Dedicated background worker |
| `internal/contacts/embedding_service.go` | Create | TitaNet extraction |
| `internal/contacts/py/extract_titanet_embedding.py` | Create | Python embedding script |
| `internal/api/contact_handlers.go` | Create | Vault-scoped CRUD + endpoints |
| `internal/api/vault_setup_handlers.go` | Modify | Wire watcher lifecycle |

---

## Assumptions

1. Filesystem is source of truth; DB is cache/index only
2. Contact deletion is immediate hard-delete
3. TitaNet is primary embedding backend for this phase
4. Speaker matching remains out of scope for this phase
5. `contact.md` is the only canonical metadata file (no `contact.json`)

---

## Test Plan

1. **Migration/backfill** — correctness and idempotency
2. **DB-to-file and file-to-DB roundtrip** — including external file edits
3. **Watcher** — debounce and self-write loop prevention
4. **Active-vault isolation** — list/get/update/delete/reindex
5. **Snippet upload and embedding** — status transitions `none → processing → ready|failed`
6. **Hard-delete** — removes folder and DB cache row
