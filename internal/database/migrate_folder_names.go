package database

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"quill/internal/models"
	"quill/pkg/logger"
	"quill/pkg/slug"

	"gorm.io/gorm"
)

// shortIDSuffix matches "-{8 hex chars}" at the end of a folder name.
var shortIDSuffix = regexp.MustCompile(`-[0-9a-f]{8}$`)

// legacyContactSeparator matches "--{uuid-like}" in contact folder names.
var legacyContactSeparator = regexp.MustCompile(`--[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]+$`)

// MigrateHumanReadableFolderNames renames legacy machine-readable bundle and
// contact folders to human-readable names. This is idempotent — folders that
// have already been migrated are skipped.
//
// Bundle folders: "talk-with-ian-c2880060" → "Talk with Ian"
// Contact folders: "john-smith--c1a2b3c4-d5e6-47f8" → "John Smith"
func MigrateHumanReadableFolderNames(db *gorm.DB) error {
	var vaults []models.Vault
	if err := db.Find(&vaults).Error; err != nil {
		return fmt.Errorf("migrate-folder-names: list vaults: %w", err)
	}

	for _, v := range vaults {
		if strings.TrimSpace(v.Path) == "" {
			continue
		}
		if _, err := os.Stat(v.Path); os.IsNotExist(err) {
			continue
		}

		migrateBundleFolders(db, v.Path)
		migrateContactFolders(db, v.Path)
	}
	return nil
}

