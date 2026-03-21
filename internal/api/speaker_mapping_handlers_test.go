package api

import (
	"bytes"
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
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	prevDB := database.DB
	database.DB = db

	h := &Handler{
		jobRepo:            repository.NewJobRepository(db),
		speakerMappingRepo: repository.NewSpeakerMappingRepository(db),
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
