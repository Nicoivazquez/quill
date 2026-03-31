package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"quill/internal/llm"
	"quill/internal/models"
	"quill/internal/sse"
	"quill/pkg/logger"
)

// regenSegment is the minimal segment representation for building
// speaker-labelled transcript text during summary regeneration.
type regenSegment struct {
	Text        string  `json:"text"`
	Speaker     *string `json:"speaker,omitempty"`
	SpeakerName *string `json:"speaker_name,omitempty"`
}

type regenPayload struct {
	Text     string         `json:"text"`
	Segments []regenSegment `json:"segments,omitempty"`
}

// formatTranscriptWithSpeakers builds a speaker-labelled transcript string
// from the JSON file, mirroring the frontend's formatTranscriptWithSpeakers().
// Each segment becomes a line like "[Speaker Name] segment text".
func formatTranscriptWithSpeakers(jsonPath string, mappings map[string]string) (string, error) {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return "", fmt.Errorf("summary_regen: read transcript %s: %w", jsonPath, err)
	}

	var payload regenPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("summary_regen: parse transcript %s: %w", jsonPath, err)
	}

	if len(payload.Segments) == 0 {
		return payload.Text, nil
	}

	var sb strings.Builder
	for _, seg := range payload.Segments {
		// Resolve display name: speaker_name > mapping > raw speaker key
		displayName := ""
		if seg.SpeakerName != nil && *seg.SpeakerName != "" {
			displayName = *seg.SpeakerName
		} else if seg.Speaker != nil {
			if mapped, ok := mappings[*seg.Speaker]; ok {
				displayName = mapped
			} else {
				displayName = *seg.Speaker
			}
		}

		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}

		if displayName != "" {
			sb.WriteString(fmt.Sprintf("[%s] %s\n", displayName, text))
		} else {
			sb.WriteString(text + "\n")
		}
	}

	return sb.String(), nil
}

// regenerateSpeakerSummaries is called after speaker mappings change.
// It finds all existing summaries for the transcription whose templates
// had include_speaker_info enabled and regenerates them with the updated
// speaker labels. Runs in a background goroutine.
func (h *Handler) regenerateSpeakerSummaries(job *models.TranscriptionJob, mappings []models.SpeakerMapping) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		jobID := job.ID
		logger.Info("summary_regen: starting for job", "job_id", jobID)

		// 1. Fetch all summaries for this transcription.
		summaries, err := h.summaryRepo.ListByTranscriptionID(ctx, jobID)
		if err != nil {
			logger.Warn("summary_regen: failed to list summaries", "job_id", jobID, "error", err)
			return
		}
		if len(summaries) == 0 {
			return
		}

		// 2. Build speaker mapping lookup.
		mappingMap := make(map[string]string, len(mappings))
		for _, m := range mappings {
			mappingMap[m.OriginalSpeaker] = m.CustomName
		}

		// 3. Check if transcript JSON exists.
		if job.TranscriptJSONPath == nil || *job.TranscriptJSONPath == "" {
			logger.Warn("summary_regen: no transcript JSON path", "job_id", jobID)
			return
		}

		// 4. Get LLM service (once for all summaries).
		svc, _, err := h.getLLMService(ctx)
		if err != nil {
			logger.Warn("summary_regen: no LLM service available", "job_id", jobID, "error", err)
			h.emitNotification(sse.NotifyLLMNotConfigured, "warning",
				"Summary regeneration skipped because no LLM provider is configured.",
				"open_settings_llm")
			return
		}

		// 5. Format transcript with updated speaker labels (once for all summaries).
		transcriptText, err := formatTranscriptWithSpeakers(*job.TranscriptJSONPath, mappingMap)
		if err != nil {
			logger.Warn("summary_regen: format transcript failed", "job_id", jobID, "error", err)
			return
		}

		regenCount := 0
		for _, summary := range summaries {
			if summary.TemplateID == nil || *summary.TemplateID == "" {
				continue
			}

			// 6. Look up the template to check include_speaker_info.
			tmpl, err := h.summaryRepo.FindByID(ctx, *summary.TemplateID)
			if err != nil {
				logger.Warn("summary_regen: template not found", "template_id", *summary.TemplateID, "error", err)
				continue
			}
			if !tmpl.IncludeSpeakerInfo {
				continue
			}

			// 7. Build the prompt (same format as frontend).
			combinedContent := fmt.Sprintf(
				"Transcript (with speaker labels - each line is prefixed with [SPEAKER_NAME]):\n%s\n\nInstructions:\n%s",
				transcriptText, tmpl.Prompt,
			)

			// 8. Call LLM (non-streaming for background regen).
			messages := []llm.ChatMessage{{Role: "user", Content: combinedContent}}
			resp, err := svc.ChatCompletion(ctx, summary.Model, messages, 0.0)
			if err != nil {
				logger.Warn("summary_regen: LLM call failed", "job_id", jobID, "summary_id", summary.ID, "error", err)
				continue
			}
			if resp == nil || len(resp.Choices) == 0 {
				logger.Warn("summary_regen: empty LLM response", "job_id", jobID, "summary_id", summary.ID)
				continue
			}

			newContent := resp.Choices[0].Message.Content

			// 9. Update the existing summary with new content.
			summary.Content = newContent
			summary.UpdatedAt = time.Now()
			if err := h.summaryRepo.SaveSummary(ctx, &summary); err != nil {
				logger.Warn("summary_regen: failed to save summary", "job_id", jobID, "summary_id", summary.ID, "error", err)
				continue
			}

			regenCount++
			logger.Info("summary_regen: regenerated summary",
				"job_id", jobID,
				"summary_id", summary.ID,
				"template", tmpl.Name,
				"model", summary.Model,
			)
		}

		if regenCount > 0 {
			// Update cached summary on the job record with the latest.
			latestSummary, err := h.summaryRepo.GetLatestSummary(ctx, jobID)
			if err == nil && latestSummary != nil {
				if err := h.jobRepo.UpdateSummary(ctx, jobID, latestSummary.Content); err != nil {
					logger.Warn("summary_regen: failed to update cached summary", "job_id", jobID, "error", err)
				}
			}
			h.syncMetadataToBundle(ctx, jobID)
			logger.Info("summary_regen: complete", "job_id", jobID, "regenerated", regenCount)
		}
	}()
}
