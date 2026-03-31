package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"quill/internal/database"
	"quill/internal/models"
	"quill/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// --- Test harness ---

func setupSpeakerMappingHarness(t *testing.T) (*Handler, *gorm.DB, func()) {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "speaker_mapping_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.SpeakerMapping{},
		&models.Summary{},
		&models.SummaryTemplate{},
		&models.SummarySetting{},
		&models.Vault{},
		&models.Contact{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	prevDB := database.DB
	database.DB = db

	h := &Handler{
		jobRepo:            repository.NewJobRepository(db),
		speakerMappingRepo: repository.NewSpeakerMappingRepository(db),
		summaryRepo:        repository.NewSummaryRepository(db),
		contactRepo:        repository.NewContactRepository(db),
	}

	cleanup := func() {
		database.DB = prevDB
	}
	return h, db, cleanup
}

// --- GetSpeakerMappings: confidence fields in response ---

func TestGetSpeakerMappings_ReturnsConfidenceFields(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	// Seed a job and mapping with confidence data.
	job := &models.TranscriptionJob{ID: "job-get-conf", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	mapping := &models.SpeakerMapping{
		TranscriptionJobID: "job-get-conf",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		ConfidenceScore:    0.92,
		MatchSource:        "auto",
		MatchTier:          "auto",
	}
	db.Create(mapping)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-get-conf"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/job-get-conf/speakers", nil)

	h.GetSpeakerMappings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var resp []SpeakerMappingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("expected 1 mapping in response, got %d", len(resp))
	}

	r := resp[0]
	if r.ConfidenceScore != 0.92 {
		t.Errorf("ConfidenceScore: got %f, want 0.92", r.ConfidenceScore)
	}
	if r.MatchSource != "auto" {
		t.Errorf("MatchSource: got %q, want %q", r.MatchSource, "auto")
	}
	if r.MatchTier != "auto" {
		t.Errorf("MatchTier: got %q, want %q", r.MatchTier, "auto")
	}
}

// --- UpdateSpeakerMappings: sets MatchSource="manual" ---

func TestUpdateSpeakerMappings_SetsManualSource(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{
		ID:        "job-update-manual",
		AudioPath: "/tmp/b.wav",
		Status:    models.StatusCompleted,
	}
	db.Create(job)

	reqBody := SpeakerMappingsUpdateRequest{
		Mappings: []SpeakerMappingRequest{
			{OriginalSpeaker: "speaker_00", CustomName: "Bob"},
			{OriginalSpeaker: "speaker_01", CustomName: "Carol"},
		},
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-update-manual"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-update-manual/speakers", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateSpeakerMappings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify DB rows have MatchSource="manual".
	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "job-update-manual").Find(&mappings)

	if len(mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(mappings))
	}

	for _, m := range mappings {
		if m.MatchSource != "manual" {
			t.Errorf("mapping %q: MatchSource got %q, want %q", m.OriginalSpeaker, m.MatchSource, "manual")
		}
		if m.ConfidenceScore != 0.0 {
			t.Errorf("mapping %q: ConfidenceScore got %f, want 0.0 for manual", m.OriginalSpeaker, m.ConfidenceScore)
		}
		if m.MatchTier != "" {
			t.Errorf("mapping %q: MatchTier got %q, want empty for manual", m.OriginalSpeaker, m.MatchTier)
		}
	}
}

// --- UpsertMapping repo method ---

func TestUpsertMapping_CreateNew(t *testing.T) {
	_, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	repo := repository.NewSpeakerMappingRepository(db)

	job := &models.TranscriptionJob{ID: "job-upsert-new", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	mapping := models.SpeakerMapping{
		TranscriptionJobID: "job-upsert-new",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		ConfidenceScore:    0.75,
		MatchSource:        "suggestion_promoted",
		MatchTier:          "suggest",
	}

	result, err := repo.UpsertMapping(t.Context(), "job-upsert-new", mapping)
	if err != nil {
		t.Fatalf("UpsertMapping: %v", err)
	}
	if result.ID == 0 {
		t.Error("expected non-zero ID for created mapping")
	}
	if result.CustomName != "Alice" {
		t.Errorf("CustomName: got %q, want %q", result.CustomName, "Alice")
	}
	if result.ConfidenceScore != 0.75 {
		t.Errorf("ConfidenceScore: got %f, want 0.75", result.ConfidenceScore)
	}
	if result.MatchSource != "suggestion_promoted" {
		t.Errorf("MatchSource: got %q, want %q", result.MatchSource, "suggestion_promoted")
	}

	// Verify exactly one row exists.
	mappings, _ := repo.ListByJob(t.Context(), "job-upsert-new")
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
}

func TestUpsertMapping_UpdateExisting(t *testing.T) {
	_, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	repo := repository.NewSpeakerMappingRepository(db)

	job := &models.TranscriptionJob{ID: "job-upsert-upd", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	// Pre-create an auto-assigned mapping.
	existing := &models.SpeakerMapping{
		TranscriptionJobID: "job-upsert-upd",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "AutoAlice",
		ConfidenceScore:    0.85,
		MatchSource:        "auto",
		MatchTier:          "auto",
	}
	db.Create(existing)

	// Upsert with new values.
	updated := models.SpeakerMapping{
		TranscriptionJobID: "job-upsert-upd",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "PromotedAlice",
		ConfidenceScore:    0.72,
		MatchSource:        "suggestion_promoted",
		MatchTier:          "suggest",
	}

	result, err := repo.UpsertMapping(t.Context(), "job-upsert-upd", updated)
	if err != nil {
		t.Fatalf("UpsertMapping: %v", err)
	}
	if result.CustomName != "PromotedAlice" {
		t.Errorf("CustomName: got %q, want %q", result.CustomName, "PromotedAlice")
	}
	if result.MatchSource != "suggestion_promoted" {
		t.Errorf("MatchSource: got %q, want %q", result.MatchSource, "suggestion_promoted")
	}

	// Verify still exactly one row (updated, not duplicated).
	mappings, _ := repo.ListByJob(t.Context(), "job-upsert-upd")
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping after upsert, got %d", len(mappings))
	}
	if mappings[0].CustomName != "PromotedAlice" {
		t.Errorf("DB CustomName: got %q, want %q", mappings[0].CustomName, "PromotedAlice")
	}
}

func TestUpsertMapping_PreservesOtherSpeakers(t *testing.T) {
	_, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	repo := repository.NewSpeakerMappingRepository(db)

	job := &models.TranscriptionJob{ID: "job-upsert-multi", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	// Pre-create mappings for two speakers.
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-upsert-multi",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		MatchSource:        "auto",
	})
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-upsert-multi",
		OriginalSpeaker:    "speaker_01",
		CustomName:         "Bob",
		MatchSource:        "manual",
	})

	// Upsert speaker_00 — should NOT touch speaker_01.
	upserted := models.SpeakerMapping{
		TranscriptionJobID: "job-upsert-multi",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "NewAlice",
		ConfidenceScore:    0.70,
		MatchSource:        "suggestion_promoted",
		MatchTier:          "suggest",
	}

	_, err := repo.UpsertMapping(t.Context(), "job-upsert-multi", upserted)
	if err != nil {
		t.Fatalf("UpsertMapping: %v", err)
	}

	mappings, _ := repo.ListByJob(t.Context(), "job-upsert-multi")
	if len(mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(mappings))
	}

	// Find each speaker and verify.
	for _, m := range mappings {
		switch m.OriginalSpeaker {
		case "speaker_00":
			if m.CustomName != "NewAlice" {
				t.Errorf("speaker_00 CustomName: got %q, want %q", m.CustomName, "NewAlice")
			}
		case "speaker_01":
			if m.CustomName != "Bob" {
				t.Errorf("speaker_01 CustomName: got %q, want %q", m.CustomName, "Bob")
			}
			if m.MatchSource != "manual" {
				t.Errorf("speaker_01 MatchSource: got %q, want %q (should be untouched)", m.MatchSource, "manual")
			}
		}
	}
}

