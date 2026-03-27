package api

import (
	"context"
	"fmt"
	"strings"

	"quill/internal/contacts"
	"quill/internal/models"
	"quill/pkg/logger"
)

// BuildRetroactiveSpeakerExtractor creates a production SpeakerEmbeddingExtractor
// that parses a job's transcript JSON, builds speaker clip windows, and runs
// FFmpeg + TitaNet to produce per-speaker embeddings.
func BuildRetroactiveSpeakerExtractor(whisperXEnv string) contacts.SpeakerEmbeddingExtractor {
	return func(ctx context.Context, job *models.TranscriptionJob, vaultPath string) (map[string][]float64, error) {
		if job.Transcript == nil || strings.TrimSpace(*job.Transcript) == "" {
			return nil, nil
		}

		transcript, err := parseTranscriptJSON(*job.Transcript)
		if err != nil {
			return nil, fmt.Errorf("parse transcript: %w", err)
		}
		if len(transcript.Segments) == 0 {
			return nil, nil
		}

		windowsBySpeaker := buildSpeakerClipWindows(transcript.Segments)
		if len(windowsBySpeaker) == 0 {
			return nil, nil
		}

		audioPath, err := resolveJobAudioPath(job, vaultPath)
		if err != nil {
			return nil, fmt.Errorf("resolve audio: %w", err)
		}

		contactWindows := make(map[string]contacts.ClipWindow, len(windowsBySpeaker))
		for speaker, w := range windowsBySpeaker {
			contactWindows[speaker] = contacts.ClipWindow{Start: w.Start, End: w.End}
		}

		embeddings, err := contacts.ExtractSpeakerEmbeddings(ctx, audioPath, contactWindows, whisperXEnv)
		if err != nil {
			return nil, fmt.Errorf("extract embeddings: %w", err)
		}

		logger.Debug("retroactive extractor: extracted embeddings",
			"job_id", job.ID,
			"speakers", len(embeddings),
		)
		return embeddings, nil
	}
}
