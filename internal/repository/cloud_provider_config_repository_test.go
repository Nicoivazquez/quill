package repository

import (
	"context"
	"testing"

	"quill/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newCloudProviderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.CloudProviderConfig{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func TestCloudProviderConfig_UpsertCreatesNew(t *testing.T) {
	db := newCloudProviderTestDB(t)
	repo := NewCloudProviderConfigRepository(db)
	ctx := context.Background()

	cfg := &models.CloudProviderConfig{
		Provider: "assemblyai",
		APIKey:   "aai-test-key",
		IsActive: true,
	}

	if err := repo.Upsert(ctx, cfg); err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}

	got, err := repo.GetByProvider(ctx, "assemblyai")
	if err != nil {
		t.Fatalf("GetByProvider returned error: %v", err)
	}
	if got.APIKey != "aai-test-key" {
		t.Errorf("expected api_key=%q, got %q", "aai-test-key", got.APIKey)
	}
	if !got.IsActive {
		t.Errorf("expected is_active=true, got false")
	}
}

func TestCloudProviderConfig_UpsertUpdatesExisting(t *testing.T) {
	db := newCloudProviderTestDB(t)
	repo := NewCloudProviderConfigRepository(db)
	ctx := context.Background()

	first := &models.CloudProviderConfig{
		Provider: "deepgram",
		APIKey:   "dg-original-key",
		IsActive: true,
	}
	if err := repo.Upsert(ctx, first); err != nil {
		t.Fatalf("first Upsert error: %v", err)
	}

	second := &models.CloudProviderConfig{
		Provider: "deepgram",
		APIKey:   "dg-updated-key",
		IsActive: true,
	}
	if err := repo.Upsert(ctx, second); err != nil {
		t.Fatalf("second Upsert error: %v", err)
	}

	var count int64
	db.Model(&models.CloudProviderConfig{}).Where("provider = ?", "deepgram").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 row for provider deepgram, got %d", count)
	}

	got, err := repo.GetByProvider(ctx, "deepgram")
	if err != nil {
		t.Fatalf("GetByProvider after upsert: %v", err)
	}
	if got.APIKey != "dg-updated-key" {
		t.Errorf("expected updated api_key=%q, got %q", "dg-updated-key", got.APIKey)
	}
}

func TestCloudProviderConfig_GetByProvider_ReturnsCorrectConfig(t *testing.T) {
	db := newCloudProviderTestDB(t)
	repo := NewCloudProviderConfigRepository(db)
	ctx := context.Background()

	for _, p := range []struct{ provider, key string }{
		{"assemblyai", "aai-key-1"},
		{"deepgram", "dg-key-2"},
		{"openai", "sk-key-3"},
	} {
		if err := repo.Upsert(ctx, &models.CloudProviderConfig{
			Provider: p.provider,
			APIKey:   p.key,
			IsActive: true,
		}); err != nil {
			t.Fatalf("Upsert %s: %v", p.provider, err)
		}
	}

	got, err := repo.GetByProvider(ctx, "deepgram")
	if err != nil {
		t.Fatalf("GetByProvider: %v", err)
	}
	if got.Provider != "deepgram" {
		t.Errorf("expected provider=deepgram, got %q", got.Provider)
	}
	if got.APIKey != "dg-key-2" {
		t.Errorf("expected api_key=dg-key-2, got %q", got.APIKey)
	}
}

func TestCloudProviderConfig_GetByProvider_NotFound(t *testing.T) {
	db := newCloudProviderTestDB(t)
	repo := NewCloudProviderConfigRepository(db)
	ctx := context.Background()

	_, err := repo.GetByProvider(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent provider, got nil")
	}
}

func TestCloudProviderConfig_ListActive_ReturnsOnlyActive(t *testing.T) {
	db := newCloudProviderTestDB(t)
	repo := NewCloudProviderConfigRepository(db)
	ctx := context.Background()

	if err := repo.Upsert(ctx, &models.CloudProviderConfig{
		Provider: "assemblyai",
		APIKey:   "aai-key",
		IsActive: true,
	}); err != nil {
		t.Fatalf("Upsert assemblyai: %v", err)
	}
	if err := repo.Upsert(ctx, &models.CloudProviderConfig{
		Provider: "deepgram",
		APIKey:   "dg-key",
		IsActive: false,
	}); err != nil {
		t.Fatalf("Upsert deepgram: %v", err)
	}

	list, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 active config, got %d", len(list))
	}
	if list[0].Provider != "assemblyai" {
		t.Errorf("expected assemblyai in active list, got %q", list[0].Provider)
	}
}

func TestCloudProviderConfig_ListActive_EmptyWhenNone(t *testing.T) {
	db := newCloudProviderTestDB(t)
	repo := NewCloudProviderConfigRepository(db)
	ctx := context.Background()

	list, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive on empty table: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d items", len(list))
	}
}

func TestCloudProviderConfig_Delete_RemovesConfig(t *testing.T) {
	db := newCloudProviderTestDB(t)
	repo := NewCloudProviderConfigRepository(db)
	ctx := context.Background()

	if err := repo.Upsert(ctx, &models.CloudProviderConfig{
		Provider: "assemblyai",
		APIKey:   "aai-key",
		IsActive: true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := repo.Delete(ctx, "assemblyai"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := repo.GetByProvider(ctx, "assemblyai")
	if err == nil {
		t.Fatal("expected error after deletion, got nil")
	}
}

func TestCloudProviderConfig_Delete_NonexistentIsNoOp(t *testing.T) {
	db := newCloudProviderTestDB(t)
	repo := NewCloudProviderConfigRepository(db)
	ctx := context.Background()

	// Deleting a provider that doesn't exist should not return an error.
	if err := repo.Delete(ctx, "nonexistent"); err != nil {
		t.Fatalf("expected no error deleting nonexistent provider, got: %v", err)
	}
}
