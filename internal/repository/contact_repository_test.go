package repository

import (
	"context"
	"testing"

	"quill/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newContactTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Vault{}, &models.Contact{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func seedVault(t *testing.T, db *gorm.DB, name string) models.Vault {
	t.Helper()
	v := models.Vault{Name: name, Path: "/tmp/" + name, IsActive: true}
	if err := db.Create(&v).Error; err != nil {
		t.Fatalf("seed vault %q: %v", name, err)
	}
	return v
}

func seedContact(t *testing.T, db *gorm.DB, vaultID uint, name string) models.Contact {
	t.Helper()
	c := models.Contact{VaultID: vaultID, Name: name, ContactUID: name + "-uid", Slug: name + "-slug", SignatureStatus: "none"}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("seed contact %q: %v", name, err)
	}
	return c
}

func TestFindByNameInVault_ExactMatch(t *testing.T) {
	db := newContactTestDB(t)
	repo := NewContactRepository(db)
	ctx := context.Background()

	vault := seedVault(t, db, "v1")
	alice := seedContact(t, db, vault.ID, "Alice")

	got, err := repo.FindByNameInVault(ctx, vault.ID, "Alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != alice.ID {
		t.Errorf("got ID %d, want %d", got.ID, alice.ID)
	}
}

func TestFindByNameInVault_CaseInsensitive(t *testing.T) {
	db := newContactTestDB(t)
	repo := NewContactRepository(db)
	ctx := context.Background()

	vault := seedVault(t, db, "v1")
	alice := seedContact(t, db, vault.ID, "Alice")

	got, err := repo.FindByNameInVault(ctx, vault.ID, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != alice.ID {
		t.Errorf("got ID %d, want %d", got.ID, alice.ID)
	}
}

func TestFindByNameInVault_TrimWhitespace(t *testing.T) {
	db := newContactTestDB(t)
	repo := NewContactRepository(db)
	ctx := context.Background()

	vault := seedVault(t, db, "v1")
	alice := seedContact(t, db, vault.ID, "Alice")

	got, err := repo.FindByNameInVault(ctx, vault.ID, "  Alice  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != alice.ID {
		t.Errorf("got ID %d, want %d", got.ID, alice.ID)
	}
}

func TestFindByNameInVault_NoMatch(t *testing.T) {
	db := newContactTestDB(t)
	repo := NewContactRepository(db)
	ctx := context.Background()

	vault := seedVault(t, db, "v1")
	seedContact(t, db, vault.ID, "Alice")

	_, err := repo.FindByNameInVault(ctx, vault.ID, "Bob")
	if err == nil {
		t.Fatal("expected error for non-existent name, got nil")
	}
}

func TestFindByNameInVault_VaultScoped(t *testing.T) {
	db := newContactTestDB(t)
	repo := NewContactRepository(db)
	ctx := context.Background()

	vault1 := seedVault(t, db, "v1")
	vault2 := seedVault(t, db, "v2")
	seedContact(t, db, vault1.ID, "Alice")

	// Alice exists in vault1 but not vault2
	_, err := repo.FindByNameInVault(ctx, vault2.ID, "Alice")
	if err == nil {
		t.Fatal("expected error: Alice should not be found in vault2")
	}
}
