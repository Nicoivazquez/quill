package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"quill/internal/models"
	"quill/internal/transcription"
	"quill/pkg/logger"

	"github.com/gin-gonic/gin"
)

const maxBatchSize = 100

// batchResult is the per-item result returned by batch endpoints.
type batchResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ---------- Batch Delete ----------

func (h *Handler) BatchDeleteTranscriptionJobs(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBatchSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("ids must contain 1-%d items", maxBatchSize)})
		return
	}

	ctx := c.Request.Context()
	results := make([]batchResult, 0, len(req.IDs))

	for _, id := range req.IDs {
		if err := h.deleteJob(ctx, id); err != nil {
			results = append(results, batchResult{ID: id, Success: false, Error: err.Error()})
		} else {
			results = append(results, batchResult{ID: id, Success: true})
		}
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// deleteJob performs the full delete of a single job (files + related records + DB row).
func (h *Handler) deleteJob(ctx context.Context, jobID string) error {
	job, err := h.jobRepo.FindByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("Job not found")
	}

	if job.Status == models.StatusProcessing {
		return fmt.Errorf("Cannot delete job that is currently processing")
	}

	// Delete bundle directory
	if job.ArtifactDir != nil && *job.ArtifactDir != "" {
		_ = h.fileService.RemoveDirectory(*job.ArtifactDir)
	}

	// Delete files outside the bundle (legacy or multi-track)
	if job.IsMultiTrack && job.MultiTrackFolder != nil {
		_ = h.fileService.RemoveDirectory(*job.MultiTrackFolder)
	} else if job.ArtifactDir == nil || *job.ArtifactDir == "" {
		_ = h.fileService.RemoveFile(job.AudioPath)
	}

	if job.AupFilePath != nil {
		_ = h.fileService.RemoveFile(*job.AupFilePath)
	}

	// Delete related records
	if err := h.chatRepo.DeleteByJobID(ctx, jobID); err != nil {
		logger.Warn("batch delete: failed to delete chat sessions", "job_id", jobID, "error", err)
	}
	if err := h.noteRepo.DeleteByTranscriptionID(ctx, jobID); err != nil {
		logger.Warn("batch delete: failed to delete notes", "job_id", jobID, "error", err)
	}
	if err := h.summaryRepo.DeleteByTranscriptionID(ctx, jobID); err != nil {
		logger.Warn("batch delete: failed to delete summaries", "job_id", jobID, "error", err)
	}
	if err := h.speakerMappingRepo.DeleteByJobID(ctx, jobID); err != nil {
		logger.Warn("batch delete: failed to delete speaker mappings", "job_id", jobID, "error", err)
	}
	if err := h.jobRepo.DeleteExecutionsByJobID(ctx, jobID); err != nil {
		logger.Warn("batch delete: failed to delete executions", "job_id", jobID, "error", err)
	}
	if err := h.jobRepo.DeleteMultiTrackFilesByJobID(ctx, jobID); err != nil {
		logger.Warn("batch delete: failed to delete multi-track files", "job_id", jobID, "error", err)
	}

	// Delete from FTS index
	if h.ftsManager != nil {
		_ = h.ftsManager.Delete(jobID)
	}

	// Delete the job itself
	if err := h.jobRepo.Delete(ctx, jobID); err != nil {
		return fmt.Errorf("Failed to delete job: %s", err.Error())
	}

	return nil
}

// ---------- Batch Move ----------

