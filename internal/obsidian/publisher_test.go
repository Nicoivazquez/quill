package obsidian

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- InjectQuillID tests ---

func TestInjectQuillID_AddsToExistingFrontmatter(t *testing.T) {
	md := "---\nid: abc123\ntitle: \"My Transcript\"\nstatus: completed\n---\n\n# My Transcript\n"
	result := InjectQuillID(md, "abc123")

	if !strings.Contains(result, "quill-id: abc123") {
		t.Errorf("expected quill-id in frontmatter, got:\n%s", result)
	}
	// Should still contain original fields
	if !strings.Contains(result, "title: \"My Transcript\"") {
		t.Errorf("original frontmatter fields should be preserved")
	}
}

func TestInjectQuillID_DoesNotDuplicateExisting(t *testing.T) {
	md := "---\nquill-id: abc123\ntitle: \"My Transcript\"\n---\n\n# My Transcript\n"
	result := InjectQuillID(md, "abc123")

	count := strings.Count(result, "quill-id:")
	if count != 1 {
		t.Errorf("expected exactly 1 quill-id, got %d in:\n%s", count, result)
	}
}

func TestInjectQuillID_UpdatesExistingQuillID(t *testing.T) {
	md := "---\nquill-id: old-id\ntitle: \"My Transcript\"\n---\n\n# My Transcript\n"
	result := InjectQuillID(md, "new-id")

	if !strings.Contains(result, "quill-id: new-id") {
		t.Errorf("expected updated quill-id, got:\n%s", result)
	}
	if strings.Contains(result, "quill-id: old-id") {
		t.Errorf("old quill-id should be replaced")
	}
}

func TestInjectQuillID_NoFrontmatter(t *testing.T) {
	md := "# No Frontmatter\n\nJust content.\n"
	result := InjectQuillID(md, "abc123")

	if !strings.Contains(result, "quill-id: abc123") {
		t.Errorf("expected quill-id prepended, got:\n%s", result)
	}
	// Body should still be present
	if !strings.Contains(result, "# No Frontmatter") {
		t.Errorf("original content should be preserved")
	}
}

// --- FindExistingByQuillID tests ---

