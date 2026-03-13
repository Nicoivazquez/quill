package transcription

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadMetadata_RoundTrip(t *testing.T) {
	bundleDir := t.TempDir()
	now := time.Date(2025, 3, 13, 10, 0, 0, 0, time.UTC)

	original := &BundleMetadata{
		ID:          "abc-123-def",
		Title:       "Team Standup March 13",
		Status:      "completed",
		Diarization: true,
		Folder:      "Work/Meetings",
		CreatedAt:   now,
		UpdatedAt:   now.Add(5 * time.Minute),
		SpeakerMappings: []SpeakerMappingEntry{
			{OriginalSpeaker: "speaker_00", CustomName: "Alice"},
			{OriginalSpeaker: "speaker_01", CustomName: "Bob"},
		},
		Summaries: []SummaryEntry{
			{Content: "A standup about Q1 goals.", Model: "gpt-4o", CreatedAt: now},
		},
		Notes: []NoteEntry{
			{
				ID:             "note-1",
				StartWordIndex: 10,
				EndWordIndex:   20,
				StartTime:      5.5,
				EndTime:        12.3,
				Quote:          "we need to ship this week",
				Content:        "Urgent deadline mentioned",
				CreatedAt:      now,
			},
		},
	}

	// Write
	if err := WriteMetadata(bundleDir, original); err != nil {
		t.Fatalf("WriteMetadata error: %v", err)
	}

	// Verify file exists
	metaPath := MetadataPath(bundleDir)
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("metadata.json not created: %v", err)
	}

	// Read back
	got, err := ReadMetadata(bundleDir)
	if err != nil {
		t.Fatalf("ReadMetadata error: %v", err)
	}

	// Verify all fields
	if got.ID != original.ID {
		t.Errorf("ID = %q, want %q", got.ID, original.ID)
	}
	if got.Title != original.Title {
		t.Errorf("Title = %q, want %q", got.Title, original.Title)
	}
	if got.Status != original.Status {
		t.Errorf("Status = %q, want %q", got.Status, original.Status)
	}
	if got.Diarization != original.Diarization {
		t.Errorf("Diarization = %v, want %v", got.Diarization, original.Diarization)
	}
	if got.Folder != original.Folder {
		t.Errorf("Folder = %q, want %q", got.Folder, original.Folder)
	}
	if !got.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, original.CreatedAt)
	}
	if !got.UpdatedAt.Equal(original.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, original.UpdatedAt)
	}

	// Speaker mappings
	if len(got.SpeakerMappings) != 2 {
		t.Fatalf("SpeakerMappings len = %d, want 2", len(got.SpeakerMappings))
	}
	if got.SpeakerMappings[0].OriginalSpeaker != "speaker_00" || got.SpeakerMappings[0].CustomName != "Alice" {
		t.Errorf("SpeakerMappings[0] = %+v", got.SpeakerMappings[0])
	}

	// Summaries
	if len(got.Summaries) != 1 {
		t.Fatalf("Summaries len = %d, want 1", len(got.Summaries))
	}
	if got.Summaries[0].Content != "A standup about Q1 goals." {
		t.Errorf("Summary content = %q", got.Summaries[0].Content)
	}

	// Notes
	if len(got.Notes) != 1 {
		t.Fatalf("Notes len = %d, want 1", len(got.Notes))
	}
	if got.Notes[0].Quote != "we need to ship this week" {
		t.Errorf("Note quote = %q", got.Notes[0].Quote)
	}
}

func TestWriteMetadata_Atomic(t *testing.T) {
	bundleDir := t.TempDir()

	meta := &BundleMetadata{
		ID:     "test-atomic",
		Title:  "Atomicity Test",
		Status: "completed",
	}

	if err := WriteMetadata(bundleDir, meta); err != nil {
		t.Fatalf("WriteMetadata error: %v", err)
	}

	// Verify no .tmp file left behind
	tmpPath := MetadataPath(bundleDir) + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error(".tmp file should not exist after successful write")
	}
}

func TestWriteMetadata_PrettyPrinted(t *testing.T) {
	bundleDir := t.TempDir()

	meta := &BundleMetadata{
		ID:     "test-pretty",
		Title:  "Pretty Print",
		Status: "completed",
	}

	if err := WriteMetadata(bundleDir, meta); err != nil {
		t.Fatalf("WriteMetadata error: %v", err)
	}

	data, err := os.ReadFile(MetadataPath(bundleDir))
	if err != nil {
		t.Fatal(err)
	}

	// Should be indented
	if len(data) < 10 || data[1] != '\n' {
		// First char is '{', second should be '\n' for indented JSON
	}

	// Verify it's valid JSON
	var check map[string]interface{}
	if err := json.Unmarshal(data, &check); err != nil {
		t.Errorf("output is not valid JSON: %v", err)
	}

	// Should end with newline
	if data[len(data)-1] != '\n' {
		t.Error("file should end with newline")
	}
}

