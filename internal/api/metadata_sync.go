package api

import (
	"context"
	"os"

	"quill/internal/transcription"
	"quill/pkg/logger"
)

// syncMetadataToBundle writes/updates the metadata.json sidecar for a
// transcription job's bundle directory. This is called after any mutation
// (speaker mapping update, summary save, note CRUD, title change, folder move)
// so the on-disk metadata stays consistent with the database.
//
// Errors are logged but not returned — metadata sync is best-effort and
// must never break the primary mutation flow.
func (h *Handler) syncMetadataToBundle(ctx context.Context, jobID string) {
	job, err := h.jobRepo.FindByID(ctx, jobID)
	if err != nil {
		logger.Warn("metadata sync: job not found", "job_id", jobID, "error", err)
		return
	}

	if job.ArtifactDir == nil || *job.ArtifactDir == "" {
		return
	}

	mappings, _ := h.speakerMappingRepo.ListByJob(ctx, jobID)
	summaries, _ := h.summaryRepo.ListByTranscriptionID(ctx, jobID)
	notes, _ := h.noteRepo.ListByJob(ctx, jobID)

	meta := transcription.BuildMetadataFromJob(job, mappings, summaries, notes)

	if err := transcription.WriteMetadata(*job.ArtifactDir, meta); err != nil {
		logger.Warn("metadata sync: failed to write metadata.json",
			"job_id", jobID, "dir", *job.ArtifactDir, "error", err)
		return
	}

	// Mark the file as self-written so the bundle watcher doesn't re-import it.
	if h.bundleManager != nil {
		if svc := h.bundleManager.SyncService(); svc != nil {
			metaPath := transcription.MetadataPath(*job.ArtifactDir)
			if info, err := os.Stat(metaPath); err == nil {
				svc.MarkSelfWrite(metaPath, info.ModTime().UnixNano())
			}
		}
	}
}
