package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"quill/internal/database"
	"quill/internal/models"
	"quill/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupSpeakerDebugHarness creates an in-memory test DB with Contact and
// SpeakerMapping models migrated, returning a Handler with the relevant repos.
func setupSpeakerDebugHarness(t *testing.T) (*Handler, *gorm.DB, func()) {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "speaker_debug_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Vault{},
		&models.Contact{},
		&models.TranscriptionJob{},
		&models.SpeakerMapping{},
		&models.Summary{},
		&models.SummaryTemplate{},
		&models.SummarySetting{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	prevDB := database.DB
	database.DB = db

	h := &Handler{
		jobRepo:            repository.NewJobRepository(db),
		speakerMappingRepo: repository.NewSpeakerMappingRepository(db),
		contactRepo:        repository.NewContactRepository(db),
		summaryRepo:        repository.NewSummaryRepository(db),
	}

	cleanup := func() {
		database.DB = prevDB
	}
	return h, db, cleanup
}

func buildSpeakerDebugRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/transcription/speakers/debug", h.SpeakerIdentificationDebug)
	return r
}

// TestSpeakerDebug_ReturnsContactSignatureBreakdown verifies that the debug
// endpoint returns contact counts grouped by signature status.
func TestSpeakerDebug_ReturnsContactSignatureBreakdown(t *testing.T) {
	h, db, cleanup := setupSpeakerDebugHarness(t)
	defer cleanup()

	vault := models.Vault{Name: "test", Path: "/tmp/test-vault", IsActive: true}
	if err := db.Create(&vault).Error; err != nil {
		t.Fatalf("seed vault: %v", err)
	}

	contacts := []models.Contact{
		{VaultID: vault.ID, ContactUID: "uid-1", Slug: "alice", Name: "Alice",
			NotePath: "/tmp/alice", SignatureStatus: "ready"},
		{VaultID: vault.ID, ContactUID: "uid-2", Slug: "bob", Name: "Bob",
			NotePath: "/tmp/bob", SignatureStatus: "ready"},
		{VaultID: vault.ID, ContactUID: "uid-3", Slug: "carol", Name: "Carol",
			NotePath: "/tmp/carol", SignatureStatus: "processing"},
		{VaultID: vault.ID, ContactUID: "uid-4", Slug: "dan", Name: "Dan",
			NotePath: "/tmp/dan", SignatureStatus: "failed",
			SignatureData: strPtr(`{"retry_count":2,"last_error":"TitaNet timeout"}`)},
		{VaultID: vault.ID, ContactUID: "uid-5", Slug: "eve", Name: "Eve",
			NotePath: "/tmp/eve", SignatureStatus: "none"},
	}
	for i := range contacts {
		if err := db.Create(&contacts[i]).Error; err != nil {
			t.Fatalf("seed contact: %v", err)
		}
	}

	r := buildSpeakerDebugRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/transcription/speakers/debug", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp speakerDebugResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Verify contact signature counts
	if resp.Contacts.Ready != 2 {
		t.Errorf("contacts.ready: got %d, want 2", resp.Contacts.Ready)
	}
	if resp.Contacts.Processing != 1 {
		t.Errorf("contacts.processing: got %d, want 1", resp.Contacts.Processing)
	}
	if resp.Contacts.Failed != 1 {
		t.Errorf("contacts.failed: got %d, want 1", resp.Contacts.Failed)
	}
	if resp.Contacts.None != 1 {
		t.Errorf("contacts.none: got %d, want 1", resp.Contacts.None)
	}
	if resp.Contacts.Total != 5 {
		t.Errorf("contacts.total: got %d, want 5", resp.Contacts.Total)
	}

	// Verify failed contacts detail
	if len(resp.Contacts.FailedDetails) != 1 {
		t.Fatalf("expected 1 failed detail, got %d", len(resp.Contacts.FailedDetails))
	}
	fd := resp.Contacts.FailedDetails[0]
	if fd.Name != "Dan" {
		t.Errorf("failed detail name: got %q, want Dan", fd.Name)
	}
	if fd.RetryCount != 2 {
		t.Errorf("failed detail retry_count: got %d, want 2", fd.RetryCount)
	}
	if fd.LastError != "TitaNet timeout" {
		t.Errorf("failed detail last_error: got %q, want TitaNet timeout", fd.LastError)
	}
}

// TestSpeakerDebug_ReturnsMappingStats verifies that the debug endpoint
// returns aggregate speaker mapping statistics.
func TestSpeakerDebug_ReturnsMappingStats(t *testing.T) {
	h, db, cleanup := setupSpeakerDebugHarness(t)
	defer cleanup()

	vault := models.Vault{Name: "test", Path: "/tmp/test-vault2", IsActive: true}
	if err := db.Create(&vault).Error; err != nil {
		t.Fatalf("seed vault: %v", err)
	}

	// Seed some jobs
	jobs := []models.TranscriptionJob{
		{ID: "dbg-job-1", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted},
		{ID: "dbg-job-2", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted},
	}
	for _, j := range jobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job: %v", err)
		}
	}

	mappings := []models.SpeakerMapping{
		{TranscriptionJobID: "dbg-job-1", OriginalSpeaker: "speaker_00", CustomName: "Alice",
			MatchSource: "auto", MatchTier: "auto", ReviewStatus: "", ConfidenceScore: 0.92},
		{TranscriptionJobID: "dbg-job-1", OriginalSpeaker: "speaker_01", CustomName: "Bob",
			MatchSource: "auto", MatchTier: "suggest", ReviewStatus: "pending", ConfidenceScore: 0.71},
		{TranscriptionJobID: "dbg-job-1", OriginalSpeaker: "speaker_02", CustomName: "Carol",
			MatchSource: "suggestion_promoted", MatchTier: "suggest", ReviewStatus: "accepted", ConfidenceScore: 0.68},
		{TranscriptionJobID: "dbg-job-2", OriginalSpeaker: "speaker_00", CustomName: "Dan",
			MatchSource: "manual", MatchTier: "", ReviewStatus: "", ConfidenceScore: 0},
		{TranscriptionJobID: "dbg-job-2", OriginalSpeaker: "speaker_01", CustomName: "Eve",
			MatchSource: "retroactive", MatchTier: "auto", ReviewStatus: "", ConfidenceScore: 0.88},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	r := buildSpeakerDebugRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/transcription/speakers/debug", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp speakerDebugResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Mapping stats
	if resp.Mappings.Total != 5 {
		t.Errorf("mappings.total: got %d, want 5", resp.Mappings.Total)
	}
	if resp.Mappings.BySource["auto"] != 2 {
		t.Errorf("mappings.by_source[auto]: got %d, want 2", resp.Mappings.BySource["auto"])
	}
	if resp.Mappings.BySource["manual"] != 1 {
		t.Errorf("mappings.by_source[manual]: got %d, want 1", resp.Mappings.BySource["manual"])
	}
	if resp.Mappings.BySource["suggestion_promoted"] != 1 {
		t.Errorf("mappings.by_source[suggestion_promoted]: got %d, want 1", resp.Mappings.BySource["suggestion_promoted"])
	}
	if resp.Mappings.BySource["retroactive"] != 1 {
		t.Errorf("mappings.by_source[retroactive]: got %d, want 1", resp.Mappings.BySource["retroactive"])
	}
	if resp.Mappings.PendingReview != 1 {
		t.Errorf("mappings.pending_review: got %d, want 1", resp.Mappings.PendingReview)
	}
	if resp.Mappings.JobsWithMappings != 2 {
		t.Errorf("mappings.jobs_with_mappings: got %d, want 2", resp.Mappings.JobsWithMappings)
	}
}