// --- PromoteSpeakerSuggestion handler ---

func TestPromoteSpeakerSuggestion_JobNotFound(t *testing.T) {
	h, _, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	reqBody := PromoteSuggestionRequest{
		OriginalSpeaker: "speaker_00",
		ContactID:       1,
		ContactName:     "Alice",
		Score:           0.72,
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "nonexistent-job"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/nonexistent-job/speakers/promote", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PromoteSpeakerSuggestion(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestPromoteSpeakerSuggestion_InvalidBody(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-promote-invalid", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	// Missing required fields.
	body := []byte(`{"original_speaker": "speaker_00"}`)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-promote-invalid"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-promote-invalid/speakers/promote", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PromoteSpeakerSuggestion(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestPromoteSpeakerSuggestion_CreatesMapping(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-promote-new", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	reqBody := PromoteSuggestionRequest{
		OriginalSpeaker: "speaker_00",
		ContactID:       1,
		ContactName:     "Alice",
		Score:           0.72,
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-promote-new"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-promote-new/speakers/promote", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PromoteSpeakerSuggestion(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify DB row.
	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "job-promote-new").Find(&mappings)

	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}

	m := mappings[0]
	if m.CustomName != "Alice" {
		t.Errorf("CustomName: got %q, want %q", m.CustomName, "Alice")
	}
	if m.ConfidenceScore != 0.72 {
		t.Errorf("ConfidenceScore: got %f, want 0.72", m.ConfidenceScore)
	}
	if m.MatchSource != "suggestion_promoted" {
		t.Errorf("MatchSource: got %q, want %q", m.MatchSource, "suggestion_promoted")
	}
	if m.MatchTier != "suggest" {
		t.Errorf("MatchTier: got %q, want %q", m.MatchTier, "suggest")
	}

	// Verify response shape.
	var resp SpeakerMappingsUpdateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Mappings) != 1 {
		t.Fatalf("expected 1 mapping in response, got %d", len(resp.Mappings))
	}
	if resp.Mappings[0].MatchSource != "suggestion_promoted" {
		t.Errorf("response MatchSource: got %q, want %q", resp.Mappings[0].MatchSource, "suggestion_promoted")
	}
}

func TestPromoteSpeakerSuggestion_OverwritesExisting(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-promote-overwrite", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	// Pre-create an auto-assigned mapping.
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-promote-overwrite",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "AutoAlice",
		ConfidenceScore:    0.85,
		MatchSource:        "auto",
		MatchTier:          "auto",
	})

	reqBody := PromoteSuggestionRequest{
		OriginalSpeaker: "speaker_00",
		ContactID:       2,
		ContactName:     "BetterAlice",
		Score:           0.73,
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-promote-overwrite"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-promote-overwrite/speakers/promote", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PromoteSpeakerSuggestion(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify the mapping was overwritten, not duplicated.
	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "job-promote-overwrite").Find(&mappings)

	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping (overwritten), got %d", len(mappings))
	}

	m := mappings[0]
	if m.CustomName != "BetterAlice" {
		t.Errorf("CustomName: got %q, want %q", m.CustomName, "BetterAlice")
	}
	if m.MatchSource != "suggestion_promoted" {
		t.Errorf("MatchSource: got %q, want %q", m.MatchSource, "suggestion_promoted")
	}
	if m.ConfidenceScore != 0.73 {
		t.Errorf("ConfidenceScore: got %f, want 0.73", m.ConfidenceScore)
	}
}

// --- GetSpeakerSuggestions handler ---

