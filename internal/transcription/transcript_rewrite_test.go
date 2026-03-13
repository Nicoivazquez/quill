package transcription

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quill/internal/models"
)

// ---------- helpers ----------

func writeTempJSON(t *testing.T, dir string, content map[string]interface{}) string {
	t.Helper()
	path := filepath.Join(dir, "transcript.json")
	b, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		t.Fatalf("writeTempJSON: marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatalf("writeTempJSON: write: %v", err)
	}
	return path
}

func writeTempMD(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "transcript.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeTempMD: write: %v", err)
	}
	return path
}

func makeTestJob(jsonPath, mdPath string) *models.TranscriptionJob {
	title := "Test Recording"
	return &models.TranscriptionJob{
		ID:                     "test-job-id",
		Title:                  &title,
		Status:                 models.StatusCompleted,
		AudioPath:              "/tmp/audio.wav",
		TranscriptJSONPath:     &jsonPath,
		TranscriptMarkdownPath: &mdPath,
		CreatedAt:              time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC),
		UpdatedAt:              time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC),
	}
}

func sampleJSONPayload(segments []map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"text":       "hello world",
		"confidence": 0.95,
		"segments":   segments,
	}
}

func readJSONFile(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readJSONFile: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("readJSONFile: unmarshal: %v", err)
	}
	return out
}

func segmentsFromJSON(t *testing.T, data map[string]interface{}) []interface{} {
	t.Helper()
	raw, ok := data["segments"]
	if !ok {
		t.Fatal("no segments in JSON")
	}
	segs, ok := raw.([]interface{})
	if !ok {
		t.Fatal("segments is not a slice")
	}
	return segs
}

func segmentField(t *testing.T, seg interface{}, field string) string {
	t.Helper()
	m, ok := seg.(map[string]interface{})
	if !ok {
		t.Fatalf("segment is not a map")
	}
	v, exists := m[field]
	if !exists {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

func segmentHasField(seg interface{}, field string) bool {
	m, ok := seg.(map[string]interface{})
	if !ok {
		return false
	}
	_, exists := m[field]
	return exists
}

// ---------- RewriteTranscriptJSON tests ----------

// TestRewriteTranscriptJSON_AppliesMappings verifies that speaker_name fields are
// added to segments that have a matching mapping.
func TestRewriteTranscriptJSON_AppliesMappings(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
		{"start": 5.0, "end": 10.0, "text": "world", "speaker": "speaker_01"},
	}))

	mappings := map[string]string{
		"speaker_00": "Nico",
		"speaker_01": "Alice",
	}

	if err := RewriteTranscriptJSON(jsonPath, mappings); err != nil {
		t.Fatalf("RewriteTranscriptJSON returned error: %v", err)
	}

	data := readJSONFile(t, jsonPath)
	segs := segmentsFromJSON(t, data)

	if got := segmentField(t, segs[0], "speaker_name"); got != "Nico" {
		t.Errorf("segment 0 speaker_name: want %q, got %q", "Nico", got)
	}
	if got := segmentField(t, segs[1], "speaker_name"); got != "Alice" {
		t.Errorf("segment 1 speaker_name: want %q, got %q", "Alice", got)
	}
}

// TestRewriteTranscriptJSON_PreservesOriginalSpeaker verifies that the "speaker"
// field is NOT modified; only "speaker_name" is added/updated.
func TestRewriteTranscriptJSON_PreservesOriginalSpeaker(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
	}))

	mappings := map[string]string{"speaker_00": "Nico"}

	if err := RewriteTranscriptJSON(jsonPath, mappings); err != nil {
		t.Fatalf("RewriteTranscriptJSON returned error: %v", err)
	}

	data := readJSONFile(t, jsonPath)
	segs := segmentsFromJSON(t, data)

	if got := segmentField(t, segs[0], "speaker"); got != "speaker_00" {
		t.Errorf("speaker field mutated: want %q, got %q", "speaker_00", got)
	}
	if got := segmentField(t, segs[0], "speaker_name"); got != "Nico" {
		t.Errorf("speaker_name: want %q, got %q", "Nico", got)
	}
}

