package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"quill/internal/config"
	"quill/internal/models"
	"quill/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupStartTranscriptionHarness creates a minimal test harness for StartTranscription tests.
func setupStartTranscriptionHarness(t *testing.T) (*Handler, *gorm.DB, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.SpeakerMapping{},
		&models.Note{},
		&models.Summary{},
		&models.ChatSession{},
		&models.ChatMessage{},
		&models.TranscriptionJobExecution{},
		&models.MultiTrackFile{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	h := &Handler{
		jobRepo:            repository.NewJobRepository(db),
		chatRepo:           repository.NewChatRepository(db),
		noteRepo:           repository.NewNoteRepository(db),
		summaryRepo:        repository.NewSummaryRepository(db),
		speakerMappingRepo: repository.NewSpeakerMappingRepository(db),
		fileService:        &noopFileService{},
		config:             &config.Config{},
	}

	r := gin.New()
	r.POST("/api/v1/transcription/:id/start", h.StartTranscription)

	return h, db, r
}

// TestStartTranscription_PyAnnoteHighAccuracy verifies the full single-start data flow for
// "High accuracy, your token" mode: JSON binding → param validation → DB save → DB load.
// Uses getValidatedTranscriptionParams + startJob to exercise the same code path as
// StartTranscription without requiring a live task queue.
func TestStartTranscription_PyAnnoteHighAccuracy(t *testing.T) {
	h, _, _ := setupStartTranscriptionHarness(t)

	// Seed a job in "uploaded" status (simulating a file that was uploaded)
	ctx := context.Background()
	if err := h.jobRepo.Create(ctx, &models.TranscriptionJob{
		ID:        "test-pyannote-1",
		AudioPath: "/tmp/test.wav",
		Status:    models.StatusUploaded,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	// Build the exact JSON payload the frontend sends for "High accuracy, your token"
	payload := map[string]interface{}{
		"model_family":                      "whisper",
		"model":                             "small",
		"device":                            "cpu",
		"batch_size":                        8,
		"compute_type":                      "float32",
		"task":                              "transcribe",
		"diarize":                           true,
		"diarize_model":                     "pyannote",
		"vad_method":                        "pyannote",
		"vad_onset":                         0.5,
		"vad_offset":                        0.363,
		"hf_token":                          "hf_test_token_abc123",
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	job, err := h.jobRepo.FindByID(ctx, "test-pyannote-1")
	if err != nil {
		t.Fatalf("find job: %v", err)
	}

	params, err := h.getValidatedTranscriptionParams(c, job, "test-pyannote-1")
	if err != nil {
		t.Fatalf("getValidatedTranscriptionParams failed: %v", err)
	}

	// Simulate what StartTranscription does after validation
	job.Parameters = *params
	job.Diarization = params.Diarize
	job.Status = models.StatusPending
	if err := h.jobRepo.Update(ctx, job); err != nil {
		t.Fatalf("update job: %v", err)
	}

	// Verify the job was saved with correct params
	loaded, err := h.jobRepo.FindByID(ctx, "test-pyannote-1")
	if err != nil {
		t.Fatalf("find saved job: %v", err)
	}

	if !loaded.Parameters.Diarize {
		t.Error("expected Diarize=true after handler")
	}
	if loaded.Parameters.DiarizeModel != "pyannote" {
		t.Errorf("expected DiarizeModel='pyannote', got %q", loaded.Parameters.DiarizeModel)
	}
	if loaded.Parameters.HfToken == nil || *loaded.Parameters.HfToken != "hf_test_token_abc123" {
		t.Errorf("expected HfToken='hf_test_token_abc123', got %v", loaded.Parameters.HfToken)
	}
	if loaded.Status != models.StatusPending {
		t.Errorf("expected status=pending, got %q", loaded.Status)
	}
}

// TestGetValidatedTranscriptionParams_PyAnnote tests the parameter validation
// for pyannote diarization with user-provided HF token.
func TestGetValidatedTranscriptionParams_PyAnnote(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.TranscriptionJob{}, &models.MultiTrackFile{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	h := &Handler{
		jobRepo: repository.NewJobRepository(db),
		config:  &config.Config{},
	}

	// Seed job
	ctx := context.Background()
	job := &models.TranscriptionJob{
		ID:        "test-param-1",
		AudioPath: "/tmp/test.wav",
		Status:    models.StatusUploaded,
	}
	if err := h.jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Build exactly what frontend sends for high-accuracy mode
	payload := map[string]interface{}{
		"model_family":                      "whisper",
		"model":                             "small",
		"model_cache_only":                  false,
		"device":                            "cpu",
		"device_index":                      0,
		"batch_size":                        8,
		"compute_type":                      "float32",
		"threads":                           0,
		"output_format":                     "all",
		"verbose":                           true,
		"task":                              "transcribe",
		"interpolate_method":                "nearest",
		"no_align":                          false,
		"return_char_alignments":            false,
		"vad_method":                        "pyannote",
		"vad_onset":                         0.5,
		"vad_offset":                        0.363,
		"chunk_size":                        30,
		"diarize":                           true,
		"diarize_model":                     "pyannote",
		"speaker_embeddings":                false,
		"temperature":                       0,
		"best_of":                           5,
		"beam_size":                         5,
		"patience":                          1.0,
		"length_penalty":                    1.0,
		"suppress_numerals":                 false,
		"condition_on_previous_text":        false,
		"fp16":                              true,
		"temperature_increment_on_fallback": 0.2,
		"compression_ratio_threshold":       2.4,
		"logprob_threshold":                 -1.0,
		"no_speech_threshold":               0.6,
		"highlight_words":                   false,
		"segment_resolution":                "sentence",
		"hf_token":                          "hf_test_token_abc123",
		"print_progress":                    false,
		"attention_context_left":            256,
		"attention_context_right":           256,
		"is_multi_track_enabled":            false,
		"api_key":                           "",
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	params, err := h.getValidatedTranscriptionParams(c, job, "test-param-1")
	if err != nil {
		t.Fatalf("getValidatedTranscriptionParams failed: %v", err)
	}

	// Verify diarize is true
	if !params.Diarize {
		t.Error("expected Diarize=true, got false")
	}

	// Verify diarize_model is "pyannote"
	if params.DiarizeModel != "pyannote" {
		t.Errorf("expected DiarizeModel='pyannote', got %q", params.DiarizeModel)
	}

	// Verify HF token is present
	if params.HfToken == nil {
		t.Fatal("expected HfToken to be non-nil")
	}
	if *params.HfToken != "hf_test_token_abc123" {
		t.Errorf("expected HfToken='hf_test_token_abc123', got %q", *params.HfToken)
	}

	// Verify model family
	if params.ModelFamily != "whisper" {
		t.Errorf("expected ModelFamily='whisper', got %q", params.ModelFamily)
	}

	// Now simulate saving to DB and loading back (round-trip test)
	job.Parameters = *params
	job.Diarization = params.Diarize
	job.Status = models.StatusPending

	if err := h.jobRepo.Update(ctx, job); err != nil {
		t.Fatalf("update job: %v", err)
	}

	// Load back from DB
	loaded, err := h.jobRepo.FindByID(ctx, "test-param-1")
	if err != nil {
		t.Fatalf("find job: %v", err)
	}

	// Verify params survived the round trip
	if !loaded.Parameters.Diarize {
		t.Error("after DB round-trip: expected Diarize=true, got false")
	}
	if loaded.Parameters.DiarizeModel != "pyannote" {
		t.Errorf("after DB round-trip: expected DiarizeModel='pyannote', got %q", loaded.Parameters.DiarizeModel)
	}
	if loaded.Parameters.HfToken == nil {
		t.Fatal("after DB round-trip: expected HfToken to be non-nil")
	}
	if *loaded.Parameters.HfToken != "hf_test_token_abc123" {
		t.Errorf("after DB round-trip: expected HfToken='hf_test_token_abc123', got %q", *loaded.Parameters.HfToken)
	}
	if loaded.Parameters.ModelFamily != "whisper" {
		t.Errorf("after DB round-trip: expected ModelFamily='whisper', got %q", loaded.Parameters.ModelFamily)
	}
	if !loaded.Diarization {
		t.Error("after DB round-trip: expected Diarization=true")
	}
}

// TestGetValidatedTranscriptionParams_CloudServerToken tests the "Cloud server token" mode
// where no user HF token is provided but the server has HF_TOKEN configured.
func TestGetValidatedTranscriptionParams_CloudServerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.TranscriptionJob{}, &models.MultiTrackFile{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	h := &Handler{
		jobRepo: repository.NewJobRepository(db),
		config:  &config.Config{HFToken: "hf_server_token_xyz"},
	}

	// Seed job
	ctx := context.Background()
	job := &models.TranscriptionJob{
		ID:        "test-cloud-1",
		AudioPath: "/tmp/test.wav",
		Status:    models.StatusUploaded,
	}
	if err := h.jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Frontend sends pyannote but no hf_token (cloud server mode)
	payload := map[string]interface{}{
		"model_family": "whisper",
		"model":        "small",
		"device":       "cpu",
		"batch_size":   8,
		"compute_type": "float32",
		"task":         "transcribe",
		"diarize":      true,
		"diarize_model": "pyannote",
		"vad_method":   "pyannote",
		"vad_onset":    0.5,
		"vad_offset":   0.363,
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	params, err := h.getValidatedTranscriptionParams(c, job, "test-cloud-1")
	if err != nil {
		t.Fatalf("getValidatedTranscriptionParams failed: %v", err)
	}

	// Should use server token
	if params.DiarizeModel != "pyannote" {
		t.Errorf("expected DiarizeModel='pyannote', got %q", params.DiarizeModel)
	}
	if params.HfToken == nil {
		t.Fatal("expected HfToken to be set from server config")
	}
	if *params.HfToken != "hf_server_token_xyz" {
		t.Errorf("expected server HfToken, got %q", *params.HfToken)
	}
}

// TestGetValidatedTranscriptionParams_NoTokenFallsBackToSortformer tests that
// pyannote without any token falls back to sortformer.
func TestGetValidatedTranscriptionParams_NoTokenFallsBackToSortformer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.TranscriptionJob{}, &models.MultiTrackFile{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	h := &Handler{
		jobRepo: repository.NewJobRepository(db),
		config:  &config.Config{}, // No server token
	}

	ctx := context.Background()
	job := &models.TranscriptionJob{
		ID:        "test-fallback-1",
		AudioPath: "/tmp/test.wav",
		Status:    models.StatusUploaded,
	}
	if err := h.jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Frontend sends pyannote but no hf_token, and no server token configured
	payload := map[string]interface{}{
		"model_family":  "whisper",
		"model":         "small",
		"device":        "cpu",
		"batch_size":    8,
		"compute_type":  "float32",
		"task":          "transcribe",
		"diarize":       true,
		"diarize_model": "pyannote",
		"vad_method":    "pyannote",
		"vad_onset":     0.5,
		"vad_offset":    0.363,
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	params, err := h.getValidatedTranscriptionParams(c, job, "test-fallback-1")
	if err != nil {
		t.Fatalf("getValidatedTranscriptionParams failed: %v", err)
	}

	// Should fall back to sortformer since no token is available
	if params.DiarizeModel != "nvidia_sortformer" {
		t.Errorf("expected fallback to nvidia_sortformer, got %q", params.DiarizeModel)
	}
}

// TestBatchStartTranscription_PyAnnoteParams tests that the batch start path
// preserves pyannote diarization params through startJob.
func TestBatchStartTranscription_PyAnnoteParams(t *testing.T) {
	h, db, _ := setupStartTranscriptionHarness(t)

	// Seed a job
	ctx := context.Background()
	if err := h.jobRepo.Create(ctx, &models.TranscriptionJob{
		ID:        "test-batch-pyannote-1",
		AudioPath: "/tmp/test.wav",
		Status:    models.StatusUploaded,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	// Call startJob directly (skipping the HTTP layer)
	token := "hf_batch_token_123"
	params := models.WhisperXParams{
		ModelFamily:  "whisper",
		Model:        "small",
		Device:       "cpu",
		BatchSize:    8,
		ComputeType:  "float32",
		Task:         "transcribe",
		Diarize:      true,
		DiarizeModel: "pyannote",
		HfToken:      &token,
		VadMethod:    "pyannote",
		VadOnset:     0.5,
		VadOffset:    0.363,
	}

	if err := h.startJob(ctx, "test-batch-pyannote-1", params); err != nil {
		t.Fatalf("startJob failed: %v", err)
	}

	// Load and verify
	var loaded models.TranscriptionJob
	if err := db.First(&loaded, "id = ?", "test-batch-pyannote-1").Error; err != nil {
		t.Fatalf("find job: %v", err)
	}

	if !loaded.Parameters.Diarize {
		t.Error("batch path: expected Diarize=true")
	}
	if loaded.Parameters.DiarizeModel != "pyannote" {
		t.Errorf("batch path: expected DiarizeModel='pyannote', got %q", loaded.Parameters.DiarizeModel)
	}
	if loaded.Parameters.HfToken == nil || *loaded.Parameters.HfToken != "hf_batch_token_123" {
		t.Errorf("batch path: expected HfToken='hf_batch_token_123', got %v", loaded.Parameters.HfToken)
	}
}

// TestBatchStartTranscription_NoTokenFallsBackToSortformer tests that the batch
// handler's normalization and fallback logic works correctly when pyannote is
// requested without any HF token.
func TestBatchStartTranscription_NoTokenFallsBackToSortformer(t *testing.T) {
	h, _, r := setupStartTranscriptionHarness(t)

	// Register the batch route
	r.POST("/api/v1/transcription/batch/start", h.BatchStartTranscriptions)

	// Seed a job
	ctx := context.Background()
	if err := h.jobRepo.Create(ctx, &models.TranscriptionJob{
		ID:        "test-batch-fallback-1",
		AudioPath: "/tmp/test.wav",
		Status:    models.StatusUploaded,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	// Batch request with pyannote but no token (and no server token configured)
	payload := map[string]interface{}{
		"ids": []string{"test-batch-fallback-1"},
		"params": map[string]interface{}{
			"model_family":  "whisper",
			"model":         "small",
			"device":        "cpu",
			"batch_size":    8,
			"compute_type":  "float32",
			"task":          "transcribe",
			"diarize":       true,
			"diarize_model": "pyannote",
			"vad_method":    "pyannote",
			"vad_onset":     0.5,
			"vad_offset":    0.363,
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Load and verify the fallback occurred
	loaded, err := h.jobRepo.FindByID(ctx, "test-batch-fallback-1")
	if err != nil {
		t.Fatalf("find job: %v", err)
	}

	// Should have fallen back to sortformer since no token was available
	if loaded.Parameters.DiarizeModel != "nvidia_sortformer" {
		t.Errorf("batch path: expected fallback to nvidia_sortformer, got %q", loaded.Parameters.DiarizeModel)
	}
}
