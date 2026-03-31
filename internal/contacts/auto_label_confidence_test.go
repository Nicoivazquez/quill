package contacts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"quill/internal/models"
	"quill/internal/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestPersistMapping_PopulatesConfidenceFields verifies that persistMapping
// stores ConfidenceScore, MatchSource="auto", and MatchTier from SpeakerMatch.
func TestPersistMapping_PopulatesConfidenceFields(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	seedJob(t, db, "job-conf-persist")

	match := SpeakerMatch{
		Speaker:     "speaker_00",
		ContactID:   1,
		ContactName: "Alice",
		Score:       0.91,
		Tier:        TierAutoAssign,
	}

	if err := svc.persistMapping(context.Background(), "job-conf-persist", match, ""); err != nil {
		t.Fatalf("persistMapping: %v", err)
	}

	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-conf-persist")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}

	m := mappings[0]
	if m.ConfidenceScore != 0.91 {
		t.Errorf("ConfidenceScore: got %f, want 0.91", m.ConfidenceScore)
	}
	if m.MatchSource != "auto" {
		t.Errorf("MatchSource: got %q, want %q", m.MatchSource, "auto")
	}
	if m.MatchTier != "auto" {
		t.Errorf("MatchTier: got %q, want %q", m.MatchTier, "auto")
	}
}

// TestPersistMapping_SuggestTier verifies suggestion-tier matches store the
// correct tier and source.
func TestPersistMapping_SuggestTier(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	seedJob(t, db, "job-suggest-persist")

	match := SpeakerMatch{
		Speaker:     "speaker_01",
		ContactID:   2,
		ContactName: "Bob",
		Score:       0.42,
		Tier:        TierSuggest,
	}

	if err := svc.persistMapping(context.Background(), "job-suggest-persist", match, "pending"); err != nil {
		t.Fatalf("persistMapping: %v", err)
	}

	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-suggest-persist")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}

	m := mappings[0]
	if m.ConfidenceScore != 0.42 {
		t.Errorf("ConfidenceScore: got %f, want 0.42", m.ConfidenceScore)
	}
	if m.MatchSource != "auto" {
		t.Errorf("MatchSource: got %q, want %q", m.MatchSource, "auto")
	}
	if m.MatchTier != "suggest" {
		t.Errorf("MatchTier: got %q, want %q", m.MatchTier, "suggest")
	}
}

// TestPersistMapping_SkipsExisting verifies that if a mapping already exists
// for the job+speaker combination, it is NOT overwritten.
func TestPersistMapping_SkipsExisting(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	seedJob(t, db, "job-skip-existing")

	// Pre-create a manual mapping.
	existing := &models.SpeakerMapping{
		TranscriptionJobID: "job-skip-existing",
		OriginalSpeaker:    "speaker_00",
		CustomName:         "ManualName",
		ConfidenceScore:    0.0,
		MatchSource:        "manual",
		MatchTier:          "",
	}
	if err := db.Create(existing).Error; err != nil {
		t.Fatalf("create existing mapping: %v", err)
	}

	// Try to persist an auto match for the same speaker.
	match := SpeakerMatch{
		Speaker:     "speaker_00",
		ContactID:   5,
		ContactName: "NewName",
		Score:       0.95,
		Tier:        TierAutoAssign,
	}
	if err := svc.persistMapping(context.Background(), "job-skip-existing", match, ""); err != nil {
		t.Fatalf("persistMapping: %v", err)
	}

	// The original mapping should be untouched.
	mappings, _ := svc.speakerMapRepo.ListByJob(context.Background(), "job-skip-existing")
	if len(mappings) != 1 {
		t.Fatalf("expected exactly 1 mapping (original), got %d", len(mappings))
	}
	if mappings[0].CustomName != "ManualName" {
		t.Errorf("expected original name %q preserved, got %q", "ManualName", mappings[0].CustomName)
	}
}

