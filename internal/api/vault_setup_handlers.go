package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"scriberr/internal/database"
	"scriberr/internal/models"
	"scriberr/pkg/logger"

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
	AuthMode         *string `json:"auth_mode,omitempty"`
	ObsidianVaultDir *string `json:"obsidian_vault_dir,omitempty"`
	OpenClawDropDir  *string `json:"openclaw_drop_dir,omitempty"`
}

type VaultRequest struct {
	Name string `json:"name" binding:"required"`
	Path string `json:"path" binding:"required"`
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

func ensureVaultStructure(vaultPath string) error {
	dirs := []string{
		filepath.Join(vaultPath, "Inbox"),
		filepath.Join(vaultPath, "Media"),
		filepath.Join(vaultPath, "Transcripts"),
		filepath.Join(vaultPath, "Contacts", "Snippets"),
		filepath.Join(vaultPath, ".scriber"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeSlug(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "transcript"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(trimmed) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '.':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "transcript"
	}
	return result
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
		baseDir = filepath.Join(activeVault.Path, "Transcripts", year, month, fmt.Sprintf("%s-%s", sanitizeSlug(title), shortID(job.ID)))
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

	vaultPath, err := filepath.Abs(strings.TrimSpace(req.VaultPath))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vault path"})
		return
	}

	if err := os.MkdirAll(vaultPath, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create vault path"})
		return
	}
	if err := ensureVaultStructure(vaultPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to initialize vault directories"})
		return
	}

	authMode := normalizeAuthMode(req.AuthMode)
	_ = os.Setenv("AUTH_MODE", authMode)

	vaultName := "Main Vault"
	if req.VaultName != nil && strings.TrimSpace(*req.VaultName) != "" {
		vaultName = strings.TrimSpace(*req.VaultName)
	}

	var vault models.Vault
	err = database.DB.Where("path = ?", vaultPath).First(&vault).Error
	if err == nil {
		vault.Name = vaultName
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

	c.JSON(http.StatusOK, gin.H{"message": "Setup completed", "auth_mode": authMode, "vault": vault})
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

	vaultPath, err := filepath.Abs(strings.TrimSpace(req.Path))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid vault path"})
		return
	}

	if err := os.MkdirAll(vaultPath, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create vault path"})
		return
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
		vault.Name = "Vault"
	}

	if err := database.DB.Create(&vault).Error; err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Vault path already exists"})
		return
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

	c.JSON(http.StatusOK, vault)
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
	filename := fmt.Sprintf("%s-%s.md", sanitizeSlug(title), job.ID)
	targetDir := filepath.Join(*setup.ObsidianVaultDir, "Scriber")
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

func (h *Handler) ListContacts(c *gin.Context) {
	var contacts []models.Contact
	if err := database.DB.Order("name ASC").Find(&contacts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list contacts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"contacts": contacts})
}

func (h *Handler) CreateContact(c *gin.Context) {
	var req ContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	contact := models.Contact{
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

	if err := database.DB.Create(&contact).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create contact"})
		return
	}
	c.JSON(http.StatusCreated, contact)
}

func (h *Handler) GetContact(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	var contact models.Contact
	if err := database.DB.First(&contact, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}
	c.JSON(http.StatusOK, contact)
}

func (h *Handler) UpdateContact(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	var contact models.Contact
	if err := database.DB.First(&contact, uint(id)).Error; err != nil {
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
	contact.Phone = req.Phone
	contact.Email = req.Email
	contact.Notes = req.Notes

	if err := database.DB.Save(&contact).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}
	c.JSON(http.StatusOK, contact)
}

func (h *Handler) DeleteContact(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	if err := database.DB.Delete(&models.Contact{}, uint(id)).Error; err != nil {
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
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact ID"})
		return
	}

	var contact models.Contact
	if err := database.DB.First(&contact, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	header, err := c.FormFile("snippet")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Snippet file is required (field: snippet)"})
		return
	}

	vaultPath, vaultErr := getActiveVaultPath()
	if vaultErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active vault configured"})
		return
	}

	snippetDir := filepath.Join(vaultPath, "Contacts", "Snippets")
	if err := os.MkdirAll(snippetDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare snippet directory"})
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
	targetPath := filepath.Join(snippetDir, fmt.Sprintf("contact-%d-%s%s", contact.ID, uuid.NewString(), ext))
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

	contact.VoiceSnippetPath = &targetPath
	contact.SignatureStatus = "processing"
	now := time.Now().Format(time.RFC3339)
	signatureMeta := map[string]string{
		"last_updated": now,
		"status":       "processing",
	}
	payload, _ := json.Marshal(signatureMeta)
	payloadStr := string(payload)
	contact.SignatureData = &payloadStr

	if err := database.DB.Save(&contact).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"contact_id": contact.ID, "snippet_path": targetPath, "signature_status": contact.SignatureStatus})
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
	jobs, _, err := h.jobRepo.ListWithParams(c.Request.Context(), 0, 200, "updated_at", "desc", "", nil)
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
