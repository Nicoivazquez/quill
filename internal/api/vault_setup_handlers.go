package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"quill/internal/contacts"
	"quill/internal/database"
	"quill/internal/models"
	"quill/pkg/logger"
	"quill/pkg/slug"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type SetupStateResponse struct {
	Completed        bool           `json:"completed"`
	AuthMode         string         `json:"auth_mode"`
	ActiveVault      *models.Vault  `json:"active_vault,omitempty"`
	Vaults           []models.Vault `json:"vaults"`
	ObsidianVaultDir *string        `json:"obsidian_vault_dir,omitempty"`
	OpenClawDropDir  *string        `json:"openclaw_drop_dir,omitempty"`
}

type CompleteSetupRequest struct {
	VaultPath        string  `json:"vault_path" binding:"required"`
	VaultName        *string `json:"vault_name,omitempty"`
	VaultMode        *string `json:"vault_mode,omitempty"`
	AuthMode         *string `json:"auth_mode,omitempty"`
	ObsidianVaultDir *string `json:"obsidian_vault_dir,omitempty"`
	OpenClawDropDir  *string `json:"openclaw_drop_dir,omitempty"`
}

type VaultRequest struct {
	Name     string `json:"name"`
	Path     string `json:"path" binding:"required"`
	Mode     string `json:"mode,omitempty"`
	Activate *bool  `json:"activate,omitempty"`
}

type VaultUpdateRequest struct {
	Name *string `json:"name,omitempty"`
	Path *string `json:"path,omitempty"`
}

type ObsidianConfigRequest struct {
	VaultPath string `json:"vault_path" binding:"required"`
}

type OpenClawConfigRequest struct {
	DropDir string `json:"drop_dir" binding:"required"`
}

type OpenClawIngestDropRequest struct {
	Limit   int   `json:"limit,omitempty"`
	Consume *bool `json:"consume,omitempty"`
}

type ContactRequest struct {
	Name  string  `json:"name" binding:"required"`
	Phone *string `json:"phone,omitempty"`
	Email *string `json:"email,omitempty"`
	Notes *string `json:"notes,omitempty"`
}

const (
	maxSignatureUploadBytes   int64 = 10 << 20
	signatureArtifactFileName       = "voice-signature.embedding.json"
	signatureSourceManual           = "manual"
	signatureSourceExtracted        = "extracted"
)