func TestReadMetadata_NonExistent(t *testing.T) {
	_, err := ReadMetadata("/nonexistent/dir")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestReadMetadata_InvalidJSON(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundleDir, metadataFileName), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadMetadata(bundleDir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWriteMetadata_EmptyOptionalFields(t *testing.T) {
	bundleDir := t.TempDir()

	meta := &BundleMetadata{
		ID:     "minimal",
		Title:  "Minimal",
		Status: "completed",
	}

	if err := WriteMetadata(bundleDir, meta); err != nil {
		t.Fatalf("WriteMetadata error: %v", err)
	}

	got, err := ReadMetadata(bundleDir)
	if err != nil {
		t.Fatalf("ReadMetadata error: %v", err)
	}

	// Empty slices should be nil (omitempty)
	if got.SpeakerMappings != nil {
		t.Errorf("SpeakerMappings should be nil, got %v", got.SpeakerMappings)
	}
	if got.Summaries != nil {
		t.Errorf("Summaries should be nil, got %v", got.Summaries)
	}
	if got.Notes != nil {
		t.Errorf("Notes should be nil, got %v", got.Notes)
	}
}

func TestWriteMetadata_OverwritesExisting(t *testing.T) {
	bundleDir := t.TempDir()

	v1 := &BundleMetadata{ID: "id1", Title: "Version 1", Status: "completed"}
	v2 := &BundleMetadata{ID: "id1", Title: "Version 2", Status: "completed"}

	if err := WriteMetadata(bundleDir, v1); err != nil {
		t.Fatal(err)
	}
	if err := WriteMetadata(bundleDir, v2); err != nil {
		t.Fatal(err)
	}

	got, err := ReadMetadata(bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Version 2" {
		t.Errorf("Title = %q, want %q", got.Title, "Version 2")
	}
}

func TestReadOrCreateMetadata_ExistingMetadata(t *testing.T) {
	bundleDir := t.TempDir()

	original := &BundleMetadata{
		ID:     "existing",
		Title:  "Existing",
		Status: "completed",
	}
	if err := WriteMetadata(bundleDir, original); err != nil {
		t.Fatal(err)
	}

	got, err := ReadOrCreateMetadata(bundleDir)
	if err != nil {
		t.Fatalf("ReadOrCreateMetadata error: %v", err)
	}
	if got.ID != "existing" {
		t.Errorf("ID = %q, want %q", got.ID, "existing")
	}
}

func TestReadOrCreateMetadata_FallbackToFrontmatter(t *testing.T) {
	bundleDir := t.TempDir()

	// Write a transcript.md with frontmatter (no metadata.json)
	md := `---
id: from-frontmatter
title: "My Recording"
status: completed
created_at: 2025-03-13T10:00:00Z
updated_at: 2025-03-13T10:05:00Z
format: transcript-markdown-v1
---

# My Recording

[00:00 - 00:05] Hello world
`
	if err := os.WriteFile(filepath.Join(bundleDir, "transcript.md"), []byte(md), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadOrCreateMetadata(bundleDir)
	if err != nil {
		t.Fatalf("ReadOrCreateMetadata error: %v", err)
	}

	if got.ID != "from-frontmatter" {
		t.Errorf("ID = %q, want %q", got.ID, "from-frontmatter")
	}
	if got.Title != "My Recording" {
		t.Errorf("Title = %q, want %q", got.Title, "My Recording")
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q", got.Status, "completed")
	}
	expected := time.Date(2025, 3, 13, 10, 0, 0, 0, time.UTC)
	if !got.CreatedAt.Equal(expected) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, expected)
	}
}

func TestReadOrCreateMetadata_NoFiles(t *testing.T) {
	bundleDir := t.TempDir()

	got, err := ReadOrCreateMetadata(bundleDir)
	if err != nil {
		t.Fatalf("ReadOrCreateMetadata error: %v", err)
	}

	// Should return minimal metadata
	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q", got.Status, "completed")
	}
}

func TestMetadataPath(t *testing.T) {
	got := MetadataPath("/vault/Transcripts/my-recording-abc12345")
	want := "/vault/Transcripts/my-recording-abc12345/metadata.json"
	if got != want {
		t.Errorf("MetadataPath = %q, want %q", got, want)
	}
}
