package transcription

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------- BundleTargetDir tests ----------

func TestBundleTargetDir_WithTitle(t *testing.T) {
	got := BundleTargetDir("/vault", "My Interview Recording", "abcdefgh-1234")
	want := filepath.Join("/vault", "Transcripts", "my-interview-recording-abcdefgh")
	if got != want {
		t.Errorf("BundleTargetDir = %q, want %q", got, want)
	}
}

func TestBundleTargetDir_EmptyTitle(t *testing.T) {
	got := BundleTargetDir("/vault", "", "abcdefgh-1234")
	want := filepath.Join("/vault", "Transcripts", "transcript-abcdefgh")
	if got != want {
		t.Errorf("BundleTargetDir = %q, want %q", got, want)
	}
}

func TestBundleTargetDir_NoDateNesting(t *testing.T) {
	dir := BundleTargetDir("/vault", "test", "abc")
	// Should NOT contain year/month subdirectories
	rel, _ := filepath.Rel(filepath.Join("/vault", "Transcripts"), dir)
	if filepath.Dir(rel) != "." {
		t.Errorf("expected flat directory under Transcripts, got nested: %q", rel)
	}
}

func TestBundleTargetDir_SpecialCharactersInTitle(t *testing.T) {
	got := BundleTargetDir("/vault", "Hello, World! @#$% Test", "deadbeef")
	want := filepath.Join("/vault", "Transcripts", "hello-world-test-deadbeef")
	if got != want {
		t.Errorf("BundleTargetDir = %q, want %q", got, want)
	}
}

// ---------- MoveAudioToBundle tests ----------

func TestMoveAudioToBundle_MovesFile(t *testing.T) {
	srcDir := t.TempDir()
	bundleDir := t.TempDir()

	// Create a fake audio file
	srcPath := filepath.Join(srcDir, "abc123.mp3")
	if err := os.WriteFile(srcPath, []byte("fake audio data"), 0644); err != nil {
		t.Fatal(err)
	}

	newPath, err := MoveAudioToBundle(srcPath, bundleDir)
	if err != nil {
		t.Fatalf("MoveAudioToBundle error: %v", err)
	}

	// File should now exist in bundle dir
	expectedPath := filepath.Join(bundleDir, "audio.mp3")
	if newPath != expectedPath {
		t.Errorf("new path = %q, want %q", newPath, expectedPath)
	}

	// File should exist at new location
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("file not found at new path: %v", err)
	}

	// File should NOT exist at old location
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Errorf("file still exists at old path")
	}
}

func TestMoveAudioToBundle_PreservesExtension(t *testing.T) {
	srcDir := t.TempDir()
	bundleDir := t.TempDir()

	srcPath := filepath.Join(srcDir, "recording.wav")
	if err := os.WriteFile(srcPath, []byte("fake wav"), 0644); err != nil {
		t.Fatal(err)
	}

	newPath, err := MoveAudioToBundle(srcPath, bundleDir)
	if err != nil {
		t.Fatalf("MoveAudioToBundle error: %v", err)
	}

	if filepath.Ext(newPath) != ".wav" {
		t.Errorf("extension = %q, want .wav", filepath.Ext(newPath))
	}
	if filepath.Base(newPath) != "audio.wav" {
		t.Errorf("filename = %q, want audio.wav", filepath.Base(newPath))
	}
}

func TestMoveAudioToBundle_PreservesContent(t *testing.T) {
	srcDir := t.TempDir()
	bundleDir := t.TempDir()

	content := []byte("this is audio content 12345")
	srcPath := filepath.Join(srcDir, "test.m4a")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	newPath, err := MoveAudioToBundle(srcPath, bundleDir)
	if err != nil {
		t.Fatalf("MoveAudioToBundle error: %v", err)
	}

	got, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("reading moved file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch after move")
	}
}

