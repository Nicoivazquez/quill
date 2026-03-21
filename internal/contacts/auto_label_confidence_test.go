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

	if err := svc.persistMapping(context.Background(), "job-conf-persist", match); err != nil {
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
		Score:       0.72,
		Tier:        TierSuggest,
	}

	if err := svc.persistMapping(context.Background(), "job-suggest-persist", match); err != nil {
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
	if m.ConfidenceScore != 0.72 {
		t.Errorf("ConfidenceScore: got %f, want 0.72", m.ConfidenceScore)
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
	if err := svc.persistMapping(context.Background(), "job-skip-existing", match); err != nil {
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
	})
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
	if m.ConfidenceScore < 0.80 {
		t.Errorf("ConfidenceScore should be >= 0.80 for auto-assign, got %f", m.ConfidenceScore)
	}
	if m.MatchSource != "auto" {
		t.Errorf("MatchSource: got %q, want %q", m.MatchSource, "auto")
	}
	if m.MatchTier != "auto" {
		t.Errorf("MatchTier: got %q, want %q", m.MatchTier, "auto")
	}

	_ = embPath // suppress unused warning
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
