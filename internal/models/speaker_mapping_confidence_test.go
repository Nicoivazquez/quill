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

// TestSpeakerMapping_ContactIDPersistedAndRetrieved verifies that a non-nil
// ContactID is stored and round-tripped correctly through GORM/SQLite.
func TestSpeakerMapping_ContactIDPersistedAndRetrieved(t *testing.T) {
	db := setupSpeakerMappingTestDB(t)

	job := &TranscriptionJob{ID: "job-contact-id", AudioPath: "/tmp/e.wav", Status: StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	contactID := uint(42)
	mapping := &SpeakerMapping{
		TranscriptionJobID: "job-contact-id",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Eve",
		ContactID:          &contactID,
		ConfidenceScore:    0.85,
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

	if loaded.ContactID == nil {
		t.Fatal("ContactID: got nil, want non-nil pointer")
	}
	if *loaded.ContactID != contactID {
		t.Errorf("ContactID: got %d, want %d", *loaded.ContactID, contactID)
	}
}

// TestSpeakerMapping_NilContactIDPersistedAsNil verifies that a nil ContactID
// (manual mapping without contact link) round-trips as nil.
func TestSpeakerMapping_NilContactIDPersistedAsNil(t *testing.T) {
	db := setupSpeakerMappingTestDB(t)

	job := &TranscriptionJob{ID: "job-nil-contact", AudioPath: "/tmp/f.wav", Status: StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	mapping := &SpeakerMapping{
		TranscriptionJobID: "job-nil-contact",
		OriginalSpeaker:    "speaker_01",
		CustomName:         "Frank",
		ContactID:          nil,
		MatchSource:        "manual",
	}

	if err := db.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	var loaded SpeakerMapping
	if err := db.First(&loaded, mapping.ID).Error; err != nil {
		t.Fatalf("load mapping: %v", err)
	}

	if loaded.ContactID != nil {
		t.Errorf("ContactID: got %v, want nil", loaded.ContactID)
	}
}

// TestSpeakerMapping_ReviewStatusTransitions_PendingToAccepted verifies that
// ReviewStatus can transition from "" through "pending" to "accepted".
func TestSpeakerMapping_ReviewStatusTransitions_PendingToAccepted(t *testing.T) {
	db := setupSpeakerMappingTestDB(t)

	job := &TranscriptionJob{ID: "job-review-accepted", AudioPath: "/tmp/g.wav", Status: StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	// Start with no review status (auto-assign scenario).
	mapping := &SpeakerMapping{
		TranscriptionJobID: "job-review-accepted",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Grace",
		MatchTier:          "suggest",
		ReviewStatus:       "",
	}
	if err := db.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	// Transition to "pending".
	if err := db.Model(mapping).Update("review_status", "pending").Error; err != nil {
		t.Fatalf("update to pending: %v", err)
	}
	var afterPending SpeakerMapping
	if err := db.First(&afterPending, mapping.ID).Error; err != nil {
		t.Fatalf("load after pending: %v", err)
	}
	if afterPending.ReviewStatus != "pending" {
		t.Errorf("after pending: got %q, want %q", afterPending.ReviewStatus, "pending")
	}

	// Transition to "accepted".
	if err := db.Model(mapping).Update("review_status", "accepted").Error; err != nil {
		t.Fatalf("update to accepted: %v", err)
	}
	var afterAccepted SpeakerMapping
	if err := db.First(&afterAccepted, mapping.ID).Error; err != nil {
		t.Fatalf("load after accepted: %v", err)
	}
	if afterAccepted.ReviewStatus != "accepted" {
		t.Errorf("after accepted: got %q, want %q", afterAccepted.ReviewStatus, "accepted")
	}
}

// TestSpeakerMapping_ReviewStatusTransitions_PendingToDismissed verifies that
// ReviewStatus can transition from "" through "pending" to "dismissed".
func TestSpeakerMapping_ReviewStatusTransitions_PendingToDismissed(t *testing.T) {
	db := setupSpeakerMappingTestDB(t)

	job := &TranscriptionJob{ID: "job-review-dismissed", AudioPath: "/tmp/h.wav", Status: StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	mapping := &SpeakerMapping{
		TranscriptionJobID: "job-review-dismissed",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Heidi",
		MatchTier:          "suggest",
		ReviewStatus:       "",
	}
	if err := db.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	if err := db.Model(mapping).Update("review_status", "pending").Error; err != nil {
		t.Fatalf("update to pending: %v", err)
	}

	if err := db.Model(mapping).Update("review_status", "dismissed").Error; err != nil {
		t.Fatalf("update to dismissed: %v", err)
	}
	var loaded SpeakerMapping
	if err := db.First(&loaded, mapping.ID).Error; err != nil {
		t.Fatalf("load mapping: %v", err)
	}
	if loaded.ReviewStatus != "dismissed" {
		t.Errorf("ReviewStatus: got %q, want %q", loaded.ReviewStatus, "dismissed")
	}
}

// TestSpeakerMapping_MultipleTiersForSameJob verifies that a single job can
// have mappings with different MatchTier values and they all persist correctly.
func TestSpeakerMapping_MultipleTiersForSameJob(t *testing.T) {
	db := setupSpeakerMappingTestDB(t)

	job := &TranscriptionJob{ID: "job-multi-tier", AudioPath: "/tmp/i.wav", Status: StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	cases := []struct {
		speaker      string
		customName   string
		tier         string
		source       string
		reviewStatus string
		score        float64
	}{
		{"speaker_00", "Ivan", "auto", "auto", "", 0.91},
		{"speaker_01", "Judy", "suggest", "auto", "pending", 0.70},
		{"speaker_02", "Mallory", "unknown", "auto", "", 0.45},
		{"speaker_03", "Oscar", "", "manual", "", 0.0},
	}

	for _, tc := range cases {
		m := &SpeakerMapping{
			TranscriptionJobID: "job-multi-tier",
			OriginalSpeaker:    tc.speaker,
			CustomName:         tc.customName,
			MatchTier:          tc.tier,
			MatchSource:        tc.source,
			ReviewStatus:       tc.reviewStatus,
			ConfidenceScore:    tc.score,
		}
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("create mapping %s: %v", tc.speaker, err)
		}
	}

	var loaded []SpeakerMapping
	if err := db.Where("transcription_job_id = ?", "job-multi-tier").
		Order("original_speaker ASC").Find(&loaded).Error; err != nil {
		t.Fatalf("list mappings: %v", err)
	}

	if len(loaded) != len(cases) {
		t.Fatalf("expected %d mappings, got %d", len(cases), len(loaded))
	}

	for i, tc := range cases {
		got := loaded[i]
		if got.MatchTier != tc.tier {
			t.Errorf("[%s] MatchTier: got %q, want %q", tc.speaker, got.MatchTier, tc.tier)
		}
		if got.ReviewStatus != tc.reviewStatus {
			t.Errorf("[%s] ReviewStatus: got %q, want %q", tc.speaker, got.ReviewStatus, tc.reviewStatus)
		}
		if got.ConfidenceScore != tc.score {
			t.Errorf("[%s] ConfidenceScore: got %f, want %f", tc.speaker, got.ConfidenceScore, tc.score)
		}
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
