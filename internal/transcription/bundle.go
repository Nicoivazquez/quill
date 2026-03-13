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

// BundleRenameResult holds the updated paths after a bundle directory rename.
type BundleRenameResult struct {
	NewDir       string
	AudioPath    string
	JSONPath     string
	MarkdownPath string
}

// RenameBundleDir renames a transcript bundle directory to reflect a new title.
// It extracts the shortID from the current directory name, computes the new name,
// and renames the directory on disk. Returns updated paths for all bundle files.
// If the new name matches the old name, it's a no-op.
func RenameBundleDir(currentDir, newTitle, jobID string) (BundleRenameResult, error) {
	// Verify directory exists
	if _, err := os.Stat(currentDir); err != nil {
		return BundleRenameResult{}, fmt.Errorf("bundle directory not found: %w", err)
	}

	parentDir := filepath.Dir(currentDir)
	sid := shortID(jobID)
	newSlug := slug.Sanitize(strings.TrimSpace(newTitle), "transcript")
	newDirName := fmt.Sprintf("%s-%s", newSlug, sid)
	newDir := filepath.Join(parentDir, newDirName)

	// No-op if names match (clean both paths for robustness)
	if filepath.Clean(currentDir) == filepath.Clean(newDir) {
		return buildRenameResult(currentDir)
	}

	// Guard: fail if destination already exists to prevent data loss
	if _, err := os.Stat(newDir); err == nil {
		return BundleRenameResult{}, fmt.Errorf("destination directory already exists: %s", newDir)
	}

	// Rename directory
	if err := os.Rename(currentDir, newDir); err != nil {
		return BundleRenameResult{}, fmt.Errorf("renaming bundle dir: %w", err)
	}

	return buildRenameResult(newDir)
}

// buildRenameResult scans a bundle directory and returns paths for known files.
func buildRenameResult(dir string) (BundleRenameResult, error) {
	result := BundleRenameResult{NewDir: dir}

	// Find audio file (audio.*)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result, fmt.Errorf("reading bundle dir: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "audio.") {
			result.AudioPath = filepath.Join(dir, name)
		}
	}

	// JSON and markdown use canonical names
	jsonPath := filepath.Join(dir, "transcript.json")
	if _, err := os.Stat(jsonPath); err == nil {
		result.JSONPath = jsonPath
	}
	mdPath := filepath.Join(dir, "transcript.md")
	if _, err := os.Stat(mdPath); err == nil {
		result.MarkdownPath = mdPath
	}

	return result, nil
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
