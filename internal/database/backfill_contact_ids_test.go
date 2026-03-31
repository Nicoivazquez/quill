package database

import (
	"testing"

	"quill/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newBackfillTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Vault{},
		&models.Contact{},
		&models.TranscriptionJob{},
		&models.SpeakerMapping{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func TestBackfillSpeakerMappingContactIDs_LinksOrphanedMappings(t *testing.T) {
	db := newBackfillTestDB(t)

	vault := models.Vault{Name: "V1", Path: "/tmp/v1", IsActive: true}
	db.Create(&vault)

	contact := models.Contact{VaultID: vault.ID, Name: "Alice", ContactUID: "alice-uid", Slug: "alice", SignatureStatus: "none"}
	db.Create(&contact)

	job := models.TranscriptionJob{ID: "job-1", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, VaultID: &vault.ID}
	db.Create(&job)

	// Orphaned mapping: custom_name matches contact, but contact_id is NULL
	mapping := models.SpeakerMapping{
		TranscriptionJobID: "job-1",
		OriginalSpeaker:    "speaker_0",
		CustomName:         "Alice",
		MatchSource:        "manual",
	}
	db.Create(&mapping)

	if err := backfillSpeakerMappingContactIDs(db); err != nil {
		t.Fatalf("backfill error: %v", err)
	}

	var updated models.SpeakerMapping
	db.First(&updated, mapping.ID)

	if updated.ContactID == nil {
		t.Fatal("expected contact_id to be set after backfill, got nil")
	}
	if *updated.ContactID != contact.ID {
		t.Errorf("contact_id: got %d, want %d", *updated.ContactID, contact.ID)
	}
}

func TestBackfillSpeakerMappingContactIDs_CaseInsensitive(t *testing.T) {
	db := newBackfillTestDB(t)

	vault := models.Vault{Name: "V1", Path: "/tmp/v1", IsActive: true}
	db.Create(&vault)

	contact := models.Contact{VaultID: vault.ID, Name: "Alice", ContactUID: "alice-uid", Slug: "alice", SignatureStatus: "none"}
	db.Create(&contact)

	job := models.TranscriptionJob{ID: "job-1", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, VaultID: &vault.ID}
	db.Create(&job)

	mapping := models.SpeakerMapping{
		TranscriptionJobID: "job-1",
		OriginalSpeaker:    "speaker_0",
		CustomName:         "alice", // lowercase
		MatchSource:        "manual",
	}
	db.Create(&mapping)

	if err := backfillSpeakerMappingContactIDs(db); err != nil {
		t.Fatalf("backfill error: %v", err)
	}

	var updated models.SpeakerMapping
	db.First(&updated, mapping.ID)

	if updated.ContactID == nil {
		t.Fatal("expected contact_id to be set for case-insensitive match, got nil")
	}
	if *updated.ContactID != contact.ID {
		t.Errorf("contact_id: got %d, want %d", *updated.ContactID, contact.ID)
	}
}

func TestBackfillSpeakerMappingContactIDs_SkipsRawBackfills(t *testing.T) {
	db := newBackfillTestDB(t)

	vault := models.Vault{Name: "V1", Path: "/tmp/v1", IsActive: true}
	db.Create(&vault)

	// Contact named "speaker_0" (edge case)
	contact := models.Contact{VaultID: vault.ID, Name: "speaker_0", ContactUID: "sp0-uid", Slug: "sp0", SignatureStatus: "none"}
	db.Create(&contact)

	job := models.TranscriptionJob{ID: "job-1", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, VaultID: &vault.ID}
	db.Create(&job)

	// Raw backfill: custom_name == original_speaker — should NOT be linked
	mapping := models.SpeakerMapping{
		TranscriptionJobID: "job-1",
		OriginalSpeaker:    "speaker_0",
		CustomName:         "speaker_0",
		MatchSource:        "auto",
		MatchTier:          "none",
	}
	db.Create(&mapping)

	if err := backfillSpeakerMappingContactIDs(db); err != nil {
		t.Fatalf("backfill error: %v", err)
	}

	var updated models.SpeakerMapping
	db.First(&updated, mapping.ID)

	if updated.ContactID != nil {
		t.Errorf("expected contact_id to remain nil for raw backfill, got %d", *updated.ContactID)
	}
}

func TestBackfillSpeakerMappingContactIDs_SkipsAlreadyLinked(t *testing.T) {
	db := newBackfillTestDB(t)

	vault := models.Vault{Name: "V1", Path: "/tmp/v1", IsActive: true}
	db.Create(&vault)

	contact1 := models.Contact{VaultID: vault.ID, Name: "Alice", ContactUID: "alice-uid", Slug: "alice", SignatureStatus: "none"}
	db.Create(&contact1)
	contact2 := models.Contact{VaultID: vault.ID, Name: "Bob", ContactUID: "bob-uid", Slug: "bob", SignatureStatus: "none"}
	db.Create(&contact2)

	job := models.TranscriptionJob{ID: "job-1", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, VaultID: &vault.ID}
	db.Create(&job)

	// Already linked to contact1
	cid := contact1.ID
	mapping := models.SpeakerMapping{
		TranscriptionJobID: "job-1",
		OriginalSpeaker:    "speaker_0",
		CustomName:         "Alice",
		ContactID:          &cid,
		MatchSource:        "auto",
	}
	db.Create(&mapping)

	if err := backfillSpeakerMappingContactIDs(db); err != nil {
		t.Fatalf("backfill error: %v", err)
	}

	var updated models.SpeakerMapping
	db.First(&updated, mapping.ID)

	// Should remain linked to contact1, not changed
	if updated.ContactID == nil || *updated.ContactID != contact1.ID {
		t.Errorf("expected contact_id to remain %d, got %v", contact1.ID, updated.ContactID)
	}
}

func TestBackfillSpeakerMappingContactIDs_Idempotent(t *testing.T) {
	db := newBackfillTestDB(t)

	vault := models.Vault{Name: "V1", Path: "/tmp/v1", IsActive: true}
	db.Create(&vault)

	contact := models.Contact{VaultID: vault.ID, Name: "Alice", ContactUID: "alice-uid", Slug: "alice", SignatureStatus: "none"}
	db.Create(&contact)

	job := models.TranscriptionJob{ID: "job-1", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, VaultID: &vault.ID}
	db.Create(&job)

	mapping := models.SpeakerMapping{
		TranscriptionJobID: "job-1",
		OriginalSpeaker:    "speaker_0",
		CustomName:         "Alice",
		MatchSource:        "manual",
	}
	db.Create(&mapping)

	// Run twice
	if err := backfillSpeakerMappingContactIDs(db); err != nil {
		t.Fatalf("first backfill error: %v", err)
	}
	if err := backfillSpeakerMappingContactIDs(db); err != nil {
		t.Fatalf("second backfill error: %v", err)
	}

	var updated models.SpeakerMapping
	db.First(&updated, mapping.ID)

	if updated.ContactID == nil || *updated.ContactID != contact.ID {
		t.Errorf("expected contact_id %d after idempotent backfill, got %v", contact.ID, updated.ContactID)
	}
}

func TestBackfillSpeakerMappingContactIDs_VaultScoped(t *testing.T) {
	db := newBackfillTestDB(t)

	vault1 := models.Vault{Name: "V1", Path: "/tmp/v1", IsActive: true}
	db.Create(&vault1)
	vault2 := models.Vault{Name: "V2", Path: "/tmp/v2"}
	db.Create(&vault2)

	// Alice in vault1 only
	contact := models.Contact{VaultID: vault1.ID, Name: "Alice", ContactUID: "alice-uid", Slug: "alice", SignatureStatus: "none"}
	db.Create(&contact)

	// Job in vault2
	job := models.TranscriptionJob{ID: "job-v2", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, VaultID: &vault2.ID}
	db.Create(&job)

	mapping := models.SpeakerMapping{
		TranscriptionJobID: "job-v2",
		OriginalSpeaker:    "speaker_0",
		CustomName:         "Alice",
		MatchSource:        "manual",
	}
	db.Create(&mapping)

	if err := backfillSpeakerMappingContactIDs(db); err != nil {
		t.Fatalf("backfill error: %v", err)
	}

	var updated models.SpeakerMapping
	db.First(&updated, mapping.ID)

	// Alice is in vault1, but job is in vault2 — should NOT be linked
	if updated.ContactID != nil {
		t.Errorf("expected contact_id nil (different vault), got %d", *updated.ContactID)
	}
}