// TestRewriteTranscriptJSON_PartialMappings verifies that segments without a
// mapping entry are left without a speaker_name field.
func TestRewriteTranscriptJSON_PartialMappings(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
		{"start": 5.0, "end": 10.0, "text": "world", "speaker": "speaker_01"},
	}))

	// Only map speaker_00
	mappings := map[string]string{"speaker_00": "Nico"}

	if err := RewriteTranscriptJSON(jsonPath, mappings); err != nil {
		t.Fatalf("RewriteTranscriptJSON returned error: %v", err)
	}

	data := readJSONFile(t, jsonPath)
	segs := segmentsFromJSON(t, data)

	if got := segmentField(t, segs[0], "speaker_name"); got != "Nico" {
		t.Errorf("segment 0 speaker_name: want %q, got %q", "Nico", got)
	}
	if segmentHasField(segs[1], "speaker_name") {
		t.Errorf("segment 1 should NOT have speaker_name when no mapping exists")
	}
}

// TestRewriteTranscriptJSON_ReRename verifies that calling RewriteTranscriptJSON
// twice updates speaker_name to the latest value (re-rename support).
func TestRewriteTranscriptJSON_ReRename(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
	}))

	// First rename
	if err := RewriteTranscriptJSON(jsonPath, map[string]string{"speaker_00": "Nico"}); err != nil {
		t.Fatalf("first RewriteTranscriptJSON error: %v", err)
	}

	// Re-rename to a different name
	if err := RewriteTranscriptJSON(jsonPath, map[string]string{"speaker_00": "Nicolas"}); err != nil {
		t.Fatalf("second RewriteTranscriptJSON error: %v", err)
	}

	data := readJSONFile(t, jsonPath)
	segs := segmentsFromJSON(t, data)

	if got := segmentField(t, segs[0], "speaker"); got != "speaker_00" {
		t.Errorf("speaker field mutated after re-rename: want %q, got %q", "speaker_00", got)
	}
	if got := segmentField(t, segs[0], "speaker_name"); got != "Nicolas" {
		t.Errorf("speaker_name after re-rename: want %q, got %q", "Nicolas", got)
	}
}

// TestRewriteTranscriptJSON_MissingFile verifies that a non-existent file
// returns an error.
func TestRewriteTranscriptJSON_MissingFile(t *testing.T) {
	err := RewriteTranscriptJSON("/nonexistent/path/transcript.json", map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// TestRewriteTranscriptJSON_InvalidJSON verifies that malformed JSON returns an error.
func TestRewriteTranscriptJSON_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not valid json {{{"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := RewriteTranscriptJSON(path, map[string]string{"speaker_00": "Nico"})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// TestRewriteTranscriptJSON_EmptyMappings verifies that an empty mappings map
// is a no-op and the file remains valid JSON.
func TestRewriteTranscriptJSON_EmptyMappings(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
	}))

	if err := RewriteTranscriptJSON(jsonPath, map[string]string{}); err != nil {
		t.Fatalf("unexpected error for empty mappings: %v", err)
	}

	data := readJSONFile(t, jsonPath)
	segs := segmentsFromJSON(t, data)

	if segmentHasField(segs[0], "speaker_name") {
		t.Error("segment should NOT have speaker_name when mappings are empty")
	}
}

// TestRewriteTranscriptJSON_NoSpeakerField verifies that segments without a
// "speaker" field are left untouched.
func TestRewriteTranscriptJSON_NoSpeakerField(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello"},
	}))

	if err := RewriteTranscriptJSON(jsonPath, map[string]string{"speaker_00": "Nico"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := readJSONFile(t, jsonPath)
	segs := segmentsFromJSON(t, data)

	if segmentHasField(segs[0], "speaker_name") {
		t.Error("segment without speaker field should NOT gain speaker_name")
	}
}

// ---------- RewriteTranscriptMarkdown tests ----------

// TestRewriteTranscriptMarkdown_WithSpeakerNames verifies that the regenerated
// markdown uses display names (speaker_name) rather than the original speaker keys.
func TestRewriteTranscriptMarkdown_WithSpeakerNames(t *testing.T) {
	dir := t.TempDir()
	// Write JSON that already has speaker_name set (post-JSON-rewrite state)
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00", "speaker_name": "Nico"},
		{"start": 5.0, "end": 10.0, "text": "world", "speaker": "speaker_01", "speaker_name": "Alice"},
	}))
	mdPath := writeTempMD(t, dir, "old content")

	job := makeTestJob(jsonPath, mdPath)
	mappings := map[string]string{
		"speaker_00": "Nico",
		"speaker_01": "Alice",
	}

	if err := RewriteTranscriptMarkdown(mdPath, jsonPath, job, mappings); err != nil {
		t.Fatalf("RewriteTranscriptMarkdown error: %v", err)
	}

	content, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	md := string(content)

	if !strings.Contains(md, "Nico:") {
		t.Errorf("expected markdown to contain %q; got:\n%s", "Nico:", md)
	}
	if !strings.Contains(md, "Alice:") {
		t.Errorf("expected markdown to contain %q; got:\n%s", "Alice:", md)
	}
	if strings.Contains(md, "speaker_00") {
		t.Errorf("markdown should NOT contain raw speaker key %q; got:\n%s", "speaker_00", md)
	}
}