func TestGetSpeakerSuggestions_ReturnsPendingOnly(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-suggestions-pending", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	// Seed three mappings: one pending (suggest-tier), one accepted, one dismissed.
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-suggestions-pending",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		ConfidenceScore:    0.72,
		MatchSource:        "auto",
		MatchTier:          "suggest",
		ReviewStatus:       "pending",
	})
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-suggestions-pending",
		OriginalSpeaker:    "speaker_01",
		CustomName:         "Bob",
		ConfidenceScore:    0.85,
		MatchSource:        "auto",
		MatchTier:          "auto",
		ReviewStatus:       "accepted",
	})
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-suggestions-pending",
		OriginalSpeaker:    "speaker_02",
		CustomName:         "Carol",
		ConfidenceScore:    0.65,
		MatchSource:        "auto",
		MatchTier:          "suggest",
		ReviewStatus:       "dismissed",
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-suggestions-pending"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/job-suggestions-pending/speakers/suggestions", nil)

	h.GetSpeakerSuggestions(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp []SpeakerMappingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("expected 1 pending suggestion, got %d", len(resp))
	}
	if resp[0].OriginalSpeaker != "speaker_00" {
		t.Errorf("OriginalSpeaker: got %q, want %q", resp[0].OriginalSpeaker, "speaker_00")
	}
	if resp[0].ReviewStatus != "pending" {
		t.Errorf("ReviewStatus: got %q, want %q", resp[0].ReviewStatus, "pending")
	}
	if resp[0].MatchTier != "suggest" {
		t.Errorf("MatchTier: got %q, want %q", resp[0].MatchTier, "suggest")
	}
}

func TestGetSpeakerSuggestions_EmptyWhenNone(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-suggestions-empty", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-suggestions-empty"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/job-suggestions-empty/speakers/suggestions", nil)

	h.GetSpeakerSuggestions(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp []SpeakerMappingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("expected empty array, got %d entries", len(resp))
	}
}

