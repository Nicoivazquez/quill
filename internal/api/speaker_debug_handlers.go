package api

import (
	"net/http"

	"quill/internal/contacts"
	"quill/internal/database"
	"quill/internal/models"

	"github.com/gin-gonic/gin"
)

// speakerDebugResponse is the response body for the speaker identification
// debug/diagnostics endpoint.
type speakerDebugResponse struct {
	Contacts contactSignatureDebug `json:"contacts"`
	Mappings mappingDebug          `json:"mappings"`
}

type contactSignatureDebug struct {
	Total         int                 `json:"total"`
	Ready         int                 `json:"ready"`
	Processing    int                 `json:"processing"`
	Failed        int                 `json:"failed"`
	None          int                 `json:"none"`
	FailedDetails []failedContactInfo `json:"failed_details,omitempty"`
}

type failedContactInfo struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	RetryCount int    `json:"retry_count"`
	LastError  string `json:"last_error,omitempty"`
}

type mappingDebug struct {
	Total            int            `json:"total"`
	BySource         map[string]int `json:"by_source"`
	PendingReview    int            `json:"pending_review"`
	JobsWithMappings int            `json:"jobs_with_mappings"`
}

// SpeakerIdentificationDebug returns diagnostic information about the speaker
// identification pipeline: contact voice signature status breakdown, speaker
// mapping statistics, and details on failed signature extractions.
//
// @Summary      Speaker identification debug info
// @Tags         transcription
// @Produce      json
// @Success      200  {object}  speakerDebugResponse
// @Router       /api/v1/transcription/speakers/debug [get]
func (h *Handler) SpeakerIdentificationDebug(c *gin.Context) {
	ctx := c.Request.Context()
	resp := speakerDebugResponse{
		Mappings: mappingDebug{
			BySource: make(map[string]int),
		},
	}

	// --- Contact signature status breakdown ---
	vault, err := getActiveVault()
	if err == nil {
		allContacts, listErr := h.contactRepo.ListByVault(ctx, vault.ID)
		if listErr == nil {
			resp.Contacts.Total = len(allContacts)
			for _, ct := range allContacts {
				switch ct.SignatureStatus {
				case "ready":
					resp.Contacts.Ready++
				case "processing":
					resp.Contacts.Processing++
				case "failed":
					resp.Contacts.Failed++
					meta := contacts.ParseSignatureMetadata(ct.SignatureData)
					resp.Contacts.FailedDetails = append(resp.Contacts.FailedDetails, failedContactInfo{
						ID:         ct.ID,
						Name:       ct.Name,
						RetryCount: meta.RetryCount,
						LastError:  meta.LastError,
					})
				default: // "none" or empty
					resp.Contacts.None++
				}
			}
		}
	}

	// --- Speaker mapping aggregate stats ---
	var allMappings []models.SpeakerMapping
	if err := database.DB.WithContext(ctx).Find(&allMappings).Error; err == nil {
		resp.Mappings.Total = len(allMappings)

		jobSet := make(map[string]struct{})
		for _, m := range allMappings {
			jobSet[m.TranscriptionJobID] = struct{}{}

			src := m.MatchSource
			if src == "" {
				src = "manual"
			}
			resp.Mappings.BySource[src]++

			if m.ReviewStatus == "pending" {
				resp.Mappings.PendingReview++
			}
		}
		resp.Mappings.JobsWithMappings = len(jobSet)
	}

	c.JSON(http.StatusOK, resp)
}

// RerunAutoLabel re-triggers the auto-label pipeline for a given job.
// This is a debug-only endpoint for diagnosing the speaker identification pipeline.
//
// @Summary      Re-run auto speaker identification for a job
// @Tags         transcription
// @Param        id   path  string  true  "Transcription job ID"
// @Produce      json
// @Success      200
// @Router       /api/v1/transcription/{id}/speakers/rerun-auto-label [post]
func (h *Handler) RerunAutoLabel(c *gin.Context) {
	jobID := c.Param("id")
	ctx := c.Request.Context()

	if err := h.AutoLabelSpeakersForJob(ctx, jobID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": jobID})
}
