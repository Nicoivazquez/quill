package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quill/internal/database"
	"quill/internal/models"
	"quill/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupObsidianHarness creates a Handler wired with real in-memory DB repos
// and migrates all models needed for Obsidian tests.
func setupObsidianHarness(t *testing.T) (*Handler, *gorm.DB, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.AppSetup{},
		&models.Vault{},
		&models.TranscriptionJob{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	jobRepo := repository.NewJobRepository(db)
	h := &Handler{
		jobRepo: jobRepo,
	}

	return h, db, func() {}
}

func buildObsidianRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.GET("/api/v1/obsidian/config", h.GetObsidianConfig)
	r.POST("/api/v1/obsidian/config", h.SaveObsidianConfig)
	r.POST("/api/v1/obsidian/sync/:id", h.SyncTranscriptToObsidian)
	r.POST("/api/v1/obsidian/sync-all", h.BulkSyncToObsidian)
	return r
}

// TestGetObsidianConfig_NotConfigured returns empty vault_path when not set.
func TestGetObsidianConfig_NotConfigured(t *testing.T) {
	h, db, cleanup := setupObsidianHarness(t)
	defer cleanup()

	oldDB := swapTestDB(t, db)
	defer restoreTestDB(oldDB)

	r := buildObsidianRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/obsidian/config", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["vault_path"] != "" {
		t.Errorf("expected empty vault_path, got %v", resp["vault_path"])
	}
}

// TestSyncTranscriptToObsidian_WritesFileWithQuillID verifies that syncing
// a transcript creates a file in the Obsidian vault with quill-id frontmatter.
func TestSyncTranscriptToObsidian_WritesFileWithQuillID(t *testing.T) {
	h, db, cleanup := setupObsidianHarness(t)
	defer cleanup()

	// Set up Obsidian config
	obsDir := t.TempDir()
	setup := models.AppSetup{
		ID:               1,
		Completed:        true,
		AuthMode:         "local",
		ObsidianVaultDir: &obsDir,
	}
	if err := db.Create(&setup).Error; err != nil {
		t.Fatal(err)
	}

	// Swap the global DB for this test
	oldDB := swapTestDB(t, db)
	defer restoreTestDB(oldDB)

	// Create a completed job with markdown on disk
	mdDir := t.TempDir()
	mdContent := "---\nid: test-job-1\ntitle: \"Test Transcript\"\nstatus: completed\n---\n\n# Test Transcript\n\nHello world.\n"
	mdPath := filepath.Join(mdDir, "transcript.md")
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		t.Fatal(err)
	}

	title := "Test Transcript"
	job := models.TranscriptionJob{
		ID:                    "test-job-1",
		Status:                models.StatusCompleted,
		Title:                 &title,
		TranscriptMarkdownPath: &mdPath,
	}
	if err := h.jobRepo.Create(context.Background(), &job); err != nil {
		t.Fatal(err)
	}

	r := buildObsidianRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/obsidian/sync/test-job-1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	synced, _ := resp["synced"].(bool)
	if !synced {
		t.Error("expected synced=true")
	}
	path, _ := resp["path"].(string)
	if path == "" {
		t.Fatal("expected non-empty path")
	}

	// Read the published file and verify quill-id
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read published file: %v", err)
	}
	if !strings.Contains(string(content), "quill-id: test-job-1") {
		t.Errorf("published file should contain quill-id, got:\n%s", string(content))
	}
}