// migrateBundleFolders renames transcript bundle folders from
// "{slug}-{shortid}" to a human-readable title.
func migrateBundleFolders(db *gorm.DB, vaultPath string) {
	transcriptsDir := filepath.Join(vaultPath, "Transcripts")
	if _, err := os.Stat(transcriptsDir); os.IsNotExist(err) {
		return
	}

	entries, err := os.ReadDir(transcriptsDir)
	if err != nil {
		logger.Warn("migrate-folder-names: read transcripts dir", "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()

		// Only process folders matching the old "{slug}-{8-hex}" pattern.
		if !shortIDSuffix.MatchString(dirName) {
			continue
		}

		folderAbs := filepath.Join(transcriptsDir, dirName)

		// Try to read the job title from metadata.json or the DB.
		title := resolveBundleTitle(db, folderAbs, dirName)
		if title == "" {
			continue
		}

		safeName := slug.SafeFilename(title, "Transcript")
		newFolderAbs := filepath.Join(transcriptsDir, slug.UniqueName(transcriptsDir, safeName))

		if filepath.Clean(folderAbs) == filepath.Clean(newFolderAbs) {
			continue
		}

		if err := os.Rename(folderAbs, newFolderAbs); err != nil {
			logger.Warn("migrate-folder-names: rename bundle",
				"from", dirName, "to", filepath.Base(newFolderAbs), "error", err)
			continue
		}

		// Update DB paths that reference the old directory.
		oldRel := filepath.ToSlash(filepath.Join("Transcripts", dirName))
		newRel := filepath.ToSlash(filepath.Join("Transcripts", filepath.Base(newFolderAbs)))
		updateJobPaths(db, oldRel, newRel)

		logger.Info("migrate-folder-names: renamed bundle",
			"from", dirName, "to", filepath.Base(newFolderAbs))
	}
}

// resolveBundleTitle tries to find the job title for a bundle folder.
func resolveBundleTitle(db *gorm.DB, folderAbs, dirName string) string {
	// Try metadata.json first.
	metaPath := filepath.Join(folderAbs, "metadata.json")
	if data, err := os.ReadFile(metaPath); err == nil {
		type metaPayload struct {
			Title string `json:"title"`
		}
		var m metaPayload
		if jsonErr := json.Unmarshal(data, &m); jsonErr == nil && strings.TrimSpace(m.Title) != "" {
			return strings.TrimSpace(m.Title)
		}
	}

	// Fallback: query DB for a job whose artifact_dir matches.
	var job models.TranscriptionJob
	// Match by the folder name fragment in artifact_dir or audio_path.
	err := db.Where("artifact_dir LIKE ? OR audio_path LIKE ?",
		"%"+dirName+"%", "%"+dirName+"%").
		First(&job).Error
	if err == nil && job.Title != nil && strings.TrimSpace(*job.Title) != "" {
		return strings.TrimSpace(*job.Title)
	}

	// Last resort: humanize the slug portion (strip the shortID suffix).
	slugPart := shortIDSuffix.ReplaceAllString(dirName, "")
	return humanizeSlug(slugPart)
}

// migrateContactFolders renames contact folders from "{slug}--{uid}" to
// human-readable names.
func migrateContactFolders(db *gorm.DB, vaultPath string) {
	peopleDir := filepath.Join(vaultPath, "Contacts", "People")
	if _, err := os.Stat(peopleDir); os.IsNotExist(err) {
		return
	}

	entries, err := os.ReadDir(peopleDir)
	if err != nil {
		logger.Warn("migrate-folder-names: read contacts dir", "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()

		// Only process folders matching the old "{slug}--{uid}" pattern.
		if !strings.Contains(dirName, "--") {
			continue
		}

		folderAbs := filepath.Join(peopleDir, dirName)

		// Read contact name from frontmatter.
		contactName := resolveContactName(db, folderAbs, dirName)
		if contactName == "" {
			continue
		}

		safeName := slug.SafeFilename(contactName, "Contact")
		newFolderAbs := filepath.Join(peopleDir, slug.UniqueName(peopleDir, safeName))

		if filepath.Clean(folderAbs) == filepath.Clean(newFolderAbs) {
			continue
		}

		if err := os.Rename(folderAbs, newFolderAbs); err != nil {
			logger.Warn("migrate-folder-names: rename contact",
				"from", dirName, "to", filepath.Base(newFolderAbs), "error", err)
			continue
		}

		// Update DB paths for this contact.
		oldRel := filepath.ToSlash(filepath.Join("Contacts", "People", dirName))
		newRel := filepath.ToSlash(filepath.Join("Contacts", "People", filepath.Base(newFolderAbs)))
		updateContactPaths(db, oldRel, newRel)

		logger.Info("migrate-folder-names: renamed contact",
			"from", dirName, "to", filepath.Base(newFolderAbs))
	}
}

// resolveContactName reads the contact name from frontmatter or the DB.
func resolveContactName(db *gorm.DB, folderAbs, dirName string) string {
	notePath := filepath.Join(folderAbs, "contact.md")
	if data, err := os.ReadFile(notePath); err == nil {
		name := extractFrontmatterValue(string(data), "name")
		if name != "" {
			return name
		}
	}

	// Fallback: query DB.
	var contact models.Contact
	if err := db.Where("note_path LIKE ?", "%"+dirName+"%").First(&contact).Error; err == nil {
		if strings.TrimSpace(contact.Name) != "" {
			return contact.Name
		}
	}

	// Last resort: humanize the slug portion.
	idx := strings.LastIndex(dirName, "--")
	if idx > 0 {
		return humanizeSlug(dirName[:idx])
	}
	return ""
}

// updateJobPaths updates all TranscriptionJob path columns that reference
// the old relative folder.
func updateJobPaths(db *gorm.DB, oldRel, newRel string) {
	for _, col := range []string{"audio_path", "artifact_dir", "transcript_json_path", "transcript_markdown_path"} {
		db.Model(&models.TranscriptionJob{}).
			Where(col+" LIKE ?", "%"+oldRel+"%").
			Update(col, gorm.Expr("REPLACE("+col+", ?, ?)", oldRel, newRel))
	}
}

// updateContactPaths updates all Contact path columns that reference
// the old relative folder.
func updateContactPaths(db *gorm.DB, oldRel, newRel string) {
	for _, col := range []string{"note_path", "voice_snippet_path", "signature_embedding_path"} {
		db.Model(&models.Contact{}).
			Where(col+" LIKE ?", "%"+oldRel+"%").
			Update(col, gorm.Expr("REPLACE("+col+", ?, ?)", oldRel, newRel))
	}
}

// humanizeSlug converts "talk-with-ian" back to "Talk with Ian".
func humanizeSlug(s string) string {
	words := strings.Split(s, "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	result := strings.Join(words, " ")
	return strings.TrimSpace(result)
}

// extractFrontmatterValue extracts a value from YAML frontmatter.
func extractFrontmatterValue(content, key string) string {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	prefix := key + ":"
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			break
		}
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, prefix) {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			// Remove surrounding quotes if present.
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}
