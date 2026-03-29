package contacts

import (
	"context"
	"fmt"
	"math"
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

// seedContactWithUID is like seedContactWithReadyEmbedding but lets the caller
// specify a ContactUID explicitly. This avoids unique-constraint collisions when
// a single test harness seeds multiple contacts.
func seedContactWithUID(t *testing.T, db *gorm.DB, vaultPath string, vaultID uint, name, uid string, vec []float64) uint {
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
		ContactUID:             uid,
		Name:                   name,
		SignatureStatus:        "ready",
		SignatureEmbeddingPath: &embPath,
	}
	if err := db.Create(contact).Error; err != nil {
		t.Fatalf("create contact %s: %v", name, err)
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

// TestRetroactiveScan_MultipleContactsSameJob verifies that when two contacts
// both have voice signatures, a single job can produce matches for both — one
// per distinct speaker — without conflict resolution discarding either.
func TestRetroactiveScan_MultipleContactsSameJob(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	seedDiarizedJob(t, db, "job-two-contacts", vaultID, filepath.Join(vaultPath, "audio.wav"))

	// Alice's embedding lives at basis vector 0; Bob's at basis vector 10.
	// These are orthogonal so each contact exclusively matches its own speaker.
	aliceVec := makeRetroVec(256, 0)
	bobVec := makeRetroVec(256, 10)

	aliceID := seedContactWithUID(t, db, vaultPath, vaultID, "Alice", "uid-alice-multi", aliceVec)
	bobID := seedContactWithUID(t, db, vaultPath, vaultID, "Bob", "uid-bob-multi", bobVec)

	// speaker_00 strongly matches Alice; speaker_10 strongly matches Bob.
	speaker00Vec := makeRetroVec(256, 0)
	speaker00Vec[1] = 0.01
	speaker10Vec := makeRetroVec(256, 10)
	speaker10Vec[11] = 0.01

	svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
		"job-two-contacts": {
			"speaker_00": speaker00Vec,
			"speaker_10": speaker10Vec,
		},
	}))

	// Scan for Alice first.
	aliceResult, err := svc.ScanForContact(context.Background(), aliceID)
	if err != nil {
		t.Fatalf("ScanForContact (Alice): %v", err)
	}
	if aliceResult.AutoAssigned != 1 {
		t.Errorf("Alice AutoAssigned: got %d, want 1", aliceResult.AutoAssigned)
	}

	// Scan for Bob second; speaker_00 is now mapped to Alice so only speaker_10 remains.
	bobResult, err := svc.ScanForContact(context.Background(), bobID)
	if err != nil {
		t.Fatalf("ScanForContact (Bob): %v", err)
	}
	if bobResult.AutoAssigned != 1 {
		t.Errorf("Bob AutoAssigned: got %d, want 1", bobResult.AutoAssigned)
	}

	// Both mappings must exist in the DB with the correct contact names.
	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-two-contacts")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(mappings))
	}

	names := make(map[string]string) // originalSpeaker → customName
	for _, m := range mappings {
		names[m.OriginalSpeaker] = m.CustomName
	}
	if names["speaker_00"] != "Alice" {
		t.Errorf("speaker_00 CustomName: got %q, want %q", names["speaker_00"], "Alice")
	}
	if names["speaker_10"] != "Bob" {
		t.Errorf("speaker_10 CustomName: got %q, want %q", names["speaker_10"], "Bob")
	}
}

// TestRetroactiveScan_ContactIDSetOnSuggestTier verifies that suggest-tier
// mappings have their ContactID field populated (not nil) and pointing to the
// matched contact.
func TestRetroactiveScan_ContactIDSetOnSuggestTier(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	seedDiarizedJob(t, db, "job-suggest-cid", vaultID, filepath.Join(vaultPath, "audio.wav"))

	aliceVec := makeRetroVec(256, 0)
	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", aliceVec)

	// Build a vector with cosine similarity ≈ 0.70 against basis-0:
	// a = [1, 0, ...], b = [0.7, sqrt(1-0.49), 0, ...] = [0.7, ~0.7141, 0, ...]
	// dot = 0.7, |b| = 1.0, so cos = 0.7 → TierSuggest.
	suggestVec := make([]float64, 256)
	suggestVec[0] = 0.7
	suggestVec[1] = 0.7141428

	svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
		"job-suggest-cid": {"speaker_00": suggestVec},
	}))

	result, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("ScanForContact: %v", err)
	}
	if result.Suggestions != 1 {
		t.Fatalf("Suggestions: got %d, want 1", result.Suggestions)
	}

	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-suggest-cid")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
	m := mappings[0]
	if m.ContactID == nil {
		t.Fatal("ContactID is nil for suggest-tier mapping; expected non-nil")
	}
	if *m.ContactID != contactID {
		t.Errorf("ContactID: got %d, want %d", *m.ContactID, contactID)
	}
}