// TestSyncTranscriptToObsidian_JobNotFound returns 404.
func TestSyncTranscriptToObsidian_JobNotFound(t *testing.T) {
	h, db, cleanup := setupObsidianHarness(t)
	defer cleanup()

	obsDir := t.TempDir()
	setup := models.AppSetup{
		ID:               1,
		Completed:        true,
		AuthMode:         "local",
		ObsidianVaultDir: &obsDir,
	}
	db.Create(&setup)

	oldDB := swapTestDB(t, db)
	defer restoreTestDB(oldDB)

	r := buildObsidianRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/obsidian/sync/nonexistent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestSyncTranscriptToObsidian_NotConfigured returns 400.
func TestSyncTranscriptToObsidian_NotConfigured(t *testing.T) {
	h, db, cleanup := setupObsidianHarness(t)
	defer cleanup()

	oldDB := swapTestDB(t, db)
	defer restoreTestDB(oldDB)

	title := "Some Transcript"
	job := models.TranscriptionJob{
		ID:     "job-no-config",
		Status: models.StatusCompleted,
		Title:  &title,
	}
	h.jobRepo.Create(context.Background(), &job)

	r := buildObsidianRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/obsidian/sync/job-no-config", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBulkSyncToObsidian_SyncsCompleted tests that bulk sync publishes
// completed jobs and skips non-completed ones.
func TestBulkSyncToObsidian_SyncsCompleted(t *testing.T) {
	h, db, cleanup := setupObsidianHarness(t)
	defer cleanup()

	obsDir := t.TempDir()
	setup := models.AppSetup{
		ID:               1,
		Completed:        true,
		AuthMode:         "local",
		ObsidianVaultDir: &obsDir,
	}
	db.Create(&setup)

	oldDB := swapTestDB(t, db)
	defer restoreTestDB(oldDB)

	// Create a vault so ListWithParams works
	vault := models.Vault{Path: t.TempDir(), Name: "test", IsActive: true}
	db.Create(&vault)

	// Create completed job with markdown
	mdDir := t.TempDir()
	md1 := "---\nid: bulk-1\ntitle: \"Bulk One\"\n---\n\n# Bulk One\n"
	mdPath1 := filepath.Join(mdDir, "t1.md")
	os.WriteFile(mdPath1, []byte(md1), 0644)

	title1 := "Bulk One"
	vaultID := vault.ID
	h.jobRepo.Create(context.Background(), &models.TranscriptionJob{
		ID:                    "bulk-1",
		Status:                models.StatusCompleted,
		Title:                 &title1,
		VaultID:               &vaultID,
		TranscriptMarkdownPath: &mdPath1,
	})

	// Create processing job (should be skipped)
	title2 := "In Progress"
	h.jobRepo.Create(context.Background(), &models.TranscriptionJob{
		ID:      "bulk-2",
		Status:  models.StatusProcessing,
		Title:   &title2,
		VaultID: &vaultID,
	})

	r := buildObsidianRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/obsidian/sync-all", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	synced := int(resp["synced"].(float64))
	total := int(resp["total"].(float64))
	if synced != 1 {
		t.Errorf("expected 1 synced, got %d", synced)
	}
	if total != 1 {
		t.Errorf("expected 1 total (completed only), got %d", total)
	}

	// Verify file exists in Obsidian vault
	entries, _ := os.ReadDir(filepath.Join(obsDir, "Quill"))
	if len(entries) != 1 {
		t.Errorf("expected 1 file in Quill dir, got %d", len(entries))
	}
}

// TestSaveObsidianConfig_SavesPath tests saving a vault path.
func TestSaveObsidianConfig_SavesPath(t *testing.T) {
	h, db, cleanup := setupObsidianHarness(t)
	defer cleanup()

	// Seed AppSetup record
	db.Create(&models.AppSetup{ID: 1, Completed: true, AuthMode: "local"})

	oldDB := swapTestDB(t, db)
	defer restoreTestDB(oldDB)

	obsDir := t.TempDir()
	body, _ := json.Marshal(map[string]string{"vault_path": obsDir})

	r := buildObsidianRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/obsidian/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Verify config was saved
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/obsidian/config", nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	var resp map[string]any
	json.NewDecoder(rec2.Body).Decode(&resp)
	if resp["vault_path"] == "" {
		t.Error("expected saved vault_path to be returned")
	}
}

// --- Test helpers for swapping the global database.DB ---

func swapTestDB(t *testing.T, db *gorm.DB) *gorm.DB {
	t.Helper()
	old := database.DB
	database.DB = db
	return old
}

func restoreTestDB(old *gorm.DB) {
	database.DB = old
}
