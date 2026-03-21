package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestSpeakerMapping_ConfidenceFields verifies that the SpeakerMapping model
// can round-trip the new confidence tracking fields through GORM/SQLite.
func TestSpeakerMapping_ConfidenceFields(t *testing.T) {
	db := setupSpeakerMappingTestDB(t)

	// Seed a minimal TranscriptionJob so the foreign key is satisfied.
	job := &TranscriptionJob{ID: "job-conf-test", AudioPath: "/tmp/a.wav", Status: StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	mapping := &SpeakerMapping{
		TranscriptionJobID: "job-conf-test",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Alice",
		ConfidenceScore:    0.92,
		MatchSource:        "auto",
		MatchTier:          "auto",
	}

	if err := db.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	var loaded SpeakerMapping
	if err := db.First(&loaded, mapping.ID).Error; err != nil {
		t.Fatalf("load mapping: %v", err)
	}

	if loaded.ConfidenceScore != 0.92 {
		t.Errorf("ConfidenceScore: got %f, want 0.92", loaded.ConfidenceScore)
	}
	if loaded.MatchSource != "auto" {
		t.Errorf("MatchSource: got %q, want %q", loaded.MatchSource, "auto")
	}
	if loaded.MatchTier != "auto" {
		t.Errorf("MatchTier: got %q, want %q", loaded.MatchTier, "auto")
	}
}

// TestSpeakerMapping_ManualSource verifies that manually created mappings
// persist MatchSource="manual" with zero confidence and empty tier.
func TestSpeakerMapping_ManualSource(t *testing.T) {
	db := setupSpeakerMappingTestDB(t)

	job := &TranscriptionJob{ID: "job-manual-test", AudioPath: "/tmp/b.wav", Status: StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	mapping := &SpeakerMapping{
		TranscriptionJobID: "job-manual-test",
		OriginalSpeaker:    "speaker_01",
		CustomName:         "Bob",
		ConfidenceScore:    0.0,
		MatchSource:        "manual",
		MatchTier:          "",
	}

	if err := db.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	var loaded SpeakerMapping
	if err := db.First(&loaded, mapping.ID).Error; err != nil {
		t.Fatalf("load mapping: %v", err)
	}

	if loaded.MatchSource != "manual" {
		t.Errorf("MatchSource: got %q, want %q", loaded.MatchSource, "manual")
	}
	if loaded.ConfidenceScore != 0.0 {
		t.Errorf("ConfidenceScore: got %f, want 0.0", loaded.ConfidenceScore)
	}
}

// TestSpeakerMapping_SuggestionPromotedSource verifies storage of promoted suggestions.
func TestSpeakerMapping_SuggestionPromotedSource(t *testing.T) {
	db := setupSpeakerMappingTestDB(t)

	job := &TranscriptionJob{ID: "job-promoted-test", AudioPath: "/tmp/c.wav", Status: StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	mapping := &SpeakerMapping{
		TranscriptionJobID: "job-promoted-test",
		OriginalSpeaker:    "speaker_02",
		CustomName:         "Carol",
		ConfidenceScore:    0.73,
		MatchSource:        "suggestion_promoted",
		MatchTier:          "suggest",
	}

	if err := db.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	var loaded SpeakerMapping
	if err := db.First(&loaded, mapping.ID).Error; err != nil {
		t.Fatalf("load mapping: %v", err)
	}

	if loaded.MatchSource != "suggestion_promoted" {
		t.Errorf("MatchSource: got %q, want %q", loaded.MatchSource, "suggestion_promoted")
	}
	if loaded.ConfidenceScore != 0.73 {
		t.Errorf("ConfidenceScore: got %f, want 0.73", loaded.ConfidenceScore)
	}
	if loaded.MatchTier != "suggest" {
		t.Errorf("MatchTier: got %q, want %q", loaded.MatchTier, "suggest")
	}
}

// TestSpeakerMapping_ConfidenceFieldDefaults verifies that old rows (without
// the new columns set) default to zero/empty values.
func TestSpeakerMapping_ConfidenceFieldDefaults(t *testing.T) {
	db := setupSpeakerMappingTestDB(t)

	job := &TranscriptionJob{ID: "job-defaults", AudioPath: "/tmp/d.wav", Status: StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	// Create with only the legacy fields set — simulate a pre-migration row.
	mapping := &SpeakerMapping{
		TranscriptionJobID: "job-defaults",
		OriginalSpeaker:    "speaker_03",
		CustomName:         "Dan",
	}

	if err := db.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	var loaded SpeakerMapping
	if err := db.First(&loaded, mapping.ID).Error; err != nil {
		t.Fatalf("load mapping: %v", err)
	}

	if loaded.ConfidenceScore != 0.0 {
		t.Errorf("default ConfidenceScore: got %f, want 0.0", loaded.ConfidenceScore)
	}
	if loaded.MatchSource != "" {
		t.Errorf("default MatchSource: got %q, want empty string", loaded.MatchSource)
	}
	if loaded.MatchTier != "" {
		t.Errorf("default MatchTier: got %q, want empty string", loaded.MatchTier)
	}
}

// --- helpers ---

func setupSpeakerMappingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&TranscriptionJob{}, &SpeakerMapping{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}