type contactSignatureMetadata struct {
	Source    string `json:"source"`
	Model     string `json:"model,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type voiceSignaturePayload struct {
	Version   int       `json:"version"`
	Model     string    `json:"model"`
	Dimension int       `json:"dimension"`
	Vector    []float64 `json:"vector"`
}

type transcriptSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker *string `json:"speaker,omitempty"`
}

type transcriptPayload struct {
	Text     string              `json:"text"`
	Segments []transcriptSegment `json:"segments,omitempty"`
}

type transcriptFrontmatter struct {
	ID        string
	Title     string
	Status    string
	CreatedAt string
	UpdatedAt string
}

func normalizeAuthMode(input *string) string {
	if input == nil || strings.TrimSpace(*input) == "" {
		return "local"
	}
	value := strings.ToLower(strings.TrimSpace(*input))
	if value != "server" {
		return "local"
	}
	return value
}

func normalizeVaultMode(input *string) string {
	if input == nil || strings.TrimSpace(*input) == "" {
		return ""
	}
	value := strings.ToLower(strings.TrimSpace(*input))
	if value == "existing" || value == "create" {
		return value
	}
	return ""
}

func deriveVaultNameFromPath(vaultPath string) string {
	base := strings.TrimSpace(filepath.Base(vaultPath))
	if base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	return "Vault"
}

func detectExistingVault(vaultPath string) (bool, error) {
	markers := []string{
		filepath.Join(vaultPath, ".quill"),
		filepath.Join(vaultPath, ".scriber"), // Legacy marker for older installs
		filepath.Join(vaultPath, "Inbox"),
		filepath.Join(vaultPath, "Media"),
		filepath.Join(vaultPath, "Transcripts"),
	}

	for _, marker := range markers {
		_, err := os.Stat(marker)
		if err == nil {
			return true, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}

	return false, nil
}

func ensureVaultStructure(vaultPath string) error {
	dirs := []string{
		filepath.Join(vaultPath, "Inbox"),
		filepath.Join(vaultPath, "Media"),
		filepath.Join(vaultPath, "Transcripts"),
		filepath.Join(vaultPath, "Contacts", "Snippets"),
		filepath.Join(vaultPath, ".quill"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

func formatMMSS(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds)
	minutes := total / 60
	secs := total % 60
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

func parseTranscriptJSON(raw string) (*transcriptPayload, error) {
	var payload transcriptPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func parseFrontmatterValue(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		if unquoted, err := strconv.Unquote(trimmed); err == nil {
			return strings.TrimSpace(unquoted)
		}
	}

	if len(trimmed) >= 2 && trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'' {
		return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}

	return trimmed
}

func parseTranscriptFrontmatter(markdown string) (*transcriptFrontmatter, bool) {
	normalized := strings.ReplaceAll(markdown, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return nil, false
	}

	rest := normalized[len("---\n"):]
	endIdx := strings.Index(rest, "\n---\n")
	if endIdx < 0 {
		return nil, false
	}

	rawFrontmatter := rest[:endIdx]
	meta := &transcriptFrontmatter{}

	for _, line := range strings.Split(rawFrontmatter, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := parseFrontmatterValue(parts[1])

		switch key {
		case "id":
			meta.ID = value
		case "title":
			meta.Title = value
		case "status":
			meta.Status = value
		case "created_at":
			meta.CreatedAt = value
		case "updated_at":
			meta.UpdatedAt = value
		}
	}

	if strings.TrimSpace(meta.ID) == "" {
		return nil, false
	}

	return meta, true
}

func normalizeRecoveredJobStatus(value string) models.JobStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(models.StatusUploaded):
		return models.StatusUploaded
	case string(models.StatusPending):
		return models.StatusPending
	case string(models.StatusProcessing):
		return models.StatusProcessing
	case string(models.StatusFailed):
		return models.StatusFailed
	default:
		return models.StatusCompleted
	}
}

func parseFrontmatterTimestamp(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func locateAudioPathForRecoveredJob(vaultPath, jobID string) string {
	trimmedID := strings.TrimSpace(jobID)
	if trimmedID == "" {
		return ""
	}

	candidateGlobs := []string{
		filepath.Join(vaultPath, "Inbox", "Media", trimmedID+".*"),
		filepath.Join(vaultPath, "Media", trimmedID+".*"),
		filepath.Join(vaultPath, "Inbox", "OpenClaw", trimmedID+".*"),
	}

	for _, pattern := range candidateGlobs {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			continue
		}
		sort.Strings(matches)
		return matches[0]
	}

	var discovered string
	_ = filepath.WalkDir(vaultPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || discovered != "" || d == nil || d.IsDir() {
			return nil
		}
		fileName := d.Name()
		ext := strings.ToLower(filepath.Ext(fileName))
		if !isSupportedIngestExtension(ext) {
			return nil
		}
		baseName := strings.TrimSuffix(fileName, ext)
		if baseName == trimmedID {
			discovered = path
		}
		return nil
	})

	return discovered
}

func collectVaultTranscriptMarkdownFiles(vaultPath string) ([]string, error) {
	transcriptsDir := filepath.Join(vaultPath, "Transcripts")
	if _, err := os.Stat(transcriptsDir); err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	paths := make([]string, 0, 64)
	err := filepath.WalkDir(transcriptsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d == nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), "transcript.md") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func recoverJobsFromVaultArtifacts(vault models.Vault) (int, error) {
	markdownFiles, err := collectVaultTranscriptMarkdownFiles(vault.Path)
	if err != nil {
		return 0, err
	}

	recoveredCount := 0
	for _, markdownPath := range markdownFiles {
		markdownBytes, readErr := os.ReadFile(markdownPath)
		if readErr != nil {
			continue
		}

		meta, ok := parseTranscriptFrontmatter(string(markdownBytes))
		if !ok {
			continue
		}

		jobID := strings.TrimSpace(meta.ID)
		if jobID == "" {
			continue
		}

		title := strings.TrimSpace(meta.Title)
		if title == "" {
			title = strings.TrimSpace(filepath.Base(filepath.Dir(markdownPath)))
		}
		var titlePtr *string
		if title != "" {
			titlePtr = &title
		}

		createdAt, createdParsed := parseFrontmatterTimestamp(meta.CreatedAt)
		updatedAt, updatedParsed := parseFrontmatterTimestamp(meta.UpdatedAt)
		if !createdParsed || !updatedParsed {
			stat, statErr := os.Stat(markdownPath)
			if statErr == nil {
				if !createdParsed {
					createdAt = stat.ModTime()
				}
				if !updatedParsed {
					updatedAt = stat.ModTime()
				}
			}
		}
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		if updatedAt.IsZero() {
			updatedAt = createdAt
		}

		artifactDir := filepath.Dir(markdownPath)
		artifactDirPtr := &artifactDir
		markdownPathValue := markdownPath
		markdownPathPtr := &markdownPathValue

		jsonPath := filepath.Join(artifactDir, "transcript.json")
		var jsonPathPtr *string
		var transcriptPtr *string
		if _, statErr := os.Stat(jsonPath); statErr == nil {
			jsonPathPtr = &jsonPath
			if transcriptBytes, transcriptErr := os.ReadFile(jsonPath); transcriptErr == nil {
				trimmedTranscript := strings.TrimSpace(string(transcriptBytes))
				if trimmedTranscript != "" {
					transcriptPtr = &trimmedTranscript
				}
			}
		}

		audioPath := locateAudioPathForRecoveredJob(vault.Path, jobID)

		var existing models.TranscriptionJob
		lookupErr := database.DB.Where("id = ?", jobID).First(&existing).Error
		if lookupErr == nil {
			updates := map[string]interface{}{}
			if existing.VaultID == nil || *existing.VaultID != vault.ID {
				updates["vault_id"] = vault.ID
			}
			if (existing.Title == nil || strings.TrimSpace(*existing.Title) == "") && titlePtr != nil {
				updates["title"] = *titlePtr
			}
			if strings.TrimSpace(existing.AudioPath) == "" && strings.TrimSpace(audioPath) != "" {
				updates["audio_path"] = audioPath
			}
			if existing.ArtifactDir == nil || strings.TrimSpace(*existing.ArtifactDir) == "" {
				updates["artifact_dir"] = *artifactDirPtr
			}
			if existing.TranscriptMarkdownPath == nil || strings.TrimSpace(*existing.TranscriptMarkdownPath) == "" {
				updates["transcript_markdown_path"] = *markdownPathPtr
			}
			if jsonPathPtr != nil && (existing.TranscriptJSONPath == nil || strings.TrimSpace(*existing.TranscriptJSONPath) == "") {
				updates["transcript_json_path"] = *jsonPathPtr
			}
			if transcriptPtr != nil && (existing.Transcript == nil || strings.TrimSpace(*existing.Transcript) == "") {
				updates["transcript"] = *transcriptPtr
			}
			if existing.CreatedAt.IsZero() {
				updates["created_at"] = createdAt
			}
			if updatedAt.After(existing.UpdatedAt) {
				updates["updated_at"] = updatedAt
			}

			if len(updates) > 0 {
				if updateErr := database.DB.Model(&models.TranscriptionJob{}).Where("id = ?", existing.ID).Updates(updates).Error; updateErr != nil {
					return recoveredCount, updateErr
				}
			}

			recoveredCount++
			continue
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return recoveredCount, lookupErr
		}

		job := models.TranscriptionJob{
			ID:                     jobID,
			Title:                  titlePtr,
			Status:                 normalizeRecoveredJobStatus(meta.Status),
			AudioPath:              audioPath,
			VaultID:                &vault.ID,
			ArtifactDir:            artifactDirPtr,
			TranscriptJSONPath:     jsonPathPtr,
			TranscriptMarkdownPath: markdownPathPtr,
			Transcript:             transcriptPtr,
			CreatedAt:              createdAt,
			UpdatedAt:              updatedAt,
		}
		if job.AudioPath == "" {
			job.AudioPath = filepath.Join(vault.Path, "Inbox", "Media", job.ID)
		}

		if createErr := database.DB.Create(&job).Error; createErr != nil {
			return recoveredCount, createErr
		}
		recoveredCount++
	}

	return recoveredCount, nil
}

func shortID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func isSupportedIngestExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".wav", ".mp3", ".m4a", ".aac", ".flac", ".ogg", ".opus", ".webm", ".mp4", ".mov", ".mkv":
		return true
	default:
		return false
	}
}

func copyFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

func renderTranscriptMarkdown(job *models.TranscriptionJob, transcript *transcriptPayload) string {
	title := "Untitled"
	if job.Title != nil && strings.TrimSpace(*job.Title) != "" {
		title = strings.TrimSpace(*job.Title)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("id: %s\n", job.ID))
	b.WriteString(fmt.Sprintf("title: %q\n", title))
	b.WriteString(fmt.Sprintf("status: %s\n", job.Status))
	b.WriteString(fmt.Sprintf("created_at: %s\n", job.CreatedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("updated_at: %s\n", job.UpdatedAt.Format(time.RFC3339)))
	b.WriteString("format: transcript-markdown-v1\n")
	b.WriteString("---\n\n")
	b.WriteString(fmt.Sprintf("# %s\n\n", title))

	if len(transcript.Segments) == 0 {
		b.WriteString(strings.TrimSpace(transcript.Text))
		b.WriteString("\n")
		return b.String()
	}

	for _, segment := range transcript.Segments {
		prefix := fmt.Sprintf("[%s - %s]", formatMMSS(segment.Start), formatMMSS(segment.End))
		if segment.Speaker != nil && strings.TrimSpace(*segment.Speaker) != "" {
			prefix += " " + strings.TrimSpace(*segment.Speaker) + ":"
		}
		b.WriteString(prefix)
		b.WriteString(" ")
		b.WriteString(strings.TrimSpace(segment.Text))
		b.WriteString("\n\n")
	}
	return b.String()
}

func writeTranscriptArtifactsForJob(job *models.TranscriptionJob) error {
	if job.Transcript == nil || strings.TrimSpace(*job.Transcript) == "" {
		return nil
	}

	transcript, err := parseTranscriptJSON(*job.Transcript)
	if err != nil {
		return err
	}

	var activeVault models.Vault
	vaultErr := database.DB.Where("is_active = ?", true).First(&activeVault).Error

	var baseDir string
	if vaultErr == nil {
		title := "transcript"
		if job.Title != nil && strings.TrimSpace(*job.Title) != "" {
			title = *job.Title
		}
		year := job.CreatedAt.Format("2006")
		month := job.CreatedAt.Format("01")
		baseDir = filepath.Join(activeVault.Path, "Transcripts", year, month, fmt.Sprintf("%s-%s", slug.Sanitize(title, "transcript"), shortID(job.ID)))
		job.VaultID = &activeVault.ID
	} else {
		baseDir = filepath.Join("data", "transcripts", job.ID)
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return err
	}

	jsonPath := filepath.Join(baseDir, "transcript.json")
	mdPath := filepath.Join(baseDir, "transcript.md")

	var pretty map[string]interface{}
	if err := json.Unmarshal([]byte(*job.Transcript), &pretty); err == nil {
		if payload, marshalErr := json.MarshalIndent(pretty, "", "  "); marshalErr == nil {
			if err := os.WriteFile(jsonPath, payload, 0644); err != nil {
				return err
			}
		}
	} else {
		if err := os.WriteFile(jsonPath, []byte(*job.Transcript), 0644); err != nil {
			return err
		}
	}

	md := renderTranscriptMarkdown(job, transcript)
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		return err
	}

	job.ArtifactDir = &baseDir
	job.TranscriptJSONPath = &jsonPath
	job.TranscriptMarkdownPath = &mdPath

	return database.DB.Save(job).Error
}

func getSetupRecord() (*models.AppSetup, error) {
	var setup models.AppSetup
	err := database.DB.First(&setup, 1).Error
	if err != nil {
		return nil, err
	}
	return &setup, nil
}

func (h *Handler) GetSetupState(c *gin.Context) {
	var vaults []models.Vault
	_ = database.DB.Order("created_at ASC").Find(&vaults).Error

	resp := SetupStateResponse{
		Completed: false,
		AuthMode:  "local",
		Vaults:    vaults,
	}

	setup, err := getSetupRecord()
	if err == nil {
		resp.Completed = setup.Completed
		resp.AuthMode = strings.ToLower(strings.TrimSpace(setup.AuthMode))
		if resp.AuthMode == "" {
			resp.AuthMode = "local"
		}
		resp.ObsidianVaultDir = setup.ObsidianVaultDir
		resp.OpenClawDropDir = setup.OpenClawDropDir
	}

	var activeVault models.Vault
	if err := database.DB.Where("is_active = ?", true).First(&activeVault).Error; err == nil {
		resp.ActiveVault = &activeVault
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) CompleteSetup(c *gin.Context) {
	var req CompleteSetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	trimmedVaultPath := strings.TrimSpace(req.VaultPath)
	if trimmedVaultPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Vault path is required"})
		return
	}

	vaultPath, err := filepath.Abs(trimmedVaultPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vault path"})
		return
	}

	vaultMode := normalizeVaultMode(req.VaultMode)
	if vaultMode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Vault mode is required (create or existing)"})
		return
	}
	logger.Info("Completing setup", "vault_mode", vaultMode, "vault_path", vaultPath)
	if vaultMode == "existing" {
		info, statErr := os.Stat(vaultPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Existing vault path does not exist"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to inspect existing vault path"})
			return
		}
		if !info.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Existing vault path must be a directory"})
			return
		}

		existingVault, detectErr := detectExistingVault(vaultPath)
		if detectErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to inspect existing vault structure"})
			return
		}
		if !existingVault {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Selected path does not look like an existing Quill vault. Choose 'Create new vault' for new folders.",
			})
			return
		}
	} else {
		if err := os.MkdirAll(vaultPath, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create vault path"})
			return
		}
	}

	if err := ensureVaultStructure(vaultPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to initialize vault directories"})
		return
	}

	authMode := normalizeAuthMode(req.AuthMode)
	_ = os.Setenv("AUTH_MODE", authMode)

	requestedVaultName := ""
	if req.VaultName != nil {
		requestedVaultName = strings.TrimSpace(*req.VaultName)
	}

	defaultVaultName := "Main Vault"
	if vaultMode == "existing" {
		defaultVaultName = deriveVaultNameFromPath(vaultPath)
	}

	vaultName := defaultVaultName
	if requestedVaultName != "" {
		vaultName = requestedVaultName
	}

	var vault models.Vault
	err = database.DB.Where("path = ?", vaultPath).First(&vault).Error
	if err == nil {
		if requestedVaultName != "" || vaultMode == "create" {
			vault.Name = vaultName
		} else if strings.TrimSpace(vault.Name) == "" {
			vault.Name = defaultVaultName
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load vault"})
		return
	} else {
		vault = models.Vault{Name: vaultName, Path: vaultPath, IsActive: true}
	}

	if txErr := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Vault{}).Where("1 = 1").Update("is_active", false).Error; err != nil {
			return err
		}
		vault.IsActive = true
		if vault.ID == 0 {
			if err := tx.Create(&vault).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Save(&vault).Error; err != nil {
				return err
			}
		}

		setup := models.AppSetup{
			ID:               1,
			Completed:        true,
			AuthMode:         authMode,
			ActiveVaultID:    &vault.ID,
			ObsidianVaultDir: req.ObsidianVaultDir,
			OpenClawDropDir:  req.OpenClawDropDir,
		}
		if err := tx.Save(&setup).Error; err != nil {
			return err
		}
		return nil
	}); txErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save setup"})
		return
	}

	recoveredJobs := 0
	if vaultMode == "existing" {
		count, recoverErr := recoverJobsFromVaultArtifacts(vault)
		if recoverErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to recover existing vault data"})
			return
		}
		recoveredJobs = count
	}

	h.syncContactManager(c.Request.Context(), vault.ID, vault.Path)

	resp := gin.H{
		"message":   "Setup completed",
		"auth_mode": authMode,
		"vault":     vault,
	}
	if vaultMode == "existing" {
		resp["recovered_jobs"] = recoveredJobs
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) ListVaults(c *gin.Context) {
	var vaults []models.Vault
	if err := database.DB.Order("created_at ASC").Find(&vaults).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list vaults"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"vaults": vaults})
}

func (h *Handler) CreateVault(c *gin.Context) {
	var req VaultRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	trimmedPath := strings.TrimSpace(req.Path)
	if trimmedPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Vault path is required"})
		return
	}

	vaultPath, err := filepath.Abs(trimmedPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vault path"})
		return
	}

	modeInput := req.Mode
	mode := normalizeVaultMode(&modeInput)
	if mode == "" {
		mode = "create"
	}

	if mode == "existing" {
		info, statErr := os.Stat(vaultPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Existing vault path does not exist"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to inspect existing vault path"})
			return
		}
		if !info.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Existing vault path must be a directory"})
			return
		}
		existingVault, detectErr := detectExistingVault(vaultPath)
		if detectErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to inspect existing vault structure"})
			return
		}
		if !existingVault {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Selected path does not look like an existing Quill vault. Use setup create mode for new folders.",
			})
			return
		}
	} else {
		if err := os.MkdirAll(vaultPath, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create vault path"})
			return
		}
	}

	if err := ensureVaultStructure(vaultPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to initialize vault"})
		return
	}

	vault := models.Vault{
		Name: strings.TrimSpace(req.Name),
		Path: vaultPath,
	}
	if vault.Name == "" {
		if mode == "existing" {
			vault.Name = deriveVaultNameFromPath(vaultPath)
		} else {
			vault.Name = "Vault"
		}
	}

	if err := database.DB.Create(&vault).Error; err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Vault path already exists"})
		return
	}

	if req.Activate != nil && *req.Activate {
		if txErr := database.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.Vault{}).Where("1 = 1").Update("is_active", false).Error; err != nil {
				return err
			}
			vault.IsActive = true
			if err := tx.Save(&vault).Error; err != nil {
				return err
			}

			var setup models.AppSetup
			setupErr := tx.First(&setup, 1).Error
			if setupErr != nil {
				if !errors.Is(setupErr, gorm.ErrRecordNotFound) {
					return setupErr
				}
				setup = models.AppSetup{
					ID:        1,
					Completed: true,
					AuthMode:  "local",
				}
			}
			setup.ActiveVaultID = &vault.ID
			setup.Completed = true
			if strings.TrimSpace(setup.AuthMode) == "" {
				setup.AuthMode = "local"
			}
			if err := tx.Save(&setup).Error; err != nil {
				return err
			}
			return nil
		}); txErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate vault"})
			return
		}
	}

	if mode == "existing" {
		if _, recoverErr := recoverJobsFromVaultArtifacts(vault); recoverErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to recover existing vault data"})
			return
		}
	}

	if req.Activate != nil && *req.Activate {
		h.syncContactManager(c.Request.Context(), vault.ID, vault.Path)
	}

	c.JSON(http.StatusCreated, vault)
}

func (h *Handler) UpdateVault(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vault ID"})
		return
	}

	var req VaultUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	var vault models.Vault
	if err := database.DB.First(&vault, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Vault not found"})
		return
	}

	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed != "" {
			vault.Name = trimmed
		}
	}
	if req.Path != nil {
		updatedPath, pathErr := filepath.Abs(strings.TrimSpace(*req.Path))
		if pathErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vault path"})
			return
		}
		vault.Path = updatedPath
		if err := ensureVaultStructure(updatedPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare updated vault path"})
			return
		}
	}

	if err := database.DB.Save(&vault).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update vault"})
		return
	}

	if vault.IsActive {
		h.syncContactManager(c.Request.Context(), vault.ID, vault.Path)
	}

	c.JSON(http.StatusOK, vault)
}

func (h *Handler) DeleteVault(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vault ID"})
		return
	}

	var vault models.Vault
	if err := database.DB.First(&vault, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Vault not found"})
		return
	}
	if vault.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete the active vault"})
		return
	}

	if err := database.DB.Delete(&vault).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete vault"})
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) ActivateVault(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vault ID"})
		return
	}

	var vault models.Vault
	if err := database.DB.First(&vault, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Vault not found"})
		return
	}

	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Vault{}).Where("1 = 1").Update("is_active", false).Error; err != nil {
			return err
		}
		vault.IsActive = true
		if err := tx.Save(&vault).Error; err != nil {
			return err
		}

		var setup models.AppSetup
		if err := tx.First(&setup, 1).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				return err
			}
			setup = models.AppSetup{ID: 1, Completed: true, AuthMode: "local"}
		}
		setup.ActiveVaultID = &vault.ID
		return tx.Save(&setup).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate vault"})
		return
	}

	h.syncContactManager(c.Request.Context(), vault.ID, vault.Path)

	c.JSON(http.StatusOK, vault)
}

func (h *Handler) RehydrateVault(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vault ID"})
		return
	}

	var vault models.Vault
	if err := database.DB.First(&vault, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Vault not found"})
		return
	}

	recoveredJobs, recoverErr := recoverJobsFromVaultArtifacts(vault)
	if recoverErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to recover vault data"})
		return
	}

	h.reindexContactManager(c.Request.Context(), vault.ID, vault.Path)

	c.JSON(http.StatusOK, gin.H{
		"vault_id":       vault.ID,
		"recovered_jobs": recoveredJobs,
	})
}

func (h *Handler) GetObsidianConfig(c *gin.Context) {
	setup, err := getSetupRecord()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"vault_path": ""})
		return
	}
	if setup.ObsidianVaultDir == nil {
		c.JSON(http.StatusOK, gin.H{"vault_path": ""})
		return
	}
	c.JSON(http.StatusOK, gin.H{"vault_path": *setup.ObsidianVaultDir})
}

func (h *Handler) SaveObsidianConfig(c *gin.Context) {
	var req ObsidianConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	path, err := filepath.Abs(strings.TrimSpace(req.VaultPath))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Obsidian vault path"})
		return
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare Obsidian vault path"})
		return
	}

	setup, err := getSetupRecord()
	if err != nil {
		setup = &models.AppSetup{
			ID:        1,
			Completed: true,
			AuthMode:  strings.ToLower(strings.TrimSpace(os.Getenv("AUTH_MODE"))),
		}
		if setup.AuthMode == "" {
			setup.AuthMode = "local"
		}
	}
	setup.ObsidianVaultDir = &path

	if err := database.DB.Save(setup).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save Obsidian config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"vault_path": path})
}

func (h *Handler) GetOpenClawConfig(c *gin.Context) {
	setup, err := getSetupRecord()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"drop_dir": ""})
		return
	}
	if setup.OpenClawDropDir == nil {
		c.JSON(http.StatusOK, gin.H{"drop_dir": ""})
		return
	}
	c.JSON(http.StatusOK, gin.H{"drop_dir": *setup.OpenClawDropDir})
}

func (h *Handler) SaveOpenClawConfig(c *gin.Context) {
	var req OpenClawConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	path, err := filepath.Abs(strings.TrimSpace(req.DropDir))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid OpenClaw drop path"})
		return
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare OpenClaw drop path"})
		return
	}

	setup, err := getSetupRecord()
	if err != nil {
		setup = &models.AppSetup{
			ID:        1,
			Completed: true,
			AuthMode:  strings.ToLower(strings.TrimSpace(os.Getenv("AUTH_MODE"))),
		}
		if setup.AuthMode == "" {
			setup.AuthMode = "local"
		}
	}
	setup.OpenClawDropDir = &path

	if err := database.DB.Save(setup).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save OpenClaw config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"drop_dir": path})
}

func (h *Handler) SyncTranscriptToObsidian(c *gin.Context) {
	jobID := c.Param("id")
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	setup, err := getSetupRecord()
	if err != nil || setup.ObsidianVaultDir == nil || strings.TrimSpace(*setup.ObsidianVaultDir) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Obsidian vault path is not configured"})
		return
	}

	var markdown string
	if job.TranscriptMarkdownPath != nil && strings.TrimSpace(*job.TranscriptMarkdownPath) != "" {
		content, readErr := os.ReadFile(*job.TranscriptMarkdownPath)
		if readErr == nil {
			markdown = string(content)
		}
	}

	if markdown == "" {
		if job.Transcript == nil || strings.TrimSpace(*job.Transcript) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Transcript is not available"})
			return
		}
		payload, parseErr := parseTranscriptJSON(*job.Transcript)
		if parseErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse transcript JSON"})
			return
		}
		markdown = renderTranscriptMarkdown(job, payload)
	}

	title := "transcript"
	if job.Title != nil && strings.TrimSpace(*job.Title) != "" {
		title = strings.TrimSpace(*job.Title)
	}
	filename := fmt.Sprintf("%s-%s.md", slug.Sanitize(title, "transcript"), job.ID)
	targetDir := filepath.Join(*setup.ObsidianVaultDir, "Quill")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create Obsidian target directory"})
		return
	}
	targetPath := filepath.Join(targetDir, filename)
	if err := os.WriteFile(targetPath, []byte(markdown), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write Obsidian markdown file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"synced": true, "path": targetPath})
}

func (h *Handler) OpenClawIngest(c *gin.Context) {
	header, err := c.FormFile("audio")
	if err != nil {
		header, err = c.FormFile("recording")
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Audio file is required (field: audio or recording)"})
		return
	}

	ingestDir := h.config.UploadDir
	var vaultID *uint
	if activeVault, pathErr := getActiveVault(); pathErr == nil && strings.TrimSpace(activeVault.Path) != "" {
		ingestDir = filepath.Join(activeVault.Path, "Inbox", "OpenClaw")
		vaultID = &activeVault.ID
	}

	filePath, err := h.fileService.SaveUpload(header, ingestDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	jobID := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	title := strings.TrimSpace(c.PostForm("title"))
	if title == "" {
		title = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}

	job := models.TranscriptionJob{
		ID:        jobID,
		AudioPath: filePath,
		VaultID:   vaultID,
		Status:    models.StatusUploaded,
		Title:     &title,
	}
	if err := h.jobRepo.Create(c.Request.Context(), &job); err != nil {
		_ = h.fileService.RemoveFile(filePath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create job"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"job_id": job.ID, "status": job.Status, "title": job.Title})
}

func (h *Handler) IngestOpenClawDropFolder(c *gin.Context) {
	var req OpenClawIngestDropRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
			return
		}
	}

	setup, err := getSetupRecord()
	if err != nil || setup.OpenClawDropDir == nil || strings.TrimSpace(*setup.OpenClawDropDir) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "OpenClaw drop directory is not configured"})
		return
	}

	dropDir := strings.TrimSpace(*setup.OpenClawDropDir)
	entries, err := os.ReadDir(dropDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read OpenClaw drop directory"})
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	consume := true
	if req.Consume != nil {
		consume = *req.Consume
	}

	ingestDir := h.config.UploadDir
	var vaultID *uint
	if activeVault, pathErr := getActiveVault(); pathErr == nil && strings.TrimSpace(activeVault.Path) != "" {
		ingestDir = filepath.Join(activeVault.Path, "Inbox", "OpenClaw")
		vaultID = &activeVault.ID
	}
	if err := os.MkdirAll(ingestDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare ingest directory"})
		return
	}

	type ingestResult struct {
		JobID    string `json:"job_id"`
		Source   string `json:"source"`
		StoredAs string `json:"stored_as"`
		Title    string `json:"title"`
	}
	type skipResult struct {
		Source string `json:"source"`
		Reason string `json:"reason"`
	}

	ingested := make([]ingestResult, 0)
	skipped := make([]skipResult, 0)

	for _, entry := range entries {
		if len(ingested) >= limit {
			break
		}
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		if strings.HasPrefix(filename, ".") {
			continue
		}

		ext := strings.ToLower(filepath.Ext(filename))
		if !isSupportedIngestExtension(ext) {
			continue
		}

		sourcePath := filepath.Join(dropDir, filename)
		jobID := uuid.NewString()
		targetPath := filepath.Join(ingestDir, fmt.Sprintf("%s%s", jobID, ext))

		var moveErr error
		if consume {
			moveErr = os.Rename(sourcePath, targetPath)
			if moveErr != nil {
				moveErr = copyFile(sourcePath, targetPath)
				if moveErr == nil {
					_ = os.Remove(sourcePath)
				}
			}
		} else {
			moveErr = copyFile(sourcePath, targetPath)
		}
		if moveErr != nil {
			skipped = append(skipped, skipResult{Source: sourcePath, Reason: moveErr.Error()})
			continue
		}

		title := strings.TrimSpace(strings.TrimSuffix(filename, ext))
		if title == "" {
			title = "OpenClaw Recording"
		}
		job := models.TranscriptionJob{
			ID:        jobID,
			AudioPath: targetPath,
			VaultID:   vaultID,
			Status:    models.StatusUploaded,
			Title:     &title,
		}

		if createErr := h.jobRepo.Create(c.Request.Context(), &job); createErr != nil {
			_ = os.Remove(targetPath)
			skipped = append(skipped, skipResult{Source: sourcePath, Reason: createErr.Error()})
			continue
		}

		ingested = append(ingested, ingestResult{
			JobID:    jobID,
			Source:   sourcePath,
			StoredAs: targetPath,
			Title:    title,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"drop_dir": dropDir,
		"consume":  consume,
		"ingested": ingested,
		"skipped":  skipped,
	})
}

func (h *Handler) GetOpenClawJob(c *gin.Context) {
	jobID := c.Param("id")
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if job.Status == models.StatusCompleted {
		if writeErr := writeTranscriptArtifactsForJob(job); writeErr != nil {
			logger.Warn("openclaw artifact materialization failed", "job_id", jobID, "error", writeErr)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":                       job.ID,
		"title":                    job.Title,
		"status":                   job.Status,
		"audio_path":               job.AudioPath,
		"artifact_dir":             job.ArtifactDir,
		"transcript_json_path":     job.TranscriptJSONPath,
		"transcript_markdown_path": job.TranscriptMarkdownPath,
	})
}

func (h *Handler) GetOpenClawJobTranscriptJSON(c *gin.Context) {
	jobID := c.Param("id")
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if job.Status == models.StatusCompleted {
		if writeErr := writeTranscriptArtifactsForJob(job); writeErr != nil {
			logger.Warn("openclaw json artifact materialization failed", "job_id", jobID, "error", writeErr)
		}
	}

	c.Header("Content-Type", "application/json")
	if job.TranscriptJSONPath != nil && strings.TrimSpace(*job.TranscriptJSONPath) != "" {
		content, readErr := os.ReadFile(*job.TranscriptJSONPath)
		if readErr == nil {
			c.String(http.StatusOK, string(content))
			return
		}
	}
	if job.Transcript != nil && strings.TrimSpace(*job.Transcript) != "" {
		c.String(http.StatusOK, strings.TrimSpace(*job.Transcript))
		return
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "Transcript JSON is not available"})
}

func (h *Handler) GetOpenClawJobTranscriptMarkdown(c *gin.Context) {
	jobID := c.Param("id")
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if job.Status == models.StatusCompleted {
		if writeErr := writeTranscriptArtifactsForJob(job); writeErr != nil {
			logger.Warn("openclaw markdown artifact materialization failed", "job_id", jobID, "error", writeErr)
		}
	}

	c.Header("Content-Type", "text/markdown; charset=utf-8")
	if job.TranscriptMarkdownPath != nil && strings.TrimSpace(*job.TranscriptMarkdownPath) != "" {
		content, readErr := os.ReadFile(*job.TranscriptMarkdownPath)
		if readErr == nil {
			c.String(http.StatusOK, string(content))
			return
		}
	}

	if job.Transcript != nil && strings.TrimSpace(*job.Transcript) != "" {
		payload, parseErr := parseTranscriptJSON(*job.Transcript)
		if parseErr == nil {
			c.String(http.StatusOK, renderTranscriptMarkdown(job, payload))
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "Transcript markdown is not available"})
}

func parseSignatureSource(signatureData *string) string {
	if signatureData == nil || strings.TrimSpace(*signatureData) == "" {
		return ""
	}
	var metadata contactSignatureMetadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(*signatureData)), &metadata); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(metadata.Source))
}

func setSignatureMetadata(contact *models.Contact, source string, model string) {
	trimmedSource := strings.TrimSpace(source)
	if trimmedSource == "" {
		contact.SignatureData = nil
		return
	}
	metadata := contactSignatureMetadata{
		Source:    trimmedSource,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if strings.TrimSpace(model) != "" {
		metadata.Model = strings.TrimSpace(model)
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		contact.SignatureData = nil
		return
	}
	value := string(payload)
	contact.SignatureData = &value
}

func hasManualSignature(contact *models.Contact) bool {
	if contact == nil {
		return false
	}
	if contact.SignatureEmbeddingPath == nil || strings.TrimSpace(*contact.SignatureEmbeddingPath) == "" {
		return false
	}
	return parseSignatureSource(contact.SignatureData) == signatureSourceManual
}

func validateSignaturePayload(raw []byte) (*voiceSignaturePayload, []byte, error) {
	var payload voiceSignaturePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, nil, fmt.Errorf("signature must be valid JSON")
	}

	if payload.Version <= 0 {
		return nil, nil, fmt.Errorf("signature.version is required and must be > 0")
	}
	if strings.TrimSpace(payload.Model) == "" {
		return nil, nil, fmt.Errorf("signature.model is required")
	}
	if payload.Dimension <= 0 {
		return nil, nil, fmt.Errorf("signature.dimension is required and must be > 0")
	}
	if len(payload.Vector) == 0 {
		return nil, nil, fmt.Errorf("signature.vector is required and must be a non-empty array")
	}
	if payload.Dimension != len(payload.Vector) {
		return nil, nil, fmt.Errorf("signature.dimension must match len(signature.vector)")
	}
	for _, value := range payload.Vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, nil, fmt.Errorf("signature.vector must contain only finite numbers")
		}
	}

	var payloadAny map[string]any
	if err := json.Unmarshal(raw, &payloadAny); err == nil {
		normalized, marshalErr := json.MarshalIndent(payloadAny, "", "  ")
		if marshalErr == nil {
			return &payload, normalized, nil
		}
	}

	normalized, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to normalize signature payload")
	}
	return &payload, normalized, nil
}

func (h *Handler) ListContacts(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	query := strings.TrimSpace(c.Query("q"))
	contacts, err := h.contactRepo.Search(c.Request.Context(), vault.ID, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list contacts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"contacts": contacts, "vault_id": vault.ID})
}

func (h *Handler) CreateContact(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	var req ContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	contact := models.Contact{
		VaultID:         vault.ID,
		ContactUID:      uuid.NewString(),
		Slug:            slug.Sanitize(strings.TrimSpace(req.Name), "contact"),
		Name:            strings.TrimSpace(req.Name),
		Phone:           req.Phone,
		Email:           req.Email,
		Notes:           req.Notes,
		SignatureStatus: "none",
	}
	if contact.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is required"})
		return
	}

	if err := h.contactRepo.Create(c.Request.Context(), &contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create contact"})
		return
	}

	if err := h.persistContactFile(c.Request.Context(), &contact); err != nil {
		_ = h.contactRepo.Delete(c.Request.Context(), contact.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create contact file artifacts"})
		return
	}

	c.JSON(http.StatusCreated, contact)
}

func (h *Handler) GetContact(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	c.JSON(http.StatusOK, contact)
}

func (h *Handler) UpdateContact(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	var req ContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	trimmed := strings.TrimSpace(req.Name)
	if trimmed == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is required"})
		return
	}

	contact.Name = trimmed
	contact.Slug = slug.Sanitize(trimmed, "contact")
	contact.Phone = req.Phone
	contact.Email = req.Email
	contact.Notes = req.Notes

	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}
	c.JSON(http.StatusOK, contact)
}

func (h *Handler) DeleteContact(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	if err := h.deleteContactFiles(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete contact artifacts"})
		return
	}
	if err := h.contactRepo.Delete(c.Request.Context(), uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete contact"})
		return
	}
	c.Status(http.StatusNoContent)
}

func getActiveVault() (*models.Vault, error) {
	var vault models.Vault
	if err := database.DB.Where("is_active = ?", true).First(&vault).Error; err != nil {
		return nil, err
	}
	return &vault, nil
}

func getActiveVaultPath() (string, error) {
	vault, err := getActiveVault()
	if err != nil {
		return "", err
	}
	return vault.Path, nil
}

func (h *Handler) UploadContactSnippet(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	header, err := c.FormFile("snippet")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Snippet file is required (field: snippet)"})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare contact folder"})
		return
	}

	src, err := header.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open snippet file"})
		return
	}
	defer src.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".wav"
	}

	folderRel := filepath.ToSlash(filepath.Dir(contact.NotePath))
	targetRel := filepath.ToSlash(filepath.Join(folderRel, "voice-snippet"+ext))
	targetPath := fileService.ResolveAbsPath(targetRel)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare contact snippet path"})
		return
	}

	dst, err := os.Create(targetPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store snippet file"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write snippet file"})
		return
	}

	contact.VoiceSnippetPath = &targetRel
	contact.SyncError = nil

	manualLocked := hasManualSignature(contact)
	if manualLocked {
		contact.SignatureStatus = "ready"
		if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"contact_id":               contact.ID,
			"snippet_path":             contact.VoiceSnippetPath,
			"signature_status":         contact.SignatureStatus,
			"signature_embedding_path": contact.SignatureEmbeddingPath,
			"manual_signature_locked":  true,
		})
		return
	}

	contact.SignatureStatus = "processing"
	contact.SignatureEmbeddingPath = nil
	setSignatureMetadata(contact, signatureSourceExtracted, "")

	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}

	if h.contactManager != nil {
		h.contactManager.EnqueueEmbedding(contact.ID)
	} else {
		errMsg := "contact embedding worker is unavailable"
		contact.SignatureStatus = "failed"
		contact.SyncError = &errMsg
		_ = h.persistContactFile(c.Request.Context(), contact)
	}

	c.JSON(http.StatusOK, gin.H{
		"contact_id":               contact.ID,
		"snippet_path":             contact.VoiceSnippetPath,
		"signature_status":         contact.SignatureStatus,
		"signature_embedding_path": contact.SignatureEmbeddingPath,
	})
}

func (h *Handler) GetContactSnippet(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	if contact.VoiceSnippetPath == nil || strings.TrimSpace(*contact.VoiceSnippetPath) == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Voice snippet not found"})
		return
	}
	fileService := contacts.NewFileService(vault.Path)
	snippetAbs, ok := fileService.ResolveAndValidate(*contact.VoiceSnippetPath)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid snippet path"})
		return
	}
	if _, statErr := os.Stat(snippetAbs); statErr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Voice snippet not found"})
		return
	}
	c.File(snippetAbs)
}

func (h *Handler) DeleteContactSnippet(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	if contact.VoiceSnippetPath != nil {
		if abs, ok := fileService.ResolveAndValidate(*contact.VoiceSnippetPath); ok {
			_ = os.Remove(abs)
		}
	}

	contact.VoiceSnippetPath = nil
	contact.SyncError = nil
	if contact.SignatureEmbeddingPath == nil || strings.TrimSpace(*contact.SignatureEmbeddingPath) == "" {
		contact.SignatureStatus = "none"
		setSignatureMetadata(contact, "", "")
	} else if strings.TrimSpace(contact.SignatureStatus) == "" || strings.EqualFold(contact.SignatureStatus, "none") {
		contact.SignatureStatus = "ready"
	}
	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) UploadContactSignature(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	header, err := c.FormFile("signature")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Signature file is required (field: signature)"})
		return
	}

	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare contact folder"})
		return
	}

	src, err := header.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open signature file"})
		return
	}
	defer src.Close()

	raw, err := io.ReadAll(io.LimitReader(src, maxSignatureUploadBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read signature file"})
		return
	}
	if int64(len(raw)) > maxSignatureUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Signature file exceeds maximum size"})
		return
	}

	payload, normalized, err := validateSignaturePayload(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	folderRel := filepath.ToSlash(filepath.Dir(contact.NotePath))
	if folderRel == "" || folderRel == "." {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve contact folder"})
		return
	}
	targetRel := filepath.ToSlash(filepath.Join(folderRel, signatureArtifactFileName))
	targetAbs, ok := fileService.ResolveAndValidate(targetRel)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid signature target path"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare signature path"})
		return
	}
	if err := os.WriteFile(targetAbs, normalized, 0o644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store signature file"})
		return
	}

	contact.SignatureEmbeddingPath = &targetRel
	contact.SignatureStatus = "ready"
	contact.SyncError = nil
	setSignatureMetadata(contact, signatureSourceManual, payload.Model)
	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"contact_id":               contact.ID,
		"signature_status":         contact.SignatureStatus,
		"signature_embedding_path": contact.SignatureEmbeddingPath,
		"signature_source":         signatureSourceManual,
	})
}

func (h *Handler) DeleteContactSignature(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	if contact.SignatureEmbeddingPath != nil {
		if abs, ok := fileService.ResolveAndValidate(*contact.SignatureEmbeddingPath); ok {
			_ = os.Remove(abs)
		}
	}

	contact.SignatureEmbeddingPath = nil
	contact.SignatureStatus = "none"
	contact.SyncError = nil
	setSignatureMetadata(contact, "", "")

	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ExtractContactSignature(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	if contact.VoiceSnippetPath == nil || strings.TrimSpace(*contact.VoiceSnippetPath) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Voice snippet is required before extraction"})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	snippetAbs, ok := fileService.ResolveAndValidate(*contact.VoiceSnippetPath)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid snippet path"})
		return
	}
	if _, statErr := os.Stat(snippetAbs); statErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Voice snippet is required before extraction"})
		return
	}

	contact.SignatureStatus = "processing"
	contact.SyncError = nil
	setSignatureMetadata(contact, signatureSourceExtracted, "")
	if err := h.persistContactFile(c.Request.Context(), contact); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}

	if h.contactManager != nil {
		h.contactManager.EnqueueEmbedding(contact.ID)
	} else {
		errMsg := "contact embedding worker is unavailable"
		contact.SignatureStatus = "failed"
		contact.SyncError = &errMsg
		_ = h.persistContactFile(c.Request.Context(), contact)
	}

	c.JSON(http.StatusOK, gin.H{
		"contact_id":               contact.ID,
		"signature_status":         contact.SignatureStatus,
		"signature_embedding_path": contact.SignatureEmbeddingPath,
	})
}

func (h *Handler) GetContactFiles(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}
	contact, err := h.contactRepo.GetByIDInVault(c.Request.Context(), vault.ID, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	fileService := contacts.NewFileService(vault.Path)
	noteAbs := ""
	if abs, ok := fileService.ResolveAndValidate(contact.NotePath); ok {
		noteAbs = abs
	}
	c.JSON(http.StatusOK, gin.H{
		"contact_id":                   contact.ID,
		"vault_id":                     contact.VaultID,
		"note_path":                    contact.NotePath,
		"note_abs_path":                noteAbs,
		"voice_snippet_path":           contact.VoiceSnippetPath,
		"signature_embedding_path":     contact.SignatureEmbeddingPath,
		"signature_status":             contact.SignatureStatus,
		"sync_error":                   contact.SyncError,
		"file_mtime_ns":                contact.FileMtimeNS,
		"voice_snippet_abs_path":       absFromOptional(fileService, contact.VoiceSnippetPath),
		"signature_embedding_abs_path": absFromOptional(fileService, contact.SignatureEmbeddingPath),
	})
}

func (h *Handler) ReindexContacts(c *gin.Context) {
	vault, err := getActiveVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	if h.contactManager != nil {
		result, reindexErr := h.contactManager.ReindexActiveVault(c.Request.Context())
		if reindexErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reindex contacts"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"vault_id": vault.ID, "result": result})
		return
	}

	fileService := contacts.NewFileService(vault.Path)
	syncService := contacts.NewSyncService(fileService, h.contactRepo, vault.ID)
	result, reindexErr := syncService.SyncFromFilesystem(c.Request.Context())
	if reindexErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reindex contacts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"vault_id": vault.ID, "result": result})
}

func (h *Handler) persistContactFile(ctx context.Context, contact *models.Contact) error {
	if h.contactManager != nil {
		return h.contactManager.WriteContact(ctx, contact)
	}
	var vault models.Vault
	if err := database.DB.WithContext(ctx).First(&vault, contact.VaultID).Error; err != nil {
		return err
	}
	fileService := contacts.NewFileService(vault.Path)
	if err := fileService.WriteContact(contact); err != nil {
		return err
	}
	return h.contactRepo.Update(ctx, contact)
}

func (h *Handler) deleteContactFiles(ctx context.Context, contact *models.Contact) error {
	if h.contactManager != nil {
		return h.contactManager.DeleteContactFiles(ctx, contact)
	}
	var vault models.Vault
	if err := database.DB.WithContext(ctx).First(&vault, contact.VaultID).Error; err != nil {
		return err
	}
	fileService := contacts.NewFileService(vault.Path)
	return fileService.DeleteContactFolder(contact)
}

func absFromOptional(fileService *contacts.FileService, value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return ""
	}
	abs, ok := fileService.ResolveAndValidate(*value)
	if !ok {
		return ""
	}
	return abs
}

func (h *Handler) syncContactManager(ctx context.Context, vaultID uint, vaultPath string) {
	if h.contactManager == nil {
		return
	}
	if activeID, activePath, ok := h.contactManager.ActiveVault(); ok &&
		activeID == vaultID &&
		filepath.Clean(activePath) == filepath.Clean(vaultPath) {
		return
	}
	if err := h.contactManager.SwitchVault(ctx, vaultID, vaultPath); err != nil {
		logger.Warn("failed to switch contact manager vault", "vault_id", vaultID, "error", err)
	}
}

func (h *Handler) reindexContactManager(ctx context.Context, vaultID uint, vaultPath string) {
	if h.contactManager == nil {
		return
	}
	if _, err := h.contactManager.ReindexVault(ctx, vaultID, vaultPath); err != nil {
		logger.Warn("failed to reindex contacts for vault", "vault_id", vaultID, "error", err)
	}
}

func (h *Handler) MaterializeTranscriptArtifacts(c *gin.Context) {
	jobID := c.Param("id")
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if job.Transcript == nil || strings.TrimSpace(*job.Transcript) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Transcript not available"})
		return
	}

	if err := writeTranscriptArtifactsForJob(job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to materialize transcript artifacts: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"job_id":                   job.ID,
		"artifact_dir":             job.ArtifactDir,
		"transcript_json_path":     job.TranscriptJSONPath,
		"transcript_markdown_path": job.TranscriptMarkdownPath,
	})
}

func (h *Handler) ListOpenClawReadyJobs(c *gin.Context) {
	var activeVaultID *uint
	if activeVault, vaultErr := getActiveVault(); vaultErr == nil {
		activeVaultID = &activeVault.ID
	}

	jobs, _, err := h.jobRepo.ListWithParams(c.Request.Context(), 0, 200, "updated_at", "desc", "", nil, activeVaultID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list jobs"})
		return
	}

	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].UpdatedAt.After(jobs[j].UpdatedAt)
	})

	rows := make([]gin.H, 0, len(jobs))
	for _, job := range jobs {
		rows = append(rows, gin.H{
			"id":                       job.ID,
			"title":                    job.Title,
			"status":                   job.Status,
			"updated_at":               job.UpdatedAt,
			"transcript_json_path":     job.TranscriptJSONPath,
			"transcript_markdown_path": job.TranscriptMarkdownPath,
		})
	}

	c.JSON(http.StatusOK, gin.H{"jobs": rows})
}
