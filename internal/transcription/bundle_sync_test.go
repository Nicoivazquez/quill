package transcription

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"quill/internal/models"
	"quill/internal/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ── ScanBundles tests (pure filesystem) ────────────────────────────────────────

func TestScanBundles_FindsBundlesAtRoot(t *testing.T) {
	dir := t.TempDir()

	bundleDir := filepath.Join(dir, "my-recording")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake")
	mustWriteMetadata(t, bundleDir, &BundleMetadata{
		ID: "test-id-1", Title: "My Recording", Status: "completed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	bundles, err := ScanBundles(dir)
	if err != nil {
		t.Fatalf("ScanBundles error: %v", err)
	}
	if len(bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(bundles))
	}
	if bundles[0].Dir != bundleDir {
		t.Errorf("expected dir %s, got %s", bundleDir, bundles[0].Dir)
	}
	if bundles[0].Folder != "" {
		t.Errorf("expected empty folder, got %q", bundles[0].Folder)
	}
	if bundles[0].MtimeNS == 0 {
		t.Error("expected non-zero mtime")
	}
}

func TestScanBundles_FindsBundlesInFolders(t *testing.T) {
	dir := t.TempDir()

	bundleDir := filepath.Join(dir, "Work", "meeting-notes")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.mp3"), "fake")
	mustWriteMetadata(t, bundleDir, &BundleMetadata{
		ID: "test-id-2", Title: "Meeting Notes", Status: "completed", Folder: "Work",
	})

	bundles, err := ScanBundles(dir)
	if err != nil {
		t.Fatalf("ScanBundles error: %v", err)
	}
	if len(bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(bundles))
	}
	if bundles[0].Folder != "Work" {
		t.Errorf("expected folder 'Work', got %q", bundles[0].Folder)
	}
}

func TestScanBundles_SkipsNonBundleDirs(t *testing.T) {
	dir := t.TempDir()

	nonBundle := filepath.Join(dir, "random-folder")
	mustMkdir(t, nonBundle)
	mustWrite(t, filepath.Join(nonBundle, "notes.txt"), "some notes")

	bundles, err := ScanBundles(dir)
	if err != nil {
		t.Fatalf("ScanBundles error: %v", err)
	}
	if len(bundles) != 0 {
		t.Fatalf("expected 0 bundles, got %d", len(bundles))
	}
}

func TestScanBundles_BundleWithoutMetadata(t *testing.T) {
	dir := t.TempDir()

	bundleDir := filepath.Join(dir, "old-recording")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake")
	mustWrite(t, filepath.Join(bundleDir, "transcript.json"), "{}")

	bundles, err := ScanBundles(dir)
	if err != nil {
		t.Fatalf("ScanBundles error: %v", err)
	}
	if len(bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(bundles))
	}
	if bundles[0].MtimeNS != 0 {
		t.Errorf("expected zero mtime for legacy bundle, got %d", bundles[0].MtimeNS)
	}
}

func TestScanBundles_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	bundles, err := ScanBundles(dir)
	if err != nil {
		t.Fatalf("ScanBundles error: %v", err)
	}
	if len(bundles) != 0 {
		t.Fatalf("expected 0 bundles, got %d", len(bundles))
	}
}

func TestScanBundles_MultipleBundlesMixedLocations(t *testing.T) {
	dir := t.TempDir()

	b1 := filepath.Join(dir, "recording-1")
	mustMkdir(t, b1)
	mustWrite(t, filepath.Join(b1, "audio.wav"), "fake")
	mustWriteMetadata(t, b1, &BundleMetadata{ID: "id-1", Status: "completed"})

	b2 := filepath.Join(dir, "Work", "recording-2")
	mustMkdir(t, b2)
	mustWrite(t, filepath.Join(b2, "audio.mp3"), "fake")
	mustWriteMetadata(t, b2, &BundleMetadata{ID: "id-2", Status: "completed", Folder: "Work"})

	b3 := filepath.Join(dir, "Personal", "2024", "recording-3")
	mustMkdir(t, b3)
	mustWrite(t, filepath.Join(b3, "transcript.json"), "{}")
	mustWriteMetadata(t, b3, &BundleMetadata{ID: "id-3", Status: "completed", Folder: "Personal/2024"})

	bundles, err := ScanBundles(dir)
	if err != nil {
		t.Fatalf("ScanBundles error: %v", err)
	}
	if len(bundles) != 3 {
		t.Fatalf("expected 3 bundles, got %d", len(bundles))
	}
}

