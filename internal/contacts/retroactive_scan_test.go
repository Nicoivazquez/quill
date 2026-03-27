package contacts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"quill/internal/models"
	"quill/internal/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// --- test harness ---

func setupRetroactiveScanHarness(t *testing.T) (*gorm.DB, *RetroactiveScanService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.SpeakerMapping{},
		&models.Vault{},
		&models.Contact{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	jobRepo := repository.NewJobRepository(db)
	contactRepo := repository.NewContactRepository(db)
	speakerMapRepo := repository.NewSpeakerMappingRepository(db)

	svc := NewRetroactiveScanService(jobRepo, contactRepo, speakerMapRepo, db)
	return db, svc
}

func seedVaultForRetro(t *testing.T, db *gorm.DB, vaultPath string) uint {
	t.Helper()
	vault := models.Vault{Name: "TestVault", Path: vaultPath, IsActive: true}
	if err := db.Create(&vault).Error; err != nil {
		t.Fatalf("create vault: %v", err)
	}
	return vault.ID
}

func seedDiarizedJob(t *testing.T, db *gorm.DB, jobID string, vaultID uint, audioPath string) {
	t.Helper()
	job := &models.TranscriptionJob{
		ID:          jobID,
		AudioPath:   audioPath,
		Status:      models.StatusCompleted,
		VaultID:     &vaultID,
		Diarization: true,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create diarized job %s: %v", jobID, err)
	}
}

func seedContactWithReadyEmbedding(t *testing.T, db *gorm.DB, vaultPath string, vaultID uint, name string, vec []float64) uint {
	t.Helper()
	importJSON := `{"version":1,"model":"titanet","dimension":256,"vector":[`
	for i, v := range vec {
		if i > 0 {
			importJSON += ","
		}
		importJSON += fmt.Sprintf("%f", v)
	}
	importJSON += `]}`

	contactDir := filepath.Join(vaultPath, "Contacts", "People", name)
	if err := os.MkdirAll(contactDir, 0o755); err != nil {
		t.Fatalf("mkdir contact: %v", err)
	}
	embPath := filepath.Join(contactDir, "voice-signature.embedding.json")
	if err := os.WriteFile(embPath, []byte(importJSON), 0o644); err != nil {
		t.Fatalf("write embedding: %v", err)
	}

	contact := &models.Contact{
		VaultID:                vaultID,
		Name:                   name,
		SignatureStatus:        "ready",
		SignatureEmbeddingPath: &embPath,
	}
	if err := db.Create(contact).Error; err != nil {
		t.Fatalf("create contact: %v", err)
	}
	return contact.ID
}

func makeRetroVec(dim, idx int) []float64 {
	v := make([]float64, dim)
	if idx < dim {
		v[idx] = 1.0
	}
	return v
}

// mockExtractor returns a SpeakerEmbeddingExtractor that returns pre-built
// embeddings keyed by job ID.
func mockExtractor(embeddingsByJob map[string]map[string][]float64) SpeakerEmbeddingExtractor {
	return func(ctx context.Context, job *models.TranscriptionJob, vaultPath string) (map[string][]float64, error) {
		if embs, ok := embeddingsByJob[job.ID]; ok {
			return embs, nil
		}
		return nil, nil
	}
}

// --- tests ---

func TestRetroactiveScan_MatchesNewContact(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	// Create a completed diarized job with two speakers.
	seedDiarizedJob(t, db, "job-retro-1", vaultID, filepath.Join(vaultPath, "audio.wav"))

	// Contact: Alice with embedding at basis vector 0.
	aliceVec := makeRetroVec(256, 0)
	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", aliceVec)

	// Speaker embeddings: speaker_00 is very similar to Alice (basis 0 + tiny perturbation).
	speaker00Vec := makeRetroVec(256, 0)
	speaker00Vec[1] = 0.01
	speaker01Vec := makeRetroVec(256, 5) // different speaker, won't match Alice

	svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
		"job-retro-1": {
			"speaker_00": speaker00Vec,
			"speaker_01": speaker01Vec,
		},
	}))

	result, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("ScanForContact: %v", err)
	}

	if result.JobsScanned != 1 {
		t.Errorf("JobsScanned: got %d, want 1", result.JobsScanned)
	}
	if result.AutoAssigned != 1 {
		t.Errorf("AutoAssigned: got %d, want 1", result.AutoAssigned)
	}

	// Verify mapping persisted.
	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-retro-1")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
	if mappings[0].CustomName != "Alice" {
		t.Errorf("CustomName: got %q, want %q", mappings[0].CustomName, "Alice")
	}
	if mappings[0].MatchSource != "retroactive" {
		t.Errorf("MatchSource: got %q, want %q", mappings[0].MatchSource, "retroactive")
	}
	if mappings[0].ConfidenceScore < 0.80 {
		t.Errorf("ConfidenceScore: got %f, want >= 0.80", mappings[0].ConfidenceScore)
	}
}

func TestRetroactiveScan_SkipsJobsWithExistingMapping(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	seedDiarizedJob(t, db, "job-retro-skip", vaultID, filepath.Join(vaultPath, "audio.wav"))

	// Pre-create a manual mapping for speaker_00.
	if err := db.Create(&models.SpeakerMapping{
		TranscriptionJobID: "job-retro-skip",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "ManuallyNamed",
		MatchSource:        "manual",
	}).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	aliceVec := makeRetroVec(256, 0)
	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", aliceVec)

	speaker00Vec := makeRetroVec(256, 0) // would match Alice
	svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
		"job-retro-skip": {
			"speaker_00": speaker00Vec,
		},
	}))

	result, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("ScanForContact: %v", err)
	}

	if result.Skipped != 1 {
		t.Errorf("Skipped: got %d, want 1 (speaker already has a mapping)", result.Skipped)
	}

	// Original mapping should be unchanged.
	mappings, _ := svc.speakerMapRepo.ListByJob(context.Background(), "job-retro-skip")
	if len(mappings) != 1 || mappings[0].CustomName != "ManuallyNamed" {
		t.Errorf("original mapping should be preserved")
	}
}

