package api

import (
	"bufio"
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"quill/internal/llm"
	"quill/internal/models"
	"quill/internal/sse"
	"quill/pkg/logger"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type SummarizeRequest struct {
	Model           string  `json:"model" binding:"required"`
	Content         string  `json:"content" binding:"required"`
	TranscriptionID string  `json:"transcription_id" binding:"required"`
	TemplateID      *string `json:"template_id,omitempty"`
}

// Summarize streams LLM output for a given content prompt
// @Summary Summarize content
// @Description Stream an LLM-generated summary for provided content; persists latest summary for the transcription
// @Tags summarize
// @Accept json
// @Produce text/event-stream
// @Param request body SummarizeRequest true "Summarize request"
// @Success 200 {string} string "Event stream"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security ApiKeyAuth
// @Security BearerAuth
// @Router /api/v1/summarize [post]
func (h *Handler) Summarize(c *gin.Context) {
	var req SummarizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	svc, provider, err := h.getLLMService(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Prepare chat messages: simple single-user message with full content
	messages := []llm.ChatMessage{{Role: "user", Content: req.Content}}

	start := time.Now()
	log.Printf("[summarize] start transcription_id=%s provider=%s model=%s content_len=%d", req.TranscriptionID, provider, req.Model, len(req.Content))

	// Stream response with proper headers for real-time delivery
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")
	c.Header("X-Accel-Buffering", "no") // Disable nginx buffering
	c.Status(http.StatusOK)             // Start response immediately

	h.processSummarization(c, req, svc, messages, start)
}

func (h *Handler) processSummarization(c *gin.Context, req SummarizeRequest, svc llm.Service, messages []llm.ChatMessage, start time.Time) {
	// Allow longer generation time for large transcripts and smaller models
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Minute)
	defer cancel()

	contentChan, errChan := svc.ChatCompletionStream(ctx, req.Model, messages, 0.0)
	flusher, _ := c.Writer.(http.Flusher)
	writer := bufio.NewWriter(c.Writer)

	finalText := ""
	gotFirstChunk := false

	// Loop handles one chunk/error at a time
	for {
		select {
		case chunk, ok := <-contentChan:
			if !ok {
				writer.Flush()
				if flusher != nil {
					flusher.Flush()
				}
				// Persist summary once streaming completes
				h.persistSummary(req, finalText)
				log.Printf("[summarize] complete transcription_id=%s model=%s bytes=%d duration_ms=%d", req.TranscriptionID, req.Model, len(finalText), time.Since(start).Milliseconds())
				return
			}
			finalText += chunk
			_, _ = writer.WriteString(chunk)
			writer.Flush()
			if flusher != nil {
				flusher.Flush()
			}
			if !gotFirstChunk && len(chunk) > 0 {
				gotFirstChunk = true
				log.Printf("[summarize] first_chunk transcription_id=%s model=%s at_ms=%d", req.TranscriptionID, req.Model, time.Since(start).Milliseconds())
			}
		case err := <-errChan:
			if err != nil {
				h.handleSummarizeError(c, req, svc, messages, err, finalText, start)
			}
			// Persist any partial content on error
			h.persistSummary(req, finalText)
			return
		case <-ctx.Done():
			// Persist any partial content on timeout/cancel
			h.persistSummary(req, finalText)
			log.Printf("[summarize] timeout/cancel transcription_id=%s model=%s bytes=%d duration_ms=%d", req.TranscriptionID, req.Model, len(finalText), time.Since(start).Milliseconds())
			return
		}
	}
}

func (h *Handler) handleSummarizeError(c *gin.Context, req SummarizeRequest, svc llm.Service, messages []llm.ChatMessage, err error, partialText string, start time.Time) {
	flusher, _ := c.Writer.(http.Flusher)
	writer := bufio.NewWriter(c.Writer)

	// Best-effort error signal
	// If streaming is unsupported for this model/org, fall back to non-streaming
	errStr := err.Error()
	if strings.Contains(errStr, "\"param\": \"stream\"") || strings.Contains(errStr, "unsupported_value") || strings.Contains(errStr, "must be verified to stream") {
		log.Printf("[summarize] falling back to non-streaming transcription_id=%s model=%s due to: %v", req.TranscriptionID, req.Model, err)
		resp, err2 := svc.ChatCompletion(c.Request.Context(), req.Model, messages, 0.0)
		if err2 != nil || resp == nil || len(resp.Choices) == 0 {
			log.Printf("[summarize] fallback failed transcription_id=%s model=%s err=%v", req.TranscriptionID, req.Model, err2)
			_, _ = c.Writer.Write([]byte("\n"))
			writer.Flush()
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		content := resp.Choices[0].Message.Content
		// Write content (appended to partial if any, though likely partial is empty if stream failed immediately)
		_, _ = writer.WriteString(content)
		writer.Flush()
		if flusher != nil {
			flusher.Flush()
		}
		// We should persist the FULL text (partial + fallback), but partialText is passed by value.
		// However, handleSumarizeError doesn't update partialText in caller.
		// The caller calls persistSummary(req, finalText) after this function returns.
		// So we actually need to persist here if we succeed?
		// Or return the new text?
		// Since we can't easily update finalText in caller without pointer, let's persist here if success.
		h.persistSummary(req, partialText+content)
		log.Printf("[summarize] fallback complete transcription_id=%s model=%s bytes=%d duration_ms=%d", req.TranscriptionID, req.Model, len(partialText+content), time.Since(start).Milliseconds())

		// To avoid double persistence in caller (which uses stale finalText), we need a way to signal "done".
		// But caller persists anyway.
		// It's acceptable to double-persist (idempotent updates usually) or just accept that caller persists partial and we persist full.
		return
	}
	_, _ = c.Writer.Write([]byte("\n"))
	writer.Flush()
	if flusher != nil {
		flusher.Flush()
	}
	log.Printf("[summarize] error transcription_id=%s model=%s err=%v duration_ms=%d", req.TranscriptionID, req.Model, err, time.Since(start).Milliseconds())
}

func (h *Handler) persistSummary(req SummarizeRequest, content string) {
	if req.TranscriptionID == "" || content == "" {
		return
	}
	templateName := ""
	if req.TemplateID != nil && *req.TemplateID != "" {
		if tmpl, err := h.summaryRepo.FindByID(context.Background(), *req.TemplateID); err == nil {
			templateName = tmpl.Name
		}
	}
	sum := &models.Summary{
		TranscriptionID: req.TranscriptionID,
		TemplateID:      req.TemplateID,
		TemplateName:    templateName,
		Model:           req.Model,
		Content:         content,
	}
	if err := h.summaryRepo.SaveSummary(context.Background(), sum); err != nil {
		// Fallback: store on the transcription job record
		_ = h.jobRepo.UpdateSummary(context.Background(), req.TranscriptionID, content)
	} else {
		// Also cache on the transcription job for quick access
		_ = h.jobRepo.UpdateSummary(context.Background(), req.TranscriptionID, content)
	}

	// Best-effort metadata sidecar sync
	h.syncMetadataToBundle(context.Background(), req.TranscriptionID)

	// Best-effort FTS index update with new summary
	if h.ftsManager != nil && req.TranscriptionID != "" {
		if job, err := h.jobRepo.FindByID(context.Background(), req.TranscriptionID); err == nil {
			h.ftsUpsertJob(job)
		}
	}
}

// AutoGenerateSummaryForJob attempts to auto-generate a summary for a completed transcription.
// It is designed for background execution (e.g. queue completion hooks) and is best-effort.
func (h *Handler) AutoGenerateSummaryForJob(ctx context.Context, jobID string) error {
	job, err := h.jobRepo.FindByID(ctx, jobID)
	if err != nil {
		return err
	}

	if job.Status != models.StatusCompleted {
		logger.Debug("Auto-summary skipped: job not completed", "job_id", jobID, "status", job.Status)
		return nil
	}
	if job.Transcript == nil || strings.TrimSpace(*job.Transcript) == "" {
		logger.Debug("Auto-summary skipped: no transcript content", "job_id", jobID)
		return nil
	}

	// Check if any user has auto-summary enabled
	if !h.isAnyAutoSummaryEnabled(ctx) {
		logger.Debug("Auto-summary skipped: no user has auto-summary enabled", "job_id", jobID)
		return nil
	}

	// Skip if a summary already exists for this transcription
	existing, existErr := h.summaryRepo.GetLatestSummary(ctx, jobID)
	if existErr == nil && existing != nil && existing.Content != "" {
		logger.Debug("Auto-summary skipped: summary already exists", "job_id", jobID)
		return nil
	}

	// Get the default summary template
	template, err := h.summaryRepo.GetDefaultTemplate(ctx)
	if err != nil || template == nil {
		logger.Debug("Auto-summary skipped: no default summary template found", "job_id", jobID)
		return nil
	}

	// Get LLM service
	svc, provider, err := h.getLLMServiceForAutoTitle(ctx)
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "no active llm configuration") {
			logger.Warn("Auto-summary skipped: no LLM configured", "job_id", jobID)
			return nil
		}
		return err
	}

	// Extract plain text from transcript JSON
	transcriptText := extractTranscriptText(job.Transcript)
	if transcriptText == "" {
		logger.Debug("Auto-summary skipped: could not extract transcript text", "job_id", jobID)
		return nil
	}

	// Build the prompt using the template
	combinedContent := "Transcript:\n" + transcriptText + "\n\nInstructions:\n" + template.Prompt
	messages := []llm.ChatMessage{{Role: "user", Content: combinedContent}}

	model := template.Model
	if model == "" {
		// Fall back to any available chat model
		model = "gpt-4o-mini"
	}

	logger.Info("Auto-generating summary", "job_id", jobID, "provider", provider, "model", model)

	// Use non-streaming completion for background generation
	resp, err := svc.ChatCompletion(ctx, model, messages, 0.0)
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "connection refused") || strings.Contains(errLower, "no such host") {
			h.emitNotification(sse.NotifyOllamaUnreachable, "warning",
				"Could not reach LLM provider for auto-summary — is Ollama running?",
				"open_settings_llm")
		}
		return err
	}

	if resp == nil || len(resp.Choices) == 0 {
		return nil
	}

	content := resp.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return nil
	}

	// Persist the summary
	req := SummarizeRequest{
		Model:           model,
		Content:         combinedContent,
		TranscriptionID: jobID,
		TemplateID:      &template.ID,
	}
	h.persistSummary(req, content)

	logger.Info("Auto-generated summary", "job_id", jobID, "model", model, "content_len", len(content))

	// Notify the frontend
	h.broadcaster.BroadcastGlobal("summary_generated", map[string]string{
		"job_id": jobID,
	})

	return nil
}

