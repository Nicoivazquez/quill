package folderwatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"quill/internal/config"
	"quill/internal/database"
	"quill/internal/models"
	"quill/internal/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type noopTaskQueue struct{}

func (noopTaskQueue) EnqueueJob(string) error {
	return nil
}

func TestImportFileAssignsActiveVaultImmediately(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "folderwatch.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}, &models.TranscriptionJob{}, &models.TranscriptionProfile{}, &models.Vault{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	user := models.User{
		Username:                 "local",
		Password:                 "secret",
		AutoTranscriptionEnabled: false,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	activeVault := models.Vault{
		Name:     "AudioVault",
		Path:     filepath.Join(root, "vault"),
		IsActive: true,
	}
	if err := db.Create(&activeVault).Error; err != nil {
		t.Fatalf("create vault: %v", err)
	}

	prevDB := database.DB
	database.DB = db
	defer func() {
		database.DB = prevDB
	}()

	uploadDir := filepath.Join(root, "uploads")
	service := NewService(
		&config.Config{UploadDir: uploadDir},
		nil,
		repository.NewJobRepository(db),
		repository.NewUserRepository(db),
		repository.NewProfileRepository(db),
		noopTaskQueue{},
	)

	sourcePath := filepath.Join(root, "sample.wav")
	if err := os.WriteFile(sourcePath, []byte("test-audio"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	if err := service.importFile(context.Background(), user.ID, sourcePath); err != nil {
		t.Fatalf("import file: %v", err)
	}

	jobs, total, err := service.jobRepo.List(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if total != 1 || len(jobs) != 1 {
		t.Fatalf("expected exactly one job, got total=%d len=%d", total, len(jobs))
	}
	if jobs[0].VaultID == nil {
		t.Fatalf("expected imported job to have active vault assigned")
	}
	if *jobs[0].VaultID != activeVault.ID {
		t.Fatalf("expected vault_id=%d, got %d", activeVault.ID, *jobs[0].VaultID)
	}
	if jobs[0].Status != models.StatusUploaded {
		t.Fatalf("expected uploaded status, got %s", jobs[0].Status)
	}
}
