package transcription

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"quill/internal/models"
)

const metadataFileName = "metadata.json"

// BundleMetadata contains all metadata needed to reconstruct a DB record from disk.
// This file lives alongside transcript.json, transcript.md, and audio.* in each bundle.
type BundleMetadata struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Status           string    `json:"status"`
	Diarization      bool      `json:"diarization"`
	IsMultiTrack     bool      `json:"is_multi_track,omitempty"`
	Folder           string    `json:"folder,omitempty"`
	OriginalFilename string    `json:"original_filename,omitempty"`
	FileHash         string    `json:"file_hash,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	SpeakerMappings []SpeakerMappingEntry `json:"speaker_mappings,omitempty"`
	Summaries       []SummaryEntry        `json:"summaries,omitempty"`
	Notes           []NoteEntry           `json:"notes,omitempty"`
}

// SpeakerMappingEntry represents a speaker mapping in the metadata sidecar.
type SpeakerMappingEntry struct {
	OriginalSpeaker string `json:"original_speaker"`
	CustomName      string `json:"custom_name"`
}

// SummaryEntry represents a summary in the metadata sidecar.
type SummaryEntry struct {
	Content    string    `json:"content"`
	Model      string    `json:"model,omitempty"`
	TemplateID string    `json:"template_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// NoteEntry represents a note/annotation in the metadata sidecar.
type NoteEntry struct {
	ID             string    `json:"id"`
	StartWordIndex int       `json:"start_word_index"`
	EndWordIndex   int       `json:"end_word_index"`
	StartTime      float64   `json:"start_time"`
	EndTime        float64   `json:"end_time"`
	Quote          string    `json:"quote"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

// MetadataPath returns the path to metadata.json in a bundle directory.
func MetadataPath(bundleDir string) string {
	return filepath.Join(bundleDir, metadataFileName)
}

// ReadMetadata reads and parses the metadata.json sidecar from a bundle directory.
func ReadMetadata(bundleDir string) (*BundleMetadata, error) {
	data, err := os.ReadFile(MetadataPath(bundleDir))
	if err != nil {
		return nil, fmt.Errorf("reading metadata: %w", err)
	}

	var meta BundleMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing metadata: %w", err)
	}

	return &meta, nil
}

// WriteMetadata atomically writes metadata.json to a bundle directory.
// Uses temp-file + rename for atomic writes.
func WriteMetadata(bundleDir string, meta *BundleMetadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling metadata: %w", err)
	}
	data = append(data, '\n')

	target := MetadataPath(bundleDir)
	tmpPath := target + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("writing temp metadata: %w", err)
	}

	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming metadata: %w", err)
	}

	return nil
}

// ReadOrCreateMetadata reads existing metadata or creates a minimal one from available info.
// This handles legacy bundles that predate the metadata sidecar.
func ReadOrCreateMetadata(bundleDir string) (*BundleMetadata, error) {
	meta, err := ReadMetadata(bundleDir)
	if err == nil {
		return meta, nil
	}

	// Metadata doesn't exist — create minimal from what we can derive
	meta = &BundleMetadata{
		Status:    "completed",
		UpdatedAt: time.Now(),
	}

	// Try to get info from transcript.md frontmatter
	mdPath := filepath.Join(bundleDir, "transcript.md")
	if mdData, readErr := os.ReadFile(mdPath); readErr == nil {
		parseFrontmatterIntoMetadata(mdData, meta)
	}

	return meta, nil
}

// parseFrontmatterIntoMetadata extracts id, title, status, created_at from markdown frontmatter.
func parseFrontmatterIntoMetadata(mdContent []byte, meta *BundleMetadata) {
	content := string(mdContent)
	if len(content) < 4 || content[:4] != "---\n" {
		return
	}

	endIdx := -1
	for i := 4; i < len(content)-3; i++ {
		if content[i:i+4] == "\n---" {
			endIdx = i + 1
			break
		}
	}
	if endIdx < 0 {
		return
	}

	frontmatter := content[4:endIdx]
	for _, line := range splitLines(frontmatter) {
		parts := splitFirst(line, ":")
		if len(parts) != 2 {
			continue
		}
		key := trimSpace(parts[0])
		val := trimSpace(parts[1])
		// Remove surrounding quotes
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}

		switch key {
		case "id":
			meta.ID = val
		case "title":
			meta.Title = val
		case "status":
			meta.Status = val
		case "created_at":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				meta.CreatedAt = t
			}
		case "updated_at":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				meta.UpdatedAt = t
			}
		}
	}
}

// splitLines splits text into lines.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// splitFirst splits on the first occurrence of sep.
func splitFirst(s, sep string) []string {
	idx := -1
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			idx = i
			break
		}
	}
	if idx < 0 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+len(sep):]}
}

// trimSpace trims whitespace from both ends.
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// BuildMetadataFromJob constructs a BundleMetadata from a TranscriptionJob
// and its related data (speaker mappings, summaries, notes).
func BuildMetadataFromJob(
	job *models.TranscriptionJob,
	mappings []models.SpeakerMapping,
	summaries []models.Summary,
	notes []models.Note,
) *BundleMetadata {
	meta := &BundleMetadata{
		ID:               job.ID,
		Status:           string(job.Status),
		Diarization:      job.Diarization,
		IsMultiTrack:     job.IsMultiTrack,
		OriginalFilename: job.OriginalFilename,
		FileHash:         job.FileHash,
		CreatedAt:        job.CreatedAt,
		UpdatedAt:        job.UpdatedAt,
	}

	if job.Title != nil {
		meta.Title = *job.Title
	}
	if job.Folder != nil {
		meta.Folder = *job.Folder
	}

	for _, m := range mappings {
		meta.SpeakerMappings = append(meta.SpeakerMappings, SpeakerMappingEntry{
			OriginalSpeaker: m.OriginalSpeaker,
			CustomName:      m.CustomName,
		})
	}

	for _, s := range summaries {
		templateID := ""
		if s.TemplateID != nil {
			templateID = *s.TemplateID
		}
		meta.Summaries = append(meta.Summaries, SummaryEntry{
			Content:    s.Content,
			Model:      s.Model,
			TemplateID: templateID,
			CreatedAt:  s.CreatedAt,
		})
	}

	for _, n := range notes {
		meta.Notes = append(meta.Notes, NoteEntry{
			ID:             n.ID,
			StartWordIndex: n.StartWordIndex,
			EndWordIndex:   n.EndWordIndex,
			StartTime:      n.StartTime,
			EndTime:        n.EndTime,
			Quote:          n.Quote,
			Content:        n.Content,
			CreatedAt:      n.CreatedAt,
		})
	}

	return meta
}