// TestLabelSpeakers_AutoAssignedMappingsHaveConfidence runs the full pipeline
// with a mock embedding and verifies the persisted mapping has confidence data.
func TestLabelSpeakers_AutoAssignedMappingsHaveConfidence(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	vaultPath := filepath.Join(t.TempDir(), "vault")
	seedVaultAndJob(t, db, vaultPath, "job-full-pipeline")

	// Create a contact with a ready embedding.
	embPath := seedContactWithEmbedding(t, db, vaultPath, 1, "Alice", makeUnitBasisForAutoLabel(256, 0))

	// Speaker embedding is nearly identical to Alice's — should auto-assign.
	speakerEmb := makeUnitBasisForAutoLabel(256, 0)
	speakerEmb[1] = 0.01 // tiny perturbation

	result, err := svc.LabelSpeakers(context.Background(), 1, vaultPath, "job-full-pipeline", map[string][]float64{
		"speaker_00": speakerEmb,
	}, "")
	if err != nil {
		t.Fatalf("LabelSpeakers: %v", err)
	}

	if len(result.AutoAssigned) != 1 {
		t.Fatalf("expected 1 auto-assigned, got %d", len(result.AutoAssigned))
	}

	// Verify the persisted mapping has confidence fields.
	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-full-pipeline")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 persisted mapping, got %d", len(mappings))
	}

	m := mappings[0]
	if m.ConfidenceScore < 0.50 {
		t.Errorf("ConfidenceScore should be >= 0.50 for auto-assign, got %f", m.ConfidenceScore)
	}
	if m.MatchSource != "auto" {
		t.Errorf("MatchSource: got %q, want %q", m.MatchSource, "auto")
	}
	if m.MatchTier != "auto" {
		t.Errorf("MatchTier: got %q, want %q", m.MatchTier, "auto")
	}

	_ = embPath // suppress unused warning
}

// TestPersistMapping_SucceedsWithCancelledContext verifies that persistMapping
// uses its own DB context and succeeds even when the caller's context is cancelled.
// This reproduces the production bug where an LLM timeout consumed the entire
// context budget, causing all subsequent DB writes to fail.
func TestPersistMapping_SucceedsWithCancelledContext(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	seedJob(t, db, "job-ctx-cancel")

	match := SpeakerMatch{
		Speaker:     "speaker_00",
		ContactID:   1,
		ContactName: "Alice",
		Score:       0.91,
		Tier:        TierAutoAssign,
	}

	// Use an already-cancelled context to simulate context budget exhaustion.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	if err := svc.persistMapping(ctx, "job-ctx-cancel", match, ""); err != nil {
		t.Fatalf("persistMapping should succeed with cancelled context: %v", err)
	}

	// The mapping MUST be persisted despite the cancelled parent context.
	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-ctx-cancel")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 persisted mapping despite cancelled context, got %d", len(mappings))
	}
	if mappings[0].ConfidenceScore != 0.91 {
		t.Errorf("expected score 0.91, got %f", mappings[0].ConfidenceScore)
	}
}

// TestLabelSpeakers_LLMTimeoutDoesNotPreventPersistence verifies that when
// the LLM caller times out, voice-only matches are still persisted.
// This is the end-to-end reproduction of the production bug.
func TestLabelSpeakers_LLMTimeoutDoesNotPreventPersistence(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)
	svc.llmTimeout = 50 * time.Millisecond // very short for test

	vaultPath := filepath.Join(t.TempDir(), "vault")
	seedVaultAndJob(t, db, vaultPath, "job-llm-timeout")

	seedContactWithEmbedding(t, db, vaultPath, 1, "Alice", makeUnitBasisForAutoLabel(256, 0))

	// Set an LLM caller that blocks until its context is cancelled (simulates Ollama timeout).
	svc.SetLLMCaller(func(ctx context.Context, prompt string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	// Speaker embedding nearly identical to Alice — should auto-assign.
	speakerEmbs := map[string][]float64{
		"speaker_00": func() []float64 {
			v := makeUnitBasisForAutoLabel(256, 0)
			v[1] = 0.01
			return v
		}(),
	}

	result, err := svc.LabelSpeakers(context.Background(), 1, vaultPath, "job-llm-timeout", speakerEmbs, "Some transcript text for LLM")
	if err != nil {
		t.Fatalf("LabelSpeakers should not fail: %v", err)
	}

	// Voice matching should succeed and persist despite LLM timeout.
	if len(result.AutoAssigned) != 1 {
		t.Fatalf("expected 1 auto-assigned, got %d (suggestions=%d, unmatched=%d)",
			len(result.AutoAssigned), len(result.Suggestions), len(result.Unmatched))
	}

	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-llm-timeout")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 persisted mapping despite LLM timeout, got %d", len(mappings))
	}
	if mappings[0].ConfidenceScore < 0.50 {
		t.Errorf("expected auto-tier score >= 0.50, got %f", mappings[0].ConfidenceScore)
	}
}

