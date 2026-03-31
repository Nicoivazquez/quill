package transcription

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// validateFolderPath checks that a resolved folder path stays within the boundary directory.
// Prevents path traversal attacks via ".." segments or absolute paths.
func validateFolderPath(boundaryDir, folder string) error {
	if folder == "" {
		return nil
	}
	resolved := filepath.Clean(filepath.Join(boundaryDir, folder))
	boundary := filepath.Clean(boundaryDir) + string(filepath.Separator)
	if !strings.HasPrefix(resolved, boundary) {
		return fmt.Errorf("folder name escapes vault boundary")
	}
	return nil
}

// MoveBundleToFolder moves a transcript bundle directory into a folder under Transcripts/.
// If folder is empty, moves to the Transcripts root.
// Returns the new bundle directory path.
func MoveBundleToFolder(bundleDir, folder string) (string, error) {
	if _, err := os.Stat(bundleDir); err != nil {
		return "", fmt.Errorf("bundle directory not found: %w", err)
	}

	bundleName := filepath.Base(bundleDir)
	transcriptsDir := findTranscriptsRoot(bundleDir)
	if transcriptsDir == "" {
		return "", fmt.Errorf("cannot determine Transcripts root from %s", bundleDir)
	}

	if err := validateFolderPath(transcriptsDir, folder); err != nil {
		return "", err
	}

	var newDir string
	if folder == "" {
		newDir = filepath.Join(transcriptsDir, bundleName)
	} else {
		newDir = filepath.Join(transcriptsDir, folder, bundleName)
	}

	// No-op if already in the right place
	if filepath.Clean(bundleDir) == filepath.Clean(newDir) {
		return bundleDir, nil
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(newDir), 0755); err != nil {
		return "", fmt.Errorf("creating folder directory: %w", err)
	}

	// Guard: destination must not already exist
	if _, err := os.Stat(newDir); err == nil {
		return "", fmt.Errorf("destination already exists: %s", newDir)
	}

	if err := os.Rename(bundleDir, newDir); err != nil {
		return "", fmt.Errorf("moving bundle to folder: %w", err)
	}

	return newDir, nil
}

// RenameFolderOnDisk renames a folder under Transcripts/.
// Returns the new folder path.
func RenameFolderOnDisk(transcriptsDir, oldFolder, newFolder string) error {
	if err := validateFolderPath(transcriptsDir, oldFolder); err != nil {
		return err
	}
	if err := validateFolderPath(transcriptsDir, newFolder); err != nil {
		return err
	}

	oldPath := filepath.Join(transcriptsDir, oldFolder)
	newPath := filepath.Join(transcriptsDir, newFolder)

	if _, err := os.Stat(oldPath); err != nil {
		return fmt.Errorf("folder not found: %w", err)
	}

	if filepath.Clean(oldPath) == filepath.Clean(newPath) {
		return nil // no-op
	}

	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("destination folder already exists: %s", newPath)
	}

	// Ensure parent of new path exists (for nested folder renames)
	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}

	return os.Rename(oldPath, newPath)
}

// CreateFolderOnDisk creates a folder under Transcripts/.
func CreateFolderOnDisk(transcriptsDir, folder string) error {
	if err := validateFolderPath(transcriptsDir, folder); err != nil {
		return err
	}
	path := filepath.Join(transcriptsDir, folder)
	return os.MkdirAll(path, 0755)
}

// DeleteFolderOnDisk removes an empty folder under Transcripts/.
// Returns an error if the folder is not empty.
func DeleteFolderOnDisk(transcriptsDir, folder string) error {
	if err := validateFolderPath(transcriptsDir, folder); err != nil {
		return err
	}
	path := filepath.Join(transcriptsDir, folder)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("folder not found: %w", err)
	}
	return os.Remove(path)
}

// ListFoldersOnDisk scans the Transcripts directory for subdirectories (folders).
// Returns folder paths relative to the Transcripts root, sorted alphabetically.
func ListFoldersOnDisk(transcriptsDir string) ([]string, error) {
	var folders []string
	err := filepath.WalkDir(transcriptsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(transcriptsDir, path)
		if rel == "." {
			return nil
		}
		// Skip bundle directories (contain audio.* files)
		if isBundleDir(path) {
			return filepath.SkipDir
		}
		folders = append(folders, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(folders)
	return folders, nil
}

// MoveFolderOnDisk moves a folder into another parent folder under Transcripts/.
// If destParent is empty, moves to the Transcripts root.
// Prevents circular moves (moving a folder into its own subtree).
func MoveFolderOnDisk(transcriptsDir, srcFolder, destParent string) error {
	if err := validateFolderPath(transcriptsDir, srcFolder); err != nil {
		return err
	}
	if destParent != "" {
		if err := validateFolderPath(transcriptsDir, destParent); err != nil {
			return err
		}
	}

	srcPath := filepath.Join(transcriptsDir, srcFolder)
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("source folder not found: %w", err)
	}

	folderName := filepath.Base(srcFolder)
	var newPath string
	if destParent == "" {
		newPath = filepath.Join(transcriptsDir, folderName)
	} else {
		newPath = filepath.Join(transcriptsDir, destParent, folderName)
	}

	// No-op if already in the right place
	if filepath.Clean(srcPath) == filepath.Clean(newPath) {
		return nil
	}

	// Prevent circular move: destination cannot be inside source
	cleanSrc := filepath.Clean(srcPath) + string(filepath.Separator)
	cleanDest := filepath.Clean(newPath)
	if strings.HasPrefix(cleanDest, cleanSrc) {
		return fmt.Errorf("cannot move folder into itself or its own subtree (circular move)")
	}

	// Guard: destination must not already exist
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("destination already exists: %s", newPath)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return fmt.Errorf("creating destination parent: %w", err)
	}

	return os.Rename(srcPath, newPath)
}

// findTranscriptsRoot walks up from a bundle directory to find the Transcripts/ root.
func findTranscriptsRoot(bundleDir string) string {
	dir := filepath.Dir(bundleDir)
	for {
		if filepath.Base(dir) == "Transcripts" {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return ""
}

// isBundleDir checks if a directory is a transcript bundle (contains audio.* or transcript.json).
func isBundleDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "audio.") || name == "transcript.json" {
			return true
		}
	}
	return false
}
