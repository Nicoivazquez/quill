package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"quill/internal/database"
	"quill/internal/models"
	"quill/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupOpenClawHarness creates an in-memory SQLite DB, wires up a Handler and
// a Gin engine with all OpenClaw routes, and swaps in database.DB so that the
// global helpers (getSetupRecord, getActiveVault) hit the test database.
// The returned cleanup function restores the original database.DB value.
func setupOpenClawHarness(t *testing.T) (*Handler, *gin.Engine, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	if err := db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.AppSetup{},
		&models.Vault{},
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

	// Create a unique index that the production migration creates so that
	// speaker_mapping inserts work correctly in tests too.
	_ = db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_speaker_mappings_unique ON speaker_mappings(transcription_job_id, original_speaker)").Error

	origDB := database.DB
	database.DB = db

	h := &Handler{
		jobRepo:            repository.NewJobRepository(db),
		chatRepo:           repository.NewChatRepository(db),
		noteRepo:           repository.NewNoteRepository(db),
		summaryRepo:        repository.NewSummaryRepository(db),
		speakerMappingRepo: repository.NewSpeakerMappingRepository(db),
		fileService:        &noopFileService{},
	}

	r := gin.New()
	r.GET("/api/v1/openclaw/config", h.GetOpenClawConfig)
	r.POST("/api/v1/openclaw/config", h.SaveOpenClawConfig)
	r.GET("/api/v1/openclaw/jobs/:id", h.GetOpenClawJob)
	r.GET("/api/v1/openclaw/jobs/:id/transcript.json", h.GetOpenClawJobTranscriptJSON)
	r.GET("/api/v1/openclaw/jobs/:id/transcript.md", h.GetOpenClawJobTranscriptMarkdown)
	r.GET("/api/v1/openclaw/jobs", h.ListOpenClawReadyJobs)

	cleanup := func() {
		database.DB = origDB
	}
	return h, r, cleanup
}

// ---------- GetOpenClawConfig ----------

func TestGetOpenClawConfig_NoSetupRecord(t *testing.T) {
	_, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/config", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, ok := resp["drop_dir"]; !ok || got != "" {
		t.Errorf("expected drop_dir=\"\", got %v", got)
	}
}

