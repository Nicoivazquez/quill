package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"quill/internal/contacts"
	"quill/internal/database"
	"quill/internal/models"
	"quill/internal/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// --- toSpeakerMatchResponses ---

func TestToSpeakerMatchResponses_Empty(t *testing.T) {
	result := toSpeakerMatchResponses(nil)
	if len(result) != 0 {
		t.Fatalf("expected empty slice for nil input, got %d elements", len(result))
	}

	result = toSpeakerMatchResponses([]contacts.SpeakerMatch{})
	if len(result) != 0 {
		t.Fatalf("expected empty slice for empty input, got %d elements", len(result))
	}
}

func TestToSpeakerMatchResponses_Single(t *testing.T) {
	matches := []contacts.SpeakerMatch{
		{
			Speaker:     "speaker_00",
			ContactID:   42,
			ContactName: "Alice",
			Score:       0.92,
			Tier:        contacts.TierAutoAssign,
		},
	}

	result := toSpeakerMatchResponses(matches)

	if len(result) != 1 {
		t.Fatalf("expected 1 response, got %d", len(result))
	}
	r := result[0]
	if r.Speaker != "speaker_00" {
		t.Errorf("Speaker: got %q, want %q", r.Speaker, "speaker_00")
	}
	if r.ContactID != 42 {
		t.Errorf("ContactID: got %d, want 42", r.ContactID)
	}
	if r.ContactName != "Alice" {
		t.Errorf("ContactName: got %q, want %q", r.ContactName, "Alice")
	}
	if r.Score != 0.92 {
		t.Errorf("Score: got %f, want 0.92", r.Score)
	}
	if r.Tier != string(contacts.TierAutoAssign) {
		t.Errorf("Tier: got %q, want %q", r.Tier, string(contacts.TierAutoAssign))
	}
}

func TestToSpeakerMatchResponses_Multiple(t *testing.T) {
	matches := []contacts.SpeakerMatch{
		{
			Speaker:     "speaker_00",
			ContactID:   1,
			ContactName: "Alice",
			Score:       0.91,
			Tier:        contacts.TierAutoAssign,
		},
		{
			Speaker:     "speaker_01",
			ContactID:   2,
			ContactName: "Bob",
			Score:       0.70,
			Tier:        contacts.TierSuggest,
		},
		{
			Speaker:     "speaker_02",
			ContactID:   3,
			ContactName: "Carol",
			Score:       0.55,
			Tier:        contacts.TierUnknown,
		},
	}

	result := toSpeakerMatchResponses(matches)

	if len(result) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(result))
	}

	cases := []struct {
		speaker string
		tier    string
		score   float64
	}{
		{"speaker_00", "auto", 0.91},
		{"speaker_01", "suggest", 0.70},
		{"speaker_02", "unknown", 0.55},
	}

	for i, c := range cases {
		if result[i].Speaker != c.speaker {
			t.Errorf("[%d] Speaker: got %q, want %q", i, result[i].Speaker, c.speaker)
		}
		if result[i].Tier != c.tier {
			t.Errorf("[%d] Tier: got %q, want %q", i, result[i].Tier, c.tier)
		}
		if result[i].Score != c.score {
			t.Errorf("[%d] Score: got %f, want %f", i, result[i].Score, c.score)
		}
	}
}

func TestToSpeakerMatchResponses_TierStrings(t *testing.T) {
	// Verify that all three tier constants map to the expected string values.
	tiers := []struct {
		tier    contacts.SpeakerMatchTier
		wantStr string
	}{
		{contacts.TierAutoAssign, "auto"},
		{contacts.TierSuggest, "suggest"},
		{contacts.TierUnknown, "unknown"},
	}

	for _, tc := range tiers {
		matches := []contacts.SpeakerMatch{{Tier: tc.tier}}
		result := toSpeakerMatchResponses(matches)
		if result[0].Tier != tc.wantStr {
			t.Errorf("tier %v: got %q, want %q", tc.tier, result[0].Tier, tc.wantStr)
		}
	}
}

// --- AutoLabelSpeakersForJob guard conditions ---