// TestRewriteTranscriptMarkdown_FallsBackToOriginal verifies that when no
// speaker_name is present, the "speaker" field value is used.
func TestRewriteTranscriptMarkdown_FallsBackToOriginal(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
	}))
	mdPath := writeTempMD(t, dir, "old content")

	job := makeTestJob(jsonPath, mdPath)
	mappings := map[string]string{}

	if err := RewriteTranscriptMarkdown(mdPath, jsonPath, job, mappings); err != nil {
		t.Fatalf("RewriteTranscriptMarkdown error: %v", err)
	}

	content, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	md := string(content)

	if !strings.Contains(md, "speaker_00:") {
		t.Errorf("expected markdown to fall back to %q; got:\n%s", "speaker_00:", md)
	}
}

// TestRewriteTranscriptMarkdown_MissingJSONFile returns an error when the JSON
// file does not exist.
func TestRewriteTranscriptMarkdown_MissingJSONFile(t *testing.T) {
	dir := t.TempDir()
	mdPath := writeTempMD(t, dir, "old content")
	job := makeTestJob("/nonexistent/transcript.json", mdPath)

	err := RewriteTranscriptMarkdown(mdPath, "/nonexistent/transcript.json", job, nil)
	if err == nil {
		t.Fatal("expected error for missing JSON file, got nil")
	}
}

// TestRewriteTranscriptMarkdown_ContainsFrontmatter verifies that the regenerated
// markdown includes the YAML front-matter block.
func TestRewriteTranscriptMarkdown_ContainsFrontmatter(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
	}))
	mdPath := writeTempMD(t, dir, "old content")
	job := makeTestJob(jsonPath, mdPath)

	if err := RewriteTranscriptMarkdown(mdPath, jsonPath, job, map[string]string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(mdPath)
	md := string(content)

	if !strings.HasPrefix(md, "---\n") {
		t.Errorf("expected markdown to start with YAML frontmatter '---'; got:\n%.200s", md)
	}
	if !strings.Contains(md, "format: transcript-markdown-v1") {
		t.Errorf("expected frontmatter to contain format field; got:\n%.200s", md)
	}
}

// ---------- RewriteTranscriptFiles integration test ----------