func TestGetSpeakerSuggestions_JobNotFound(t *testing.T) {
	h, _, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "nonexistent-job-suggestions"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/nonexistent-job-suggestions/speakers/suggestions", nil)

	h.GetSpeakerSuggestions(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

// --- DismissSpeakerSuggestion handler ---

func TestDismissSpeakerSuggestion_SetsDismissedStatus(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-dismiss", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	m := &models.SpeakerMapping{
		TranscriptionJobID: "job-dismiss",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		ConfidenceScore:    0.70,
		MatchSource:        "auto",
		MatchTier:          "suggest",
		ReviewStatus:       "pending",
	}
	db.Create(m)

	reqBody := DismissSuggestionRequest{MappingID: m.ID}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-dismiss"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-dismiss/speakers/dismiss", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.DismissSpeakerSuggestion(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "dismissed" {
		t.Errorf("response status: got %q, want %q", resp["status"], "dismissed")
	}

	// Verify the DB row has review_status="dismissed".
	var loaded models.SpeakerMapping
	db.First(&loaded, m.ID)
	if loaded.ReviewStatus != "dismissed" {
		t.Errorf("DB ReviewStatus: got %q, want %q", loaded.ReviewStatus, "dismissed")
	}
}

func TestDismissSpeakerSuggestion_InvalidBody(t *testing.T) {
	h, _, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	// Empty JSON body — missing required mapping_id field.
	body := []byte(`{}`)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-dismiss-invalid"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-dismiss-invalid/speakers/dismiss", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.DismissSpeakerSuggestion(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestDismissSpeakerSuggestion_MalformedBody(t *testing.T) {
	h, _, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	// Non-JSON body.
	body := []byte(`not-json`)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-dismiss-malformed"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-dismiss-malformed/speakers/dismiss", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.DismissSpeakerSuggestion(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// --- PromoteSpeakerSuggestion: review_status and contact_id ---

func TestPromoteSpeakerSuggestion_SetsAcceptedStatus(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-promote-status", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	reqBody := PromoteSuggestionRequest{
		OriginalSpeaker: "speaker_00",
		ContactID:       42,
		ContactName:     "Eve",
		Score:           0.78,
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-promote-status"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-promote-status/speakers/promote", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PromoteSpeakerSuggestion(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify ReviewStatus="accepted" in the DB.
	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "job-promote-status").Find(&mappings)

	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
	m := mappings[0]
	if m.ReviewStatus != "accepted" {
		t.Errorf("ReviewStatus: got %q, want %q", m.ReviewStatus, "accepted")
	}
}

func TestPromoteSpeakerSuggestion_SetsContactID(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-promote-contact", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	contactID := uint(99)
	reqBody := PromoteSuggestionRequest{
		OriginalSpeaker: "speaker_01",
		ContactID:       contactID,
		ContactName:     "Frank",
		Score:           0.81,
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-promote-contact"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-promote-contact/speakers/promote", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PromoteSpeakerSuggestion(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify ContactID is set in the DB.
	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "job-promote-contact").Find(&mappings)

	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
	m := mappings[0]
	if m.ContactID == nil {
		t.Fatal("ContactID is nil, expected non-nil")
	}
	if *m.ContactID != contactID {
		t.Errorf("ContactID: got %d, want %d", *m.ContactID, contactID)
	}
	if m.ReviewStatus != "accepted" {
		t.Errorf("ReviewStatus: got %q, want %q", m.ReviewStatus, "accepted")
	}

	// Also verify the response includes the contact_id.
	var resp SpeakerMappingsUpdateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Mappings) != 1 {
		t.Fatalf("expected 1 mapping in response, got %d", len(resp.Mappings))
	}
	if resp.Mappings[0].ContactID == nil {
		t.Fatal("response ContactID is nil, expected non-nil")
	}
	if *resp.Mappings[0].ContactID != contactID {
		t.Errorf("response ContactID: got %d, want %d", *resp.Mappings[0].ContactID, contactID)
	}
}

func TestPromoteSpeakerSuggestion_OverwritesPendingSuggestion(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-promote-overwrite-pending", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	// Pre-seed a pending suggest-tier mapping.
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-promote-overwrite-pending",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "AutoSuggested",
		ConfidenceScore:    0.68,
		MatchSource:        "auto",
		MatchTier:          "suggest",
		ReviewStatus:       "pending",
	})

	reqBody := PromoteSuggestionRequest{
		OriginalSpeaker: "speaker_00",
		ContactID:       7,
		ContactName:     "George",
		Score:           0.71,
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-promote-overwrite-pending"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-promote-overwrite-pending/speakers/promote", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PromoteSpeakerSuggestion(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Should still be exactly 1 row — updated, not duplicated.
	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "job-promote-overwrite-pending").Find(&mappings)

	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping after promote, got %d", len(mappings))
	}
	m := mappings[0]
	if m.ReviewStatus != "accepted" {
		t.Errorf("ReviewStatus: got %q, want %q", m.ReviewStatus, "accepted")
	}
	if m.CustomName != "George" {
		t.Errorf("CustomName: got %q, want %q", m.CustomName, "George")
	}
}

// --- CountPendingSuggestions repo method ---

func TestCountPendingSuggestions_MultipleJobs(t *testing.T) {
	_, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	repo := repository.NewSpeakerMappingRepository(db)

	jobs := []string{"job-count-a", "job-count-b", "job-count-c"}
	for _, jid := range jobs {
		db.Create(&models.TranscriptionJob{ID: jid, AudioPath: "/tmp/a.wav", Status: models.StatusCompleted})
	}

	// job-count-a: 2 pending, 1 accepted
	db.Create(&models.SpeakerMapping{TranscriptionJobID: "job-count-a", OriginalSpeaker: "speaker_00", CustomName: "A0", ReviewStatus: "pending"})
	db.Create(&models.SpeakerMapping{TranscriptionJobID: "job-count-a", OriginalSpeaker: "speaker_01", CustomName: "A1", ReviewStatus: "pending"})
	db.Create(&models.SpeakerMapping{TranscriptionJobID: "job-count-a", OriginalSpeaker: "speaker_02", CustomName: "A2", ReviewStatus: "accepted"})

	// job-count-b: 1 pending
	db.Create(&models.SpeakerMapping{TranscriptionJobID: "job-count-b", OriginalSpeaker: "speaker_00", CustomName: "B0", ReviewStatus: "pending"})

	// job-count-c: 0 pending (1 dismissed)
	db.Create(&models.SpeakerMapping{TranscriptionJobID: "job-count-c", OriginalSpeaker: "speaker_00", CustomName: "C0", ReviewStatus: "dismissed"})

	counts, err := repo.CountPendingSuggestions(t.Context(), jobs)
	if err != nil {
		t.Fatalf("CountPendingSuggestions: %v", err)
	}

	tests := []struct {
		jobID string
		want  int
	}{
		{"job-count-a", 2},
		{"job-count-b", 1},
		{"job-count-c", 0}, // dismissed rows are not counted; key should be absent or zero
	}
	for _, tt := range tests {
		got := counts[tt.jobID]
		if got != tt.want {
			t.Errorf("job %q: pending count got %d, want %d", tt.jobID, got, tt.want)
		}
	}
}

func TestCountPendingSuggestions_EmptyInput(t *testing.T) {
	_, _, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	// Use a fresh repo from the harness db — but we only need the interface here.
	_, db2, cleanup2 := setupSpeakerMappingHarness(t)
	defer cleanup2()

	repo := repository.NewSpeakerMappingRepository(db2)

	counts, err := repo.CountPendingSuggestions(t.Context(), []string{})
	if err != nil {
		t.Fatalf("CountPendingSuggestions with empty input: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("expected empty map, got %v", counts)
	}
}

func TestCountPendingSuggestions_NoSuggestions(t *testing.T) {
	_, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	repo := repository.NewSpeakerMappingRepository(db)

	// Seed jobs but no pending mappings.
	db.Create(&models.TranscriptionJob{ID: "job-no-pending-1", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted})
	db.Create(&models.TranscriptionJob{ID: "job-no-pending-2", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted})
	db.Create(&models.SpeakerMapping{TranscriptionJobID: "job-no-pending-1", OriginalSpeaker: "speaker_00", CustomName: "Manual", MatchSource: "manual"})

	counts, err := repo.CountPendingSuggestions(t.Context(), []string{"job-no-pending-1", "job-no-pending-2"})
	if err != nil {
		t.Fatalf("CountPendingSuggestions: %v", err)
	}
	// Neither job should appear in the result map (or have count 0).
	for _, jid := range []string{"job-no-pending-1", "job-no-pending-2"} {
		if counts[jid] != 0 {
			t.Errorf("job %q: expected count 0, got %d", jid, counts[jid])
		}
	}
}

// --- GetSpeakerMappings: additional coverage ---

func TestGetSpeakerMappings_JobNotFound(t *testing.T) {
	h, _, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "nonexistent-job-get"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/nonexistent-job-get/speakers", nil)

	h.GetSpeakerMappings(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestGetSpeakerMappings_EmptyMappingsReturnsEmptyArray(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-get-empty", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-get-empty"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/job-get-empty/speakers", nil)

	h.GetSpeakerMappings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp []SpeakerMappingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("expected empty array, got %d entries", len(resp))
	}
}

func TestGetSpeakerMappings_MixedAutoAndManualMappings(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-get-mixed", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-get-mixed",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		ConfidenceScore:    0.90,
		MatchSource:        "auto",
		MatchTier:          "auto",
		ReviewStatus:       "accepted",
	})
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-get-mixed",
		OriginalSpeaker:    "speaker_01",
		CustomName:         "Bob",
		ConfidenceScore:    0.0,
		MatchSource:        "manual",
		MatchTier:          "",
		ReviewStatus:       "",
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-get-mixed"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/job-get-mixed/speakers", nil)

	h.GetSpeakerMappings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp []SpeakerMappingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(resp))
	}

	found := make(map[string]SpeakerMappingResponse)
	for _, r := range resp {
		found[r.OriginalSpeaker] = r
	}

	auto := found["speaker_00"]
	if auto.MatchSource != "auto" {
		t.Errorf("speaker_00 MatchSource: got %q, want %q", auto.MatchSource, "auto")
	}
	if auto.ConfidenceScore != 0.90 {
		t.Errorf("speaker_00 ConfidenceScore: got %f, want 0.90", auto.ConfidenceScore)
	}

	manual := found["speaker_01"]
	if manual.MatchSource != "manual" {
		t.Errorf("speaker_01 MatchSource: got %q, want %q", manual.MatchSource, "manual")
	}
	if manual.ConfidenceScore != 0.0 {
		t.Errorf("speaker_01 ConfidenceScore: got %f, want 0.0", manual.ConfidenceScore)
	}
}

// --- UpdateSpeakerMappings: additional coverage ---

func TestUpdateSpeakerMappings_InvalidRequestBody(t *testing.T) {
	h, _, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	// Non-JSON body triggers ShouldBindJSON failure.
	body := []byte(`not-valid-json`)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-update-invalid"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-update-invalid/speakers", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateSpeakerMappings(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestUpdateSpeakerMappings_JobNotFound(t *testing.T) {
	h, _, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	reqBody := SpeakerMappingsUpdateRequest{
		Mappings: []SpeakerMappingRequest{
			{OriginalSpeaker: "speaker_00", CustomName: "Alice"},
		},
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "nonexistent-update-job"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/nonexistent-update-job/speakers", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateSpeakerMappings(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestUpdateSpeakerMappings_OverwritesExistingMappings(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-update-overwrite", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	// Pre-seed an auto mapping that should be overwritten.
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-update-overwrite",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "AutoAlice",
		ConfidenceScore:    0.88,
		MatchSource:        "auto",
		MatchTier:          "auto",
	})

	reqBody := SpeakerMappingsUpdateRequest{
		Mappings: []SpeakerMappingRequest{
			{OriginalSpeaker: "speaker_00", CustomName: "ManualAlice"},
		},
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-update-overwrite"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-update-overwrite/speakers", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateSpeakerMappings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "job-update-overwrite").Find(&mappings)

	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping after overwrite, got %d", len(mappings))
	}
	if mappings[0].CustomName != "ManualAlice" {
		t.Errorf("CustomName: got %q, want %q", mappings[0].CustomName, "ManualAlice")
	}
	if mappings[0].MatchSource != "manual" {
		t.Errorf("MatchSource: got %q, want %q", mappings[0].MatchSource, "manual")
	}
}

func TestUpdateSpeakerMappings_CreatesNewMappings(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-update-create", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	reqBody := SpeakerMappingsUpdateRequest{
		Mappings: []SpeakerMappingRequest{
			{OriginalSpeaker: "speaker_00", CustomName: "Alice"},
			{OriginalSpeaker: "speaker_01", CustomName: "Bob"},
			{OriginalSpeaker: "speaker_02", CustomName: "Carol"},
		},
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-update-create"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-update-create/speakers", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateSpeakerMappings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp SpeakerMappingsUpdateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Mappings) != 3 {
		t.Fatalf("expected 3 mappings in response, got %d", len(resp.Mappings))
	}
	for _, m := range resp.Mappings {
		if m.MatchSource != "manual" {
			t.Errorf("mapping %q: MatchSource got %q, want %q", m.OriginalSpeaker, m.MatchSource, "manual")
		}
	}
}

