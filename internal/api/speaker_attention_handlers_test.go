package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"quill/internal/models"

	"github.com/gin-gonic/gin"
)

// buildListRouter sets up a minimal gin engine with only the list endpoint.
func buildListRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/transcription/jobs", h.ListTranscriptionJobs)
	return r
}

// TestListTranscriptionJobs_ReturnsSpeakerAttention verifies that the list
// endpoint includes a speaker_attention field in the response alongside
// the existing pending_suggestions.
func TestListTranscriptionJobs_ReturnsSpeakerAttention(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	// Seed completed jobs
	jobs := []models.TranscriptionJob{
		{ID: "job-sa-1", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted},
		{ID: "job-sa-2", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted},
	}
	for _, j := range jobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job: %v", err)
		}
	}

	// Seed mappings: job-sa-1 has 2 pending + 1 auto, job-sa-2 has 1 auto only
	mappings := []models.SpeakerMapping{
		{TranscriptionJobID: "job-sa-1", OriginalSpeaker: "speaker_00", CustomName: "Alice",
			MatchTier: "suggest", ReviewStatus: "pending", ConfidenceScore: 0.72},
		{TranscriptionJobID: "job-sa-1", OriginalSpeaker: "speaker_01", CustomName: "Bob",
			MatchTier: "suggest", ReviewStatus: "pending", ConfidenceScore: 0.65},
		{TranscriptionJobID: "job-sa-1", OriginalSpeaker: "speaker_02", CustomName: "Carol",
			MatchTier: "auto", ReviewStatus: "", ConfidenceScore: 0.91},
		{TranscriptionJobID: "job-sa-2", OriginalSpeaker: "speaker_00", CustomName: "Dan",
			MatchTier: "auto", ReviewStatus: "", ConfidenceScore: 0.85},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	r := buildListRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/transcription/jobs?limit=10&all_vaults=true", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify speaker_attention is present in the response
	saRaw, ok := resp["speaker_attention"]
	if !ok {
		t.Fatal("response missing speaker_attention field")
	}

	var speakerAttention map[string]struct {
		PendingSuggestions int `json:"pending_suggestions"`
		AutoAssigned       int `json:"auto_assigned"`
		TotalMappings      int `json:"total_mappings"`
	}
	if err := json.Unmarshal(saRaw, &speakerAttention); err != nil {
		t.Fatalf("decode speaker_attention: %v", err)
	}

	// job-sa-1: 2 pending, 1 auto, 3 total
	sa1 := speakerAttention["job-sa-1"]
	if sa1.PendingSuggestions != 2 {
		t.Errorf("job-sa-1 pending_suggestions: got %d, want 2", sa1.PendingSuggestions)
	}
	if sa1.AutoAssigned != 1 {
		t.Errorf("job-sa-1 auto_assigned: got %d, want 1", sa1.AutoAssigned)
	}
	if sa1.TotalMappings != 3 {
		t.Errorf("job-sa-1 total_mappings: got %d, want 3", sa1.TotalMappings)
	}

	// job-sa-2: 0 pending, 1 auto, 1 total
	sa2 := speakerAttention["job-sa-2"]
	if sa2.AutoAssigned != 1 {
		t.Errorf("job-sa-2 auto_assigned: got %d, want 1", sa2.AutoAssigned)
	}
	if sa2.TotalMappings != 1 {
		t.Errorf("job-sa-2 total_mappings: got %d, want 1", sa2.TotalMappings)
	}
}

// TestListTranscriptionJobs_SpeakerStatusFilter verifies that the list
// endpoint accepts a speaker_status query parameter to filter jobs.
func TestListTranscriptionJobs_SpeakerStatusFilter(t *testing.T) {
	h, db, cleanup := setupSpeakerMappingHarness(t)
	defer cleanup()

	jobs := []models.TranscriptionJob{
		{ID: "job-filter-pending", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted},
		{ID: "job-filter-clean", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted},
	}
	for _, j := range jobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job: %v", err)
		}
	}

	contactID := uint(1)
	mappings := []models.SpeakerMapping{
		// Unidentified: custom_name still equals original_speaker
		{TranscriptionJobID: "job-filter-pending", OriginalSpeaker: "speaker_00",
			CustomName: "speaker_00"},
		// Identified: renamed and linked to a contact
		{TranscriptionJobID: "job-filter-clean", OriginalSpeaker: "speaker_00",
			CustomName: "Alice", ContactID: &contactID},
	}
	if err := db.Create(&mappings).Error; err != nil {
		t.Fatalf("seed mappings: %v", err)
	}

	r := buildListRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/transcription/jobs?limit=10&all_vaults=true&speaker_status=needs_attention", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Jobs       []models.TranscriptionJob `json:"jobs"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Pagination.Total != 1 {
		t.Errorf("expected total=1 for needs_attention filter, got %d", resp.Pagination.Total)
	}
	if len(resp.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(resp.Jobs))
	}
	if resp.Jobs[0].ID != "job-filter-pending" {
		t.Errorf("expected job-filter-pending, got %q", resp.Jobs[0].ID)
	}
}
