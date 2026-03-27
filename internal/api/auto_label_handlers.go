package api

import (
	"context"
	"fmt"
	"strings"

	"quill/internal/contacts"
	"quill/internal/database"
	"quill/internal/llm"
	"quill/internal/transcription"
	"quill/pkg/logger"
)

// autoLabelSSEEvent is the SSE payload sent after auto speaker identification.
type autoLabelSSEEvent struct {
	JobID        string                  `json:"job_id"`
	AutoAssigned []speakerMatchResponse  `json:"auto_assigned"`
	Suggestions  []speakerMatchResponse  `json:"suggestions"`
	Unmatched    []string                `json:"unmatched"`
}

type speakerMatchResponse struct {
	Speaker     string  `json:"speaker"`
	ContactID   uint    `json:"contact_id"`
	ContactName string  `json:"contact_name"`
	Score       float64 `json:"score"`
	Tier        string  `json:"tier"`
}

// AutoLabelSpeakersForJob runs the speaker auto-identification pipeline after a
// transcription job completes. It extracts per-speaker voice embeddings from the
// audio, compares them against contacts with ready voice signatures, persists
// auto-assigned mappings, rewrites transcript files, and broadcasts the result
// via SSE.
//
// This method is designed to be called from the queue completion hook alongside
// AutoGenerateTranscriptionTitleForJob. It is best-effort: errors are logged
// and returned but should not prevent other post-completion work.
func (h *Handler) AutoLabelSpeakersForJob(ctx context.Context, jobID string) error {
	// Guard: need both contact repo and contact manager.
	if h.contactRepo == nil || h.contactManager == nil {
		return nil
	}

	// Step 1: Load the completed job.
	job, err := h.jobRepo.FindByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("auto-label: find job: %w", err)
	}
	if job.Status != "completed" {
		return nil
	}
	if job.Transcript == nil || strings.TrimSpace(*job.Transcript) == "" {
		return nil
	}

	// Step 2: Parse transcript to get speaker labels + clip windows.
	transcript, err := parseTranscriptJSON(*job.Transcript)
	if err != nil {
		return fmt.Errorf("auto-label: parse transcript: %w", err)
	}
	if len(transcript.Segments) == 0 {
		return nil
	}

	windowsBySpeaker := buildSpeakerClipWindows(transcript.Segments)
	if len(windowsBySpeaker) == 0 {
		return nil
	}

	// Step 3: Resolve vault.
	vault, err := resolveJobVault(ctx, job)
	if err != nil {
		return fmt.Errorf("auto-label: resolve vault: %w", err)
	}

	// Check if any contacts have ready voice signatures before doing expensive extraction.
	readyContacts, err := h.contactRepo.ListBySignatureStatus(ctx, vault.ID, "ready")
	if err != nil {
		return fmt.Errorf("auto-label: list ready contacts: %w", err)
	}
	if len(readyContacts) == 0 {
		logger.Debug("auto-label: no contacts with ready voice signatures, skipping", "job_id", jobID)
		return nil
	}

	// Step 4: Resolve audio path.
	audioPath, err := resolveJobAudioPath(job, vault.Path)
	if err != nil {
		return fmt.Errorf("auto-label: resolve audio path: %w", err)
	}

	// Step 5: Convert internal clipWindows to contacts.ClipWindow.
	contactWindows := make(map[string]contacts.ClipWindow, len(windowsBySpeaker))
	for speaker, w := range windowsBySpeaker {
		contactWindows[speaker] = contacts.ClipWindow{Start: w.Start, End: w.End}
	}

	// Step 6: Extract per-speaker embeddings (FFmpeg + TitaNet).
	speakerEmbeddings, err := contacts.ExtractSpeakerEmbeddings(
		ctx, audioPath, contactWindows, h.config.WhisperXEnv,
	)
	if err != nil {
		return fmt.Errorf("auto-label: extract speaker embeddings: %w", err)
	}
	if len(speakerEmbeddings) == 0 {
		logger.Debug("auto-label: no speaker embeddings extracted", "job_id", jobID)
		return nil
	}

	// Step 7: Run the auto-label pipeline.
	autoLabelService := contacts.NewAutoLabelService(
		h.contactRepo,
		h.speakerMappingRepo,
		database.DB,
	)

	// Optionally inject LLM caller for voice+LLM fusion scoring.
	if caller := h.buildSpeakerIDLLMCaller(ctx); caller != nil {
		autoLabelService.SetLLMCaller(caller)
	}

	transcriptText := ""
	if job.Transcript != nil {
		transcriptText = *job.Transcript
	}
	result, err := autoLabelService.LabelSpeakers(ctx, vault.ID, vault.Path, jobID, speakerEmbeddings, transcriptText)
	if err != nil {
		return fmt.Errorf("auto-label: label speakers: %w", err)
	}

	// Step 8: Rewrite transcript files with auto-assigned speaker names.
	if len(result.AutoAssigned) > 0 {
		updatedMappings, mapErr := h.speakerMappingRepo.ListByJob(ctx, jobID)
		if mapErr == nil && len(updatedMappings) > 0 {
			if rewriteErr := transcription.RewriteTranscriptFiles(job, updatedMappings); rewriteErr != nil {
				logger.Warn("auto-label: transcript rewrite failed", "job_id", jobID, "error", rewriteErr)
			}
		}
	}

	// Step 9: Broadcast SSE event.
	if h.broadcaster != nil {
		event := autoLabelSSEEvent{
			JobID:        jobID,
			AutoAssigned: toSpeakerMatchResponses(result.AutoAssigned),
			Suggestions:  toSpeakerMatchResponses(result.Suggestions),
			Unmatched:    result.Unmatched,
		}
		h.broadcaster.Broadcast(jobID, "speaker_identification", event)
	}

	logger.Info("Auto speaker identification complete",
		"job_id", jobID,
		"auto_assigned", len(result.AutoAssigned),
		"suggestions", len(result.Suggestions),
		"unmatched", len(result.Unmatched),
	)

	return nil
}

// buildSpeakerIDLLMCaller attempts to build a contacts.LLMCaller from the
// configured LLM service. Returns nil if no LLM is available (voice-only
// matching will be used as a fallback).
func (h *Handler) buildSpeakerIDLLMCaller(ctx context.Context) contacts.LLMCaller {
	svc, _, err := h.getLLMServiceForAutoTitle(ctx)
	if err != nil {
		return nil
	}

	model, err := h.resolveAutoTitleModel(ctx, svc, "")
	if err != nil {
		return nil
	}

	return func(callCtx context.Context, prompt string) (string, error) {
		resp, err := svc.ChatCompletion(callCtx, model, []llm.ChatMessage{
			{Role: "user", Content: prompt},
		}, 0.1)
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("LLM returned no choices")
		}
		return resp.Choices[0].Message.Content, nil
	}
}

func toSpeakerMatchResponses(matches []contacts.SpeakerMatch) []speakerMatchResponse {
	resp := make([]speakerMatchResponse, len(matches))
	for i, m := range matches {
		resp[i] = speakerMatchResponse{
			Speaker:     m.Speaker,
			ContactID:   m.ContactID,
			ContactName: m.ContactName,
			Score:       m.Score,
			Tier:        string(m.Tier),
		}
	}
	return resp
}