func TestMoveAudioToBundle_CreatesBundleDir(t *testing.T) {
	srcDir := t.TempDir()
	bundleDir := filepath.Join(t.TempDir(), "nested", "bundle")

	srcPath := filepath.Join(srcDir, "test.mp3")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	newPath, err := MoveAudioToBundle(srcPath, bundleDir)
	if err != nil {
		t.Fatalf("MoveAudioToBundle error: %v", err)
	}

	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("file not found at new path after dir creation: %v", err)
	}
}

func TestMoveAudioToBundle_SourceNotExist(t *testing.T) {
	bundleDir := t.TempDir()
	_, err := MoveAudioToBundle("/nonexistent/audio.mp3", bundleDir)
	if err == nil {
		t.Fatal("expected error for nonexistent source, got nil")
	}
}

// ---------- RenameBundleDir tests ----------

// mustSetup creates a directory and writes files into it, failing the test on error.
func mustSetup(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRenameBundleDir_RenamesDirectory(t *testing.T) {
	vaultDir := t.TempDir()
	transcriptsDir := filepath.Join(vaultDir, "Transcripts")
	oldDir := filepath.Join(transcriptsDir, "old-title-abcdefgh")

	mustSetup(t, oldDir, map[string]string{
		"audio.mp3":       "audio",
		"transcript.json": "{}",
		"transcript.md":   "# md",
	})

	result, err := RenameBundleDir(oldDir, "New Title", "abcdefgh-1234-5678")
	if err != nil {
		t.Fatalf("RenameBundleDir error: %v", err)
	}

	// Old dir should not exist
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("old directory still exists")
	}

	// New dir should exist
	if _, err := os.Stat(result.NewDir); err != nil {
		t.Errorf("new directory not found: %v", err)
	}

	// Files should exist in new dir
	for _, name := range []string{"audio.mp3", "transcript.json", "transcript.md"} {
		if _, err := os.Stat(filepath.Join(result.NewDir, name)); err != nil {
			t.Errorf("file %q not found in new dir: %v", name, err)
		}
	}

	// NewDir should contain new-title slug
	if base := filepath.Base(result.NewDir); base != "new-title-abcdefgh" {
		t.Errorf("new dir base = %q, want %q", base, "new-title-abcdefgh")
	}
}

func TestRenameBundleDir_UpdatesPaths(t *testing.T) {
	vaultDir := t.TempDir()
	transcriptsDir := filepath.Join(vaultDir, "Transcripts")
	oldDir := filepath.Join(transcriptsDir, "old-title-abcd1234")

	mustSetup(t, oldDir, map[string]string{
		"audio.wav":       "wav",
		"transcript.json": "{}",
		"transcript.md":   "md",
	})

	result, err := RenameBundleDir(oldDir, "Updated Title", "abcd1234-5678")
	if err != nil {
		t.Fatalf("RenameBundleDir error: %v", err)
	}

	expectedDir := filepath.Join(transcriptsDir, "updated-title-abcd1234")
	if result.NewDir != expectedDir {
		t.Errorf("NewDir = %q, want %q", result.NewDir, expectedDir)
	}

	// Verify paths are under new dir
	if filepath.Dir(result.AudioPath) != result.NewDir {
		t.Errorf("AudioPath %q not under NewDir %q", result.AudioPath, result.NewDir)
	}
	if filepath.Dir(result.JSONPath) != result.NewDir {
		t.Errorf("JSONPath %q not under NewDir %q", result.JSONPath, result.NewDir)
	}
	if filepath.Dir(result.MarkdownPath) != result.NewDir {
		t.Errorf("MarkdownPath %q not under NewDir %q", result.MarkdownPath, result.NewDir)
	}
}

func TestRenameBundleDir_SameTitle_NoOp(t *testing.T) {
	vaultDir := t.TempDir()
	transcriptsDir := filepath.Join(vaultDir, "Transcripts")
	dir := filepath.Join(transcriptsDir, "my-title-abcdefgh")

	mustSetup(t, dir, map[string]string{"audio.mp3": "data"})

	result, err := RenameBundleDir(dir, "My Title", "abcdefgh-1234")
	if err != nil {
		t.Fatalf("RenameBundleDir error: %v", err)
	}

	if result.NewDir != dir {
		t.Errorf("expected no-op, got NewDir = %q, want %q", result.NewDir, dir)
	}

	// Original dir should still exist
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("directory should still exist: %v", err)
	}
}