// TestRewriteTranscriptFiles_Integration runs the orchestrator end-to-end:
// verifies both JSON speaker_name fields and markdown display names are applied.
func TestRewriteTranscriptFiles_Integration(t *testing.T) {
	dir := t.TempDir()

	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
		{"start": 5.0, "end": 10.0, "text": "world", "speaker": "speaker_01"},
	}))
	mdPath := writeTempMD(t, dir, "old content")

	job := makeTestJob(jsonPath, mdPath)
	speakerMappings := []models.SpeakerMapping{
		{TranscriptionJobID: "test-job-id", OriginalSpeaker: "speaker_00", CustomName: "Nico"},
		{TranscriptionJobID: "test-job-id", OriginalSpeaker: "speaker_01", CustomName: "Alice"},
	}

	if err := RewriteTranscriptFiles(job, speakerMappings); err != nil {
		t.Fatalf("RewriteTranscriptFiles error: %v", err)
	}

	// Verify JSON
	data := readJSONFile(t, jsonPath)
	segs := segmentsFromJSON(t, data)

	if got := segmentField(t, segs[0], "speaker"); got != "speaker_00" {
		t.Errorf("JSON speaker mutated: want %q, got %q", "speaker_00", got)
	}
	if got := segmentField(t, segs[0], "speaker_name"); got != "Nico" {
		t.Errorf("JSON speaker_name[0]: want %q, got %q", "Nico", got)
	}
	if got := segmentField(t, segs[1], "speaker_name"); got != "Alice" {
		t.Errorf("JSON speaker_name[1]: want %q, got %q", "Alice", got)
	}

	// Verify Markdown
	content, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	md := string(content)

	if !strings.Contains(md, "Nico:") {
		t.Errorf("markdown missing %q; got:\n%s", "Nico:", md)
	}
	if !strings.Contains(md, "Alice:") {
		t.Errorf("markdown missing %q; got:\n%s", "Alice:", md)
	}
	if strings.Contains(md, "speaker_00") {
		t.Errorf("markdown should not expose raw key %q; got:\n%s", "speaker_00", md)
	}
}

// TestRewriteTranscriptFiles_NilJSONPath returns error when TranscriptJSONPath is nil.
func TestRewriteTranscriptFiles_NilJSONPath(t *testing.T) {
	job := &models.TranscriptionJob{ID: "x", AudioPath: "/tmp/a.wav"}
	// Both paths nil
	err := RewriteTranscriptFiles(job, []models.SpeakerMapping{
		{OriginalSpeaker: "speaker_00", CustomName: "Nico"},
	})
	if err == nil {
		t.Fatal("expected error when TranscriptJSONPath is nil")
	}
}

// TestRewriteTranscriptFiles_NilMarkdownPath returns an error when the
// markdown path is nil (JSON exists but MD missing).
func TestRewriteTranscriptFiles_NilMarkdownPath(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
	}))
	job := &models.TranscriptionJob{
		ID:                 "x",
		AudioPath:          "/tmp/a.wav",
		TranscriptJSONPath: &jsonPath,
		// TranscriptMarkdownPath intentionally nil
	}
	err := RewriteTranscriptFiles(job, []models.SpeakerMapping{
		{OriginalSpeaker: "speaker_00", CustomName: "Nico"},
	})
	if err == nil {
		t.Fatal("expected error when TranscriptMarkdownPath is nil")
	}
}

// TestRewriteTranscriptFiles_EmptyMappings is a no-op: both files are still
// written (JSON unchanged, MD regenerated without display names).
func TestRewriteTranscriptFiles_EmptyMappings(t *testing.T) {
	dir := t.TempDir()
	jsonPath := writeTempJSON(t, dir, sampleJSONPayload([]map[string]interface{}{
		{"start": 0.0, "end": 5.0, "text": "hello", "speaker": "speaker_00"},
	}))
	mdPath := writeTempMD(t, dir, "old content")
	job := makeTestJob(jsonPath, mdPath)

	if err := RewriteTranscriptFiles(job, nil); err != nil {
		t.Fatalf("unexpected error with empty mappings: %v", err)
	}

	// JSON must still be valid
	readJSONFile(t, jsonPath)

	// Markdown must have been regenerated (not old content)
	content, _ := os.ReadFile(mdPath)
	if string(content) == "old content" {
		t.Error("markdown should be regenerated even with empty mappings")
	}
}