// TestLabelSpeakers_RawMappingsUseFreshContext verifies that unmatched speakers
// get raw mappings using fresh DB contexts (not the caller's potentially-expired context).
func TestLabelSpeakers_RawMappingsUseFreshContext(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	vaultPath := filepath.Join(t.TempDir(), "vault")
	seedVaultAndJob(t, db, vaultPath, "job-raw-ctx")

	// Create one contact — but speaker_01 will be orthogonal (unmatched).
	seedContactWithEmbedding(t, db, vaultPath, 1, "Alice", makeUnitBasisForAutoLabel(256, 0))

	speakerEmbs := map[string][]float64{
		"speaker_00": func() []float64 {
			v := makeUnitBasisForAutoLabel(256, 0)
			v[1] = 0.01
			return v
		}(),
		// Orthogonal to Alice — will be unmatched (score near 0).
		"speaker_01": makeUnitBasisForAutoLabel(256, 200),
	}

	result, err := svc.LabelSpeakers(context.Background(), 1, vaultPath, "job-raw-ctx", speakerEmbs, "")
	if err != nil {
		t.Fatalf("LabelSpeakers: %v", err)
	}

	if len(result.Unmatched) == 0 {
		t.Fatal("expected at least one unmatched speaker")
	}

	// Raw mappings for unmatched speakers should be persisted.
	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-raw-ctx")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}

	// Should have mappings for BOTH speakers (auto-assigned + raw unmatched).
	if len(mappings) < 2 {
		t.Errorf("expected at least 2 mappings (assigned + raw), got %d", len(mappings))
	}
}

// --- helpers ---

func setupAutoLabelTestHarness(t *testing.T) (*gorm.DB, *AutoLabelService) {
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

	contactRepo := repository.NewContactRepository(db)
	speakerMapRepo := repository.NewSpeakerMappingRepository(db)
	svc := NewAutoLabelService(contactRepo, speakerMapRepo, db)

	return db, svc
}

func seedJob(t *testing.T, db *gorm.DB, jobID string) {
	t.Helper()
	job := &models.TranscriptionJob{ID: jobID, AudioPath: "/tmp/a.wav", Status: models.StatusCompleted}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job %s: %v", jobID, err)
	}
}

func seedVaultAndJob(t *testing.T, db *gorm.DB, vaultPath, jobID string) {
	t.Helper()
	vault := models.Vault{Name: "TestVault", Path: vaultPath, IsActive: true}
	if err := db.Create(&vault).Error; err != nil {
		t.Fatalf("create vault: %v", err)
	}
	vaultID := vault.ID
	job := &models.TranscriptionJob{
		ID:        jobID,
		AudioPath: filepath.Join(vaultPath, "audio.wav"),
		Status:    models.StatusCompleted,
		VaultID:   &vaultID,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
}

func seedContactWithEmbedding(t *testing.T, db *gorm.DB, vaultPath string, vaultID uint, name string, vec []float64) string {
	t.Helper()
	// Write embedding file to disk.
	import_json := `{"version":1,"model":"titanet","dimension":256,"vector":[`
	for i, v := range vec {
		if i > 0 {
			import_json += ","
		}
		import_json += fmt.Sprintf("%f", v)
	}
	import_json += `]}`

	contactDir := filepath.Join(vaultPath, "Contacts", "People", name)
	if err := os.MkdirAll(contactDir, 0o755); err != nil {
		t.Fatalf("mkdir contact: %v", err)
	}
	embPath := filepath.Join(contactDir, "voice-signature.json")
	if err := os.WriteFile(embPath, []byte(import_json), 0o644); err != nil {
		t.Fatalf("write embedding: %v", err)
	}

	contact := &models.Contact{
		VaultID:                vaultID,
		ContactUID:             fmt.Sprintf("uid-%s-%d", name, time.Now().UnixNano()),
		Slug:                   name,
		Name:                   name,
		SignatureStatus:        "ready",
		SignatureEmbeddingPath: &embPath,
	}
	if err := db.Create(contact).Error; err != nil {
		t.Fatalf("create contact: %v", err)
	}

	return embPath
}

func makeUnitBasisForAutoLabel(dim, idx int) []float64 {
	v := make([]float64, dim)
	if idx < dim {
		v[idx] = 1.0
	}
	return v
}