func TestScanBundles_NestedFolders(t *testing.T) {
	dir := t.TempDir()

	bundleDir := filepath.Join(dir, "Work", "Meetings", "2024", "quarterly-review")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake")
	mustWriteMetadata(t, bundleDir, &BundleMetadata{ID: "id-nested", Status: "completed"})

	bundles, err := ScanBundles(dir)
	if err != nil {
		t.Fatalf("ScanBundles error: %v", err)
	}
	if len(bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(bundles))
	}
	expected := filepath.Join("Work", "Meetings", "2024")
	if bundles[0].Folder != expected {
		t.Errorf("expected folder %q, got %q", expected, bundles[0].Folder)
	}
}

// ── BundleSyncService tests (filesystem + DB) ─────────────────────────────────

func setupSyncTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	err = db.AutoMigrate(
		&models.TranscriptionJob{},
		&models.SpeakerMapping{},
		&models.Summary{},
		&models.Note{},
	)
	if err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db
}

func newSyncService(t *testing.T, db *gorm.DB, transcriptsDir string, vaultID *uint) *BundleSyncService {
	t.Helper()
	return NewBundleSyncService(
		repository.NewJobRepository(db),
		repository.NewSpeakerMappingRepository(db),
		repository.NewSummaryRepository(db),
		repository.NewNoteRepository(db),
		transcriptsDir,
		vaultID,
	)
}

func TestBundleSyncService_ImportsNewBundle(t *testing.T) {
	db := setupSyncTestDB(t)
	dir := t.TempDir()
	svc := newSyncService(t, db, dir, nil)

	// Create bundle on disk
	bundleDir := filepath.Join(dir, "my-recording")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake audio")
	mustWrite(t, filepath.Join(bundleDir, "transcript.json"), `{"segments":[]}`)
	mustWrite(t, filepath.Join(bundleDir, "transcript.md"), "# Transcript")
	mustWriteMetadata(t, bundleDir, &BundleMetadata{
		ID:          "import-test-1",
		Title:       "Test Import",
		Status:      "completed",
		Diarization: true,
		CreatedAt:   time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		SpeakerMappings: []SpeakerMappingEntry{
			{OriginalSpeaker: "speaker_00", CustomName: "Alice"},
			{OriginalSpeaker: "speaker_01", CustomName: "Bob"},
		},
		Summaries: []SummaryEntry{
			{Content: "Meeting about Q1 goals", Model: "gpt-4", CreatedAt: time.Now()},
		},
		Notes: []NoteEntry{
			{ID: "note-1", StartWordIndex: 0, EndWordIndex: 5, StartTime: 0.0, EndTime: 2.5,
				Quote: "Hello everyone", Content: "Opening remarks", CreatedAt: time.Now()},
		},
	})

	// Act
	result, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("sync error: %v", err)
	}

	// Assert sync result
	if result.Imported != 1 {
		t.Errorf("expected 1 imported, got %d", result.Imported)
	}
	if result.Updated != 0 {
		t.Errorf("expected 0 updated, got %d", result.Updated)
	}

	// Verify job in DB
	var job models.TranscriptionJob
	if err := db.First(&job, "id = ?", "import-test-1").Error; err != nil {
		t.Fatalf("job not found in DB: %v", err)
	}
	if job.Title == nil || *job.Title != "Test Import" {
		t.Errorf("expected title 'Test Import', got %v", job.Title)
	}
	if job.Status != models.StatusCompleted {
		t.Errorf("expected status completed, got %s", job.Status)
	}
	if !job.Diarization {
		t.Error("expected diarization=true")
	}

	// Verify speaker mappings
	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "import-test-1").Find(&mappings)
	if len(mappings) != 2 {
		t.Errorf("expected 2 speaker mappings, got %d", len(mappings))
	}

	// Verify summary
	var summaries []models.Summary
	db.Where("transcription_id = ?", "import-test-1").Find(&summaries)
	if len(summaries) != 1 {
		t.Errorf("expected 1 summary, got %d", len(summaries))
	}

	// Verify notes
	var notes []models.Note
	db.Where("transcription_id = ?", "import-test-1").Find(&notes)
	if len(notes) != 1 {
		t.Errorf("expected 1 note, got %d", len(notes))
	}
}