func TestGetOpenClawConfig_NilDropDir(t *testing.T) {
	_, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	// Insert an AppSetup record with a nil OpenClawDropDir.
	setup := models.AppSetup{
		ID:        1,
		Completed: true,
		AuthMode:  "local",
		// OpenClawDropDir intentionally left nil
	}
	if err := database.DB.Create(&setup).Error; err != nil {
		t.Fatalf("seed setup: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/config", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, ok := resp["drop_dir"]; !ok || got != "" {
		t.Errorf("expected drop_dir=\"\", got %v", got)
	}
}

func TestGetOpenClawConfig_WithDropDir(t *testing.T) {
	_, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	dir := "/tmp/openclaw-drop"
	setup := models.AppSetup{
		ID:              1,
		Completed:       true,
		AuthMode:        "local",
		OpenClawDropDir: &dir,
	}
	if err := database.DB.Create(&setup).Error; err != nil {
		t.Fatalf("seed setup: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/config", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := resp["drop_dir"]; got != dir {
		t.Errorf("expected drop_dir=%q, got %v", dir, got)
	}
}

// ---------- SaveOpenClawConfig ----------

func TestSaveOpenClawConfig_InvalidBody(t *testing.T) {
	_, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	// Omit the required drop_dir field.
	body, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/openclaw/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSaveOpenClawConfig_Success(t *testing.T) {
	_, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	// Use a real writable path so os.MkdirAll succeeds.
	dir := t.TempDir()

	body, _ := json.Marshal(map[string]any{"drop_dir": dir})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/openclaw/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The handler returns the abs path; since t.TempDir() is already absolute
	// the returned value should equal dir.
	if got := resp["drop_dir"]; got != dir {
		t.Errorf("expected drop_dir=%q, got %v", dir, got)
	}

	// Verify persistence in DB.
	var saved models.AppSetup
	if err := database.DB.First(&saved, 1).Error; err != nil {
		t.Fatalf("load saved setup: %v", err)
	}
	if saved.OpenClawDropDir == nil || *saved.OpenClawDropDir != dir {
		t.Errorf("DB drop_dir not persisted; got %v", saved.OpenClawDropDir)
	}
}

// ---------- GetOpenClawJob ----------

func TestGetOpenClawJob_NotFound(t *testing.T) {
	_, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/jobs/nonexistent-id", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetOpenClawJob_Found(t *testing.T) {
	h, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	title := "My Recording"
	// Use StatusUploaded so artifact materialization is skipped (avoids disk I/O).
	if err := h.jobRepo.Create(context.Background(), &models.TranscriptionJob{
		ID:     "job-oc-1",
		Status: models.StatusUploaded,
		Title:  &title,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/jobs/job-oc-1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["id"] != "job-oc-1" {
		t.Errorf("unexpected id: %v", resp["id"])
	}
	if resp["status"] != string(models.StatusUploaded) {
		t.Errorf("unexpected status: %v", resp["status"])
	}
}

// ---------- GetOpenClawJobTranscriptJSON ----------

func TestGetOpenClawJobTranscriptJSON_NotFound(t *testing.T) {
	_, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/jobs/no-such-job/transcript.json", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetOpenClawJobTranscriptJSON_NoTranscript(t *testing.T) {
	h, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	// Job exists but has no transcript data.
	if err := h.jobRepo.Create(context.Background(), &models.TranscriptionJob{
		ID:     "job-oc-notx",
		Status: models.StatusUploaded,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/jobs/job-oc-notx/transcript.json", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetOpenClawJobTranscriptJSON_FromField(t *testing.T) {
	h, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	// Inline JSON stored in the Transcript field.
	inlineJSON := `[{"start":0,"end":1,"text":"hello","speaker":"SPEAKER_00"}]`
	if err := h.jobRepo.Create(context.Background(), &models.TranscriptionJob{
		ID:         "job-oc-tx",
		Status:     models.StatusUploaded,
		Transcript: &inlineJSON,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/jobs/job-oc-tx/transcript.json", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if body == "" {
		t.Error("expected non-empty body")
	}
	// Body should contain the speaker label from the inline JSON.
	if !bytes.Contains([]byte(body), []byte("SPEAKER_00")) {
		t.Errorf("expected body to contain SPEAKER_00, got: %s", body)
	}
}

// ---------- GetOpenClawJobTranscriptMarkdown ----------

func TestGetOpenClawJobTranscriptMarkdown_NotFound(t *testing.T) {
	_, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/jobs/no-such-job/transcript.md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetOpenClawJobTranscriptMarkdown_NoTranscript(t *testing.T) {
	h, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	// Job exists but has no markdown path and no transcript field.
	if err := h.jobRepo.Create(context.Background(), &models.TranscriptionJob{
		ID:     "job-oc-nomd",
		Status: models.StatusUploaded,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/jobs/job-oc-nomd/transcript.md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- ListOpenClawReadyJobs ----------

func TestListOpenClawReadyJobs_Empty(t *testing.T) {
	_, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/jobs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Jobs []interface{} `json:"jobs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Jobs) != 0 {
		t.Errorf("expected empty jobs array, got %d items", len(resp.Jobs))
	}
}

func TestListOpenClawReadyJobs_ReturnsJobs(t *testing.T) {
	h, r, cleanup := setupOpenClawHarness(t)
	defer cleanup()

	title1 := "First"
	title2 := "Second"
	ctx := context.Background()
	if err := h.jobRepo.Create(ctx, &models.TranscriptionJob{
		ID:     "oc-list-1",
		Status: models.StatusCompleted,
		Title:  &title1,
	}); err != nil {
		t.Fatalf("seed job 1: %v", err)
	}
	if err := h.jobRepo.Create(ctx, &models.TranscriptionJob{
		ID:     "oc-list-2",
		Status: models.StatusUploaded,
		Title:  &title2,
	}); err != nil {
		t.Fatalf("seed job 2: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openclaw/jobs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Jobs []map[string]interface{} `json:"jobs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(resp.Jobs))
	}

	// Verify each returned row carries the expected fields.
	for _, job := range resp.Jobs {
		if _, ok := job["id"]; !ok {
			t.Error("job row missing 'id' field")
		}
		if _, ok := job["status"]; !ok {
			t.Error("job row missing 'status' field")
		}
	}
}
