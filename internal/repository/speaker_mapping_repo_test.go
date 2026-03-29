package repository

import (
	"context"
	"testing"

	"quill/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newSpeakerMappingTestDB opens a fresh in-memory SQLite DB and migrates the
// tables required by SpeakerMappingRepository tests.
func newSpeakerMappingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.TranscriptionJob{}, &models.SpeakerMapping{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedJob inserts a minimal TranscriptionJob so foreign-key constraints are met.
func seedJob(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	job := &models.TranscriptionJob{ID: id, AudioPath: "/tmp/audio.wav", Status: models.StatusUploaded}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job %q: %v", id, err)
	}
}

// TestListPendingSuggestions_ReturnsSuggestTierPending verifies that only
// suggest-tier mappings with review_status="pending" are returned.
func TestListPendingSuggestions_ReturnsSuggestTierPending(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-list-pending")

	mappings := []models.SpeakerMapping{
		// Should be returned: suggest tier, pending review.
		{TranscriptionJobID: "job-list-pending", OriginalSpeaker: "speaker_00", CustomName: "Alice",
			MatchTier: "suggest", ReviewStatus: "pending", ConfidenceScore: 0.72},
		// Should be returned: suggest tier, pending review.
		{TranscriptionJobID: "job-list-pending", OriginalSpeaker: "speaker_01", CustomName: "Bob",
			MatchTier: "suggest", ReviewStatus: "pending", ConfidenceScore: 0.65},
		// Should NOT be returned: auto tier, no review status.
		{TranscriptionJobID: "job-list-pending", OriginalSpeaker: "speaker_02", CustomName: "Carol",
			MatchTier: "auto", ReviewStatus: "", ConfidenceScore: 0.91},
		// Should NOT be returned: already accepted.
		{TranscriptionJobID: "job-list-pending", OriginalSpeaker: "speaker_03", CustomName: "Dan",
			MatchTier: "suggest", ReviewStatus: "accepted", ConfidenceScore: 0.68},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	got, err := repo.ListPendingSuggestions(ctx, "job-list-pending")
	if err != nil {
		t.Fatalf("ListPendingSuggestions: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 pending suggestions, got %d", len(got))
	}

	names := map[string]bool{}
	for _, m := range got {
		names[m.CustomName] = true
		if m.ReviewStatus != "pending" {
			t.Errorf("mapping %q: expected review_status=pending, got %q", m.CustomName, m.ReviewStatus)
		}
	}
	if !names["Alice"] || !names["Bob"] {
		t.Errorf("expected Alice and Bob in results, got %v", names)
	}
}

// TestListPendingSuggestions_ExcludesDismissed verifies that dismissed
// suggestions are excluded from the result set.
func TestListPendingSuggestions_ExcludesDismissed(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-dismissed")

	mappings := []models.SpeakerMapping{
		{TranscriptionJobID: "job-dismissed", OriginalSpeaker: "speaker_00", CustomName: "Eve",
			MatchTier: "suggest", ReviewStatus: "pending"},
		{TranscriptionJobID: "job-dismissed", OriginalSpeaker: "speaker_01", CustomName: "Frank",
			MatchTier: "suggest", ReviewStatus: "dismissed"},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	got, err := repo.ListPendingSuggestions(ctx, "job-dismissed")
	if err != nil {
		t.Fatalf("ListPendingSuggestions: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 pending suggestion (dismissed excluded), got %d", len(got))
	}
	if got[0].CustomName != "Eve" {
		t.Errorf("expected Eve, got %q", got[0].CustomName)
	}
}

// TestListPendingSuggestions_ReturnsEmptyForJobWithNone verifies that an
// empty slice (not an error) is returned when no pending suggestions exist.
func TestListPendingSuggestions_ReturnsEmptyForJobWithNone(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-no-pending")

	// Seed only an auto-tier mapping with no review status.
	m := models.SpeakerMapping{
		TranscriptionJobID: "job-no-pending",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Grace",
		MatchTier:          "auto",
		ReviewStatus:       "",
	}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	got, err := repo.ListPendingSuggestions(ctx, "job-no-pending")
	if err != nil {
		t.Fatalf("ListPendingSuggestions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d items", len(got))
	}
}

// TestCountPendingSuggestions_CorrectCountsPerJob verifies that counts are
// correctly aggregated per job.
func TestCountPendingSuggestions_CorrectCountsPerJob(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-count-a")
	seedJob(t, db, "job-count-b")

	mappings := []models.SpeakerMapping{
		// job-count-a: 3 pending.
		{TranscriptionJobID: "job-count-a", OriginalSpeaker: "speaker_00", CustomName: "Alice", ReviewStatus: "pending"},
		{TranscriptionJobID: "job-count-a", OriginalSpeaker: "speaker_01", CustomName: "Bob", ReviewStatus: "pending"},
		{TranscriptionJobID: "job-count-a", OriginalSpeaker: "speaker_02", CustomName: "Carol", ReviewStatus: "pending"},
		// job-count-a: 1 accepted (should not count).
		{TranscriptionJobID: "job-count-a", OriginalSpeaker: "speaker_03", CustomName: "Dan", ReviewStatus: "accepted"},
		// job-count-b: 1 pending.
		{TranscriptionJobID: "job-count-b", OriginalSpeaker: "speaker_00", CustomName: "Eve", ReviewStatus: "pending"},
		// job-count-b: 1 dismissed (should not count).
		{TranscriptionJobID: "job-count-b", OriginalSpeaker: "speaker_01", CustomName: "Frank", ReviewStatus: "dismissed"},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	counts, err := repo.CountPendingSuggestions(ctx, []string{"job-count-a", "job-count-b"})
	if err != nil {
		t.Fatalf("CountPendingSuggestions: %v", err)
	}

	if counts["job-count-a"] != 3 {
		t.Errorf("job-count-a: expected 3 pending, got %d", counts["job-count-a"])
	}
	if counts["job-count-b"] != 1 {
		t.Errorf("job-count-b: expected 1 pending, got %d", counts["job-count-b"])
	}
}

// TestCountPendingSuggestions_EmptyMapForJobsWithNoSuggestions verifies that
// jobs with no pending suggestions are absent from the returned map (zero value
// when accessed, not an error).
func TestCountPendingSuggestions_EmptyMapForJobsWithNoSuggestions(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-zero-a")
	seedJob(t, db, "job-zero-b")

	// Seed only non-pending mappings so both jobs produce zero pending counts.
	mappings := []models.SpeakerMapping{
		{TranscriptionJobID: "job-zero-a", OriginalSpeaker: "speaker_00", CustomName: "Grace", ReviewStatus: "accepted"},
		{TranscriptionJobID: "job-zero-b", OriginalSpeaker: "speaker_00", CustomName: "Heidi", ReviewStatus: "dismissed"},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	counts, err := repo.CountPendingSuggestions(ctx, []string{"job-zero-a", "job-zero-b"})
	if err != nil {
		t.Fatalf("CountPendingSuggestions: %v", err)
	}

	// Map should not contain entries for jobs with zero pending suggestions.
	if counts["job-zero-a"] != 0 {
		t.Errorf("job-zero-a: expected 0 (absent key), got %d", counts["job-zero-a"])
	}
	if counts["job-zero-b"] != 0 {
		t.Errorf("job-zero-b: expected 0 (absent key), got %d", counts["job-zero-b"])
	}
}

// TestCountPendingSuggestions_EmptyInputReturnsEmptyMap verifies that passing
// an empty job ID slice returns an empty map without hitting the database.
func TestCountPendingSuggestions_EmptyInputReturnsEmptyMap(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	counts, err := repo.CountPendingSuggestions(ctx, []string{})
	if err != nil {
		t.Fatalf("CountPendingSuggestions with empty input: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("expected empty map, got %v", counts)
	}
}

// TestUpdateReviewStatus_ChangesStatusCorrectly verifies that a mapping's
// review_status is updated to the requested value.
func TestUpdateReviewStatus_ChangesStatusCorrectly(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-update-status")

	m := models.SpeakerMapping{
		TranscriptionJobID: "job-update-status",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Ivan",
		MatchTier:          "suggest",
		ReviewStatus:       "pending",
	}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	tests := []struct {
		transition   string
		wantStatus   string
	}{
		{"accepted", "accepted"},
		{"dismissed", "dismissed"},
		{"pending", "pending"},
	}

	for _, tc := range tests {
		if err := repo.UpdateReviewStatus(ctx, m.ID, tc.transition); err != nil {
			t.Fatalf("UpdateReviewStatus to %q: %v", tc.transition, err)
		}

		var loaded models.SpeakerMapping
		if err := db.First(&loaded, m.ID).Error; err != nil {
			t.Fatalf("load after UpdateReviewStatus: %v", err)
		}
		if loaded.ReviewStatus != tc.wantStatus {
			t.Errorf("after %q: ReviewStatus got %q, want %q", tc.transition, loaded.ReviewStatus, tc.wantStatus)
		}
	}
}

// TestUpdateReviewStatus_NonexistentMappingNoError verifies the current
// behavior: updating a nonexistent ID is a no-op that returns nil (GORM does
// not error on zero RowsAffected for UPDATE statements).
func TestUpdateReviewStatus_NonexistentMappingNoError(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	const nonexistentID = uint(99999)

	err := repo.UpdateReviewStatus(ctx, nonexistentID, "accepted")
	// The implementation uses a plain GORM Update (no RowsAffected check),
	// so it returns nil even when no row is matched.
	if err != nil {
		t.Errorf("UpdateReviewStatus on nonexistent ID: expected nil error, got %v", err)
	}

	// Confirm no row was silently created.
	var count int64
	db.Model(&models.SpeakerMapping{}).Where("id = ?", nonexistentID).Count(&count)
	if count != 0 {
		t.Errorf("expected 0 rows for nonexistent ID %d, got %d", nonexistentID, count)
	}
}

// TestUpsertMapping_CreatesNewWhenAbsent verifies that UpsertMapping inserts a
// row when no existing row matches (job_id, original_speaker).
func TestUpsertMapping_CreatesNewWhenAbsent(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-upsert-new")

	mapping := models.SpeakerMapping{
		TranscriptionJobID: "job-upsert-new",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Judy",
		ConfidenceScore:    0.88,
		MatchSource:        "auto",
		MatchTier:          "auto",
	}

	result, err := repo.UpsertMapping(ctx, "job-upsert-new", mapping)
	if err != nil {
		t.Fatalf("UpsertMapping (create): %v", err)
	}
	if result.ID == 0 {
		t.Error("expected non-zero ID after create")
	}
	if result.CustomName != "Judy" {
		t.Errorf("CustomName: got %q, want %q", result.CustomName, "Judy")
	}

	var count int64
	db.Model(&models.SpeakerMapping{}).Where("transcription_job_id = ? AND original_speaker = ?", "job-upsert-new", "speaker_00").Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 row after upsert create, got %d", count)
	}
}

// TestUpsertMapping_UpdatesExistingRow verifies that UpsertMapping updates the
// CustomName (and other mutable fields) when a row already exists.
func TestUpsertMapping_UpdatesExistingRow(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-upsert-update")

	initial := models.SpeakerMapping{
		TranscriptionJobID: "job-upsert-update",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Mallory",
		ConfidenceScore:    0.75,
		MatchSource:        "auto",
		MatchTier:          "suggest",
		ReviewStatus:       "pending",
	}
	if _, err := repo.UpsertMapping(ctx, "job-upsert-update", initial); err != nil {
		t.Fatalf("UpsertMapping (initial): %v", err)
	}

	updated := models.SpeakerMapping{
		TranscriptionJobID: "job-upsert-update",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "Mallory Confirmed",
		ConfidenceScore:    0.75,
		MatchSource:        "suggestion_promoted",
		MatchTier:          "suggest",
		ReviewStatus:       "accepted",
	}
	result, err := repo.UpsertMapping(ctx, "job-upsert-update", updated)
	if err != nil {
		t.Fatalf("UpsertMapping (update): %v", err)
	}

	if result.CustomName != "Mallory Confirmed" {
		t.Errorf("CustomName: got %q, want %q", result.CustomName, "Mallory Confirmed")
	}
	if result.MatchSource != "suggestion_promoted" {
		t.Errorf("MatchSource: got %q, want %q", result.MatchSource, "suggestion_promoted")
	}
	if result.ReviewStatus != "accepted" {
		t.Errorf("ReviewStatus: got %q, want %q", result.ReviewStatus, "accepted")
	}

	// Confirm exactly one row still exists (no duplicate created).
	var count int64
	db.Model(&models.SpeakerMapping{}).
		Where("transcription_job_id = ? AND original_speaker = ?", "job-upsert-update", "speaker_00").
		Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 row after upsert update, got %d", count)
	}
}

// TestListByJob_ReturnsAllMappingsForJob verifies that ListByJob returns every
// mapping belonging to the specified job and nothing from other jobs.
func TestListByJob_ReturnsAllMappingsForJob(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-list-a")
	seedJob(t, db, "job-list-b")

	mappingsA := []models.SpeakerMapping{
		{TranscriptionJobID: "job-list-a", OriginalSpeaker: "speaker_00", CustomName: "Alpha"},
		{TranscriptionJobID: "job-list-a", OriginalSpeaker: "speaker_01", CustomName: "Beta"},
	}
	mappingsB := []models.SpeakerMapping{
		{TranscriptionJobID: "job-list-b", OriginalSpeaker: "speaker_00", CustomName: "Gamma"},
	}
	if err := db.Create(&mappingsA).Error; err != nil {
		t.Fatalf("seed mappings A: %v", err)
	}
	if err := db.Create(&mappingsB).Error; err != nil {
		t.Fatalf("seed mappings B: %v", err)
	}

	got, err := repo.ListByJob(ctx, "job-list-a")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 mappings for job-list-a, got %d", len(got))
	}
	for _, m := range got {
		if m.TranscriptionJobID != "job-list-a" {
			t.Errorf("unexpected job ID %q in results (want job-list-a)", m.TranscriptionJobID)
		}
	}
}
