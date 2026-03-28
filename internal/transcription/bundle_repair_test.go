package transcription

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"quill/internal/models"
	"quill/internal/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupRepairTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.TranscriptionJob{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db
}

func TestAuditVault_HealthyBundle(t *testing.T) {
	db := setupRepairTestDB(t)
	repo := repository.NewJobRepository(db)
	ctx := context.Background()

	// Create a bundle on disk
	tmpDir := t.TempDir()
	transcriptsDir := filepath.Join(tmpDir, "Transcripts")
	bundleDir := filepath.Join(transcriptsDir, "test-bundle")
	os.MkdirAll(bundleDir, 0755)
	os.WriteFile(filepath.Join(bundleDir, "audio.mp3"), []byte("fake audio"), 0644)
	os.WriteFile(filepath.Join(bundleDir, "transcript.json"), []byte("{}"), 0644)

	// Create matching DB record
	audioPath := filepath.Join(bundleDir, "audio.mp3")
	jsonPath := filepath.Join(bundleDir, "transcript.json")
	job := &models.TranscriptionJob{
		ID:                 "test-1",
		Status:             models.StatusCompleted,
		AudioPath:          audioPath,
		ArtifactDir:        &bundleDir,
		TranscriptJSONPath: &jsonPath,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	svc := NewBundleRepairService(repo, transcriptsDir, "", nil)
	result, err := svc.AuditVault(ctx)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	if result.Healthy != 1 {
		t.Errorf("expected 1 healthy, got %d", result.Healthy)
	}
	if result.IssueCount != 0 {
		t.Errorf("expected 0 issues, got %d: %+v", result.IssueCount, result.Issues)
	}
}

func TestAuditVault_MissingAudio(t *testing.T) {
	db := setupRepairTestDB(t)
	repo := repository.NewJobRepository(db)
	ctx := context.Background()

	tmpDir := t.TempDir()
	bundleDir := filepath.Join(tmpDir, "bundle")
	os.MkdirAll(bundleDir, 0755)

	// DB points to non-existent audio
	job := &models.TranscriptionJob{
		ID:          "test-2",
		Status:      models.StatusCompleted,
		AudioPath:   filepath.Join(bundleDir, "audio.mp3"), // doesn't exist
		ArtifactDir: &bundleDir,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	svc := NewBundleRepairService(repo, tmpDir, "", nil)
	result, err := svc.AuditVault(ctx)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	if result.IssueCount == 0 {
		t.Fatal("expected at least one issue for missing audio")
	}
	found := false
	for _, issue := range result.Issues {
		if issue.Type == IssueMissingAudio {
			found = true
		}
	}
	if !found {
		t.Error("expected missing_audio issue")
	}
}

func TestRepairPaths_FixesMissingAudio(t *testing.T) {
	db := setupRepairTestDB(t)
	repo := repository.NewJobRepository(db)
	ctx := context.Background()

	tmpDir := t.TempDir()
	bundleDir := filepath.Join(tmpDir, "bundle")
	os.MkdirAll(bundleDir, 0755)

	// Audio exists in bundle but DB path is wrong
	os.WriteFile(filepath.Join(bundleDir, "audio.wav"), []byte("fake"), 0644)

	job := &models.TranscriptionJob{
		ID:          "test-3",
		Status:      models.StatusCompleted,
		AudioPath:   "/nonexistent/audio.wav",
		ArtifactDir: &bundleDir,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	svc := NewBundleRepairService(repo, tmpDir, "", nil)
	result, err := svc.RepairPaths(ctx)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}

	if result.Fixed != 1 {
		t.Errorf("expected 1 fixed, got %d", result.Fixed)
	}

	// Verify the path was updated
	updated, _ := repo.FindByID(ctx, "test-3")
	expected := filepath.Join(bundleDir, "audio.wav")
	if updated.AudioPath != expected {
		t.Errorf("expected audio path %s, got %s", expected, updated.AudioPath)
	}
}

func TestRepairPaths_FixesTranscriptPath(t *testing.T) {
	db := setupRepairTestDB(t)
	repo := repository.NewJobRepository(db)
	ctx := context.Background()

	tmpDir := t.TempDir()
	bundleDir := filepath.Join(tmpDir, "bundle")
	os.MkdirAll(bundleDir, 0755)

	// Audio is fine, but transcript path is broken
	audioPath := filepath.Join(bundleDir, "audio.mp3")
	os.WriteFile(audioPath, []byte("audio"), 0644)
	os.WriteFile(filepath.Join(bundleDir, "transcript.json"), []byte("{}"), 0644)

	badPath := "/wrong/transcript.json"
	job := &models.TranscriptionJob{
		ID:                 "test-4",
		Status:             models.StatusCompleted,
		AudioPath:          audioPath,
		ArtifactDir:        &bundleDir,
		TranscriptJSONPath: &badPath,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	svc := NewBundleRepairService(repo, tmpDir, "", nil)
	result, err := svc.RepairPaths(ctx)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}

	if result.Fixed != 1 {
		t.Errorf("expected 1 fixed, got %d", result.Fixed)
	}

	updated, _ := repo.FindByID(ctx, "test-4")
	expected := filepath.Join(bundleDir, "transcript.json")
	if updated.TranscriptJSONPath == nil || *updated.TranscriptJSONPath != expected {
		t.Errorf("expected transcript path %s, got %v", expected, updated.TranscriptJSONPath)
	}
}

func TestRepairPaths_NothingToRepair(t *testing.T) {
	db := setupRepairTestDB(t)
	repo := repository.NewJobRepository(db)
	ctx := context.Background()

	tmpDir := t.TempDir()
	bundleDir := filepath.Join(tmpDir, "bundle")
	os.MkdirAll(bundleDir, 0755)

	audioPath := filepath.Join(bundleDir, "audio.mp3")
	os.WriteFile(audioPath, []byte("audio"), 0644)

	job := &models.TranscriptionJob{
		ID:          "test-5",
		Status:      models.StatusCompleted,
		AudioPath:   audioPath,
		ArtifactDir: &bundleDir,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	svc := NewBundleRepairService(repo, tmpDir, "", nil)
	result, err := svc.RepairPaths(ctx)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}

	if result.Fixed != 0 {
		t.Errorf("expected 0 fixed, got %d", result.Fixed)
	}
	if result.Attempted != 0 {
		t.Errorf("expected 0 attempted, got %d", result.Attempted)
	}
}