// TestRetroactiveScan_ReviewStatusByTier is a table-driven test that verifies
// the ReviewStatus field is set correctly for each tier:
//
//	auto-tier   → ""        (immediately accepted, no review required)
//	suggest-tier → "pending" (awaiting user review)
func TestRetroactiveScan_ReviewStatusByTier(t *testing.T) {
	// Build a unit-norm vector aligned with basis index idx.
	makeAligned := func(idx int, score float64) []float64 {
		// Construct v such that cos(basis[idx], v) == score.
		// v[idx] = score, v[idx+1] = sqrt(1 - score^2), everything else 0.
		v := make([]float64, 256)
		v[idx] = score
		if idx+1 < 256 {
			remainder := 1.0 - score*score
			if remainder > 0 {
				v[idx+1] = math.Sqrt(remainder)
			}
		}
		return v
	}

	cases := []struct {
		name           string
		score          float64
		wantTier       string
		wantReview     string
		wantAutoCount  int
		wantSuggestCnt int
	}{
		{
			name:           "auto-tier has empty review status",
			score:          0.85, // well above 0.80 threshold
			wantTier:       "auto",
			wantReview:     "",
			wantAutoCount:  1,
			wantSuggestCnt: 0,
		},
		{
			name:           "suggest-tier has pending review status",
			score:          0.70, // 0.60–0.79 range
			wantTier:       "suggest",
			wantReview:     "pending",
			wantAutoCount:  0,
			wantSuggestCnt: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, svc := setupRetroactiveScanHarness(t)
			vaultPath := t.TempDir()
			vaultID := seedVaultForRetro(t, db, vaultPath)

			jobID := "job-review-" + tc.wantTier
			seedDiarizedJob(t, db, jobID, vaultID, filepath.Join(vaultPath, "audio.wav"))

			contactVec := makeRetroVec(256, 0)
			contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", contactVec)

			speakerVec := makeAligned(0, tc.score)
			svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
				jobID: {"speaker_00": speakerVec},
			}))

			result, err := svc.ScanForContact(context.Background(), contactID)
			if err != nil {
				t.Fatalf("ScanForContact: %v", err)
			}
			if result.AutoAssigned != tc.wantAutoCount {
				t.Errorf("AutoAssigned: got %d, want %d", result.AutoAssigned, tc.wantAutoCount)
			}
			if result.Suggestions != tc.wantSuggestCnt {
				t.Errorf("Suggestions: got %d, want %d", result.Suggestions, tc.wantSuggestCnt)
			}

			mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), jobID)
			if err != nil {
				t.Fatalf("ListByJob: %v", err)
			}
			if len(mappings) != 1 {
				t.Fatalf("expected 1 mapping, got %d", len(mappings))
			}
			m := mappings[0]
			if m.MatchTier != tc.wantTier {
				t.Errorf("MatchTier: got %q, want %q", m.MatchTier, tc.wantTier)
			}
			if m.ReviewStatus != tc.wantReview {
				t.Errorf("ReviewStatus: got %q, want %q", m.ReviewStatus, tc.wantReview)
			}
		})
	}
}

// TestRetroactiveScan_ReScanDoesNotDuplicate verifies that running
// ScanForContact a second time for the same contact does not create a
// duplicate mapping. The first scan creates a retroactive mapping; on the
// second pass filterUnmappedSpeakers sees the already-named speaker and skips
// the job entirely.
func TestRetroactiveScan_ReScanDoesNotDuplicate(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	seedDiarizedJob(t, db, "job-rescan", vaultID, filepath.Join(vaultPath, "audio.wav"))

	aliceVec := makeRetroVec(256, 0)
	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", aliceVec)

	// speaker_00 strongly matches Alice.
	speakerVec := makeRetroVec(256, 0)
	speakerVec[1] = 0.01

	svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
		"job-rescan": {"speaker_00": speakerVec},
	}))

	// First scan — should produce one auto-assigned mapping.
	first, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("first ScanForContact: %v", err)
	}
	if first.AutoAssigned != 1 {
		t.Fatalf("first scan AutoAssigned: got %d, want 1", first.AutoAssigned)
	}

	// Second scan — speaker_00 is now mapped (CustomName="Alice" != "speaker_00"),
	// so the job should be counted as skipped and no new mapping created.
	second, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("second ScanForContact: %v", err)
	}
	if second.AutoAssigned != 0 {
		t.Errorf("second scan AutoAssigned: got %d, want 0 (speaker already mapped)", second.AutoAssigned)
	}
	if second.Skipped != 1 {
		t.Errorf("second scan Skipped: got %d, want 1", second.Skipped)
	}

	// Exactly one mapping row should exist after both scans.
	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-rescan")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 1 {
		t.Errorf("expected exactly 1 mapping after re-scan, got %d", len(mappings))
	}
}

