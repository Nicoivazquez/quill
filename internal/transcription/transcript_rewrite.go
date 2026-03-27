package transcription

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"quill/internal/models"
)

// RewriteTranscriptJSON reads the transcript JSON at jsonPath, adds or updates
// the "speaker_name" field in each segment based on the provided mappings
// (key = original_speaker, value = custom display name), and writes back
// atomically using a temp-file rename.
//
// The "speaker" field is never modified; it remains the stable original key
// (e.g. "speaker_00"). If a segment has no "speaker" field, or the speaker
// value has no entry in mappings, no "speaker_name" field is set/updated.
func RewriteTranscriptJSON(jsonPath string, mappings map[string]string) error {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("transcript_rewrite: read %s: %w", jsonPath, err)
	}

	// Unmarshal into a generic structure so we can preserve all existing fields.
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("transcript_rewrite: parse %s: %w", jsonPath, err)
	}

	// Update segments in-place.
	if rawSegs, ok := payload["segments"]; ok {
		if segs, ok := rawSegs.([]interface{}); ok {
			for _, rawSeg := range segs {
				seg, ok := rawSeg.(map[string]interface{})
				if !ok {
					continue
				}
				speakerVal, hasSpeaker := seg["speaker"]
				if !hasSpeaker {
					continue
				}
				speakerStr, ok := speakerVal.(string)
				if !ok {
					continue
				}
				if displayName, mapped := mappings[speakerStr]; mapped {
					seg["speaker_name"] = displayName
				}
				// If no mapping exists we intentionally leave speaker_name absent.
			}
		}
	}

	// Marshal with pretty-printing.
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("transcript_rewrite: marshal %s: %w", jsonPath, err)
	}

	// Atomic write: write to a sibling temp file, then rename.
	dir := filepath.Dir(jsonPath)
	tmp, err := os.CreateTemp(dir, ".transcript_rewrite_*.json.tmp")
	if err != nil {
		return fmt.Errorf("transcript_rewrite: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Clean up temp file on any error path (rename will have removed it on success).
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("transcript_rewrite: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("transcript_rewrite: close temp file: %w", err)
	}

	if err := os.Rename(tmpName, jsonPath); err != nil {
		return fmt.Errorf("transcript_rewrite: rename temp to %s: %w", jsonPath, err)
	}

	return nil
}

// transcriptSegmentForRewrite is the segment representation used when
// reading JSON to regenerate the markdown file.
type transcriptSegmentForRewrite struct {
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	Text        string   `json:"text"`
	Speaker     *string  `json:"speaker,omitempty"`
	SpeakerName *string  `json:"speaker_name,omitempty"`
}

// transcriptPayloadForRewrite is the top-level JSON structure used when
// regenerating the markdown file.
type transcriptPayloadForRewrite struct {
	Text     string                        `json:"text"`
	Segments []transcriptSegmentForRewrite `json:"segments,omitempty"`
}

// RewriteTranscriptMarkdown regenerates the markdown transcript file at mdPath
// by reading the JSON at jsonPath and applying the mappings (original_speaker →
// custom_name). Display name resolution order:
//  1. The "speaker_name" field already set in the JSON (written by RewriteTranscriptJSON).
//  2. The mappings parameter (allows callers to pass the latest mappings before
//     the JSON file is updated).
//  3. The raw "speaker" field as fallback.
func RewriteTranscriptMarkdown(mdPath string, jsonPath string, job *models.TranscriptionJob, mappings map[string]string) error {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("transcript_rewrite: read json %s: %w", jsonPath, err)
	}

	var payload transcriptPayloadForRewrite
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("transcript_rewrite: parse json %s: %w", jsonPath, err)
	}

	// Convert to the markdownTranscriptPayload used by renderMarkdownTranscript,
	// applying display-name resolution.
	mdPayload := &markdownTranscriptPayload{
		Text: payload.Text,
	}
	for _, seg := range payload.Segments {
		displayName := resolveDisplayName(seg.Speaker, seg.SpeakerName, mappings)
		mdSeg := markdownTranscriptSegment{
			Start:   seg.Start,
			End:     seg.End,
			Text:    seg.Text,
			Speaker: displayName,
		}
		mdPayload.Segments = append(mdPayload.Segments, mdSeg)
	}

	markdown := renderMarkdownTranscript(job, mdPayload, nil)

	// Atomic write.
	dir := filepath.Dir(mdPath)
	tmp, err := os.CreateTemp(dir, ".transcript_rewrite_*.md.tmp")
	if err != nil {
		return fmt.Errorf("transcript_rewrite: create temp md file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.WriteString(markdown); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("transcript_rewrite: write temp md file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("transcript_rewrite: close temp md file: %w", err)
	}
	if err := os.Rename(tmpName, mdPath); err != nil {
		return fmt.Errorf("transcript_rewrite: rename temp md to %s: %w", mdPath, err)
	}

	return nil
}

// resolveDisplayName picks the best display name for a segment speaker.
// Priority: speaker_name field > mappings lookup > raw speaker key.
func resolveDisplayName(speaker *string, speakerName *string, mappings map[string]string) *string {
	// Use existing speaker_name if present and non-empty.
	if speakerName != nil && strings.TrimSpace(*speakerName) != "" {
		return speakerName
	}
	// Try mappings.
	if speaker != nil {
		if display, ok := mappings[*speaker]; ok && display != "" {
			return &display
		}
		// Fallback to raw speaker key.
		return speaker
	}
	return nil
}

// RewriteTranscriptFiles is the orchestrator that rewrites both the JSON and
// the markdown transcript files for a job after speaker mappings have been
// updated.
//
// It requires that both TranscriptJSONPath and TranscriptMarkdownPath are set
// on the job; if either is nil an error is returned.
func RewriteTranscriptFiles(job *models.TranscriptionJob, speakerMappings []models.SpeakerMapping) error {
	if job.TranscriptJSONPath == nil {
		return fmt.Errorf("transcript_rewrite: TranscriptJSONPath is nil for job %s", job.ID)
	}
	if job.TranscriptMarkdownPath == nil {
		return fmt.Errorf("transcript_rewrite: TranscriptMarkdownPath is nil for job %s", job.ID)
	}

	// Build the lookup map.
	mappings := make(map[string]string, len(speakerMappings))
	for _, m := range speakerMappings {
		mappings[m.OriginalSpeaker] = m.CustomName
	}

	// Stamp UpdatedAt so the markdown front-matter stays current.
	job.UpdatedAt = time.Now()

	if err := RewriteTranscriptJSON(*job.TranscriptJSONPath, mappings); err != nil {
		return err
	}

	if err := RewriteTranscriptMarkdown(
		*job.TranscriptMarkdownPath,
		*job.TranscriptJSONPath,
		job,
		mappings,
	); err != nil {
		return err
	}

	return nil
}