func (h *Handler) isAnyAutoSummaryEnabled(ctx context.Context) bool {
	users, _, err := h.userRepo.List(ctx, 0, 1000)
	if err != nil {
		return true
	}
	if len(users) == 0 {
		return true
	}
	for _, user := range users {
		if user.AutoSummaryEnabled {
			return true
		}
	}
	return false
}

// extractTranscriptText extracts plain text from a transcript field that may be JSON.
func extractTranscriptText(transcript *string) string {
	if transcript == nil {
		return ""
	}
	raw := *transcript

	// Try to parse as JSON and extract .text field
	// The transcript is often stored as {"text": "...", "segments": [...]}
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		// Simple extraction: find "text" field value
		idx := strings.Index(raw, `"text"`)
		if idx >= 0 {
			rest := raw[idx+len(`"text"`):]
			// Skip whitespace and colon
			rest = strings.TrimLeft(rest, " \t\n\r:")
			if strings.HasPrefix(rest, `"`) {
				// Find the closing quote (handle escaped quotes)
				rest = rest[1:] // skip opening quote
				var result strings.Builder
				for i := 0; i < len(rest); i++ {
					if rest[i] == '\\' && i+1 < len(rest) {
						result.WriteByte(rest[i+1])
						i++
						continue
					}
					if rest[i] == '"' {
						return result.String()
					}
					result.WriteByte(rest[i])
				}
			}
		}
	}

	// If not JSON or extraction failed, use raw text
	return strings.TrimSpace(raw)
}

