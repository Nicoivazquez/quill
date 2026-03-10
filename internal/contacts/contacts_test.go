package contacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"quill/internal/models"
	"quill/internal/repository"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func setupContactsDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "contacts-test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Vault{}, &models.Contact{}); err != nil {
		t.Fatalf("auto-migrate test db: %v", err)
	}
	return db, root
}

func createVault(t *testing.T, db *gorm.DB, vaultPath string, active bool) models.Vault {
	t.Helper()
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatalf("create vault path: %v", err)
	}
	vault := models.Vault{
		Name:     "Test Vault",
		Path:     vaultPath,
		IsActive: active,
	}
	if err := db.Create(&vault).Error; err != nil {
		t.Fatalf("create vault row: %v", err)
	}
	return vault
}

func TestBackfillContactsFileFirst_MaterializesLegacyContactAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, root := setupContactsDB(t)
	vault := createVault(t, db, filepath.Join(root, "vault"), true)

	legacy := models.Contact{
		Name: "Legacy Contact",
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy contact: %v", err)
	}

	if err := BackfillContactsFileFirst(ctx, db); err != nil {
		t.Fatalf("backfill contacts: %v", err)
	}

	var first models.Contact
	if err := db.First(&first, legacy.ID).Error; err != nil {
		t.Fatalf("load backfilled contact: %v", err)
	}
	if first.VaultID != vault.ID {
		t.Fatalf("expected vault_id=%d, got %d", vault.ID, first.VaultID)
	}
	if first.ContactUID == "" {
		t.Fatalf("expected contact_uid to be populated")
	}
	if first.NotePath == "" {
		t.Fatalf("expected note_path to be populated")
	}
	if first.FileMtimeNS <= 0 {
		t.Fatalf("expected file_mtime_ns > 0")
	}
	if first.SignatureStatus != "none" {
		t.Fatalf("expected signature_status=none, got %q", first.SignatureStatus)
	}

	noteAbs := NewFileService(vault.Path).ResolveAbsPath(first.NotePath)
	if _, err := os.Stat(noteAbs); err != nil {
		t.Fatalf("expected contact markdown to exist at %s: %v", noteAbs, err)
	}

	if err := BackfillContactsFileFirst(ctx, db); err != nil {
		t.Fatalf("second backfill contacts: %v", err)
	}

	var second models.Contact
	if err := db.First(&second, legacy.ID).Error; err != nil {
		t.Fatalf("load second backfilled contact: %v", err)
	}
	if second.ContactUID != first.ContactUID {
		t.Fatalf("expected contact_uid to stay stable, first=%q second=%q", first.ContactUID, second.ContactUID)
	}
	if second.NotePath != first.NotePath {
		t.Fatalf("expected note_path to stay stable, first=%q second=%q", first.NotePath, second.NotePath)
	}
}

func TestSyncService_RoundTripExternalEditAndDelete(t *testing.T) {
	ctx := context.Background()
	db, root := setupContactsDB(t)
	vault := createVault(t, db, filepath.Join(root, "vault"), true)

	repo := repository.NewContactRepository(db)
	fileService := NewFileService(vault.Path)
	syncService := NewSyncService(fileService, repo, vault.ID)

	contact := models.Contact{
		VaultID:         vault.ID,
		ContactUID:      uuid.NewString(),
		Slug:            "john-example",
		Name:            "John Example",
		SignatureStatus: "none",
	}
	if err := repo.Create(ctx, &contact); err != nil {
		t.Fatalf("create contact row: %v", err)
	}
	if err := syncService.WriteContactToFile(ctx, &contact); err != nil {
		t.Fatalf("write contact file: %v", err)
	}

	readFromFile, err := fileService.ReadContactFromNotePath(fileService.ResolveAbsPath(contact.NotePath))
	if err != nil {
		t.Fatalf("read canonical note: %v", err)
	}
	readFromFile.Name = "Jane Example"
	n := "updated externally from markdown"
	readFromFile.Notes = &n
	if err := fileService.WriteContact(readFromFile); err != nil {
		t.Fatalf("simulate external contact edit: %v", err)
	}

	result, err := syncService.SyncFromFilesystem(ctx)
	if err != nil {
		t.Fatalf("sync after external edit: %v", err)
	}
	if result.Updated < 1 {
		t.Fatalf("expected at least one updated contact, got %+v", result)
	}

	updated, err := repo.GetByUID(ctx, vault.ID, contact.ContactUID)
	if err != nil {
		t.Fatalf("load updated contact: %v", err)
	}
	if updated.Name != "Jane Example" {
		t.Fatalf("expected synced name %q, got %q", "Jane Example", updated.Name)
	}
	if updated.Notes == nil || *updated.Notes != n {
		t.Fatalf("expected synced notes %q, got %v", n, updated.Notes)
	}

	if err := fileService.DeleteContactFolder(updated); err != nil {
		t.Fatalf("delete contact folder: %v", err)
	}
	deleteResult, err := syncService.SyncFromFilesystem(ctx)
	if err != nil {
		t.Fatalf("sync after folder delete: %v", err)
	}
	if deleteResult.Deleted < 1 {
		t.Fatalf("expected at least one deleted contact, got %+v", deleteResult)
	}

	_, err = repo.GetByID(ctx, updated.ID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected contact row to be hard-deleted, got err=%v", err)
	}
}

