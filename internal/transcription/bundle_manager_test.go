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
)

func setupManagerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.SpeakerMapping{},
		&models.Summary{},
		&models.Note{},
		&models.Vault{},
	); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db
}

func TestBundleManager_StartStop(t *testing.T) {
	db := setupManagerTestDB(t)
	dir := t.TempDir()

	// Create a vault pointing at dir
	vault := models.Vault{Path: dir, IsActive: true}
	db.Create(&vault)

	mgr := NewBundleManager(db)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	mgr.Stop()
}

func TestBundleManager_StartWithNoVault(t *testing.T) {
	db := setupManagerTestDB(t)
	mgr := NewBundleManager(db)

	// Should not error even without an active vault
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	mgr.Stop()
}

func TestBundleManager_SwitchVault(t *testing.T) {
	db := setupManagerTestDB(t)
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// Create vault 1 with a bundle
	vault1 := models.Vault{Path: dir1, IsActive: true}
	db.Create(&vault1)
	bundleDir1 := filepath.Join(dir1, "Transcripts", "rec-1")
	os.MkdirAll(bundleDir1, 0o755)
	os.WriteFile(filepath.Join(bundleDir1, "audio.wav"), []byte("fake"), 0o644)
	WriteMetadata(bundleDir1, &BundleMetadata{
		ID: "mgr-test-1", Title: "Vault1 Rec", Status: "completed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	mgr := NewBundleManager(db)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer mgr.Stop()

	// Verify job was imported
	jobRepo := repository.NewJobRepository(db)
	job, err := jobRepo.FindByID(context.Background(), "mgr-test-1")
	if err != nil {
		t.Fatalf("expected job mgr-test-1 to be imported: %v", err)
	}
	if job.Title == nil || *job.Title != "Vault1 Rec" {
		t.Errorf("expected title 'Vault1 Rec', got %v", job.Title)
	}

	// Create vault 2 with a different bundle
	vault2 := models.Vault{Path: dir2, IsActive: false}
	db.Create(&vault2)
	bundleDir2 := filepath.Join(dir2, "Transcripts", "rec-2")
	os.MkdirAll(bundleDir2, 0o755)
	os.WriteFile(filepath.Join(bundleDir2, "audio.wav"), []byte("fake"), 0o644)
	WriteMetadata(bundleDir2, &BundleMetadata{
		ID: "mgr-test-2", Title: "Vault2 Rec", Status: "completed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	// Switch to vault 2
	if err := mgr.SwitchVault(context.Background(), vault2.ID, vault2.Path); err != nil {
		t.Fatalf("SwitchVault failed: %v", err)
	}

	// Verify vault 2 job was imported
	job2, err := jobRepo.FindByID(context.Background(), "mgr-test-2")
	if err != nil {
		t.Fatalf("expected job mgr-test-2 to be imported: %v", err)
	}
	if job2.Title == nil || *job2.Title != "Vault2 Rec" {
		t.Errorf("expected title 'Vault2 Rec', got %v", job2.Title)
	}
}

func TestBundleManager_ReindexActiveVault(t *testing.T) {
	db := setupManagerTestDB(t)
	dir := t.TempDir()

	vault := models.Vault{Path: dir, IsActive: true}
	db.Create(&vault)

	// Create a bundle
	bundleDir := filepath.Join(dir, "Transcripts", "reindex-rec")
	os.MkdirAll(bundleDir, 0o755)
	os.WriteFile(filepath.Join(bundleDir, "audio.wav"), []byte("fake"), 0o644)
	WriteMetadata(bundleDir, &BundleMetadata{
		ID: "reindex-1", Title: "Reindex Test", Status: "completed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	mgr := NewBundleManager(db)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer mgr.Stop()

	// Reindex should work and not error
	result, err := mgr.ReindexActiveVault(context.Background())
	if err != nil {
		t.Fatalf("ReindexActiveVault failed: %v", err)
	}

	// The bundle was already imported by Start, so reindex should skip it
	if result.Skipped != 1 {
		t.Errorf("expected 1 skipped on reindex, got %d (imported=%d updated=%d)", result.Skipped, result.Imported, result.Updated)
	}
}

func TestBundleManager_ReindexWithoutVault(t *testing.T) {
	db := setupManagerTestDB(t)
	mgr := NewBundleManager(db)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer mgr.Stop()

	result, err := mgr.ReindexActiveVault(context.Background())
	if err != nil {
		t.Fatalf("ReindexActiveVault without vault should not error: %v", err)
	}
	if result.Imported+result.Updated+result.Deleted+result.Skipped != 0 {
		t.Errorf("expected empty result without vault, got %+v", result)
	}
}

func TestBundleManager_SyncServiceAccessor(t *testing.T) {
	db := setupManagerTestDB(t)
	dir := t.TempDir()

	vault := models.Vault{Path: dir, IsActive: true}
	db.Create(&vault)

	mgr := NewBundleManager(db)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer mgr.Stop()

	// SyncService should be available
	svc := mgr.SyncService()
	if svc == nil {
		t.Error("expected non-nil SyncService after Start with active vault")
	}
}

func TestBundleManager_SyncServiceNilWithoutVault(t *testing.T) {
	db := setupManagerTestDB(t)
	mgr := NewBundleManager(db)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer mgr.Stop()

	svc := mgr.SyncService()
	if svc != nil {
		t.Error("expected nil SyncService without active vault")
	}
}
