package api

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"quill/internal/repository"
	"quill/internal/transcription"
	"quill/pkg/logger"

	"github.com/gin-gonic/gin"
)

// transcriptsDirForVault returns the Transcripts directory path for the active vault.
func transcriptsDirForVault() (string, error) {
	vault, err := getActiveVault()
	if err != nil {
		return "", err
	}
	return filepath.Join(vault.Path, "Transcripts"), nil
}

// ListFolders returns all folders for the active vault.
// Merges folders from the database with folders on disk.
func (h *Handler) ListFolders(c *gin.Context) {
	var activeVaultID *uint
	if vault, err := getActiveVault(); err == nil {
		activeVaultID = &vault.ID
	}

	// Get folders from DB
	dbFolders, err := h.jobRepo.ListFolders(c.Request.Context(), activeVaultID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list folders"})
		return
	}

	// Get folders from disk
	transcriptsDir, dirErr := transcriptsDirForVault()
	var diskFolders []string
	if dirErr == nil {
		var diskErr error
		diskFolders, diskErr = transcription.ListFoldersOnDisk(transcriptsDir)
		if diskErr != nil {
			logger.Warn("Failed to list folders on disk: %v", diskErr)
		}
	}

	// Merge and deduplicate
	folderSet := make(map[string]bool)
	for _, f := range dbFolders {
		folderSet[f] = true
	}
	for _, f := range diskFolders {
		folderSet[f] = true
	}

	folders := make([]string, 0, len(folderSet))
	for f := range folderSet {
		folders = append(folders, f)
	}

	// Sort alphabetically
	sort.Strings(folders)

	c.JSON(http.StatusOK, gin.H{"folders": folders})
}

// CreateFolder creates a new empty folder on disk.
func (h *Handler) CreateFolder(c *gin.Context) {
	var body struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "folder name cannot be empty"})
		return
	}

	transcriptsDir, err := transcriptsDirForVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no active vault"})
		return
	}

	if err := transcription.CreateFolderOnDisk(transcriptsDir, name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create folder"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"folder": name})
}

