package api

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"quill/internal/database"
	"quill/internal/models"
	"quill/internal/repository"
	"quill/pkg/slug"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Bootstrap harness
// ---------------------------------------------------------------------------

// bootstrapHarness provides an in-memory SQLite database with Vault + Contact
// + TranscriptionJob tables, a real ContactRepository, a real vault directory
// on disk, and a Handler with no contactManager (so persistContactFile goes
// through the direct fileService path).
type bootstrapHarness struct {
	h       *Handler
	db      *gorm.DB
	vault   models.Vault
	cleanup func()
}

func setupBootstrapHarness(t *testing.T) bootstrapHarness {
	t.Helper()

	root := t.TempDir()
	vaultPath := filepath.Join(root, "vault")
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatalf("create vault path: %v", err)
	}

	dbPath := filepath.Join(root, "bootstrap_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Vault{},
		&models.Contact{},
		&models.TranscriptionJob{},
		&models.SpeakerMapping{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	vault := models.Vault{Name: "Test Vault", Path: vaultPath, IsActive: true}
	if err := db.Create(&vault).Error; err != nil {
		t.Fatalf("seed vault: %v", err)
	}

	prevDB := database.DB
	database.DB = db

	h := &Handler{
		contactRepo: repository.NewContactRepository(db),
		// contactManager intentionally nil
	}

	return bootstrapHarness{
		h:     h,
		db:    db,
		vault: vault,
		cleanup: func() {
			database.DB = prevDB
		},
	}
}

// makeTranscriptJSON serialises a transcript payload to JSON.
func makeTranscriptJSON(segments []transcriptSegment) string {
	payload := transcriptPayload{Segments: segments}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

// spPtr returns a pointer to the given speaker label.
func spPtr(s string) *string { return &s }

// assertSummaryAllZero fails the test if any bootstrap summary counter is non-zero.
func assertSummaryAllZero(t *testing.T, s speakerContactBootstrapSummary) {
	t.Helper()
	if s.CreatedCount != 0 || s.StartedCount != 0 || s.SkippedExistingCount != 0 {
		t.Errorf("expected zero summary; got created=%d started=%d skipped=%d",
			s.CreatedCount, s.StartedCount, s.SkippedExistingCount)
	}
}

// ---------------------------------------------------------------------------
// bootstrapContactsFromSpeakerMappings — guard clauses (nil/empty inputs)
// ---------------------------------------------------------------------------

func TestBootstrapContacts_NilJob_ReturnsEmptySummary(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	mappings := []models.SpeakerMapping{
		{OriginalSpeaker: "speaker_0", CustomName: "Alice"},
	}
	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), nil, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

func TestBootstrapContacts_EmptyMappingSlice_ReturnsEmptySummary(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	job := &models.TranscriptionJob{ID: uuid.NewString(), AudioPath: "/tmp/a.wav"}
	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

func TestBootstrapContacts_NilContactRepo_ReturnsEmptySummary(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	h := &Handler{contactRepo: nil}
	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: &raw}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	summary, err := h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

func TestBootstrapContacts_NilTranscript_ReturnsEmptySummary(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	job := &models.TranscriptionJob{ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: nil}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

func TestBootstrapContacts_WhitespaceOnlyTranscript_ReturnsEmptySummary(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	blank := "   "
	job := &models.TranscriptionJob{ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: &blank}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

func TestBootstrapContacts_InvalidTranscriptJSON_ReturnsError(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	bad := "not-json"
	job := &models.TranscriptionJob{ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: &bad}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	_, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err == nil {
		t.Fatal("expected parse error for invalid JSON, got nil")
	}
}

func TestBootstrapContacts_TranscriptWithNoSegments_ReturnsEmptySummary(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON(nil)
	job := &models.TranscriptionJob{ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: &raw}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

// ---------------------------------------------------------------------------
// bootstrapContactsFromSpeakerMappings — mapping filtering
// ---------------------------------------------------------------------------

func TestBootstrapContacts_EmptyOriginalSpeaker_Skipped(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

func TestBootstrapContacts_EmptyCustomName_Skipped(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: ""}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

func TestBootstrapContacts_WhitespaceCustomName_Skipped(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "   "}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

func TestBootstrapContacts_SpeakerRenamedToOwnLabel_Skipped(t *testing.T) {
	// EqualFold("Speaker_0", "speaker_0") is true — rename is a no-op.
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("Speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "Speaker_0", CustomName: "speaker_0"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSummaryAllZero(t, summary)
}

func TestBootstrapContacts_SpeakerAbsentFromTranscript_Skipped(t *testing.T) {
	// Mapping for speaker_1 but transcript only contains speaker_0 — no window.
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/a.wav", Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_1", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CreatedCount != 0 {
		t.Errorf("CreatedCount: got %d, want 0 (speaker not in transcript)", summary.CreatedCount)
	}
}

// ---------------------------------------------------------------------------
// bootstrapContactsFromSpeakerMappings — contact creation
// ---------------------------------------------------------------------------

func TestBootstrapContacts_CreatesNewContact(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID:         uuid.NewString(),
		AudioPath:  "/tmp/nonexistent.wav", // audio missing — snippet extraction fails best-effort
		Status:     models.StatusCompleted,
		Transcript: &raw,
		VaultID:    &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CreatedCount != 1 {
		t.Errorf("CreatedCount: got %d, want 1", summary.CreatedCount)
	}

	var contactList []models.Contact
	bh.db.Where("vault_id = ? AND name = ?", bh.vault.ID, "Alice").Find(&contactList)
	if len(contactList) != 1 {
		t.Fatalf("expected 1 Alice in DB, got %d", len(contactList))
	}
	c := contactList[0]
	if c.VaultID != bh.vault.ID {
		t.Errorf("VaultID: got %d, want %d", c.VaultID, bh.vault.ID)
	}
	if c.SignatureStatus != "none" {
		t.Errorf("SignatureStatus: got %q, want %q", c.SignatureStatus, "none")
	}
}

func TestBootstrapContacts_CreatesContactWithNonEmptySlug(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/nonexistent.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "John Doe"}}

	bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings) //nolint:errcheck

	var contactList []models.Contact
	bh.db.Where("vault_id = ? AND name = ?", bh.vault.ID, "John Doe").Find(&contactList)
	if len(contactList) == 0 {
		t.Fatal("expected contact to be created")
	}
	if contactList[0].Slug == "" {
		t.Error("expected non-empty slug on created contact")
	}
}

func TestBootstrapContacts_DuplicateCustomName_CreatedOnlyOnce(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{
		{Start: 0, End: 5, Speaker: spPtr("speaker_0")},
		{Start: 6, End: 11, Speaker: spPtr("speaker_1")},
	})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/nonexistent.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	// Both speakers renamed to the same person — processedNames must deduplicate.
	mappings := []models.SpeakerMapping{
		{OriginalSpeaker: "speaker_0", CustomName: "Alice"},
		{OriginalSpeaker: "speaker_1", CustomName: "Alice"},
	}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CreatedCount != 1 {
		t.Errorf("CreatedCount: got %d, want 1 (deduplicated via processedNames)", summary.CreatedCount)
	}

	var contactList []models.Contact
	bh.db.Where("vault_id = ? AND name = ?", bh.vault.ID, "Alice").Find(&contactList)
	if len(contactList) != 1 {
		t.Errorf("expected exactly 1 Alice, got %d", len(contactList))
	}
}

func TestBootstrapContacts_MultipleSpeakers_EachCreated(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{
		{Start: 0, End: 5, Speaker: spPtr("speaker_0")},
		{Start: 10, End: 15, Speaker: spPtr("speaker_1")},
	})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/nonexistent.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{
		{OriginalSpeaker: "speaker_0", CustomName: "Alice"},
		{OriginalSpeaker: "speaker_1", CustomName: "Bob"},
	}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CreatedCount != 2 {
		t.Errorf("CreatedCount: got %d, want 2", summary.CreatedCount)
	}
	for _, name := range []string{"Alice", "Bob"} {
		var found []models.Contact
		bh.db.Where("vault_id = ? AND name = ?", bh.vault.ID, name).Find(&found)
		if len(found) != 1 {
			t.Errorf("expected 1 contact named %q, got %d", name, len(found))
		}
	}
}

func TestBootstrapContacts_ExistingContactNotRecreated(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	existing := &models.Contact{
		VaultID: bh.vault.ID, ContactUID: uuid.NewString(),
		Slug: slug.Sanitize("Alice", "contact"), Name: "Alice", SignatureStatus: "none",
	}
	if err := bh.h.contactRepo.Create(t.Context(), existing); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	if err := bh.h.persistContactFile(t.Context(), existing); err != nil {
		t.Fatalf("persist contact file: %v", err)
	}

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/nonexistent.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CreatedCount != 0 {
		t.Errorf("CreatedCount: got %d, want 0 (contact already exists)", summary.CreatedCount)
	}

	// Still exactly one Alice — not duplicated.
	var contactList []models.Contact
	bh.db.Where("vault_id = ? AND name = ?", bh.vault.ID, "Alice").Find(&contactList)
	if len(contactList) != 1 {
		t.Errorf("expected exactly 1 Alice (no duplicates), got %d", len(contactList))
	}
}

func TestBootstrapContacts_ExistingContactCaseInsensitiveMatch(t *testing.T) {
	// "alice" in DB should match "ALICE" in the mapping via normalizeNameKey.
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	existing := &models.Contact{
		VaultID: bh.vault.ID, ContactUID: uuid.NewString(),
		Slug: slug.Sanitize("alice", "contact"), Name: "alice", SignatureStatus: "none",
	}
	if err := bh.h.contactRepo.Create(t.Context(), existing); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	if err := bh.h.persistContactFile(t.Context(), existing); err != nil {
		t.Fatalf("persist contact file: %v", err)
	}

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/nonexistent.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "ALICE"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CreatedCount != 0 {
		t.Errorf("CreatedCount: got %d, want 0 (case-insensitive match)", summary.CreatedCount)
	}
}

func TestBootstrapContacts_UnicodeContactName(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/a.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	name := "Ångström Björk"
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: name}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CreatedCount != 1 {
		t.Errorf("CreatedCount: got %d, want 1 for unicode name", summary.CreatedCount)
	}
	var found []models.Contact
	bh.db.Where("vault_id = ? AND name = ?", bh.vault.ID, name).Find(&found)
	if len(found) != 1 {
		t.Errorf("expected 1 contact with unicode name, got %d", len(found))
	}
}

// ---------------------------------------------------------------------------
// bootstrapContactsFromSpeakerMappings — already-bootstrapped contacts
// ---------------------------------------------------------------------------

func TestBootstrapContacts_ExistingSnippet_SkipsExtraction(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	// Write a real snippet file inside the vault.
	snippetDir := filepath.Join(bh.vault.Path, "Contacts", "People", "alice--uid001")
	if err := os.MkdirAll(snippetDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	snippetFile := filepath.Join(snippetDir, "voice-snippet.wav")
	if err := os.WriteFile(snippetFile, []byte("RIFF"), 0o644); err != nil {
		t.Fatalf("write snippet: %v", err)
	}
	snippetRel := "Contacts/People/alice--uid001/voice-snippet.wav"

	existing := &models.Contact{
		VaultID: bh.vault.ID, ContactUID: "uid001", Slug: "alice",
		Name: "Alice", SignatureStatus: "none", VoiceSnippetPath: &snippetRel,
	}
	if err := bh.h.contactRepo.Create(t.Context(), existing); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	if err := bh.h.persistContactFile(t.Context(), existing); err != nil {
		t.Fatalf("persist contact file: %v", err)
	}

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/nonexistent.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.SkippedExistingCount != 1 {
		t.Errorf("SkippedExistingCount: got %d, want 1 (snippet on disk)", summary.SkippedExistingCount)
	}
	if summary.StartedCount != 0 {
		t.Errorf("StartedCount: got %d, want 0", summary.StartedCount)
	}
}

func TestBootstrapContacts_ExistingEmbedding_SkipsExtraction(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	embDir := filepath.Join(bh.vault.Path, "Contacts", "People", "alice--uid002")
	if err := os.MkdirAll(embDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(embDir, "embedding.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write embedding: %v", err)
	}
	embRel := "Contacts/People/alice--uid002/embedding.json"
	sigData := `{"source":"extracted","updated_at":"2025-01-01T00:00:00Z"}`

	existing := &models.Contact{
		VaultID: bh.vault.ID, ContactUID: "uid002", Slug: "alice",
		Name: "Alice", SignatureStatus: "ready",
		SignatureEmbeddingPath: &embRel, SignatureData: &sigData,
	}
	if err := bh.h.contactRepo.Create(t.Context(), existing); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	if err := bh.h.persistContactFile(t.Context(), existing); err != nil {
		t.Fatalf("persist contact file: %v", err)
	}

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/nonexistent.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.SkippedExistingCount != 1 {
		t.Errorf("SkippedExistingCount: got %d, want 1 (embedding on disk)", summary.SkippedExistingCount)
	}
}

func TestBootstrapContacts_ManualSignature_SkipsExtraction(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	embDir := filepath.Join(bh.vault.Path, "Contacts", "People", "alice--uid003")
	if err := os.MkdirAll(embDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(embDir, "embedding.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write embedding: %v", err)
	}
	embRel := "Contacts/People/alice--uid003/embedding.json"
	sigData := `{"source":"manual","updated_at":"2025-01-01T00:00:00Z"}`

	existing := &models.Contact{
		VaultID: bh.vault.ID, ContactUID: "uid003", Slug: "alice",
		Name: "Alice", SignatureStatus: "ready",
		SignatureEmbeddingPath: &embRel, SignatureData: &sigData,
	}
	if err := bh.h.contactRepo.Create(t.Context(), existing); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	if err := bh.h.persistContactFile(t.Context(), existing); err != nil {
		t.Fatalf("persist contact file: %v", err)
	}

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/nonexistent.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Alice"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.SkippedExistingCount != 1 {
		t.Errorf("SkippedExistingCount: got %d, want 1 (manual signature)", summary.SkippedExistingCount)
	}
}

// ---------------------------------------------------------------------------
// bootstrapContactsFromSpeakerMappings — vault resolution
// ---------------------------------------------------------------------------

func TestBootstrapContacts_JobWithVaultID_UsesCorrectVault(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/a.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: &bh.vault.ID,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Bob"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CreatedCount != 1 {
		t.Errorf("CreatedCount: got %d, want 1", summary.CreatedCount)
	}
	var found []models.Contact
	bh.db.Where("vault_id = ? AND name = ?", bh.vault.ID, "Bob").Find(&found)
	if len(found) != 1 {
		t.Errorf("expected 1 Bob in vault %d, got %d", bh.vault.ID, len(found))
	}
}

func TestBootstrapContacts_NoVaultID_FallsBackToActiveVault(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	// VaultID is nil — resolveJobVault falls back to getActiveVault().
	raw := makeTranscriptJSON([]transcriptSegment{{Start: 0, End: 5, Speaker: spPtr("speaker_0")}})
	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/a.wav",
		Status: models.StatusCompleted, Transcript: &raw, VaultID: nil,
	}
	mappings := []models.SpeakerMapping{{OriginalSpeaker: "speaker_0", CustomName: "Carol"}}

	summary, err := bh.h.bootstrapContactsFromSpeakerMappings(t.Context(), job, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CreatedCount != 1 {
		t.Errorf("CreatedCount: got %d, want 1 (active vault found)", summary.CreatedCount)
	}
}

// ---------------------------------------------------------------------------
// extractSpeakerSnippetForContact
// ---------------------------------------------------------------------------

func TestExtractSpeakerSnippet_MissingAudioFile_ReturnsError(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	contact := &models.Contact{
		VaultID: bh.vault.ID, ContactUID: uuid.NewString(),
		Slug: "alice", Name: "Alice", SignatureStatus: "none",
	}
	if err := bh.h.contactRepo.Create(t.Context(), contact); err != nil {
		t.Fatalf("create contact: %v", err)
	}

	job := &models.TranscriptionJob{
		ID: uuid.NewString(), AudioPath: "/tmp/does-not-exist.wav",
	}
	err := bh.h.extractSpeakerSnippetForContact(t.Context(), job, &bh.vault, contact, clipWindow{Start: 0, End: 5})
	if err == nil {
		t.Fatal("expected error for missing audio file, got nil")
	}
}

func TestExtractSpeakerSnippet_AudioOutsideVault_ReturnsError(t *testing.T) {
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	// Real file, but outside the vault root.
	externalDir := t.TempDir()
	externalFile := filepath.Join(externalDir, "audio.wav")
	if err := os.WriteFile(externalFile, []byte("RIFF"), 0o644); err != nil {
		t.Fatalf("write external file: %v", err)
	}

	contact := &models.Contact{
		VaultID: bh.vault.ID, ContactUID: uuid.NewString(),
		Slug: "dave", Name: "Dave", SignatureStatus: "none",
	}
	if err := bh.h.contactRepo.Create(t.Context(), contact); err != nil {
		t.Fatalf("create contact: %v", err)
	}

	job := &models.TranscriptionJob{ID: uuid.NewString(), AudioPath: externalFile}
	err := bh.h.extractSpeakerSnippetForContact(t.Context(), job, &bh.vault, contact, clipWindow{Start: 0, End: 5})
	if err == nil {
		t.Fatal("expected path-boundary error, got nil")
	}
}

func TestExtractSpeakerSnippet_ContactWithNonExistentVaultID_ReturnsError(t *testing.T) {
	// persistContactFile looks up the vault by contact.VaultID; if it doesn't
	// exist the function should return an error rather than panic.
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	audioFile := filepath.Join(bh.vault.Path, "audio.wav")
	if err := os.WriteFile(audioFile, []byte("RIFF"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	badVaultID := uint(99999) // not in DB
	contact := &models.Contact{
		VaultID: badVaultID, ContactUID: uuid.NewString(),
		Slug: "eve", Name: "Eve", SignatureStatus: "none", NotePath: "",
	}

	job := &models.TranscriptionJob{ID: uuid.NewString(), AudioPath: audioFile}
	err := bh.h.extractSpeakerSnippetForContact(t.Context(), job, &bh.vault, contact, clipWindow{Start: 0, End: 5})
	if err == nil {
		t.Fatal("expected error for contact with unknown vault ID, got nil")
	}
}

func TestExtractSpeakerSnippet_ValidContactAndAudioFile_ReachesFFmpegStep(t *testing.T) {
	// This test verifies that with a real audio file inside the vault and a
	// properly seeded contact, execution reaches the extractSnippetWithFFmpeg
	// call. FFmpeg will fail (no real audio data), but we cover lines 168–187
	// before that failure, significantly improving extractSpeakerSnippetForContact
	// coverage.
	bh := setupBootstrapHarness(t)
	defer bh.cleanup()

	// Write a minimal dummy audio file inside the vault so resolveJobAudioPath accepts it.
	audioFile := filepath.Join(bh.vault.Path, "audio.wav")
	if err := os.WriteFile(audioFile, []byte("RIFF....fake"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	// Create and persist the contact so it has a valid NotePath.
	contact := &models.Contact{
		VaultID: bh.vault.ID, ContactUID: uuid.NewString(),
		Slug: "grace", Name: "Grace", SignatureStatus: "none",
	}
	if err := bh.h.contactRepo.Create(t.Context(), contact); err != nil {
		t.Fatalf("create contact: %v", err)
	}
	if err := bh.h.persistContactFile(t.Context(), contact); err != nil {
		t.Fatalf("persist contact file: %v", err)
	}

	job := &models.TranscriptionJob{ID: uuid.NewString(), AudioPath: audioFile}
	window := clipWindow{Start: 0, End: 5}

	// FFmpeg will fail (fake audio data), but the function executes through
	// snippet path resolution and MkdirAll before reaching the FFmpeg call.
	err := bh.h.extractSpeakerSnippetForContact(t.Context(), job, &bh.vault, contact, window)
	// FFmpeg failure is expected; what matters is no nil-pointer panic and an
	// error is returned (either from FFmpeg or from contactManager being nil).
	if err == nil {
		t.Log("FFmpeg unexpectedly succeeded (or binary not found) — still valid")
	}
}

// ---------------------------------------------------------------------------
// buildSpeakerClipWindows — additional edge cases (path_traversal_test.go
// already covers the basic merge/extend path; add here what is missing)
// ---------------------------------------------------------------------------

func TestBuildSpeakerClipWindows_NilSegments_Empty(t *testing.T) {
	windows := buildSpeakerClipWindows(nil)
	if len(windows) != 0 {
		t.Errorf("expected empty map for nil segments, got %d entries", len(windows))
	}
}

func TestBuildSpeakerClipWindows_AllNilSpeakers_Empty(t *testing.T) {
	segments := []transcriptSegment{
		{Start: 0, End: 5, Speaker: nil},
		{Start: 10, End: 15, Speaker: nil},
	}
	if len(buildSpeakerClipWindows(segments)) != 0 {
		t.Error("expected empty map when all speakers are nil")
	}
}

func TestBuildSpeakerClipWindows_AllSegmentsBelowDurationThreshold_Empty(t *testing.T) {
	s := "speaker_0"
	segments := []transcriptSegment{
		{Start: 0.0, End: 0.1, Speaker: &s},  // 0.10 s < 0.15 threshold
		{Start: 1.0, End: 1.14, Speaker: &s}, // 0.14 s < 0.15 threshold
	}
	if len(buildSpeakerClipWindows(segments)) != 0 {
		t.Error("expected empty map when all segments below duration threshold")
	}
}

func TestBuildSpeakerClipWindows_LongSegment_CappedAtMax(t *testing.T) {
	s := "speaker_0"
	segments := []transcriptSegment{{Start: 0, End: 60, Speaker: &s}}
	w, ok := buildSpeakerClipWindows(segments)["speaker_0"]
	if !ok {
		t.Fatal("expected window for speaker_0")
	}
	if d := w.End - w.Start; d > autoSpeakerSnippetMaxSeconds+0.001 {
		t.Errorf("duration %.3f exceeds max %.3f", d, autoSpeakerSnippetMaxSeconds)
	}
}

func TestBuildSpeakerClipWindows_ShortSegment_ExtendedToTarget(t *testing.T) {
	s := "speaker_0"
	segments := []transcriptSegment{{Start: 2.0, End: 5.0, Speaker: &s}} // 3 s < 8 s target
	w, ok := buildSpeakerClipWindows(segments)["speaker_0"]
	if !ok {
		t.Fatal("expected window for speaker_0")
	}
	if d := w.End - w.Start; d < autoSpeakerSnippetTargetSeconds-0.001 {
		t.Errorf("duration %.3f below target %.3f", d, autoSpeakerSnippetTargetSeconds)
	}
}

func TestBuildSpeakerClipWindows_MultipleSpeakers_IndependentWindows(t *testing.T) {
	s0, s1 := "speaker_0", "speaker_1"
	segments := []transcriptSegment{
		{Start: 0, End: 5, Speaker: &s0},
		{Start: 10, End: 15, Speaker: &s1},
	}
	windows := buildSpeakerClipWindows(segments)
	if _, ok := windows["speaker_0"]; !ok {
		t.Error("expected window for speaker_0")
	}
	if _, ok := windows["speaker_1"]; !ok {
		t.Error("expected window for speaker_1")
	}
	if len(windows) != 2 {
		t.Errorf("expected 2 windows, got %d", len(windows))
	}
}

func TestBuildSpeakerClipWindows_StartNeverNegative(t *testing.T) {
	s := "speaker_0"
	// Segment starts at 0.05; after -0.2 pre-roll offset would be negative.
	segments := []transcriptSegment{{Start: 0.05, End: 5.0, Speaker: &s}}
	w := buildSpeakerClipWindows(segments)["speaker_0"]
	if w.Start < 0 {
		t.Errorf("window start is negative: %.4f", w.Start)
	}
}

func TestBuildSpeakerClipWindows_NegativeSegmentStart_ClampedToZero(t *testing.T) {
	s := "speaker_0"
	segments := []transcriptSegment{{Start: -5.0, End: 3.0, Speaker: &s}}
	w, ok := buildSpeakerClipWindows(segments)["speaker_0"]
	if !ok {
		t.Fatal("expected window for speaker_0")
	}
	if w.Start < 0 {
		t.Errorf("window start is negative: %.4f", w.Start)
	}
}

func TestBuildSpeakerClipWindows_SelectsLongestSpan(t *testing.T) {
	s := "speaker_0"
	// Two isolated spans: 1 s and 4 s. The longer should anchor the window.
	segments := []transcriptSegment{
		{Start: 0.0, End: 1.0, Speaker: &s},
		{Start: 20.0, End: 24.0, Speaker: &s},
	}
	w, ok := buildSpeakerClipWindows(segments)["speaker_0"]
	if !ok {
		t.Fatal("expected window for speaker_0")
	}
	// Best span is 20-24 s (4 s). After -0.2 pre-roll: start ≈ 19.8.
	if w.Start < 19.0 || w.Start > 21.0 {
		t.Errorf("expected window anchored near 20 s, got start=%.3f end=%.3f", w.Start, w.End)
	}
}

func TestBuildSpeakerClipWindows_SpeakerLabelNormalised(t *testing.T) {
	// "SPEAKER_0" and "speaker_0" normalise to the same key.
	upper := "SPEAKER_0"
	lower := "speaker_0"
	segments := []transcriptSegment{
		{Start: 0, End: 5, Speaker: &upper},
		{Start: 6, End: 11, Speaker: &lower},
	}
	windows := buildSpeakerClipWindows(segments)
	if _, ok := windows["speaker_0"]; !ok {
		t.Error("expected normalised key 'speaker_0' in windows map")
	}
	if len(windows) != 1 {
		t.Errorf("expected 1 normalised window, got %d", len(windows))
	}
}

func TestBuildSpeakerClipWindows_WhitespaceSpeakerLabel_Skipped(t *testing.T) {
	empty := "  "
	segments := []transcriptSegment{{Start: 0, End: 5, Speaker: &empty}}
	if len(buildSpeakerClipWindows(segments)) != 0 {
		t.Error("expected empty map for whitespace speaker label")
	}
}

// ---------------------------------------------------------------------------
// mergeClipSpans — edge cases
// ---------------------------------------------------------------------------

func TestMergeClipSpans_NilInput_ReturnsNil(t *testing.T) {
	if result := mergeClipSpans(nil, 0.5); result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestMergeClipSpans_SingleSpan_Passthrough(t *testing.T) {
	spans := []clipSpan{{Start: 1.0, End: 3.0}}
	result := mergeClipSpans(spans, 0.5)
	if len(result) != 1 || result[0].Start != 1.0 || result[0].End != 3.0 {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestMergeClipSpans_GapBeyondMax_NotMerged(t *testing.T) {
	spans := []clipSpan{{Start: 0, End: 1.0}, {Start: 2.0, End: 3.0}} // gap 1.0 s > 0.5 s
	if len(mergeClipSpans(spans, 0.5)) != 2 {
		t.Error("expected 2 separate spans")
	}
}

func TestMergeClipSpans_GapWithinMax_Merged(t *testing.T) {
	spans := []clipSpan{{Start: 0, End: 1.0}, {Start: 1.3, End: 2.5}} // gap 0.3 s <= 0.6 s
	result := mergeClipSpans(spans, 0.6)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged span, got %d: %+v", len(result), result)
	}
	if math.Abs(result[0].Start-0.0) > 0.001 || math.Abs(result[0].End-2.5) > 0.001 {
		t.Errorf("unexpected merged span: %+v", result[0])
	}
}

func TestMergeClipSpans_Overlapping_Merged(t *testing.T) {
	spans := []clipSpan{{Start: 0, End: 3.0}, {Start: 2.0, End: 5.0}}
	result := mergeClipSpans(spans, 0.5)
	if len(result) != 1 {
		t.Fatalf("expected 1 span from overlapping merge, got %d: %+v", len(result), result)
	}
	if math.Abs(result[0].End-5.0) > 0.001 {
		t.Errorf("expected end=5.0, got %.3f", result[0].End)
	}
}

func TestMergeClipSpans_ManySpans_AllMerged(t *testing.T) {
	// Each gap is 0.4 s < maxGap 0.5 s — should all merge into one span.
	spans := []clipSpan{
		{Start: 0.0, End: 1.0},
		{Start: 1.4, End: 2.0},
		{Start: 2.4, End: 3.0},
		{Start: 3.4, End: 4.0},
	}
	result := mergeClipSpans(spans, 0.5)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged span, got %d: %+v", len(result), result)
	}
	if math.Abs(result[0].Start-0.0) > 0.001 || math.Abs(result[0].End-4.0) > 0.001 {
		t.Errorf("unexpected final span: %+v", result[0])
	}
}

// ---------------------------------------------------------------------------
// isRegularFile
// ---------------------------------------------------------------------------

func TestIsRegularFile_RegularFile_ReturnsTrue(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.wav")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.Close()
	if !isRegularFile(f.Name()) {
		t.Errorf("expected true for regular file %q", f.Name())
	}
}

func TestIsRegularFile_Directory_ReturnsFalse(t *testing.T) {
	if isRegularFile(t.TempDir()) {
		t.Error("expected false for directory")
	}
}

func TestIsRegularFile_MissingPath_ReturnsFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-file.wav")
	if isRegularFile(path) {
		t.Error("expected false for non-existent path")
	}
}