func TestBundleSyncService_SkipsUpToDateBundle(t *testing.T) {
	db := setupSyncTestDB(t)
	dir := t.TempDir()
	svc := newSyncService(t, db, dir, nil)

	bundleDir := filepath.Join(dir, "existing-recording")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake")
	mustWriteMetadata(t, bundleDir, &BundleMetadata{
		ID: "skip-test-1", Title: "Existing", Status: "completed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	// First sync — imports
	result1, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	if result1.Imported != 1 {
		t.Fatalf("expected 1 imported on first sync, got %d", result1.Imported)
	}

	// Second sync without changes — should skip
	result2, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}
	if result2.Skipped != 1 {
		t.Errorf("expected 1 skipped on second sync, got %d", result2.Skipped)
	}
	if result2.Imported != 0 {
		t.Errorf("expected 0 imported on second sync, got %d", result2.Imported)
	}
}

func TestBundleSyncService_UpdatesModifiedBundle(t *testing.T) {
	db := setupSyncTestDB(t)
	dir := t.TempDir()
	svc := newSyncService(t, db, dir, nil)

	bundleDir := filepath.Join(dir, "modified-recording")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake")
	mustWriteMetadata(t, bundleDir, &BundleMetadata{
		ID: "update-test-1", Title: "Original Title", Status: "completed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	// First sync — imports
	_, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}

	// Modify metadata on disk with a future mtime so it's clearly newer than the DB record.
	// We use os.Chtimes because SQLite truncates timestamps to second precision,
	// so a sub-second delay would still land in the same second.
	mustWriteMetadata(t, bundleDir, &BundleMetadata{
		ID: "update-test-1", Title: "Updated Title", Status: "completed",
		Diarization: true,
		CreatedAt:   time.Now(), UpdatedAt: time.Now(),
		SpeakerMappings: []SpeakerMappingEntry{
			{OriginalSpeaker: "speaker_00", CustomName: "Charlie"},
		},
	})
	futureTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(MetadataPath(bundleDir), futureTime, futureTime); err != nil {
		t.Fatalf("failed to set future mtime: %v", err)
	}

	// Second sync — should update
	result, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", result.Updated)
	}

	// Verify updated fields
	var job models.TranscriptionJob
	db.First(&job, "id = ?", "update-test-1")
	if job.Title == nil || *job.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got %v", job.Title)
	}
	if !job.Diarization {
		t.Error("expected diarization=true after update")
	}

	// Verify speaker mappings were replaced
	var mappings []models.SpeakerMapping
	db.Where("transcription_job_id = ?", "update-test-1").Find(&mappings)
	if len(mappings) != 1 {
		t.Errorf("expected 1 speaker mapping after update, got %d", len(mappings))
	}
}

func TestBundleSyncService_SelfWriteSkip(t *testing.T) {
	db := setupSyncTestDB(t)
	dir := t.TempDir()
	svc := newSyncService(t, db, dir, nil)

	bundleDir := filepath.Join(dir, "self-write-test")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake")
	mustWriteMetadata(t, bundleDir, &BundleMetadata{
		ID: "selfwrite-1", Title: "Self Write", Status: "completed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	// Mark as self-written with the current mtime
	metaInfo, _ := os.Stat(MetadataPath(bundleDir))
	svc.MarkSelfWrite(MetadataPath(bundleDir), metaInfo.ModTime().UnixNano())

	// Sync should skip this bundle
	result, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("sync error: %v", err)
	}
	if result.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", result.Skipped)
	}
	if result.Imported != 0 {
		t.Errorf("expected 0 imported, got %d", result.Imported)
	}
}

func TestBundleSyncService_DeletesOrphanedJob(t *testing.T) {
	db := setupSyncTestDB(t)
	dir := t.TempDir()
	vaultID := uint(1)
	svc := newSyncService(t, db, dir, &vaultID)

	// Create a job in DB that points to a bundle dir inside our transcripts dir
	orphanDir := filepath.Join(dir, "deleted-recording")
	job := &models.TranscriptionJob{
		ID:          "orphan-1",
		Status:      models.StatusCompleted,
		AudioPath:   filepath.Join(orphanDir, "audio.wav"),
		ArtifactDir: &orphanDir,
		VaultID:     &vaultID,
	}
	db.Create(job)

	// Directory doesn't exist on disk — sync should delete
	result, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("sync error: %v", err)
	}
	if result.Deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", result.Deleted)
	}

	// Verify job is gone from DB
	var count int64
	db.Model(&models.TranscriptionJob{}).Where("id = ?", "orphan-1").Count(&count)
	if count != 0 {
		t.Error("expected orphaned job to be deleted from DB")
	}
}