func TestSyncService_SelfWriteSuppression(t *testing.T) {
	ctx := context.Background()
	db, root := setupContactsDB(t)
	vault := createVault(t, db, filepath.Join(root, "vault"), true)

	repo := repository.NewContactRepository(db)
	fileService := NewFileService(vault.Path)
	syncService := NewSyncService(fileService, repo, vault.ID)

	contact := models.Contact{
		VaultID:         vault.ID,
		ContactUID:      uuid.NewString(),
		Slug:            "self-write",
		Name:            "Self Write",
		SignatureStatus: "none",
	}
	if err := repo.Create(ctx, &contact); err != nil {
		t.Fatalf("create contact row: %v", err)
	}
	if err := syncService.WriteContactToFile(ctx, &contact); err != nil {
		t.Fatalf("write contact file: %v", err)
	}

	result, err := syncService.SyncFromFilesystem(ctx)
	if err != nil {
		t.Fatalf("sync after self-write: %v", err)
	}
	if result.Imported != 0 || result.Updated != 0 || result.Deleted != 0 {
		t.Fatalf("expected no data mutations from self-write sync, got %+v", result)
	}
}

func TestWatcher_SyncsExternalContactEdits(t *testing.T) {
	ctx := context.Background()
	db, root := setupContactsDB(t)
	vault := createVault(t, db, filepath.Join(root, "vault"), true)

	repo := repository.NewContactRepository(db)
	fileService := NewFileService(vault.Path)
	syncService := NewSyncService(fileService, repo, vault.ID)

	contact := models.Contact{
		VaultID:         vault.ID,
		ContactUID:      uuid.NewString(),
		Slug:            "watch-target",
		Name:            "Watch Target",
		SignatureStatus: "none",
	}
	if err := repo.Create(ctx, &contact); err != nil {
		t.Fatalf("create contact row: %v", err)
	}
	if err := syncService.WriteContactToFile(ctx, &contact); err != nil {
		t.Fatalf("write contact file: %v", err)
	}

	watcher := NewWatcher(syncService, fileService)
	if err := watcher.Start(); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	defer func() {
		if err := watcher.Stop(); err != nil {
			t.Fatalf("stop watcher: %v", err)
		}
	}()

	time.Sleep(20 * time.Millisecond)
	parsed, err := fileService.ReadContactFromNotePath(fileService.ResolveAbsPath(contact.NotePath))
	if err != nil {
		t.Fatalf("read contact note for external update: %v", err)
	}
	parsed.Name = "Watch Target Updated"
	if err := fileService.WriteContact(parsed); err != nil {
		t.Fatalf("write external contact update: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := repo.GetByUID(ctx, vault.ID, contact.ContactUID)
		if getErr == nil && current.Name == "Watch Target Updated" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	current, err := repo.GetByUID(ctx, vault.ID, contact.ContactUID)
	if err != nil {
		t.Fatalf("load contact after watcher sync wait: %v", err)
	}
	t.Fatalf("watcher did not sync external edit before timeout, current name=%q", current.Name)
}

func TestSyncService_VaultIsolation(t *testing.T) {
	ctx := context.Background()
	db, root := setupContactsDB(t)

	vaultA := createVault(t, db, filepath.Join(root, "vault-a"), true)
	vaultB := createVault(t, db, filepath.Join(root, "vault-b"), false)

	repo := repository.NewContactRepository(db)
	fileServiceA := NewFileService(vaultA.Path)
	fileServiceB := NewFileService(vaultB.Path)
	syncA := NewSyncService(fileServiceA, repo, vaultA.ID)
	syncB := NewSyncService(fileServiceB, repo, vaultB.ID)

	contactA := models.Contact{
		VaultID:         vaultA.ID,
		ContactUID:      uuid.NewString(),
		Slug:            "vault-a-contact",
		Name:            "Vault A Contact",
		SignatureStatus: "none",
	}
	if err := repo.Create(ctx, &contactA); err != nil {
		t.Fatalf("create vault A contact: %v", err)
	}
	if err := syncA.WriteContactToFile(ctx, &contactA); err != nil {
		t.Fatalf("write vault A contact: %v", err)
	}

	contactB := models.Contact{
		VaultID:         vaultB.ID,
		ContactUID:      uuid.NewString(),
		Slug:            "vault-b-contact",
		Name:            "Vault B Contact",
		SignatureStatus: "none",
	}
	if err := repo.Create(ctx, &contactB); err != nil {
		t.Fatalf("create vault B contact: %v", err)
	}
	if err := syncB.WriteContactToFile(ctx, &contactB); err != nil {
		t.Fatalf("write vault B contact: %v", err)
	}

	if err := fileServiceA.DeleteContactFolder(&contactA); err != nil {
		t.Fatalf("delete vault A folder: %v", err)
	}
	resultA, err := syncA.SyncFromFilesystem(ctx)
	if err != nil {
		t.Fatalf("sync vault A: %v", err)
	}
	if resultA.Deleted < 1 {
		t.Fatalf("expected at least one delete in vault A sync, got %+v", resultA)
	}

	if _, err := repo.GetByUID(ctx, vaultB.ID, contactB.ContactUID); err != nil {
		t.Fatalf("expected vault B contact to remain isolated, err=%v", err)
	}
}