// --- DismissSpeakerSuggestion: additional coverage ---

func TestDismissSpeakerSuggestion_NonexistentMappingSucceeds(t *testing.T) {
	// The handler now verifies ownership — a nonexistent mapping ID returns 404.
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-dismiss-nonexistent", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	reqBody := DismissSuggestionRequest{MappingID: 99999}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-dismiss-nonexistent"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-dismiss-nonexistent/speakers/dismiss", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.DismissSpeakerSuggestion(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestDismissSpeakerSuggestion_AlreadyDismissed(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-dismiss-again", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	m := &models.SpeakerMapping{
		TranscriptionJobID: "job-dismiss-again",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		ConfidenceScore:    0.70,
		MatchSource:        "auto",
		MatchTier:          "suggest",
		ReviewStatus:       "dismissed", // already dismissed
	}
	db.Create(m)

	reqBody := DismissSuggestionRequest{MappingID: m.ID}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-dismiss-again"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-dismiss-again/speakers/dismiss", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.DismissSpeakerSuggestion(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "dismissed" {
		t.Errorf("response status: got %q, want %q", resp["status"], "dismissed")
	}

	// Verify DB row is still dismissed.
	var loaded models.SpeakerMapping
	db.First(&loaded, m.ID)
	if loaded.ReviewStatus != "dismissed" {
		t.Errorf("DB ReviewStatus: got %q, want %q", loaded.ReviewStatus, "dismissed")
	}
}

// --- GetSpeakerSuggestions: multiple suggestions ---

func TestGetSpeakerSuggestions_MultipleSortedByScore(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-suggestions-multi", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	// Insert out-of-order so we can verify the response is independent of
	// insertion order. The handler delegates sorting to the repo query.
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-suggestions-multi",
		OriginalSpeaker:    "speaker_02",
		CustomName:         "Carol",
		ConfidenceScore:    0.62,
		MatchSource:        "auto",
		MatchTier:          "suggest",
		ReviewStatus:       "pending",
	})
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-suggestions-multi",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		ConfidenceScore:    0.79,
		MatchSource:        "auto",
		MatchTier:          "suggest",
		ReviewStatus:       "pending",
	})
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-suggestions-multi",
		OriginalSpeaker:    "speaker_01",
		CustomName:         "Bob",
		ConfidenceScore:    0.71,
		MatchSource:        "auto",
		MatchTier:          "suggest",
		ReviewStatus:       "pending",
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-suggestions-multi"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/job-suggestions-multi/speakers/suggestions", nil)

	h.GetSpeakerSuggestions(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp []SpeakerMappingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) != 3 {
		t.Fatalf("expected 3 suggestions, got %d", len(resp))
	}

	// All returned entries must be pending.
	for _, r := range resp {
		if r.ReviewStatus != "pending" {
			t.Errorf("speaker %q: ReviewStatus got %q, want %q", r.OriginalSpeaker, r.ReviewStatus, "pending")
		}
		if r.MatchTier != "suggest" {
			t.Errorf("speaker %q: MatchTier got %q, want %q", r.OriginalSpeaker, r.MatchTier, "suggest")
		}
	}
}

// --- formatTranscriptWithSpeakers ---

