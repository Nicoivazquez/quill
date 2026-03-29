package api

import (
	"errors"
	"net/http"

	"quill/internal/models"
	"quill/internal/transcription"
	"quill/pkg/logger"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SpeakerMappingRequest represents a speaker mapping update request
type SpeakerMappingRequest struct {
	OriginalSpeaker string `json:"original_speaker" binding:"required"`
	CustomName      string `json:"custom_name" binding:"required"`
}

// SpeakerMappingsUpdateRequest represents a bulk speaker mappings update
type SpeakerMappingsUpdateRequest struct {
	Mappings []SpeakerMappingRequest `json:"mappings" binding:"required"`
}

// PromoteSuggestionRequest represents a request to promote a voice suggestion to a mapping
type PromoteSuggestionRequest struct {
	OriginalSpeaker string  `json:"original_speaker" binding:"required"`
	ContactID       uint    `json:"contact_id" binding:"required"`
	ContactName     string  `json:"contact_name" binding:"required"`
	Score           float64 `json:"score" binding:"required"`
}

// SpeakerMappingResponse represents a speaker mapping response
type SpeakerMappingResponse struct {
	ID              uint    `json:"id"`
	OriginalSpeaker string  `json:"original_speaker"`
	CustomName      string  `json:"custom_name"`
	ContactID       *uint   `json:"contact_id,omitempty"`
	ConfidenceScore float64 `json:"confidence_score"`
	MatchSource     string  `json:"match_source"`
	MatchTier       string  `json:"match_tier"`
	ReviewStatus    string  `json:"review_status"`
}

// DismissSuggestionRequest represents a request to dismiss a speaker suggestion
type DismissSuggestionRequest struct {
	MappingID uint `json:"mapping_id" binding:"required"`
}

type SpeakerMappingsUpdateResponse struct {
	Mappings         []SpeakerMappingResponse       `json:"mappings"`
	ContactBootstrap speakerContactBootstrapSummary `json:"contact_bootstrap"`
}

// GetSpeakerMappings retrieves all speaker mappings for a transcription
// @Summary Get speaker mappings for a transcription
// @Description Retrieves all custom speaker names for a transcription job
// @Tags transcription
// @Produce json
// @Param id path string true "Transcription Job ID"
// @Success 200 {array} SpeakerMappingResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/transcription/{id}/speakers [get]
func (h *Handler) GetSpeakerMappings(c *gin.Context) {
	jobID := c.Param("id")

	// Verify the transcription job exists
	if _, err := h.jobRepo.FindByID(c.Request.Context(), jobID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Transcription job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transcription job"})
		return
	}

	// Get speaker mappings
	mappings, err := h.speakerMappingRepo.ListByJob(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get speaker mappings"})
		return
	}

	// Convert to response format
	response := make([]SpeakerMappingResponse, len(mappings))
	for i, mapping := range mappings {
		response[i] = speakerMappingToResponse(mapping)
	}

	c.JSON(http.StatusOK, response)
}

// UpdateSpeakerMappings updates speaker mappings for a transcription
// @Summary Update speaker mappings for a transcription
// @Description Updates or creates custom speaker names for a transcription job
// @Tags transcription
// @Accept json
// @Produce json
// @Param id path string true "Transcription Job ID"
// @Param request body SpeakerMappingsUpdateRequest true "Speaker mappings to update"
// @Success 200 {array} SpeakerMappingResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/transcription/{id}/speakers [post]
func (h *Handler) UpdateSpeakerMappings(c *gin.Context) {
	jobID := c.Param("id")

	var req SpeakerMappingsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Verify the transcription job exists
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Transcription job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transcription job"})
		return
	}

	// Convert request to model — user-entered mappings are always "manual".
	var mappings []models.SpeakerMapping
	for _, mapping := range req.Mappings {
		mappings = append(mappings, models.SpeakerMapping{
			TranscriptionJobID: jobID,
			OriginalSpeaker:    mapping.OriginalSpeaker,
			CustomName:         mapping.CustomName,
			MatchSource:        "manual",
		})
	}

	// Update mappings using repository
	if err := h.speakerMappingRepo.UpdateMappings(c.Request.Context(), jobID, mappings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update speaker mappings"})
		return
	}

	// Fetch updated mappings to return
	updatedMappings, err := h.speakerMappingRepo.ListByJob(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch updated mappings"})
		return
	}

	// Best-effort transcript file rewrite: update speaker_name in JSON segments
	// and regenerate the markdown with display names.
	if rewriteErr := transcription.RewriteTranscriptFiles(job, updatedMappings); rewriteErr != nil {
		// Do not fail the rename flow; the DB mappings are already committed.
		logger.Warn("transcript file rewrite failed", "job_id", jobID, "error", rewriteErr)
	}

	// Best-effort bootstrap: when a speaker is renamed to a real contact name,
	// auto-create/fill contact voice artifacts once from transcript timestamps.
	bootstrapSummary, bootstrapErr := h.bootstrapContactsFromSpeakerMappings(c.Request.Context(), job, updatedMappings)
	if bootstrapErr != nil {
		// Do not fail rename flow on background-contact bootstrap issues.
		// The user can still manage contacts manually.
		logger.Warn("speaker->contact bootstrap failed", "job_id", jobID, "error", bootstrapErr)
	}

	// Best-effort metadata sidecar sync
	h.syncMetadataToBundle(c.Request.Context(), jobID)

	// Best-effort auto-publish to Obsidian with updated speaker names.
	AutoPublishToObsidian(job)

	// Best-effort: regenerate summaries that include speaker info.
	h.regenerateSpeakerSummaries(job, updatedMappings)

	// Convert to response format
	response := make([]SpeakerMappingResponse, len(updatedMappings))
	for i, mapping := range updatedMappings {
		response[i] = speakerMappingToResponse(mapping)
	}

	c.JSON(http.StatusOK, SpeakerMappingsUpdateResponse{
		Mappings:         response,
		ContactBootstrap: bootstrapSummary,
	})
}