func TestRenameBundleDir_PreservesAllFiles(t *testing.T) {
	vaultDir := t.TempDir()
	transcriptsDir := filepath.Join(vaultDir, "Transcripts")
	oldDir := filepath.Join(transcriptsDir, "old-abcdefgh")

	files := map[string]string{
		"audio.m4a":       "audio content",
		"transcript.json": `{"segments":[]}`,
		"transcript.md":   "# Transcript",
		"extra-file.txt":  "extra data",
	}
	mustSetup(t, oldDir, files)

	result, err := RenameBundleDir(oldDir, "New Name", "abcdefgh")
	if err != nil {
		t.Fatalf("RenameBundleDir error: %v", err)
	}

	// All files should be preserved
	for name, expectedContent := range files {
		data, err := os.ReadFile(filepath.Join(result.NewDir, name))
		if err != nil {
			t.Errorf("file %q not found in new dir: %v", name, err)
			continue
		}
		if string(data) != expectedContent {
			t.Errorf("file %q content mismatch: got %q, want %q", name, string(data), expectedContent)
		}
	}
}

func TestRenameBundleDir_DestinationExists(t *testing.T) {
	vaultDir := t.TempDir()
	transcriptsDir := filepath.Join(vaultDir, "Transcripts")
	oldDir := filepath.Join(transcriptsDir, "old-title-abcdefgh")
	conflictDir := filepath.Join(transcriptsDir, "new-title-abcdefgh")

	// Create both directories
	if err := os.MkdirAll(oldDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(conflictDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "audio.mp3"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conflictDir, "audio.mp3"), []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := RenameBundleDir(oldDir, "New Title", "abcdefgh-1234")
	if err == nil {
		t.Fatal("expected error when destination exists, got nil")
	}

	// Original dir should still exist (no data loss)
	if _, statErr := os.Stat(oldDir); statErr != nil {
		t.Errorf("original directory should still exist: %v", statErr)
	}
}

func TestRenameBundleDir_NonExistentDir(t *testing.T) {
	_, err := RenameBundleDir("/nonexistent/dir", "Title", "abc")
	if err == nil {
		t.Fatal("expected error for nonexistent directory, got nil")
	}
}

func TestRenameBundleDir_DiscoverAudioExtension(t *testing.T) {
	vaultDir := t.TempDir()
	transcriptsDir := filepath.Join(vaultDir, "Transcripts")
	oldDir := filepath.Join(transcriptsDir, "old-abc12345")

	mustSetup(t, oldDir, map[string]string{
		"audio.ogg":       "ogg",
		"transcript.json": "{}",
		"transcript.md":   "md",
	})

	result, err := RenameBundleDir(oldDir, "New", "abc12345")
	if err != nil {
		t.Fatalf("RenameBundleDir error: %v", err)
	}

	if filepath.Base(result.AudioPath) != "audio.ogg" {
		t.Errorf("AudioPath base = %q, want audio.ogg", filepath.Base(result.AudioPath))
	}
}

func TestMoveAudioToBundle_AlreadyInBundle(t *testing.T) {
	bundleDir := t.TempDir()

	// Audio file already in the bundle dir
	srcPath := filepath.Join(bundleDir, "audio.mp3")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	newPath, err := MoveAudioToBundle(srcPath, bundleDir)
	if err != nil {
		t.Fatalf("MoveAudioToBundle error: %v", err)
	}

	// Should return the same path (no-op)
	if newPath != srcPath {
		t.Errorf("expected same path when already in bundle, got %q", newPath)
	}

	// File should still exist
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("file should still exist: %v", err)
	}
}