func TestFormatTranscriptWithSpeakers_NoSegments_ReturnsPlainText(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "transcript.json")

	payload := `{"text":"plain transcript text","segments":[]}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	result, err := formatTranscriptWithSpeakers(jsonPath, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "plain transcript text" {
		t.Errorf("got %q, want %q", result, "plain transcript text")
	}
}

func TestFormatTranscriptWithSpeakers_WithSpeakerName_Field(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "transcript.json")

	payload := `{
		"text": "hello world",
		"segments": [
			{"text": "Hello there.", "speaker": "speaker_0", "speaker_name": "Alice"},
			{"text": "How are you?", "speaker": "speaker_1", "speaker_name": "Bob"}
		]
	}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	result, err := formatTranscriptWithSpeakers(jsonPath, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "[Alice] Hello there.\n[Bob] How are you?\n" {
		t.Errorf("unexpected result:\n%s", result)
	}
}

func TestFormatTranscriptWithSpeakers_MappingOverridesRawSpeaker(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "transcript.json")

	payload := `{
		"text": "hello",
		"segments": [
			{"text": "Hello.", "speaker": "speaker_0"},
			{"text": "Goodbye.", "speaker": "speaker_1"}
		]
	}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	mappings := map[string]string{
		"speaker_0": "Alice",
		// speaker_1 intentionally absent to test fallback to raw key
	}

	result, err := formatTranscriptWithSpeakers(jsonPath, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "[Alice] Hello.\n[speaker_1] Goodbye.\n" {
		t.Errorf("unexpected result:\n%s", result)
	}
}

func TestFormatTranscriptWithSpeakers_NoSpeakerKey_NoLabel(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "transcript.json")

	payload := `{
		"text": "narration",
		"segments": [
			{"text": "This is narration."}
		]
	}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	result, err := formatTranscriptWithSpeakers(jsonPath, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "This is narration.\n" {
		t.Errorf("unexpected result:\n%s", result)
	}
}