// PromoteSpeakerSuggestion promotes a voice-match suggestion to a persisted mapping.
// @Summary Promote a speaker suggestion
// @Description Converts a suggest-tier voice match into a committed speaker mapping
// @Tags transcription
// @Accept json
// @Produce json
// @Param id path string true "Transcription Job ID"
// @Param request body PromoteSuggestionRequest true "Suggestion to promote"
// @Success 200 {object} SpeakerMappingsUpdateResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/transcription/{id}/speakers/promote [post]
func (h *Handler) PromoteSpeakerSuggestion(c *gin.Context) {
	jobID := c.Param("id")

	var req PromoteSuggestionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Verify the transcription job exists.
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Transcription job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transcription job"})
		return
	}

	// Build the mapping with promotion metadata.
	contactID := req.ContactID
	mapping := models.SpeakerMapping{
		TranscriptionJobID: jobID,
		OriginalSpeaker:    req.OriginalSpeaker,
		CustomName:         req.ContactName,
		ContactID:          &contactID,
		ConfidenceScore:    req.Score,
		MatchSource:        "suggestion_promoted",
		MatchTier:          "suggest",
		ReviewStatus:       "accepted",
	}

	upserted, err := h.speakerMappingRepo.UpsertMapping(c.Request.Context(), jobID, mapping)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to promote suggestion"})
		return
	}

	// Fetch all mappings for side effects.
	allMappings, err := h.speakerMappingRepo.ListByJob(c.Request.Context(), jobID)
	if err != nil {
		logger.Warn("promote: failed to list mappings for side effects", "job_id", jobID, "error", err)
	}

	// Best-effort side effects (same as bulk update).
	if len(allMappings) > 0 {
		if rewriteErr := transcription.RewriteTranscriptFiles(job, allMappings); rewriteErr != nil {
			logger.Warn("promote: transcript file rewrite failed", "job_id", jobID, "error", rewriteErr)
		}
	}

	bootstrapSummary, bootstrapErr := h.bootstrapContactsFromSpeakerMappings(c.Request.Context(), job, allMappings)
	if bootstrapErr != nil {
		logger.Warn("promote: contact bootstrap failed", "job_id", jobID, "error", bootstrapErr)
	}

	h.syncMetadataToBundle(c.Request.Context(), jobID)

	// Best-effort auto-publish to Obsidian with updated speaker names.
	AutoPublishToObsidian(job)

	// Best-effort: regenerate summaries that include speaker info.
	h.regenerateSpeakerSummaries(job, allMappings)

	// Build response with the single promoted mapping.
	resp := SpeakerMappingsUpdateResponse{
		Mappings:         []SpeakerMappingResponse{speakerMappingToResponse(*upserted)},
		ContactBootstrap: bootstrapSummary,
	}

	c.JSON(http.StatusOK, resp)
}

// GetSpeakerSuggestions returns pending speaker suggestions for a transcription.
// @Summary Get pending speaker suggestions
// @Description Returns suggest-tier speaker matches awaiting user review
// @Tags transcription
// @Produce json
// @Param id path string true "Transcription Job ID"
// @Success 200 {array} SpeakerMappingResponse
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/transcription/{id}/speakers/suggestions [get]
func (h *Handler) GetSpeakerSuggestions(c *gin.Context) {
	jobID := c.Param("id")

	// Verify the transcription job exists.
	if _, err := h.jobRepo.FindByID(c.Request.Context(), jobID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Transcription job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transcription job"})
		return
	}

	suggestions, err := h.speakerMappingRepo.ListPendingSuggestions(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get speaker suggestions"})
		return
	}

	response := make([]SpeakerMappingResponse, len(suggestions))
	for i, s := range suggestions {
		response[i] = speakerMappingToResponse(s)
	}
	c.JSON(http.StatusOK, response)
}

// DismissSpeakerSuggestion marks a pending suggestion as dismissed.
// @Summary Dismiss a speaker suggestion
// @Description Marks a suggest-tier match as dismissed so it no longer appears
// @Tags transcription
// @Accept json
// @Produce json
// @Param id path string true "Transcription Job ID"
// @Param request body DismissSuggestionRequest true "Suggestion to dismiss"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/transcription/{id}/speakers/dismiss [post]
func (h *Handler) DismissSpeakerSuggestion(c *gin.Context) {
	jobID := c.Param("id")

	var req DismissSuggestionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Verify the mapping belongs to this job (prevent IDOR).
	mappings, err := h.speakerMappingRepo.ListByJob(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify suggestion ownership"})
		return
	}
	found := false
	for _, m := range mappings {
		if m.ID == req.MappingID {
			found = true
			break
		}
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Suggestion not found for this transcription"})
		return
	}

	if err := h.speakerMappingRepo.UpdateReviewStatus(c.Request.Context(), req.MappingID, "dismissed"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to dismiss suggestion"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "dismissed"})
}

// speakerMappingToResponse converts a model to an API response.
func speakerMappingToResponse(m models.SpeakerMapping) SpeakerMappingResponse {
	return SpeakerMappingResponse{
		ID:              m.ID,
		OriginalSpeaker: m.OriginalSpeaker,
		CustomName:      m.CustomName,
		ContactID:       m.ContactID,
		ConfidenceScore: m.ConfidenceScore,
		MatchSource:     m.MatchSource,
		MatchTier:       m.MatchTier,
		ReviewStatus:    m.ReviewStatus,
	}
}