func TestFindExistingByQuillID_FoundInSubdir(t *testing.T) {
	dir := t.TempDir()
	quillDir := filepath.Join(dir, "Quill")
	if err := os.MkdirAll(quillDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := "---\nquill-id: target-id\ntitle: \"Found Me\"\n---\n\n# Found Me\n"
	if err := os.WriteFile(filepath.Join(quillDir, "found-me-target-i.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Decoy file with different quill-id
	decoy := "---\nquill-id: other-id\ntitle: \"Not Me\"\n---\n\n# Not Me\n"
	if err := os.WriteFile(filepath.Join(quillDir, "not-me-other-id.md"), []byte(decoy), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewPublisher(dir)
	found, err := p.FindExistingByQuillID("target-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found == "" {
		t.Fatal("expected to find file with quill-id: target-id")
	}
	if !strings.HasSuffix(found, "found-me-target-i.md") {
		t.Errorf("expected found-me file, got: %s", found)
	}
}

func TestFindExistingByQuillID_NotFound(t *testing.T) {
	dir := t.TempDir()
	quillDir := filepath.Join(dir, "Quill")
	if err := os.MkdirAll(quillDir, 0755); err != nil {
		t.Fatal(err)
	}

	p := NewPublisher(dir)
	found, err := p.FindExistingByQuillID("nonexistent-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found != "" {
		t.Errorf("expected empty string for not-found, got: %s", found)
	}
}

func TestFindExistingByQuillID_EmptyVault(t *testing.T) {
	dir := t.TempDir()
	// No Quill subdirectory at all

	p := NewPublisher(dir)
	found, err := p.FindExistingByQuillID("any-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found != "" {
		t.Errorf("expected empty string when Quill dir doesn't exist, got: %s", found)
	}
}

// --- PublishTranscript tests ---

func TestPublishTranscript_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	p := NewPublisher(dir)

	md := "---\nid: job-1\ntitle: \"Test Transcript\"\nstatus: completed\n---\n\n# Test Transcript\n\nContent here.\n"
	path, err := p.PublishTranscript(md, "job-1", "Test Transcript")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be under Quill subdirectory
	if !strings.Contains(path, filepath.Join(dir, "Quill")) {
		t.Errorf("expected path under Quill dir, got: %s", path)
	}

	// File should exist and contain quill-id
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read published file: %v", err)
	}
	if !strings.Contains(string(content), "quill-id: job-1") {
		t.Errorf("published file should contain quill-id, got:\n%s", string(content))
	}
}

func TestPublishTranscript_UpdatesExistingFile(t *testing.T) {
	dir := t.TempDir()
	quillDir := filepath.Join(dir, "Quill")
	if err := os.MkdirAll(quillDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Pre-existing file with quill-id
	existing := "---\nquill-id: job-1\ntitle: \"Old Title\"\n---\n\n# Old Title\n\nOld content.\n"
	existingPath := filepath.Join(quillDir, "old-title-job-1.md")
	if err := os.WriteFile(existingPath, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewPublisher(dir)
	newMd := "---\nid: job-1\ntitle: \"New Title\"\nstatus: completed\n---\n\n# New Title\n\nUpdated content.\n"
	path, err := p.PublishTranscript(newMd, "job-1", "New Title")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should overwrite the existing file (deterministic by quill-id)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read published file: %v", err)
	}
	if !strings.Contains(string(content), "Updated content.") {
		t.Errorf("expected updated content, got:\n%s", string(content))
	}

	// Old file should be cleaned up if path changed
	if path != existingPath {
		if _, err := os.Stat(existingPath); err == nil {
			t.Error("old file should be removed when title changes the filename")
		}
	}
}

func TestPublishTranscript_DeterministicFilename(t *testing.T) {
	dir := t.TempDir()
	p := NewPublisher(dir)

	md := "---\nid: abc\ntitle: \"Hello World\"\n---\n\n# Hello World\n"
	path1, _ := p.PublishTranscript(md, "abc", "Hello World")
	path2, _ := p.PublishTranscript(md, "abc", "Hello World")

	if path1 != path2 {
		t.Errorf("same job+title should produce same path, got %s and %s", path1, path2)
	}
}

// --- BulkPublish tests ---

func TestBulkPublish_MultipleJobs(t *testing.T) {
	dir := t.TempDir()
	p := NewPublisher(dir)

	jobs := []PublishableJob{
		{
			JobID:    "job-1",
			Title:    "First Transcript",
			Markdown: "---\nid: job-1\ntitle: \"First Transcript\"\n---\n\n# First\n",
		},
		{
			JobID:    "job-2",
			Title:    "Second Transcript",
			Markdown: "---\nid: job-2\ntitle: \"Second Transcript\"\n---\n\n# Second\n",
		},
	}

	results, err := p.BulkPublish(jobs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for _, r := range results {
		if r.Error != nil {
			t.Errorf("job %s failed: %v", r.JobID, r.Error)
		}
		if r.Path == "" {
			t.Errorf("job %s has empty path", r.JobID)
		}
	}

	// Verify files on disk
	quillDir := filepath.Join(dir, "Quill")
	entries, _ := os.ReadDir(quillDir)
	if len(entries) != 2 {
		t.Errorf("expected 2 files in Quill dir, got %d", len(entries))
	}
}

func TestBulkPublish_EmptySlice(t *testing.T) {
	dir := t.TempDir()
	p := NewPublisher(dir)

	results, err := p.BulkPublish(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestBulkPublish_PartialFailure(t *testing.T) {
	dir := t.TempDir()
	p := NewPublisher(dir)

	jobs := []PublishableJob{
		{
			JobID:    "good-job",
			Title:    "Good Transcript",
			Markdown: "---\nid: good-job\ntitle: \"Good\"\n---\n\n# Good\n",
		},
		{
			JobID:    "bad-job",
			Title:    "Bad Transcript",
			Markdown: "", // Empty markdown should produce an error
		},
	}

	results, err := p.BulkPublish(jobs)
	if err != nil {
		t.Fatalf("bulk publish should not return top-level error for partial failure: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Error != nil {
		t.Errorf("good job should succeed, got: %v", results[0].Error)
	}
	if results[1].Error == nil {
		t.Error("bad job with empty markdown should fail")
	}
}
