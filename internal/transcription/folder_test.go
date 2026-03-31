package transcription

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- MoveBundleToFolder tests ----------

func setupTranscripts(t *testing.T) (transcriptsDir string) {
	t.Helper()
	vaultDir := t.TempDir()
	transcriptsDir = filepath.Join(vaultDir, "Transcripts")
	if err := os.MkdirAll(transcriptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	return transcriptsDir
}

func setupBundle(t *testing.T, transcriptsDir, name string) string {
	t.Helper()
	bundleDir := filepath.Join(transcriptsDir, name)
	mustSetup(t, bundleDir, map[string]string{
		"audio.mp3":       "audio",
		"transcript.json": "{}",
		"transcript.md":   "# md",
	})
	return bundleDir
}

func TestMoveBundleToFolder_MovesToFolder(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	bundleDir := setupBundle(t, transcriptsDir, "my-recording-abc12345")

	newDir, err := MoveBundleToFolder(bundleDir, "Work")
	if err != nil {
		t.Fatalf("MoveBundleToFolder error: %v", err)
	}

	expected := filepath.Join(transcriptsDir, "Work", "my-recording-abc12345")
	if newDir != expected {
		t.Errorf("newDir = %q, want %q", newDir, expected)
	}

	// Old dir should not exist
	if _, err := os.Stat(bundleDir); !os.IsNotExist(err) {
		t.Error("old directory still exists")
	}

	// New dir should exist with files
	for _, f := range []string{"audio.mp3", "transcript.json", "transcript.md"} {
		if _, err := os.Stat(filepath.Join(newDir, f)); err != nil {
			t.Errorf("file %q not found in new dir: %v", f, err)
		}
	}
}

func TestMoveBundleToFolder_MovesToRoot(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	// Create bundle inside a folder
	folderDir := filepath.Join(transcriptsDir, "Work")
	bundleDir := setupBundle(t, folderDir, "my-recording-abc12345")

	newDir, err := MoveBundleToFolder(bundleDir, "")
	if err != nil {
		t.Fatalf("MoveBundleToFolder error: %v", err)
	}

	expected := filepath.Join(transcriptsDir, "my-recording-abc12345")
	if newDir != expected {
		t.Errorf("newDir = %q, want %q", newDir, expected)
	}
}

func TestMoveBundleToFolder_NoOp(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	bundleDir := setupBundle(t, transcriptsDir, "my-recording-abc12345")

	// Already at root, moving to root = no-op
	newDir, err := MoveBundleToFolder(bundleDir, "")
	if err != nil {
		t.Fatalf("MoveBundleToFolder error: %v", err)
	}
	if newDir != bundleDir {
		t.Errorf("expected no-op, got newDir = %q", newDir)
	}
}

func TestMoveBundleToFolder_NestedFolder(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	bundleDir := setupBundle(t, transcriptsDir, "my-recording-abc12345")

	newDir, err := MoveBundleToFolder(bundleDir, filepath.Join("Work", "Meetings"))
	if err != nil {
		t.Fatalf("MoveBundleToFolder error: %v", err)
	}

	expected := filepath.Join(transcriptsDir, "Work", "Meetings", "my-recording-abc12345")
	if newDir != expected {
		t.Errorf("newDir = %q, want %q", newDir, expected)
	}
}

func TestMoveBundleToFolder_DestinationExists(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	bundleDir := setupBundle(t, transcriptsDir, "my-recording-abc12345")

	// Create conflicting directory
	conflictDir := filepath.Join(transcriptsDir, "Work", "my-recording-abc12345")
	if err := os.MkdirAll(conflictDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := MoveBundleToFolder(bundleDir, "Work")
	if err == nil {
		t.Fatal("expected error when destination exists, got nil")
	}

	// Original should still exist
	if _, err := os.Stat(bundleDir); err != nil {
		t.Errorf("original directory should still exist: %v", err)
	}
}

func TestMoveBundleToFolder_NonExistentBundle(t *testing.T) {
	_, err := MoveBundleToFolder("/nonexistent/dir", "Work")
	if err == nil {
		t.Fatal("expected error for nonexistent bundle, got nil")
	}
}

// ---------- RenameFolderOnDisk tests ----------

func TestRenameFolderOnDisk_Renames(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	oldFolder := filepath.Join(transcriptsDir, "Work")
	if err := os.MkdirAll(oldFolder, 0755); err != nil {
		t.Fatal(err)
	}
	// Put a bundle inside
	setupBundle(t, oldFolder, "rec-abc12345")

	err := RenameFolderOnDisk(transcriptsDir, "Work", "Projects")
	if err != nil {
		t.Fatalf("RenameFolderOnDisk error: %v", err)
	}

	// Old should not exist
	if _, err := os.Stat(oldFolder); !os.IsNotExist(err) {
		t.Error("old folder still exists")
	}

	// New should exist
	newFolder := filepath.Join(transcriptsDir, "Projects")
	if _, err := os.Stat(newFolder); err != nil {
		t.Errorf("new folder not found: %v", err)
	}

	// Bundle should exist in new folder
	if _, err := os.Stat(filepath.Join(newFolder, "rec-abc12345", "audio.mp3")); err != nil {
		t.Errorf("bundle file not found in renamed folder: %v", err)
	}
}

func TestRenameFolderOnDisk_NoOp(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	folder := filepath.Join(transcriptsDir, "Work")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}

	err := RenameFolderOnDisk(transcriptsDir, "Work", "Work")
	if err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}

	if _, err := os.Stat(folder); err != nil {
		t.Errorf("folder should still exist: %v", err)
	}
}