func TestBundleSyncService_DoesNotDeleteExistingBundle(t *testing.T) {
	db := setupSyncTestDB(t)
	dir := t.TempDir()
	vaultID := uint(1)
	svc := newSyncService(t, db, dir, &vaultID)

	// Create bundle on disk AND in DB
	bundleDir := filepath.Join(dir, "still-here")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake")
	mustWriteMetadata(t, bundleDir, &BundleMetadata{
		ID: "existing-1", Title: "Still Here", Status: "completed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	existDir := bundleDir
	job := &models.TranscriptionJob{
		ID:          "existing-1",
		Status:      models.StatusCompleted,
		AudioPath:   filepath.Join(bundleDir, "audio.wav"),
		ArtifactDir: &existDir,
		VaultID:     &vaultID,
	}
	db.Create(job)

	result, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("sync error: %v", err)
	}
	if result.Deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", result.Deleted)
	}
}

func TestBundleSyncService_LegacyBundleWithoutMetadata(t *testing.T) {
	db := setupSyncTestDB(t)
	dir := t.TempDir()
	svc := newSyncService(t, db, dir, nil)

	// Create legacy bundle (no metadata.json, just audio + transcript.md with frontmatter)
	bundleDir := filepath.Join(dir, "legacy-recording")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake")
	mustWrite(t, filepath.Join(bundleDir, "transcript.md"), `---
id: "legacy-id-1"
title: "Legacy Recording"
status: "completed"
created_at: "2024-06-01T12:00:00Z"
---

Some transcript content here.
`)

	result, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("sync error: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("expected 1 imported, got %d", result.Imported)
	}

	// Verify job was created with frontmatter data
	var job models.TranscriptionJob
	if err := db.First(&job, "id = ?", "legacy-id-1").Error; err != nil {
		t.Fatalf("legacy job not found in DB: %v", err)
	}
	if job.Title == nil || *job.Title != "Legacy Recording" {
		t.Errorf("expected title 'Legacy Recording', got %v", job.Title)
	}
}

func TestBundleSyncService_FolderInferredFromDisk(t *testing.T) {
	db := setupSyncTestDB(t)
	dir := t.TempDir()
	svc := newSyncService(t, db, dir, nil)

	// Bundle in "Projects" folder but metadata doesn't have folder field
	bundleDir := filepath.Join(dir, "Projects", "my-project")
	mustMkdir(t, bundleDir)
	mustWrite(t, filepath.Join(bundleDir, "audio.wav"), "fake")
	mustWriteMetadata(t, bundleDir, &BundleMetadata{
		ID: "folder-infer-1", Title: "Project Recording", Status: "completed",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		// Note: no Folder field set
	})

	result, err := svc.SyncFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("sync error: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("expected 1 imported, got %d", result.Imported)
	}

	// Verify folder was inferred from disk location
	var job models.TranscriptionJob
	db.First(&job, "id = ?", "folder-infer-1")
	if job.Folder == nil || *job.Folder != "Projects" {
		t.Errorf("expected folder 'Projects' inferred from disk, got %v", job.Folder)
	}
}

func TestFindAudioFile(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name     string
		files    []string
		expected string
	}{
		{"wav file", []string{"audio.wav"}, "audio.wav"},
		{"mp3 file", []string{"audio.mp3"}, "audio.mp3"},
		{"m4a file", []string{"audio.m4a"}, "audio.m4a"},
		{"no audio", []string{"transcript.json", "notes.txt"}, ""},
		{"multiple files", []string{"audio.wav", "transcript.json", "metadata.json"}, "audio.wav"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subDir := filepath.Join(dir, tt.name)
			mustMkdir(t, subDir)
			for _, f := range tt.files {
				mustWrite(t, filepath.Join(subDir, f), "content")
			}

			got := findAudioFile(subDir)
			if tt.expected == "" {
				if got != "" {
					t.Errorf("expected no audio file, got %s", got)
				}
			} else {
				expectedPath := filepath.Join(subDir, tt.expected)
				if got != expectedPath {
					t.Errorf("expected %s, got %s", expectedPath, got)
				}
			}
		})
	}
}

// ── Test helpers ───────────────────────────────────────────────────────────────

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustWriteMetadata(t *testing.T, bundleDir string, meta *BundleMetadata) {
	t.Helper()
	if err := WriteMetadata(bundleDir, meta); err != nil {
		t.Fatalf("write metadata in %s: %v", bundleDir, err)
	}
}
