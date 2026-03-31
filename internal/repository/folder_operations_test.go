package repository

import (
	"context"
	"testing"

	"quill/internal/models"
)

// TestBulkUpdateFolderPrefix_CascadesRename verifies that renaming "Work"
// to "Projects" also updates "Work/Meetings" to "Projects/Meetings".
func TestBulkUpdateFolderPrefix_CascadesRename(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	workFolder := "Work"
	meetingsFolder := "Work/Meetings"
	otherFolder := "Personal"

	jobs := []models.TranscriptionJob{
		{ID: "job-work", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, Folder: &workFolder},
		{ID: "job-meetings", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted, Folder: &meetingsFolder},
		{ID: "job-personal", AudioPath: "/tmp/c.wav", Status: models.StatusCompleted, Folder: &otherFolder},
	}
	for _, j := range jobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job %q: %v", j.ID, err)
		}
	}

	affected, err := repo.BulkUpdateFolderPrefix(ctx, "Work", "Projects", nil)
	if err != nil {
		t.Fatalf("BulkUpdateFolderPrefix: %v", err)
	}

	if affected != 2 {
		t.Errorf("expected 2 affected rows, got %d", affected)
	}

	// Verify job-work now has folder "Projects"
	var jobWork models.TranscriptionJob
	db.First(&jobWork, "id = ?", "job-work")
	if jobWork.Folder == nil || *jobWork.Folder != "Projects" {
		t.Errorf("job-work folder: got %v, want 'Projects'", jobWork.Folder)
	}

	// Verify job-meetings now has folder "Projects/Meetings"
	var jobMeetings models.TranscriptionJob
	db.First(&jobMeetings, "id = ?", "job-meetings")
	if jobMeetings.Folder == nil || *jobMeetings.Folder != "Projects/Meetings" {
		t.Errorf("job-meetings folder: got %v, want 'Projects/Meetings'", jobMeetings.Folder)
	}

	// Verify job-personal is unaffected
	var jobPersonal models.TranscriptionJob
	db.First(&jobPersonal, "id = ?", "job-personal")
	if jobPersonal.Folder == nil || *jobPersonal.Folder != "Personal" {
		t.Errorf("job-personal folder: got %v, want 'Personal'", jobPersonal.Folder)
	}
}

// TestBulkUpdateFolderPrefix_ExactMatchOnly ensures "Work" rename doesn't
// affect "Workflow" (must be exact or exact-prefix with "/").
func TestBulkUpdateFolderPrefix_ExactMatchOnly(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	workFolder := "Work"
	workflowFolder := "Workflow"

	jobs := []models.TranscriptionJob{
		{ID: "job-work", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, Folder: &workFolder},
		{ID: "job-workflow", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted, Folder: &workflowFolder},
	}
	for _, j := range jobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job %q: %v", j.ID, err)
		}
	}

	affected, err := repo.BulkUpdateFolderPrefix(ctx, "Work", "Projects", nil)
	if err != nil {
		t.Fatalf("BulkUpdateFolderPrefix: %v", err)
	}

	if affected != 1 {
		t.Errorf("expected 1 affected row (not Workflow), got %d", affected)
	}

	// Workflow should be untouched
	var jobWorkflow models.TranscriptionJob
	db.First(&jobWorkflow, "id = ?", "job-workflow")
	if jobWorkflow.Folder == nil || *jobWorkflow.Folder != "Workflow" {
		t.Errorf("job-workflow folder should be unchanged, got %v", jobWorkflow.Folder)
	}
}

// TestBulkUpdateFolderPrefix_DeeplyNested verifies cascading through
// multiple nesting levels.
func TestBulkUpdateFolderPrefix_DeeplyNested(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	f1 := "A"
	f2 := "A/B"
	f3 := "A/B/C"

	jobs := []models.TranscriptionJob{
		{ID: "job-a", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, Folder: &f1},
		{ID: "job-ab", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted, Folder: &f2},
		{ID: "job-abc", AudioPath: "/tmp/c.wav", Status: models.StatusCompleted, Folder: &f3},
	}
	for _, j := range jobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job %q: %v", j.ID, err)
		}
	}

	affected, err := repo.BulkUpdateFolderPrefix(ctx, "A", "X", nil)
	if err != nil {
		t.Fatalf("BulkUpdateFolderPrefix: %v", err)
	}

	if affected != 3 {
		t.Errorf("expected 3 affected rows, got %d", affected)
	}

	var jobA, jobAB, jobABC models.TranscriptionJob
	db.First(&jobA, "id = ?", "job-a")
	if *jobA.Folder != "X" {
		t.Errorf("job-a: got %q, want 'X'", *jobA.Folder)
	}
	db.First(&jobAB, "id = ?", "job-ab")
	if *jobAB.Folder != "X/B" {
		t.Errorf("job-ab: got %q, want 'X/B'", *jobAB.Folder)
	}
	db.First(&jobABC, "id = ?", "job-abc")
	if *jobABC.Folder != "X/B/C" {
		t.Errorf("job-abc: got %q, want 'X/B/C'", *jobABC.Folder)
	}
}

// TestBulkUpdateFolderPrefix_VaultScoped verifies that only jobs in the
// specified vault are affected.
func TestBulkUpdateFolderPrefix_VaultScoped(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	workFolder := "Work"
	vault1 := uint(1)
	vault2 := uint(2)

	jobs := []models.TranscriptionJob{
		{ID: "job-v1", AudioPath: "/tmp/a.wav", Status: models.StatusCompleted, Folder: &workFolder, VaultID: &vault1},
		{ID: "job-v2", AudioPath: "/tmp/b.wav", Status: models.StatusCompleted, Folder: &workFolder, VaultID: &vault2},
	}
	for _, j := range jobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job %q: %v", j.ID, err)
		}
	}

	affected, err := repo.BulkUpdateFolderPrefix(ctx, "Work", "Projects", &vault1)
	if err != nil {
		t.Fatalf("BulkUpdateFolderPrefix: %v", err)
	}

	if affected != 1 {
		t.Errorf("expected 1 affected row (vault-scoped), got %d", affected)
	}

	// Vault 2 should be untouched
	var jobV2 models.TranscriptionJob
	db.First(&jobV2, "id = ?", "job-v2")
	if *jobV2.Folder != "Work" {
		t.Errorf("vault 2 job should be unchanged, got %q", *jobV2.Folder)
	}
}

// TestBulkUpdateFolderPrefix_NoMatchReturnsZero verifies that when no jobs
// match the prefix, zero rows are affected and no error occurs.
func TestBulkUpdateFolderPrefix_NoMatchReturnsZero(t *testing.T) {
	db := newSpeakerMappingTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	affected, err := repo.BulkUpdateFolderPrefix(ctx, "NonExistent", "New", nil)
	if err != nil {
		t.Fatalf("BulkUpdateFolderPrefix: %v", err)
	}
	if affected != 0 {
		t.Errorf("expected 0 affected, got %d", affected)
	}
}
