package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"quill/internal/models"
	"quill/internal/repository"
	"quill/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// noopFileService is a no-op implementation of service.FileService for testing.
type noopFileService struct{}

func (n *noopFileService) SaveUpload(_ *multipart.FileHeader, _ string) (string, error) {
	return "", nil
}
func (n *noopFileService) CreateDirectory(_ string) error             { return nil }
func (n *noopFileService) RemoveFile(_ string) error                  { return nil }
func (n *noopFileService) RemoveDirectory(_ string) error             { return nil }
func (n *noopFileService) ReadFile(_ string) ([]byte, error)          { return nil, nil }
func (n *noopFileService) FileExists(_ string) (bool, error)          { return false, nil }

func setupBatchHarness(t *testing.T) (*Handler, *gin.Engine, func()) {
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
	}

	r := gin.New()
	r.POST("/api/v1/transcription/batch/delete", h.BatchDeleteTranscriptionJobs)
	r.POST("/api/v1/transcription/batch/move", h.BatchMoveTranscriptsToFolder)

	return h, r, func() {}
}

func seedBatchJobs(t *testing.T, h *Handler) {
	t.Helper()
	ctx := context.Background()

	title1 := "Job One"
	h.jobRepo.Create(ctx, &models.TranscriptionJob{
		ID: "job-1", Status: models.StatusCompleted, Title: &title1,
	})

	title2 := "Job Two"
	h.jobRepo.Create(ctx, &models.TranscriptionJob{
		ID: "job-2", Status: models.StatusCompleted, Title: &title2,
	})

	title3 := "Job Three"
	h.jobRepo.Create(ctx, &models.TranscriptionJob{
		ID: "job-3", Status: models.StatusProcessing, Title: &title3,
	})

	title4 := "Job Four"
	h.jobRepo.Create(ctx, &models.TranscriptionJob{
		ID: "job-4", Status: models.StatusUploaded, Title: &title4,
	})
}

// ---------- BatchDelete tests ----------

func TestBatchDelete_EmptyIDs(t *testing.T) {
	h, r, cleanup := setupBatchHarness(t)
	defer cleanup()
	_ = h

	body, _ := json.Marshal(map[string]any{"ids": []string{}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBatchDelete_TooManyIDs(t *testing.T) {
	h, r, cleanup := setupBatchHarness(t)
	defer cleanup()
	_ = h

	ids := make([]string, 101)
	for i := range ids {
		ids[i] = "fake-id"
	}
	body, _ := json.Marshal(map[string]any{"ids": ids})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBatchDelete_SuccessfulDeletion(t *testing.T) {
	h, r, cleanup := setupBatchHarness(t)
	defer cleanup()
	seedBatchJobs(t, h)

	body, _ := json.Marshal(map[string]any{"ids": []string{"job-1", "job-2"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []batchResult `json:"results"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	for _, r := range resp.Results {
		if !r.Success {
			t.Errorf("expected success for %s, got error: %s", r.ID, r.Error)
		}
	}

	// Verify jobs are actually deleted
	_, err := h.jobRepo.FindByID(context.Background(), "job-1")
	if err == nil {
		t.Error("job-1 should have been deleted")
	}
}

func TestBatchDelete_ProcessingJobFails(t *testing.T) {
	h, r, cleanup := setupBatchHarness(t)
	defer cleanup()
	seedBatchJobs(t, h)

	// job-3 is processing, should fail; job-1 is completed, should succeed
	body, _ := json.Marshal(map[string]any{"ids": []string{"job-1", "job-3"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []batchResult `json:"results"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	results := make(map[string]batchResult)
	for _, r := range resp.Results {
		results[r.ID] = r
	}

	if !results["job-1"].Success {
		t.Error("job-1 should have been deleted")
	}
	if results["job-3"].Success {
		t.Error("job-3 (processing) should have failed")
	}
}

func TestBatchDelete_NonexistentJobFails(t *testing.T) {
	h, r, cleanup := setupBatchHarness(t)
	defer cleanup()
	seedBatchJobs(t, h)

	body, _ := json.Marshal(map[string]any{"ids": []string{"nonexistent"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []batchResult `json:"results"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	if len(resp.Results) != 1 || resp.Results[0].Success {
		t.Error("nonexistent job should fail")
	}
}

// ---------- BatchMove tests ----------

func TestBatchMove_EmptyIDs(t *testing.T) {
	h, r, cleanup := setupBatchHarness(t)
	defer cleanup()
	_ = h

	body, _ := json.Marshal(map[string]any{"ids": []string{}, "folder": "Work"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBatchMove_SuccessfulMove(t *testing.T) {
	h, r, cleanup := setupBatchHarness(t)
	defer cleanup()
	seedBatchJobs(t, h)

	body, _ := json.Marshal(map[string]any{"ids": []string{"job-1", "job-2"}, "folder": "Work"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []batchResult `json:"results"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	for _, r := range resp.Results {
		if !r.Success {
			t.Errorf("expected success for %s, error: %s", r.ID, r.Error)
		}
	}

	// Verify folders set in DB
	job, _ := h.jobRepo.FindByID(context.Background(), "job-1")
	if job.Folder == nil || *job.Folder != "Work" {
		t.Errorf("expected folder=Work, got %v", job.Folder)
	}
}

func TestBatchMove_ToRoot(t *testing.T) {
	h, r, cleanup := setupBatchHarness(t)
	defer cleanup()
	seedBatchJobs(t, h)

	// First move to a folder
	folder := "Work"
	job, _ := h.jobRepo.FindByID(context.Background(), "job-1")
	job.Folder = &folder
	h.jobRepo.Update(context.Background(), job)

	// Now move back to root (empty string)
	body, _ := json.Marshal(map[string]any{"ids": []string{"job-1"}, "folder": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	job, _ = h.jobRepo.FindByID(context.Background(), "job-1")
	if job.Folder != nil {
		t.Errorf("expected nil folder (root), got %v", *job.Folder)
	}
}

func TestBatchMove_NonexistentJobFails(t *testing.T) {
	_, r, cleanup := setupBatchHarness(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]any{"ids": []string{"nonexistent"}, "folder": "Work"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcription/batch/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []batchResult `json:"results"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	if len(resp.Results) != 1 || resp.Results[0].Success {
		t.Error("nonexistent job should fail")
	}
}

// Verify noopFileService satisfies the interface at compile time.
var _ service.FileService = (*noopFileService)(nil)