func TestRenameFolderOnDisk_DestinationExists(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	for _, name := range []string{"Work", "Projects"} {
		if err := os.MkdirAll(filepath.Join(transcriptsDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	err := RenameFolderOnDisk(transcriptsDir, "Work", "Projects")
	if err == nil {
		t.Fatal("expected error when destination exists, got nil")
	}
}

func TestRenameFolderOnDisk_NonExistent(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	err := RenameFolderOnDisk(transcriptsDir, "NonExistent", "New")
	if err == nil {
		t.Fatal("expected error for nonexistent folder, got nil")
	}
}

// ---------- CreateFolderOnDisk tests ----------

func TestCreateFolderOnDisk_Creates(t *testing.T) {
	transcriptsDir := setupTranscripts(t)

	err := CreateFolderOnDisk(transcriptsDir, "Work")
	if err != nil {
		t.Fatalf("CreateFolderOnDisk error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(transcriptsDir, "Work")); err != nil {
		t.Errorf("folder not created: %v", err)
	}
}

func TestCreateFolderOnDisk_Nested(t *testing.T) {
	transcriptsDir := setupTranscripts(t)

	err := CreateFolderOnDisk(transcriptsDir, filepath.Join("Work", "Meetings"))
	if err != nil {
		t.Fatalf("CreateFolderOnDisk error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(transcriptsDir, "Work", "Meetings")); err != nil {
		t.Errorf("nested folder not created: %v", err)
	}
}

func TestCreateFolderOnDisk_Idempotent(t *testing.T) {
	transcriptsDir := setupTranscripts(t)

	// Create twice should not error
	for i := 0; i < 2; i++ {
		if err := CreateFolderOnDisk(transcriptsDir, "Work"); err != nil {
			t.Fatalf("CreateFolderOnDisk attempt %d error: %v", i+1, err)
		}
	}
}

// ---------- DeleteFolderOnDisk tests ----------

func TestDeleteFolderOnDisk_DeletesEmpty(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	folder := filepath.Join(transcriptsDir, "Work")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}

	err := DeleteFolderOnDisk(transcriptsDir, "Work")
	if err != nil {
		t.Fatalf("DeleteFolderOnDisk error: %v", err)
	}

	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Error("folder should be deleted")
	}
}

func TestDeleteFolderOnDisk_NonEmpty(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	setupBundle(t, filepath.Join(transcriptsDir, "Work"), "rec-abc12345")

	err := DeleteFolderOnDisk(transcriptsDir, "Work")
	if err == nil {
		t.Fatal("expected error for non-empty folder, got nil")
	}
}

func TestDeleteFolderOnDisk_NonExistent(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	err := DeleteFolderOnDisk(transcriptsDir, "NonExistent")
	if err == nil {
		t.Fatal("expected error for nonexistent folder, got nil")
	}
}

// ---------- ListFoldersOnDisk tests ----------

func TestListFoldersOnDisk_Empty(t *testing.T) {
	transcriptsDir := setupTranscripts(t)

	folders, err := ListFoldersOnDisk(transcriptsDir)
	if err != nil {
		t.Fatalf("ListFoldersOnDisk error: %v", err)
	}
	if len(folders) != 0 {
		t.Errorf("expected 0 folders, got %d: %v", len(folders), folders)
	}
}

func TestListFoldersOnDisk_WithFolders(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	for _, name := range []string{"Work", "Personal", "Archive"} {
		if err := os.MkdirAll(filepath.Join(transcriptsDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	folders, err := ListFoldersOnDisk(transcriptsDir)
	if err != nil {
		t.Fatalf("ListFoldersOnDisk error: %v", err)
	}
	if len(folders) != 3 {
		t.Fatalf("expected 3 folders, got %d: %v", len(folders), folders)
	}
	// Should be sorted
	if folders[0] != "Archive" || folders[1] != "Personal" || folders[2] != "Work" {
		t.Errorf("unexpected folder order: %v", folders)
	}
}

func TestListFoldersOnDisk_SkipsBundles(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	// Create a bundle at root (should be skipped)
	setupBundle(t, transcriptsDir, "my-recording-abc12345")
	// Create a real folder
	if err := os.MkdirAll(filepath.Join(transcriptsDir, "Work"), 0755); err != nil {
		t.Fatal(err)
	}

	folders, err := ListFoldersOnDisk(transcriptsDir)
	if err != nil {
		t.Fatalf("ListFoldersOnDisk error: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("expected 1 folder (bundles skipped), got %d: %v", len(folders), folders)
	}
	if folders[0] != "Work" {
		t.Errorf("expected Work, got %q", folders[0])
	}
}

func TestListFoldersOnDisk_NestedFolders(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	if err := os.MkdirAll(filepath.Join(transcriptsDir, "Work", "Meetings"), 0755); err != nil {
		t.Fatal(err)
	}

	folders, err := ListFoldersOnDisk(transcriptsDir)
	if err != nil {
		t.Fatalf("ListFoldersOnDisk error: %v", err)
	}
	// Should include both parent and child
	if len(folders) != 2 {
		t.Fatalf("expected 2 folders, got %d: %v", len(folders), folders)
	}
	if folders[0] != "Work" {
		t.Errorf("expected Work, got %q", folders[0])
	}
	expected := filepath.Join("Work", "Meetings")
	if folders[1] != expected {
		t.Errorf("expected %q, got %q", expected, folders[1])
	}
}

// ---------- Path traversal prevention tests ----------

func TestCreateFolderOnDisk_PathTraversal(t *testing.T) {
	transcriptsDir := setupTranscripts(t)

	cases := []string{
		"../escape",
		"../../etc",
		"valid/../../../escape",
		"foo/../../escape",
	}
	for _, name := range cases {
		err := CreateFolderOnDisk(transcriptsDir, name)
		if err == nil {
			t.Errorf("CreateFolderOnDisk(%q) should have returned an error", name)
		}
	}
}

func TestDeleteFolderOnDisk_PathTraversal(t *testing.T) {
	transcriptsDir := setupTranscripts(t)

	err := DeleteFolderOnDisk(transcriptsDir, "../../../etc")
	if err == nil {
		t.Fatal("DeleteFolderOnDisk with traversal should have returned an error")
	}
}

func TestRenameFolderOnDisk_PathTraversal(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	if err := os.MkdirAll(filepath.Join(transcriptsDir, "Legit"), 0755); err != nil {
		t.Fatal(err)
	}

	// Traversal in old name
	err := RenameFolderOnDisk(transcriptsDir, "../../../etc", "New")
	if err == nil {
		t.Fatal("RenameFolderOnDisk with old traversal should have returned an error")
	}

	// Traversal in new name
	err = RenameFolderOnDisk(transcriptsDir, "Legit", "../../../escape")
	if err == nil {
		t.Fatal("RenameFolderOnDisk with new traversal should have returned an error")
	}
}

func TestMoveBundleToFolder_PathTraversal(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	bundleDir := setupBundle(t, transcriptsDir, "my-recording-abc12345")

	_, err := MoveBundleToFolder(bundleDir, "../../../escape")
	if err == nil {
		t.Fatal("MoveBundleToFolder with traversal should have returned an error")
	}

	// Original bundle should still exist
	if _, statErr := os.Stat(bundleDir); statErr != nil {
		t.Errorf("original bundle should still exist: %v", statErr)
	}
}

// ---------- findTranscriptsRoot tests ----------

func TestFindTranscriptsRoot(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	bundleDir := filepath.Join(transcriptsDir, "my-recording-abc12345")
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		t.Fatal(err)
	}

	got := findTranscriptsRoot(bundleDir)
	if got != transcriptsDir {
		t.Errorf("findTranscriptsRoot = %q, want %q", got, transcriptsDir)
	}
}

func TestFindTranscriptsRoot_Nested(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	bundleDir := filepath.Join(transcriptsDir, "Work", "my-recording-abc12345")
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		t.Fatal(err)
	}

	got := findTranscriptsRoot(bundleDir)
	if got != transcriptsDir {
		t.Errorf("findTranscriptsRoot = %q, want %q", got, transcriptsDir)
	}
}

func TestFindTranscriptsRoot_NotFound(t *testing.T) {
	got := findTranscriptsRoot("/tmp/some/random/dir")
	if got != "" {
		t.Errorf("expected empty string for non-Transcripts path, got %q", got)
	}
}

// ---------- MoveFolderOnDisk tests ----------

func TestMoveFolderOnDisk_MoveIntoParent(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	// Create two folders: "Work" and "Archive"
	for _, f := range []string{"Work", "Archive"} {
		if err := os.MkdirAll(filepath.Join(transcriptsDir, f), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Put a bundle inside Work
	setupBundle(t, filepath.Join(transcriptsDir, "Work"), "rec-abc12345")

	err := MoveFolderOnDisk(transcriptsDir, "Work", "Archive")
	if err != nil {
		t.Fatalf("MoveFolderOnDisk error: %v", err)
	}

	// Work should now be at Archive/Work
	newPath := filepath.Join(transcriptsDir, "Archive", "Work")
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("moved folder not found at %s: %v", newPath, err)
	}
	// Bundle should exist in new location
	if _, err := os.Stat(filepath.Join(newPath, "rec-abc12345", "audio.mp3")); err != nil {
		t.Errorf("bundle not found in moved folder: %v", err)
	}
	// Original should not exist
	if _, err := os.Stat(filepath.Join(transcriptsDir, "Work")); !os.IsNotExist(err) {
		t.Error("original folder should not exist after move")
	}
}

func TestMoveFolderOnDisk_MoveToRoot(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	// Create nested folder Work/Meetings
	if err := os.MkdirAll(filepath.Join(transcriptsDir, "Work", "Meetings"), 0755); err != nil {
		t.Fatal(err)
	}
	setupBundle(t, filepath.Join(transcriptsDir, "Work", "Meetings"), "rec-abc12345")

	// Move Meetings to root (out of Work)
	err := MoveFolderOnDisk(transcriptsDir, "Work/Meetings", "")
	if err != nil {
		t.Fatalf("MoveFolderOnDisk to root error: %v", err)
	}

	// Meetings should now be at root
	if _, err := os.Stat(filepath.Join(transcriptsDir, "Meetings")); err != nil {
		t.Errorf("folder not found at root: %v", err)
	}
	// Should not exist at old location
	if _, err := os.Stat(filepath.Join(transcriptsDir, "Work", "Meetings")); !os.IsNotExist(err) {
		t.Error("folder should not exist at old location")
	}
}

func TestMoveFolderOnDisk_CircularMovePrevented(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	if err := os.MkdirAll(filepath.Join(transcriptsDir, "Work", "Meetings"), 0755); err != nil {
		t.Fatal(err)
	}

	// Moving Work into Work/Meetings is circular — should fail
	err := MoveFolderOnDisk(transcriptsDir, "Work", "Work/Meetings")
	if err == nil {
		t.Fatal("expected error for circular move, got nil")
	}
	if !strings.Contains(err.Error(), "circular") && !strings.Contains(err.Error(), "into itself") {
		t.Errorf("expected circular move error, got: %v", err)
	}
}

func TestMoveFolderOnDisk_DestinationConflict(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	// Create Archive/Work and Work — moving Work into Archive should conflict
	for _, f := range []string{"Work", "Archive/Work"} {
		if err := os.MkdirAll(filepath.Join(transcriptsDir, f), 0755); err != nil {
			t.Fatal(err)
		}
	}

	err := MoveFolderOnDisk(transcriptsDir, "Work", "Archive")
	if err == nil {
		t.Fatal("expected error for destination conflict, got nil")
	}
}

func TestMoveFolderOnDisk_NonExistent(t *testing.T) {
	transcriptsDir := setupTranscripts(t)

	err := MoveFolderOnDisk(transcriptsDir, "DoesNotExist", "Archive")
	if err == nil {
		t.Fatal("expected error for nonexistent folder, got nil")
	}
}

func TestMoveFolderOnDisk_PathTraversal(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	if err := os.MkdirAll(filepath.Join(transcriptsDir, "Legit"), 0755); err != nil {
		t.Fatal(err)
	}

	// Source traversal
	err := MoveFolderOnDisk(transcriptsDir, "../../../etc", "Legit")
	if err == nil {
		t.Fatal("expected error for source path traversal")
	}

	// Destination traversal
	err = MoveFolderOnDisk(transcriptsDir, "Legit", "../../../escape")
	if err == nil {
		t.Fatal("expected error for destination path traversal")
	}
}

func TestMoveFolderOnDisk_NoOpSameLocation(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	if err := os.MkdirAll(filepath.Join(transcriptsDir, "Archive", "Work"), 0755); err != nil {
		t.Fatal(err)
	}

	// Work is already inside Archive — moving it to Archive is a no-op
	err := MoveFolderOnDisk(transcriptsDir, "Archive/Work", "Archive")
	if err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
	// Should still exist
	if _, err := os.Stat(filepath.Join(transcriptsDir, "Archive", "Work")); err != nil {
		t.Errorf("folder should still exist: %v", err)
	}
}

func TestMoveFolderOnDisk_WithNestedSubfolders(t *testing.T) {
	transcriptsDir := setupTranscripts(t)
	// Create Work/Meetings/Daily with bundles at each level
	if err := os.MkdirAll(filepath.Join(transcriptsDir, "Work", "Meetings", "Daily"), 0755); err != nil {
		t.Fatal(err)
	}
	setupBundle(t, filepath.Join(transcriptsDir, "Work", "Meetings"), "rec-a")
	setupBundle(t, filepath.Join(transcriptsDir, "Work", "Meetings", "Daily"), "rec-b")
	if err := os.MkdirAll(filepath.Join(transcriptsDir, "Archive"), 0755); err != nil {
		t.Fatal(err)
	}

	// Move entire Work into Archive
	err := MoveFolderOnDisk(transcriptsDir, "Work", "Archive")
	if err != nil {
		t.Fatalf("MoveFolderOnDisk error: %v", err)
	}

	// Full subtree should be at Archive/Work/...
	checks := []string{
		"Archive/Work",
		"Archive/Work/Meetings",
		"Archive/Work/Meetings/Daily",
	}
	for _, p := range checks {
		if _, err := os.Stat(filepath.Join(transcriptsDir, p)); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
	// Bundles should be intact
	if _, err := os.Stat(filepath.Join(transcriptsDir, "Archive", "Work", "Meetings", "rec-a", "audio.mp3")); err != nil {
		t.Errorf("bundle rec-a not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(transcriptsDir, "Archive", "Work", "Meetings", "Daily", "rec-b", "audio.mp3")); err != nil {
		t.Errorf("bundle rec-b not found: %v", err)
	}
}