func (h *Handler) BatchMoveTranscriptsToFolder(c *gin.Context) {
	var req struct {
		IDs    []string `json:"ids"`
		Folder string   `json:"folder"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBatchSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("ids must contain 1-%d items", maxBatchSize)})
		return
	}

	ctx := c.Request.Context()
	folder := strings.TrimSpace(req.Folder)
	results := make([]batchResult, 0, len(req.IDs))

	for _, id := range req.IDs {
		if err := h.moveJobToFolder(ctx, id, folder); err != nil {
			results = append(results, batchResult{ID: id, Success: false, Error: err.Error()})
		} else {
			results = append(results, batchResult{ID: id, Success: true})
		}
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// moveJobToFolder moves a single job to the specified folder, both on disk and in DB.
func (h *Handler) moveJobToFolder(ctx context.Context, jobID, folder string) error {
	job, err := h.jobRepo.FindByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("Job not found")
	}

	var artifactDir, audioPath, jsonPath, mdPath *string

	// Move bundle on disk if we have an artifact directory
	if job.ArtifactDir != nil && *job.ArtifactDir != "" {
		oldDir := *job.ArtifactDir
		newDir, moveErr := transcription.MoveBundleToFolder(oldDir, folder)
		if moveErr != nil {
			return fmt.Errorf("Failed to move bundle: %s", moveErr.Error())
		}

		if newDir != oldDir {
			// Suppress BundleWatcher from reacting before the DB is updated.
			if h.bundleManager != nil {
				if svc := h.bundleManager.SyncService(); svc != nil {
					metaPath := transcription.MetadataPath(newDir)
					if info, statErr := os.Stat(metaPath); statErr == nil {
						svc.MarkSelfWrite(metaPath, info.ModTime().UnixNano())
					}
				}
			}

			artifactDir = &newDir
			rebased := rebaseBundlePath(job.AudioPath, oldDir, newDir)
			audioPath = &rebased
			if job.TranscriptJSONPath != nil {
				p := rebaseBundlePath(*job.TranscriptJSONPath, oldDir, newDir)
				jsonPath = &p
			}
			if job.TranscriptMarkdownPath != nil {
				p := rebaseBundlePath(*job.TranscriptMarkdownPath, oldDir, newDir)
				mdPath = &p
			}
		}
	}

	var folderPtr *string
	if folder != "" {
		folderPtr = &folder
	}

	if err := h.jobRepo.UpdateBundlePaths(ctx, jobID, artifactDir, audioPath, jsonPath, mdPath, folderPtr); err != nil {
		// Rollback disk move if DB update fails.
		if artifactDir != nil && job.ArtifactDir != nil {
			if rollbackErr := os.Rename(*artifactDir, *job.ArtifactDir); rollbackErr != nil {
				logger.Warn("batch move: disk rollback failed", "job_id", jobID, "error", rollbackErr)
			}
		}
		return fmt.Errorf("Failed to update job: %s", err.Error())
	}

	// Best-effort metadata sidecar sync
	h.syncMetadataToBundle(ctx, jobID)

	return nil
}

// ---------- Batch Start ----------

func (h *Handler) BatchStartTranscriptions(c *gin.Context) {
	var req struct {
		IDs    []string              `json:"ids"`
		Params models.WhisperXParams `json:"params"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBatchSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("ids must contain 1-%d items", maxBatchSize)})
		return
	}

	// Normalize and validate diarization params (same as single-start path)
	normalizedDiarizeModel, validDiarizeModel := normalizeDiarizeModel(req.Params.DiarizeModel)
	if !validDiarizeModel {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid diarize_model. Must be 'nvidia_sortformer' or 'sherpa-onnx'"})
		return
	}
	req.Params.DiarizeModel = normalizedDiarizeModel
	// h.fallbackDiarizationModelIfTokenMissing(&req.Params, "batch_start_transcriptions", h.config.HFToken) // pyannote disabled

	ctx := c.Request.Context()
	results := make([]batchResult, 0, len(req.IDs))

	for _, id := range req.IDs {
		if err := h.startJob(ctx, id, req.Params); err != nil {
			results = append(results, batchResult{ID: id, Success: false, Error: err.Error()})
		} else {
			results = append(results, batchResult{ID: id, Success: true})
		}
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// startJob validates and enqueues a single job for transcription.
func (h *Handler) startJob(ctx context.Context, jobID string, params models.WhisperXParams) error {
	job, err := h.jobRepo.FindByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("Job not found")
	}

	// Allow transcription for uploaded, completed, and failed jobs
	if job.Status != models.StatusUploaded && job.Status != models.StatusCompleted && job.Status != models.StatusFailed {
		return fmt.Errorf("Cannot start transcription: job is currently processing or pending")
	}

	// Update job with parameters
	job.Parameters = params
	job.Diarization = params.Diarize
	job.Status = models.StatusPending
	job.Transcript = nil
	job.Summary = nil
	job.ErrorMessage = nil

	if err := h.jobRepo.Update(ctx, job); err != nil {
		return fmt.Errorf("Failed to update job: %s", err.Error())
	}

	// Enqueue (taskQueue may be nil in test)
	if h.taskQueue != nil {
		if err := h.taskQueue.EnqueueJob(jobID); err != nil {
			return fmt.Errorf("Failed to enqueue job: %s", err.Error())
		}
	}

	return nil
}