// RenameFolder renames a folder on disk and updates all transcripts in that folder.
func (h *Handler) RenameFolder(c *gin.Context) {
	var body struct {
		OldName string `json:"old_name" binding:"required"`
		NewName string `json:"new_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "old_name and new_name are required"})
		return
	}

	oldName := strings.TrimSpace(body.OldName)
	newName := strings.TrimSpace(body.NewName)
	if oldName == "" || newName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "folder names cannot be empty"})
		return
	}

	transcriptsDir, err := transcriptsDirForVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no active vault"})
		return
	}

	// Rename on disk
	if err := transcription.RenameFolderOnDisk(transcriptsDir, oldName, newName); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	// Update DB records
	var activeVaultID *uint
	if vault, vaultErr := getActiveVault(); vaultErr == nil {
		activeVaultID = &vault.ID
	}

	newFolder := newName
	affected, dbErr := h.jobRepo.BulkUpdateFolder(c.Request.Context(), oldName, &newFolder, activeVaultID)
	if dbErr != nil {
		// Rollback disk rename
		_ = transcription.RenameFolderOnDisk(transcriptsDir, newName, oldName)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update database"})
		return
	}

	// Also update ArtifactDir paths for affected jobs
	h.updateArtifactDirsForFolderRename(c, oldName, newName, transcriptsDir)

	logger.Info("Renamed folder %q -> %q, affected %d transcripts", oldName, newName, affected)
	c.JSON(http.StatusOK, gin.H{"folder": newName, "affected": affected})
}

// DeleteFolder deletes an empty folder from disk.
// All transcripts must be moved out first.
func (h *Handler) DeleteFolder(c *gin.Context) {
	folder := c.Query("name")
	if folder == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name query parameter is required"})
		return
	}

	transcriptsDir, err := transcriptsDirForVault()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no active vault"})
		return
	}

	if err := transcription.DeleteFolderOnDisk(transcriptsDir, folder); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": folder})
}

// MoveTranscriptToFolder moves a transcript to a folder.
func (h *Handler) MoveTranscriptToFolder(c *gin.Context) {
	jobID := c.Param("id")
	var body struct {
		Folder string `json:"folder"` // empty string = root
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	folder := strings.TrimSpace(body.Folder)

	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Transcript not found"})
		return
	}

	// Move bundle on disk if we have an artifact directory
	if job.ArtifactDir != nil && *job.ArtifactDir != "" {
		newDir, moveErr := transcription.MoveBundleToFolder(*job.ArtifactDir, folder)
		if moveErr != nil {
			c.JSON(http.StatusConflict, gin.H{"error": moveErr.Error()})
			return
		}

		// Update paths
		if newDir != *job.ArtifactDir {
			oldDir := *job.ArtifactDir
			job.ArtifactDir = &newDir
			// Update file paths relative to new directory
			job.AudioPath = updatePathForNewDir(job.AudioPath, oldDir, newDir)
			if job.TranscriptJSONPath != nil {
				newJSON := updatePathForNewDir(*job.TranscriptJSONPath, oldDir, newDir)
				job.TranscriptJSONPath = &newJSON
			}
			if job.TranscriptMarkdownPath != nil {
				newMD := updatePathForNewDir(*job.TranscriptMarkdownPath, oldDir, newDir)
				job.TranscriptMarkdownPath = &newMD
			}
		}
	}

	// Update folder in DB
	var folderPtr *string
	if folder != "" {
		folderPtr = &folder
	}
	job.Folder = folderPtr

	if err := h.jobRepo.Update(c.Request.Context(), job); err != nil {
		// Rollback disk move if DB update fails
		if job.ArtifactDir != nil {
			// Best-effort rollback
			logger.Warn("DB update failed after moving bundle, manual recovery may be needed: %v", err)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update transcript"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":     jobID,
		"folder": folder,
	})
}

// updatePathForNewDir replaces the old directory prefix with the new one.
func updatePathForNewDir(path, oldDir, newDir string) string {
	cleanPath := filepath.Clean(path)
	cleanOld := filepath.Clean(oldDir)
	if strings.HasPrefix(cleanPath, cleanOld+string(filepath.Separator)) {
		rel := strings.TrimPrefix(cleanPath, cleanOld+string(filepath.Separator))
		return filepath.Join(newDir, rel)
	}
	if cleanPath == cleanOld {
		return newDir
	}
	return path
}

// updateArtifactDirsForFolderRename updates ArtifactDir and file paths
// for all transcripts whose folder was renamed.
func (h *Handler) updateArtifactDirsForFolderRename(c *gin.Context, oldFolder, newFolder, transcriptsDir string) {
	// List all jobs that had the old folder
	var activeVaultID *uint
	if vault, err := getActiveVault(); err == nil {
		activeVaultID = &vault.ID
	}

	jobs, _, err := h.jobRepo.ListWithParams(c.Request.Context(), repository.ListParams{
		Offset:  0,
		Limit:   1000,
		VaultID: activeVaultID,
		Folder:  &newFolder,
	})
	if err != nil {
		logger.Warn("Failed to list jobs for artifact dir update: %v", err)
		return
	}

	oldFolderPath := filepath.Join(transcriptsDir, oldFolder)
	newFolderPath := filepath.Join(transcriptsDir, newFolder)

	for _, job := range jobs {
		if job.ArtifactDir == nil || !strings.HasPrefix(*job.ArtifactDir, oldFolderPath) {
			continue
		}

		newArtifactDir := strings.Replace(*job.ArtifactDir, oldFolderPath, newFolderPath, 1)
		job.ArtifactDir = &newArtifactDir
		job.AudioPath = strings.Replace(job.AudioPath, oldFolderPath, newFolderPath, 1)
		if job.TranscriptJSONPath != nil {
			newJSON := strings.Replace(*job.TranscriptJSONPath, oldFolderPath, newFolderPath, 1)
			job.TranscriptJSONPath = &newJSON
		}
		if job.TranscriptMarkdownPath != nil {
			newMD := strings.Replace(*job.TranscriptMarkdownPath, oldFolderPath, newFolderPath, 1)
			job.TranscriptMarkdownPath = &newMD
		}

		if err := h.jobRepo.Update(c.Request.Context(), &job); err != nil {
			logger.Warn("Failed to update artifact dir for job %s: %v", job.ID, err)
		}
	}
}