// TestRetroactiveScan_NonDiarizedJobProducesZeroScanned verifies that a vault
// containing only non-diarized completed jobs yields no scanned jobs and
// produces no mappings. This is enforced at the DB query level in
// findEligibleJobs (diarization = true filter).
func TestRetroactiveScan_NonDiarizedJobProducesZeroScanned(t *testing.T) {
	db, svc := setupRetroactiveScanHarness(t)
	vaultPath := t.TempDir()
	vaultID := seedVaultForRetro(t, db, vaultPath)

	// Completed jobs WITHOUT diarization — should be invisible to the scanner.
	for i := 0; i < 3; i++ {
		job := &models.TranscriptionJob{
			ID:          fmt.Sprintf("job-nodiar-%d", i),
			AudioPath:   filepath.Join(vaultPath, "audio.wav"),
			Status:      models.StatusCompleted,
			VaultID:     &vaultID,
			Diarization: false,
		}
		if err := db.Create(job).Error; err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", makeRetroVec(256, 0))

	// The extractor would return a perfect match — but it should never be called.
	extractorCalled := false
	svc.SetExtractor(func(ctx context.Context, job *models.TranscriptionJob, vaultPath string) (map[string][]float64, error) {
		extractorCalled = true
		return map[string][]float64{"speaker_00": makeRetroVec(256, 0)}, nil
	})

	result, err := svc.ScanForContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("ScanForContact: %v", err)
	}

	if extractorCalled {
		t.Error("extractor was called for non-diarized job; should have been filtered at DB level")
	}
	if result.JobsScanned != 0 {
		t.Errorf("JobsScanned: got %d, want 0", result.JobsScanned)
	}
	if result.AutoAssigned != 0 {
		t.Errorf("AutoAssigned: got %d, want 0", result.AutoAssigned)
	}
	if result.Errors != 0 {
		t.Errorf("Errors: got %d, want 0", result.Errors)
	}
}

// TestRetroactiveScan_ScoreBoundaries is a table-driven test covering the
// exact threshold values defined in ClassifySpeakerMatch:
//
//	score == 0.60 → TierSuggest  (boundary: 0.60 is inclusive for suggest)
//	score == 0.80 → TierAutoAssign (boundary: 0.80 is inclusive for auto)
//
// Vectors are constructed so that cos(contactVec, speakerVec) equals the
// target score exactly using the identity: v = [s, sqrt(1-s^2), 0, ...].
func TestRetroactiveScan_ScoreBoundaries(t *testing.T) {
	cases := []struct {
		name        string
		score       float64
		wantAuto    int
		wantSuggest int
		wantTier    string
		wantReview  string
	}{
		{
			name:        "exact 0.60 is suggest tier",
			score:       0.60,
			wantAuto:    0,
			wantSuggest: 1,
			wantTier:    "suggest",
			wantReview:  "pending",
		},
		{
			name:        "exact 0.80 is auto-assign tier",
			score:       0.80,
			wantAuto:    1,
			wantSuggest: 0,
			wantTier:    "auto",
			wantReview:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, svc := setupRetroactiveScanHarness(t)
			vaultPath := t.TempDir()
			vaultID := seedVaultForRetro(t, db, vaultPath)

			jobID := "job-boundary-" + fmt.Sprintf("%.2f", tc.score)
			seedDiarizedJob(t, db, jobID, vaultID, filepath.Join(vaultPath, "audio.wav"))

			// Contact embedding: pure basis-0 unit vector.
			contactVec := makeRetroVec(256, 0)
			contactID := seedContactWithReadyEmbedding(t, db, vaultPath, vaultID, "Alice", contactVec)

			// Speaker vector: [score, sqrt(1-score^2), 0, ...] gives
			// cos(contactVec, speakerVec) == score exactly.
			speakerVec := make([]float64, 256)
			speakerVec[0] = tc.score
			speakerVec[1] = math.Sqrt(1.0 - tc.score*tc.score)

			svc.SetExtractor(mockExtractor(map[string]map[string][]float64{
				jobID: {"speaker_00": speakerVec},
			}))

			result, err := svc.ScanForContact(context.Background(), contactID)
			if err != nil {
				t.Fatalf("ScanForContact: %v", err)
			}

			if result.AutoAssigned != tc.wantAuto {
				t.Errorf("AutoAssigned: got %d, want %d (score=%.2f)", result.AutoAssigned, tc.wantAuto, tc.score)
			}
			if result.Suggestions != tc.wantSuggest {
				t.Errorf("Suggestions: got %d, want %d (score=%.2f)", result.Suggestions, tc.wantSuggest, tc.score)
			}

			mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), jobID)
			if err != nil {
				t.Fatalf("ListByJob: %v", err)
			}
			if len(mappings) != 1 {
				t.Fatalf("expected 1 mapping, got %d (score=%.2f)", len(mappings), tc.score)
			}
			m := mappings[0]
			if m.MatchTier != tc.wantTier {
				t.Errorf("MatchTier: got %q, want %q (score=%.2f)", m.MatchTier, tc.wantTier, tc.score)
			}
			if m.ReviewStatus != tc.wantReview {
				t.Errorf("ReviewStatus: got %q, want %q (score=%.2f)", m.ReviewStatus, tc.wantReview, tc.score)
			}
		})
	}
}