// setupAutoLabelHarness creates a minimal Handler with a real sqlite DB and
// returns the handler, db, and a cleanup func.
func setupAutoLabelHarness(t *testing.T) (*Handler, *gorm.DB, func()) {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "auto_label_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.Vault{},
		&models.Contact{},
		&models.SpeakerMapping{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	prevDB := database.DB
	database.DB = db

	cleanup := func() {
		database.DB = prevDB
	}
	return &Handler{}, db, cleanup
}

func TestAutoLabelSpeakersForJob_NilContactRepo_ReturnsNil(t *testing.T) {
	h, _, cleanup := setupAutoLabelHarness(t)
	defer cleanup()

	// contactRepo is nil, contactManager is also nil — both guards must be absent
	h.contactRepo = nil
	h.contactManager = nil

	err := h.AutoLabelSpeakersForJob(context.Background(), "some-job-id")
	if err != nil {
		t.Errorf("expected nil error when contactRepo is nil, got: %v", err)
	}
}

func TestAutoLabelSpeakersForJob_NilContactManager_ReturnsNil(t *testing.T) {
	h, db, cleanup := setupAutoLabelHarness(t)
	defer cleanup()

	// Provide a real contactRepo but leave contactManager nil.
	h.contactRepo = repository.NewContactRepository(db)
	h.contactManager = nil

	err := h.AutoLabelSpeakersForJob(context.Background(), "some-job-id")
	if err != nil {
		t.Errorf("expected nil error when contactManager is nil, got: %v", err)
	}
}

func TestAutoLabelSpeakersForJob_ContactRepoNil_ContactManagerSet_ReturnsNil(t *testing.T) {
	h, db, cleanup := setupAutoLabelHarness(t)
	defer cleanup()

	// contactManager is set but contactRepo is nil — guard must short-circuit.
	h.contactRepo = nil
	h.contactManager = contacts.NewManager(db, repository.NewContactRepository(db), "")

	err := h.AutoLabelSpeakersForJob(context.Background(), "any-job-id")
	if err != nil {
		t.Errorf("expected nil error when contactRepo is nil, got: %v", err)
	}
}

func TestAutoLabelSpeakersForJob_NonCompletedJob_ReturnsNil(t *testing.T) {
	h, db, cleanup := setupAutoLabelHarness(t)
	defer cleanup()

	h.contactRepo = repository.NewContactRepository(db)
	h.contactManager = contacts.NewManager(db, h.contactRepo, "")
	h.jobRepo = repository.NewJobRepository(db)

	statuses := []models.JobStatus{
		models.StatusPending,
		models.StatusProcessing,
		models.StatusFailed,
		models.StatusUploaded,
	}

	for _, status := range statuses {
		jobID := "job-" + string(status)
		job := &models.TranscriptionJob{
			ID:        jobID,
			AudioPath: "/tmp/audio.wav",
			Status:    status,
		}
		if err := db.Create(job).Error; err != nil {
			t.Fatalf("create job with status %s: %v", status, err)
		}

		err := h.AutoLabelSpeakersForJob(context.Background(), jobID)
		if err != nil {
			t.Errorf("status %s: expected nil error, got: %v", status, err)
		}
	}
}

func TestAutoLabelSpeakersForJob_CompletedJobWithNilTranscript_ReturnsNil(t *testing.T) {
	h, db, cleanup := setupAutoLabelHarness(t)
	defer cleanup()

	h.contactRepo = repository.NewContactRepository(db)
	h.contactManager = contacts.NewManager(db, h.contactRepo, "")
	h.jobRepo = repository.NewJobRepository(db)

	job := &models.TranscriptionJob{
		ID:         "job-nil-transcript",
		AudioPath:  "/tmp/audio.wav",
		Status:     models.StatusCompleted,
		Transcript: nil, // no transcript
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	err := h.AutoLabelSpeakersForJob(context.Background(), "job-nil-transcript")
	if err != nil {
		t.Errorf("expected nil error for nil transcript, got: %v", err)
	}
}

func TestAutoLabelSpeakersForJob_CompletedJobWithEmptyTranscript_ReturnsNil(t *testing.T) {
	h, db, cleanup := setupAutoLabelHarness(t)
	defer cleanup()

	h.contactRepo = repository.NewContactRepository(db)
	h.contactManager = contacts.NewManager(db, h.contactRepo, "")
	h.jobRepo = repository.NewJobRepository(db)

	empty := "   "
	job := &models.TranscriptionJob{
		ID:         "job-empty-transcript",
		AudioPath:  "/tmp/audio.wav",
		Status:     models.StatusCompleted,
		Transcript: &empty,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	err := h.AutoLabelSpeakersForJob(context.Background(), "job-empty-transcript")
	if err != nil {
		t.Errorf("expected nil error for whitespace-only transcript, got: %v", err)
	}
}

func TestAutoLabelSpeakersForJob_UnknownJobID_ReturnsError(t *testing.T) {
	h, db, cleanup := setupAutoLabelHarness(t)
	defer cleanup()

	h.contactRepo = repository.NewContactRepository(db)
	h.contactManager = contacts.NewManager(db, h.contactRepo, "")
	h.jobRepo = repository.NewJobRepository(db)

	err := h.AutoLabelSpeakersForJob(context.Background(), "does-not-exist")
	if err == nil {
		t.Error("expected an error for unknown job ID, got nil")
	}
}

func TestAutoLabelSpeakersForJob_CompletedJobNoSegments_ReturnsNil(t *testing.T) {
	h, db, cleanup := setupAutoLabelHarness(t)
	defer cleanup()

	h.contactRepo = repository.NewContactRepository(db)
	h.contactManager = contacts.NewManager(db, h.contactRepo, "")
	h.jobRepo = repository.NewJobRepository(db)

	// Valid JSON but empty segments array.
	transcript := `{"text":"hello","segments":[]}`
	job := &models.TranscriptionJob{
		ID:         "job-no-segments",
		AudioPath:  "/tmp/audio.wav",
		Status:     models.StatusCompleted,
		Transcript: &transcript,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	err := h.AutoLabelSpeakersForJob(context.Background(), "job-no-segments")
	if err != nil {
		t.Errorf("expected nil error for empty segments, got: %v", err)
	}
}

func TestAutoLabelSpeakersForJob_NoReadyContacts_ReturnsNil(t *testing.T) {
	h, db, cleanup := setupAutoLabelHarness(t)
	defer cleanup()

	if err := db.AutoMigrate(&models.Vault{}); err != nil {
		t.Fatalf("migrate vault: %v", err)
	}

	vaultPath := filepath.Join(t.TempDir(), "vault")
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatalf("create vault dir: %v", err)
	}
	vault := models.Vault{Name: "Test Vault", Path: vaultPath, IsActive: true}
	if err := db.Create(&vault).Error; err != nil {
		t.Fatalf("create vault: %v", err)
	}

	h.contactRepo = repository.NewContactRepository(db)
	h.contactManager = contacts.NewManager(db, h.contactRepo, "")
	h.jobRepo = repository.NewJobRepository(db)

	// Transcript has a speaker segment long enough to pass the window check.
	speaker := "speaker_00"
	transcript := buildTranscriptWithSpeaker(speaker)
	vaultID := vault.ID
	job := &models.TranscriptionJob{
		ID:         "job-no-ready-contacts",
		AudioPath:  filepath.Join(vaultPath, "audio.wav"),
		Status:     models.StatusCompleted,
		Transcript: &transcript,
		VaultID:    &vaultID,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	// No contacts in DB at all — ListBySignatureStatus returns empty.
	err := h.AutoLabelSpeakersForJob(context.Background(), "job-no-ready-contacts")
	if err != nil {
		t.Errorf("expected nil error when no ready contacts exist, got: %v", err)
	}
}

// buildTranscriptWithSpeaker returns a minimal valid transcript JSON string that
// has one segment long enough to survive the clip-window duration threshold.
func buildTranscriptWithSpeaker(speaker string) string {
	// The segment needs to accumulate enough time to pass the clip-window
	// duration guard inside buildSpeakerClipWindows; 10 s is safe for any
	// reasonable threshold value.
	return `{
		"text": "hello world",
		"segments": [
			{
				"start": 0.0,
				"end": 10.0,
				"text": "hello world",
				"speaker": "` + speaker + `"
			}
		]
	}`
}