func TestRetroactiveScan_MultipleJobsMultipleMatches(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	seedDiarizedJob(t, db, "job-multi-1", vaultID, filepath.Join(vaultPath, "audio1.wav"))
	seedDiarizedJob(t, db, "job-multi-2", vaultID, filepath.Join(vaultPath, "audio2.wav"))
	seedDiarizedJob(t, db, "job-multi-3", vaultID, filepath.Join(vaultPath, "audio3.wav"))

	aliceVec := makeRetroVec(256, 0)
	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", aliceVec)

	speaker00Vec := makeRetroVec(256, 0)
	speaker00Vec[1] = 0.01

	svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
		"job-multi-1": {"speaker_00": speaker00Vec, "speaker_01": makeRetroVec(256, 5)},
		"job-multi-2": {"speaker_00": makeRetroVec(256, 5)}, // no match
		"job-multi-3": {"speaker_02": speaker00Vec},         // match under different label
	}))

	result, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("ScanForContact: %v", err)
	}

	if result.JobsScanned != 3 {
		t.Errorf("JobsScanned: got %d, want 3", result.JobsScanned)
	}
	if result.AutoAssigned != 2 {
		t.Errorf("AutoAssigned: got %d, want 2 (job-multi-1 speaker_00 + job-multi-3 speaker_02)", result.AutoAssigned)
	}
}

func TestRetroactiveScan_SuggestTierRecorded(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	seedDiarizedJob(t, db, "job-suggest", vaultID, filepath.Join(vaultPath, "audio.wav"))

	// Create a contact embedding that will produce a suggest-tier match (0.60 - 0.79).
	aliceVec := makeRetroVec(256, 0)
	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", aliceVec)

	// Build a speaker vector that has ~0.70 cosine similarity with basis-0.
	// cos(a, b) = dot(a,b) / (|a|*|b|)
	// a = [1, 0, 0, ...], b = [0.7, 0.7141, 0, ...]
	// dot = 0.7, |b| = sqrt(0.49 + 0.51) = 1.0 → cos = 0.7
	suggestVec := make([]float64, 256)
	suggestVec[0] = 0.7
	suggestVec[1] = 0.7141428 // sqrt(1 - 0.49)

	svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
		"job-suggest": {"speaker_00": suggestVec},
	}))

	result, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("ScanForContact: %v", err)
	}

	if result.Suggestions != 1 {
		t.Errorf("Suggestions: got %d, want 1", result.Suggestions)
	}
}

func TestRetroactiveScan_SkipsNonDiarizedJobs(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	// Create a completed job WITHOUT diarization.
	job := &models.TranscriptionJob{
		ID:          "job-no-diarize",
		AudioPath:   filepath.Join(vaultPath, "audio.wav"),
		Status:      models.StatusCompleted,
		VaultID:     &vaultID,
		Diarization: false,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	aliceVec := makeRetroVec(256, 0)
	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", aliceVec)

	svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
		"job-no-diarize": {"speaker_00": makeRetroVec(256, 0)},
	}))

	result, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("ScanForContact: %v", err)
	}

	if result.JobsScanned != 0 {
		t.Errorf("JobsScanned: got %d, want 0 (non-diarized should be skipped)", result.JobsScanned)
	}
}

func TestRetroactiveScan_SkipsIncompleteJobs(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	// Create jobs in non-completed statuses.
	for _, status := range []models.JobStatus{models.StatusPending, models.StatusProcessing, models.StatusFailed} {
		job := &models.TranscriptionJob{
			ID:          fmt.Sprintf("job-%s", status),
			AudioPath:   filepath.Join(vaultPath, "audio.wav"),
			Status:      status,
			VaultID:     &vaultID,
			Diarization: true,
		}
		if err := db.Create(job).Error; err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", makeRetroVec(256, 0))
	svc.SetExtractor(mockExtractor(map[string]map[string][]float64{}))

	result, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("ScanForContact: %v", err)
	}

	if result.JobsScanned != 0 {
		t.Errorf("JobsScanned: got %d, want 0 (non-completed should be skipped)", result.JobsScanned)
	}
}

func TestRetroactiveScan_ContactNotReady(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)

	// Create a contact with "processing" status (not ready yet).
	contact := &models.Contact{
		VaultID:         1,
		Name:            "NotReady",
		SignatureStatus: "processing",
	}
	if err := db.Create(contact).Error; err != nil {
		t.Fatalf("create contact: %v", err)
	}

	result, err := svc.ScanForContact(context.Background(), contact.ID)
	if err != nil {
		t.Fatalf("ScanForContact: %v", err)
	}

	if result.JobsScanned != 0 {
		t.Errorf("should skip scan when contact is not ready")
	}
}

func TestRetroactiveScan_ExtractorError(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	seedDiarizedJob(t, db, "job-err", vaultID, filepath.Join(vaultPath, "audio.wav"))

	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", makeRetroVec(256, 0))

	// Extractor returns an error for this job.
	svc.SetExtractor(func(ctx context.Context, job *models.TranscriptionJob, vaultPath string) (map[string][]float64, error) {
		return nil, fmt.Errorf("ffmpeg not found")
	})

	result, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("ScanForContact should not return fatal error for per-job failures: %v", err)
	}

	if result.Errors != 1 {
		t.Errorf("Errors: got %d, want 1", result.Errors)
	}
}