func TestFormatTranscriptWithSpeakers_SkipsEmptySegments(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "transcript.json")

	payload := `{
		"text": "hello",
		"segments": [
			{"text": "  ", "speaker": "speaker_0"},
			{"text": "Real text.", "speaker": "speaker_0"}
		]
	}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	mappings := map[string]string{"speaker_0": "Alice"}

	result, err := formatTranscriptWithSpeakers(jsonPath, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "[Alice] Real text.\n" {
		t.Errorf("unexpected result:\n%s", result)
	}
}

func TestFormatTranscriptWithSpeakers_FileNotFound(t *testing.T) {
	_, err := formatTranscriptWithSpeakers("/nonexistent/path/transcript.json", map[string]string{})
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestFormatTranscriptWithSpeakers_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "bad.json")

	if err := os.WriteFile(jsonPath, []byte(`not-json`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := formatTranscriptWithSpeakers(jsonPath, map[string]string{})
	if err == nil {
		t.Error("expected parse error, got nil")
	}
}

// --- loadSpeakerNames ---

func TestLoadSpeakerNames_ReturnsMappedNames(t *testing.T) {
	_, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()
	// setupSpeakerMappingHarness already patches database.DB.

	job := &models.TranscriptionJob{ID: "job-load-names", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-load-names",
		OriginalSpeaker:    "speaker_0",
		CustomName:         "Alice",
		MatchSource:        "manual",
	})
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-load-names",
		OriginalSpeaker:    "speaker_1",
		CustomName:         "Bob",
		MatchSource:        "auto",
	})
	// Mapping where custom_name equals original_speaker should be excluded.
	db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-load-names",
		OriginalSpeaker:    "speaker_2",
		CustomName:         "speaker_2",
		MatchSource:        "manual",
	})

	names := loadSpeakerNames("job-load-names")

	if names["speaker_0"] != "Alice" {
		t.Errorf("speaker_0: got %q, want %q", names["speaker_0"], "Alice")
	}
	if names["speaker_1"] != "Bob" {
		t.Errorf("speaker_1: got %q, want %q", names["speaker_1"], "Bob")
	}
	if _, ok := names["speaker_2"]; ok {
		t.Error("speaker_2 should be excluded (custom_name == original_speaker)")
	}
}

func TestLoadSpeakerNames_EmptyForUnknownJob(t *testing.T) {
	_, _, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	names := loadSpeakerNames("job-does-not-exist")
	if len(names) != 0 {
		t.Errorf("expected empty map, got %v", names)
	}
}

// --- BuildRetroactiveSpeakerExtractor ---

func TestBuildRetroactiveSpeakerExtractor_NilTranscript(t *testing.T) {
	extractor := BuildRetroactiveSpeakerExtractor("")

	job := &models.TranscriptionJob{
		ID:        "job-retro-nil",
		AudioPath: "/tmp/a.wav",
		Transcript: nil,
	}

	embeddings, err := extractor(t.Context(), job, "/tmp/vault")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if embeddings != nil {
		t.Errorf("expected nil embeddings for nil transcript, got %v", embeddings)
	}
}

func TestBuildRetroactiveSpeakerExtractor_EmptyTranscript(t *testing.T) {
	extractor := BuildRetroactiveSpeakerExtractor("")

	empty := "   "
	job := &models.TranscriptionJob{
		ID:         "job-retro-empty",
		AudioPath:  "/tmp/a.wav",
		Transcript: &empty,
	}

	embeddings, err := extractor(t.Context(), job, "/tmp/vault")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if embeddings != nil {
		t.Errorf("expected nil embeddings for blank transcript, got %v", embeddings)
	}
}

func TestBuildRetroactiveSpeakerExtractor_InvalidTranscriptJSON(t *testing.T) {
	extractor := BuildRetroactiveSpeakerExtractor("")

	bad := "not-valid-json"
	job := &models.TranscriptionJob{
		ID:         "job-retro-bad-json",
		AudioPath:  "/tmp/a.wav",
		Transcript: &bad,
	}

	_, err := extractor(t.Context(), job, "/tmp/vault")
	if err == nil {
		t.Error("expected parse error for invalid transcript JSON, got nil")
	}
}

func TestBuildRetroactiveSpeakerExtractor_EmptySegments(t *testing.T) {
	extractor := BuildRetroactiveSpeakerExtractor("")

	transcript := `{"text":"hello","segments":[]}`
	job := &models.TranscriptionJob{
		ID:         "job-retro-no-segs",
		AudioPath:  "/tmp/a.wav",
		Transcript: &transcript,
	}

	embeddings, err := extractor(t.Context(), job, "/tmp/vault")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if embeddings != nil {
		t.Errorf("expected nil embeddings for empty segments, got %v", embeddings)
	}
}

func TestBuildRetroactiveSpeakerExtractor_NoSpeakerWindowsBuilt(t *testing.T) {
	extractor := BuildRetroactiveSpeakerExtractor("")

	// Segments present but all have no speaker field, so buildSpeakerClipWindows
	// returns an empty map.
	transcript := fmt.Sprintf(`{
		"text": "hello",
		"segments": [
			{"text": "Hello.", "start": 0.0, "end": 1.0}
		]
	}`)
	job := &models.TranscriptionJob{
		ID:         "job-retro-no-windows",
		AudioPath:  "/tmp/a.wav",
		Transcript: &transcript,
	}

	embeddings, err := extractor(t.Context(), job, "/tmp/vault")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if embeddings != nil {
		t.Errorf("expected nil embeddings when no speaker windows built, got %v", embeddings)
	}
}

// --- Mock speaker mapping repository for error-path tests ---

// errSpeakerMappingRepo is a minimal SpeakerMappingRepository that delegates
// all operations to a real repo but lets individual methods be overridden to
// return errors, enabling testing of internal server error branches.
type errSpeakerMappingRepo struct {
	delegate           repository.SpeakerMappingRepository
	listByJobErr       error
	listPendingErr     error
	updateReviewErr    error
	updateMappingsErr  error
}

func (r *errSpeakerMappingRepo) Create(ctx context.Context, entity *models.SpeakerMapping) error {
	return r.delegate.Create(ctx, entity)
}
func (r *errSpeakerMappingRepo) FindByID(ctx context.Context, id interface{}) (*models.SpeakerMapping, error) {
	return r.delegate.FindByID(ctx, id)
}
func (r *errSpeakerMappingRepo) Update(ctx context.Context, entity *models.SpeakerMapping) error {
	return r.delegate.Update(ctx, entity)
}
func (r *errSpeakerMappingRepo) Delete(ctx context.Context, id interface{}) error {
	return r.delegate.Delete(ctx, id)
}
func (r *errSpeakerMappingRepo) List(ctx context.Context, offset, limit int) ([]models.SpeakerMapping, int64, error) {
	return r.delegate.List(ctx, offset, limit)
}
func (r *errSpeakerMappingRepo) ListByJob(ctx context.Context, jobID string) ([]models.SpeakerMapping, error) {
	if r.listByJobErr != nil {
		return nil, r.listByJobErr
	}
	return r.delegate.ListByJob(ctx, jobID)
}
func (r *errSpeakerMappingRepo) ListPendingSuggestions(ctx context.Context, jobID string) ([]models.SpeakerMapping, error) {
	if r.listPendingErr != nil {
		return nil, r.listPendingErr
	}
	return r.delegate.ListPendingSuggestions(ctx, jobID)
}
func (r *errSpeakerMappingRepo) CountPendingSuggestions(ctx context.Context, jobIDs []string) (map[string]int, error) {
	return r.delegate.CountPendingSuggestions(ctx, jobIDs)
}
func (r *errSpeakerMappingRepo) UpdateReviewStatus(ctx context.Context, id uint, status string) error {
	if r.updateReviewErr != nil {
		return r.updateReviewErr
	}
	return r.delegate.UpdateReviewStatus(ctx, id, status)
}
func (r *errSpeakerMappingRepo) UpdateMappings(ctx context.Context, jobID string, mappings []models.SpeakerMapping) error {
	if r.updateMappingsErr != nil {
		return r.updateMappingsErr
	}
	return r.delegate.UpdateMappings(ctx, jobID, mappings)
}
func (r *errSpeakerMappingRepo) UpsertMapping(ctx context.Context, jobID string, mapping models.SpeakerMapping) (*models.SpeakerMapping, error) {
	return r.delegate.UpsertMapping(ctx, jobID, mapping)
}
func (r *errSpeakerMappingRepo) DeleteByJobID(ctx context.Context, jobID string) error {
	return r.delegate.DeleteByJobID(ctx, jobID)
}
func (r *errSpeakerMappingRepo) GetSpeakerAttentionSummary(ctx context.Context, jobIDs []string) (map[string]repository.SpeakerAttentionSummary, error) {
	return r.delegate.GetSpeakerAttentionSummary(ctx, jobIDs)
}
func (r *errSpeakerMappingRepo) ListJobIDsByContactID(ctx context.Context, contactID uint) ([]string, error) {
	return r.delegate.ListJobIDsByContactID(ctx, contactID)
}
func (r *errSpeakerMappingRepo) ListByContactID(ctx context.Context, contactID uint) ([]models.SpeakerMapping, error) {
	return r.delegate.ListByContactID(ctx, contactID)
}
func (r *errSpeakerMappingRepo) SetContactID(ctx context.Context, mappingID uint, contactID *uint) error {
	return r.delegate.SetContactID(ctx, mappingID, contactID)
}

// --- GetSpeakerMappings: internal server error on ListByJob ---

func TestGetSpeakerMappings_ListByJobError_Returns500(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-get-listerr", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	h.speakerMappingRepo = &errSpeakerMappingRepo{
		delegate:     repository.NewSpeakerMappingRepository(db),
		listByJobErr: fmt.Errorf("db connection lost"),
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-get-listerr"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/job-get-listerr/speakers", nil)

	h.GetSpeakerMappings(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

// --- GetSpeakerSuggestions: internal server error on ListPendingSuggestions ---

func TestGetSpeakerSuggestions_ListPendingError_Returns500(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-suggest-listerr", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	h.speakerMappingRepo = &errSpeakerMappingRepo{
		delegate:       repository.NewSpeakerMappingRepository(db),
		listPendingErr: fmt.Errorf("db connection lost"),
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-suggest-listerr"}}
	c.Request, _ = http.NewRequest("GET", "/api/v1/transcription/job-suggest-listerr/speakers/suggestions", nil)

	h.GetSpeakerSuggestions(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

// --- DismissSpeakerSuggestion: internal server error on UpdateReviewStatus ---

func TestDismissSpeakerSuggestion_UpdateReviewError_Returns500(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	// Seed a job and mapping so the ownership check passes before the error mock fires.
	job := &models.TranscriptionJob{ID: "job-dismiss-err", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)
	m := &models.SpeakerMapping{
		TranscriptionJobID: "job-dismiss-err",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		ReviewStatus:       "pending",
	}
	db.Create(m)

	h.speakerMappingRepo = &errSpeakerMappingRepo{
		delegate:        repository.NewSpeakerMappingRepository(db),
		updateReviewErr: fmt.Errorf("db locked"),
	}

	reqBody := DismissSuggestionRequest{MappingID: m.ID}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-dismiss-err"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-dismiss-err/speakers/dismiss", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.DismissSpeakerSuggestion(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

// --- UpdateSpeakerMappings: internal server error on UpdateMappings ---

func TestUpdateSpeakerMappings_UpdateMappingsError_Returns500(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{ID: "job-update-dberr", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	db.Create(job)

	h.speakerMappingRepo = &errSpeakerMappingRepo{
		delegate:          repository.NewSpeakerMappingRepository(db),
		updateMappingsErr: fmt.Errorf("disk full"),
	}

	reqBody := SpeakerMappingsUpdateRequest{
		Mappings: []SpeakerMappingRequest{
			{OriginalSpeaker: "speaker_00", CustomName: "Alice"},
		},
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-update-dberr"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-update-dberr/speakers", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateSpeakerMappings(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

// --- Response format includes confidence fields ---

func TestUpdateSpeakerMappings_ResponseIncludesConfidenceFields(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	job := &models.TranscriptionJob{
		ID:        "job-resp-conf",
		AudioPath: "/tmp/c.wav",
		Status:    models.StatusCompleted,
	}
	db.Create(job)

	reqBody := SpeakerMappingsUpdateRequest{
		Mappings: []SpeakerMappingRequest{
			{OriginalSpeaker: "speaker_00", CustomName: "Dan"},
		},
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-resp-conf"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-resp-conf/speakers", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateSpeakerMappings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp SpeakerMappingsUpdateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Mappings) != 1 {
		t.Fatalf("expected 1 mapping in response, got %d", len(resp.Mappings))
	}

	r := resp.Mappings[0]
	// Response should include confidence fields (manual defaults).
	if r.MatchSource != "manual" {
		t.Errorf("response MatchSource: got %q, want %q", r.MatchSource, "manual")
	}
	if r.ConfidenceScore != 0.0 {
		t.Errorf("response ConfidenceScore: got %f, want 0.0", r.ConfidenceScore)
	}
	if r.MatchTier != "" {
		t.Errorf("response MatchTier: got %q, want empty", r.MatchTier)
	}
}

// --- UpdateSpeakerMappings: auto-link contact_id ---

func TestUpdateSpeakerMappings_AutoLinksContactID(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	vault := models.Vault{Name: "TestVault", Path: t.TempDir(), IsActive: true}
	db.Create(&vault)

	contact := models.Contact{VaultID: vault.ID, Name: "Alice", ContactUID: "alice-uid", Slug: "alice", SignatureStatus: "none"}
	db.Create(&contact)

	job := &models.TranscriptionJob{ID: "job-autolink", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, VaultID: &vault.ID}
	db.Create(job)

	reqBody := SpeakerMappingsUpdateRequest{
		Mappings: []SpeakerMappingRequest{
			{OriginalSpeaker: "speaker_0", CustomName: "Alice"},
			{OriginalSpeaker: "speaker_1", CustomName: "UnknownPerson"},
		},
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-autolink"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-autolink/speakers", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateSpeakerMappings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "job-autolink").Order("original_speaker").Find(&mappings)

	if len(mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(mappings))
	}

	// Alice should be linked to the contact
	alice := mappings[0]
	if alice.ContactID == nil {
		t.Fatal("Alice mapping: expected contact_id to be set, got nil")
	}
	if *alice.ContactID != contact.ID {
		t.Errorf("Alice mapping: contact_id got %d, want %d", *alice.ContactID, contact.ID)
	}

	// UnknownPerson should NOT be linked
	unknown := mappings[1]
	if unknown.ContactID != nil {
		t.Errorf("UnknownPerson mapping: expected contact_id nil, got %d", *unknown.ContactID)
	}
}

func TestUpdateSpeakerMappings_AutoLinksContactID_CaseInsensitive(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	vault := models.Vault{Name: "TestVault", Path: t.TempDir(), IsActive: true}
	db.Create(&vault)

	contact := models.Contact{VaultID: vault.ID, Name: "Alice", ContactUID: "alice-uid", Slug: "alice", SignatureStatus: "none"}
	db.Create(&contact)

	job := &models.TranscriptionJob{ID: "job-autolink-ci", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, VaultID: &vault.ID}
	db.Create(job)

	reqBody := SpeakerMappingsUpdateRequest{
		Mappings: []SpeakerMappingRequest{
			{OriginalSpeaker: "speaker_0", CustomName: "alice"}, // lowercase
		},
	}
	body, _ := json.Marshal(reqBody)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "job-autolink-ci"}}
	c.Request, _ = http.NewRequest("POST", "/api/v1/transcription/job-autolink-ci/speakers", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateSpeakerMappings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var mapping models.SpeakerMapping
	db.Where("transcription_job_id = ? AND original_speaker = ?", "job-autolink-ci", "speaker_0").First(&mapping)

	if mapping.ContactID == nil {
		t.Fatal("expected contact_id to be set for case-insensitive match, got nil")
	}
	if *mapping.ContactID != contact.ID {
		t.Errorf("contact_id: got %d, want %d", *mapping.ContactID, contact.ID)
	}
}
