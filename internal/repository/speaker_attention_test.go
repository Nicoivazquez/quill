package repository

import (
	"context"
	"testing"

	"quill/internal/models"
)

// TestGetSpeakerAttentionSummary_CorrectCountsPerJob verifies that the summary
// returns correct counts for pending suggestions, auto-assigned, and total
// mappings per job.
func TestGetSpeakerAttentionSummary_CorrectCountsPerJob(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-attn-a")
	seedJob(t, db, "job-attn-b")

	mappings := []models.SpeakerMapping{
		// job-attn-a: 2 pending suggestions, 1 auto-assigned, 1 accepted = 4 total
		{TranscriptionJobID: "job-attn-a", OriginalSpeaker: "speaker_00", CustomName: "Alice",
			MatchTier: "suggest", ReviewStatus: "pending", ConfidenceScore: 0.72},
		{TranscriptionJobID: "job-attn-a", OriginalSpeaker: "speaker_01", CustomName: "Bob",
			MatchTier: "suggest", ReviewStatus: "pending", ConfidenceScore: 0.65},
		{TranscriptionJobID: "job-attn-a", OriginalSpeaker: "speaker_02", CustomName: "Carol",
			MatchTier: "auto", ReviewStatus: "", ConfidenceScore: 0.91},
		{TranscriptionJobID: "job-attn-a", OriginalSpeaker: "speaker_03", CustomName: "Dan",
			MatchTier: "suggest", ReviewStatus: "accepted", ConfidenceScore: 0.68},

		// job-attn-b: 1 pending, 2 auto-assigned = 3 total
		{TranscriptionJobID: "job-attn-b", OriginalSpeaker: "speaker_00", CustomName: "Eve",
			MatchTier: "suggest", ReviewStatus: "pending", ConfidenceScore: 0.75},
		{TranscriptionJobID: "job-attn-b", OriginalSpeaker: "speaker_01", CustomName: "Frank",
			MatchTier: "auto", ReviewStatus: "", ConfidenceScore: 0.88},
		{TranscriptionJobID: "job-attn-b", OriginalSpeaker: "speaker_02", CustomName: "Grace",
			MatchTier: "auto", ReviewStatus: "", ConfidenceScore: 0.85},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	summaries, err := repo.GetSpeakerAttentionSummary(ctx, []string{"job-attn-a", "job-attn-b"})
	if err != nil {
		t.Fatalf("GetSpeakerAttentionSummary: %v", err)
	}

	// job-attn-a
	sa := summaries["job-attn-a"]
	if sa.PendingSuggestions != 2 {
		t.Errorf("job-attn-a PendingSuggestions: got %d, want 2", sa.PendingSuggestions)
	}
	if sa.AutoAssigned != 1 {
		t.Errorf("job-attn-a AutoAssigned: got %d, want 1", sa.AutoAssigned)
	}
	if sa.TotalMappings != 4 {
		t.Errorf("job-attn-a TotalMappings: got %d, want 4", sa.TotalMappings)
	}

	// job-attn-b
	sb := summaries["job-attn-b"]
	if sb.PendingSuggestions != 1 {
		t.Errorf("job-attn-b PendingSuggestions: got %d, want 1", sb.PendingSuggestions)
	}
	if sb.AutoAssigned != 2 {
		t.Errorf("job-attn-b AutoAssigned: got %d, want 2", sb.AutoAssigned)
	}
	if sb.TotalMappings != 3 {
		t.Errorf("job-attn-b TotalMappings: got %d, want 3", sb.TotalMappings)
	}
}

// TestGetSpeakerAttentionSummary_EmptyInputReturnsEmptyMap verifies that
// passing an empty slice returns an empty map without errors.
func TestGetSpeakerAttentionSummary_EmptyInputReturnsEmptyMap(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	summaries, err := repo.GetSpeakerAttentionSummary(ctx, []string{})
	if err != nil {
		t.Fatalf("GetSpeakerAttentionSummary with empty input: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected empty map, got %v", summaries)
	}
}

// TestGetSpeakerAttentionSummary_JobWithNoMappingsAbsent verifies that jobs
// with no speaker mappings are absent from the result map.
func TestGetSpeakerAttentionSummary_JobWithNoMappingsAbsent(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-no-mappings")

	summaries, err := repo.GetSpeakerAttentionSummary(ctx, []string{"job-no-mappings"})
	if err != nil {
		t.Fatalf("GetSpeakerAttentionSummary: %v", err)
	}

	if _, exists := summaries["job-no-mappings"]; exists {
		t.Errorf("expected job-no-mappings to be absent from map, got %v", summaries["job-no-mappings"])
	}
}

// TestGetSpeakerAttentionSummary_DismissedNotCountedAsPending verifies that
// dismissed suggestions are excluded from the pending count.
func TestGetSpeakerAttentionSummary_DismissedNotCountedAsPending(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewSpeakerMappingRepository(db)
	ctx := context.Background()

	seedJob(t, db, "job-dismissed-attn")

	mappings := []models.SpeakerMapping{
		{TranscriptionJobID: "job-dismissed-attn", OriginalSpeaker: "speaker_00", CustomName: "Alice",
			MatchTier: "suggest", ReviewStatus: "pending", ConfidenceScore: 0.72},
		{TranscriptionJobID: "job-dismissed-attn", OriginalSpeaker: "speaker_01", CustomName: "Bob",
			MatchTier: "suggest", ReviewStatus: "dismissed", ConfidenceScore: 0.65},
		{TranscriptionJobID: "job-dismissed-attn", OriginalSpeaker: "speaker_02", CustomName: "Carol",
			MatchTier: "auto", ReviewStatus: "", ConfidenceScore: 0.91},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	summaries, err := repo.GetSpeakerAttentionSummary(ctx, []string{"job-dismissed-attn"})
	if err != nil {
		t.Fatalf("GetSpeakerAttentionSummary: %v", err)
	}

	s := summaries["job-dismissed-attn"]
	if s.PendingSuggestions != 1 {
		t.Errorf("PendingSuggestions: got %d, want 1 (dismissed excluded)", s.PendingSuggestions)
	}
	if s.AutoAssigned != 1 {
		t.Errorf("AutoAssigned: got %d, want 1", s.AutoAssigned)
	}
	if s.TotalMappings != 3 {
		t.Errorf("TotalMappings: got %d, want 3", s.TotalMappings)
	}
}

// TestListWithParams_SpeakerStatusFilter verifies that setting
// SpeakerStatus="needs_attention" filters to jobs with unidentified speakers
// (custom_name still equals original_speaker).
func TestListWithParams_SpeakerStatusFilter(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	jobRepo := NewJobRepository(db)
	ctx := context.Background()

	// Create 3 jobs
	jobs := []models.TranscriptionJob{
		{ID: "job-needs-attn", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted},
		{ID: "job-all-renamed", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted},
		{ID: "job-no-mappings", AudioPath: "/tmp/c.wav", Status: models.StatusCompleted},
	}
	for _, j := range jobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job %q: %v", j.ID, err)
		}
	}

	// job-needs-attn has one speaker still using the default name
	mappings := []models.SpeakerMapping{
		{TranscriptionJobID: "job-needs-attn", OriginalSpeaker: "speaker_00", CustomName: "speaker_00"},
		{TranscriptionJobID: "job-needs-attn", OriginalSpeaker: "speaker_01", CustomName: "Bob"},
		// job-all-renamed has all speakers renamed
		{TranscriptionJobID: "job-all-renamed", OriginalSpeaker: "speaker_00", CustomName: "Carol"},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	// Filter with SpeakerStatus="needs_attention"
	result, count, err := jobRepo.ListWithParams(ctx, ListParams{
		Limit:         100,
		SpeakerStatus: "needs_attention",
	})
	if err != nil {
		t.Fatalf("ListWithParams with SpeakerStatus: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 job with needs_attention, got %d", count)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 job in results, got %d", len(result))
	}
	if result[0].ID != "job-needs-attn" {
		t.Errorf("expected job-needs-attn, got %q", result[0].ID)
	}
}

// TestListWithParams_SpeakerStatusIdentified filters to jobs where all speakers
// are linked to contacts (contact_id IS NOT NULL).
func TestListWithParams_SpeakerStatusIdentified(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	jobRepo := NewJobRepository(db)
	ctx := context.Background()

	jobs := []models.TranscriptionJob{
		{ID: "job-no-contact", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted},
		{ID: "job-all-identified", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted},
	}
	for _, j := range jobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job %q: %v", j.ID, err)
		}
	}

	contactID1 := uint(1)
	contactID2 := uint(2)
	mappings := []models.SpeakerMapping{
		// job-no-contact: speaker has no contact_id linked
		{TranscriptionJobID: "job-no-contact", OriginalSpeaker: "speaker_00",
			CustomName: "Alice", ContactID: nil},
		// job-all-identified: both speakers linked to contacts
		{TranscriptionJobID: "job-all-identified", OriginalSpeaker: "speaker_00",
			CustomName: "Bob", ContactID: &contactID1},
		{TranscriptionJobID: "job-all-identified", OriginalSpeaker: "speaker_01",
			CustomName: "Carol", ContactID: &contactID2},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	result, count, err := jobRepo.ListWithParams(ctx, ListParams{
		Limit:         100,
		SpeakerStatus: "identified",
	})
	if err != nil {
		t.Fatalf("ListWithParams with SpeakerStatus=identified: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 identified job, got %d", count)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 job in results, got %d", len(result))
	}
	if result[0].ID != "job-all-identified" {
		t.Errorf("expected job-all-identified, got %q", result[0].ID)
	}
}