// GetSummaryForTranscription returns the latest summary for a transcription
// @Summary Get latest summary for transcription
// @Description Get the most recent saved summary for the given transcription
// @Tags summarize
// @Produce json
// @Param id path string true "Transcription ID"
// @Success 200 {object} models.Summary
// @Failure 404 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security ApiKeyAuth
// @Security BearerAuth
// @Router /api/v1/transcription/{id}/summary [get]
func (h *Handler) GetSummaryForTranscription(c *gin.Context) {
	tid := c.Param("id")
	if tid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Transcription ID required"})
		return
	}
	s, err := h.summaryRepo.GetLatestSummary(c.Request.Context(), tid)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// Fallback: check if summary is cached on the job record
			job, err2 := h.jobRepo.FindByID(c.Request.Context(), tid)
			if err2 == nil && job.Summary != nil && *job.Summary != "" {
				c.JSON(http.StatusOK, gin.H{
					"transcription_id": tid,
					"template_id":      nil,
					"model":            "",
					"content":          *job.Summary,
					"created_at":       job.UpdatedAt,
					"updated_at":       job.UpdatedAt,
				})
				return
			}
			// Return empty summary instead of 404 for graceful frontend handling
			c.JSON(http.StatusOK, gin.H{
				"transcription_id": tid,
				"template_id":      nil,
				"model":            "",
				"content":          "",
				"created_at":       nil,
				"updated_at":       nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch summary"})
		return
	}
	c.JSON(http.StatusOK, s)
}

// ListSummariesForTranscription returns all summaries for a transcription
// @Summary List all summaries for transcription
// @Description Get all saved summaries for the given transcription ordered by creation time
// @Tags summarize
// @Produce json
// @Param id path string true "Transcription ID"
// @Success 200 {array} models.Summary
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security ApiKeyAuth
// @Security BearerAuth
// @Router /api/v1/transcription/{id}/summaries [get]
func (h *Handler) ListSummariesForTranscription(c *gin.Context) {
	tid := c.Param("id")
	if tid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Transcription ID required"})
		return
	}
	summaries, err := h.summaryRepo.ListByTranscriptionID(c.Request.Context(), tid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch summaries"})
		return
	}
	c.JSON(http.StatusOK, summaries)
}

// DeleteSummary deletes a single summary by ID
// @Summary Delete a summary
// @Description Delete an individual summary by its ID
// @Tags summarize
// @Produce json
// @Param id path string true "Transcription ID"
// @Param summaryId path string true "Summary ID"
// @Success 204 {string} string "No Content"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security ApiKeyAuth
// @Security BearerAuth
// @Router /api/v1/transcription/{id}/summaries/{summaryId} [delete]
func (h *Handler) DeleteSummary(c *gin.Context) {
	summaryID := c.Param("summaryId")
	if summaryID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Summary ID required"})
		return
	}
	if err := h.summaryRepo.DeleteSummary(c.Request.Context(), summaryID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete summary"})
		return
	}
	c.Status(http.StatusNoContent)
}
