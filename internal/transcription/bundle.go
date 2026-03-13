package transcription

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"quill/pkg/slug"
)

// BundleTargetDir computes the artifact bundle directory path for a transcript.
// Layout: {vaultPath}/Transcripts/{slug}-{shortID}/
// No date-based nesting — flat under Transcripts for Obsidian-like organization.
func BundleTargetDir(vaultPath, title, jobID string) string {
	safeTitle := slug.Sanitize(strings.TrimSpace(title), "transcript")
	return filepath.Join(vaultPath, "Transcripts", fmt.Sprintf("%s-%s", safeTitle, shortID(jobID)))
}

// MoveAudioToBundle moves an audio file into the bundle directory, renaming it
// to "audio.{ext}". If the file is already in the bundle directory, it's a no-op.
// Returns the new path of the audio file.
func MoveAudioToBundle(audioPath, bundleDir string) (string, error) {
	// Check source exists
	if _, err := os.Stat(audioPath); err != nil {
		return "", fmt.Errorf("source audio not found: %w", err)
	}

	ext := filepath.Ext(audioPath)
	targetPath := filepath.Join(bundleDir, "audio"+ext)

	// If already at the target location, no-op
	absAudio, _ := filepath.Abs(audioPath)
	absTarget, _ := filepath.Abs(targetPath)
	if absAudio == absTarget {
		return audioPath, nil
	}

	// Ensure bundle directory exists
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return "", fmt.Errorf("creating bundle dir: %w", err)
	}

	// Try os.Rename first (fast, same filesystem)
	if err := os.Rename(audioPath, targetPath); err == nil {
		return targetPath, nil
	}

	// Cross-device fallback: copy + remove
	if err := copyFile(audioPath, targetPath); err != nil {
		return "", fmt.Errorf("copying audio to bundle: %w", err)
	}
	if err := os.Remove(audioPath); err != nil {
		// Non-fatal: file was copied successfully
		_ = err
	}

	return targetPath, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
