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
