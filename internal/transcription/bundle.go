package transcription

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"quill/pkg/logger"
	"quill/pkg/slug"
)

// BundleTargetDir computes the artifact bundle directory path for a transcript.
// Layout: {vaultPath}/Transcripts/{human-readable title}/
// No date-based nesting — flat under Transcripts for Obsidian-like organization.
// Uses SafeFilename for a clean, human-readable folder name and UniqueName
// to resolve collisions (Finder-style " 2", " 3" suffixes).
func BundleTargetDir(vaultPath, title, jobID string) string {
	safeTitle := slug.SafeFilename(strings.TrimSpace(title), "Transcript")
	transcriptsDir := filepath.Join(vaultPath, "Transcripts")
	return filepath.Join(transcriptsDir, slug.UniqueName(transcriptsDir, safeTitle))
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
		logger.Debug("MoveAudioToBundle: no-op, audio already in bundle",
			"audio_path", audioPath, "bundle_dir", bundleDir)
		return audioPath, nil
	}

	logger.Debug("MoveAudioToBundle: moving audio to bundle",
		"from", absAudio, "to", absTarget, "bundle_dir", bundleDir)

	// Ensure bundle directory exists
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return "", fmt.Errorf("creating bundle dir: %w", err)
	}

	// Try os.Rename first (fast, same filesystem)
	if err := os.Rename(audioPath, targetPath); err == nil {
		logger.Debug("MoveAudioToBundle: rename succeeded", "target", targetPath)
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
	safeName := slug.SafeFilename(strings.TrimSpace(newTitle), "Transcript")
	newDir := filepath.Join(parentDir, safeName)

	// No-op if names match (clean both paths for robustness)
	if filepath.Clean(currentDir) == filepath.Clean(newDir) {
		return buildRenameResult(currentDir)
	}

	// Guard: fail if destination already exists to prevent data loss
	if _, err := os.Stat(newDir); err == nil {
		return BundleRenameResult{}, fmt.Errorf("destination directory already exists: %s", newDir)
	}

	// Rename directory
	logger.Debug("RenameBundleDir: renaming bundle",
		"from", currentDir, "to", newDir, "job_id", jobID)
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

// CopyAudioToBundle copies an audio file into the bundle directory as "audio.{ext}".
// Unlike MoveAudioToBundle, the source file is preserved.
// Returns the new path of the audio file in the bundle.
func CopyAudioToBundle(audioPath, bundleDir string) (string, error) {
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

	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return "", fmt.Errorf("creating bundle dir: %w", err)
	}

	if err := copyFile(audioPath, targetPath); err != nil {
		return "", fmt.Errorf("copying audio to bundle: %w", err)
	}

	return targetPath, nil
}

// VerifyAudioInBundle checks that an audio.* file exists in the bundle directory.
func VerifyAudioInBundle(bundleDir string) error {
	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		return fmt.Errorf("reading bundle dir: %w", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "audio.") {
			info, infoErr := e.Info()
			if infoErr != nil {
				return fmt.Errorf("stat audio file: %w", infoErr)
			}
			if info.Size() == 0 {
				return fmt.Errorf("audio file is empty: %s", e.Name())
			}
			return nil
		}
	}
	return fmt.Errorf("no audio file found in bundle %s", bundleDir)
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
