package adapters

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"

	"quill/internal/transcription/interfaces"
)

// ---------------------------------------------------------------------------
// floatPtr
// ---------------------------------------------------------------------------

func TestFloatPtr(t *testing.T) {
	v := floatPtr(3.14)
	if v == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *v != 3.14 {
		t.Errorf("expected 3.14, got %f", *v)
	}

	// Zero value
	z := floatPtr(0)
	if *z != 0 {
		t.Errorf("expected 0, got %f", *z)
	}

	// Negative
	n := floatPtr(-1.5)
	if *n != -1.5 {
		t.Errorf("expected -1.5, got %f", *n)
	}
}

// ---------------------------------------------------------------------------
// NewSherpaOnnxAdapter
// ---------------------------------------------------------------------------

func TestNewSherpaOnnxAdapter(t *testing.T) {
	adapter := NewSherpaOnnxAdapter("/tmp/models")

	t.Run("capabilities", func(t *testing.T) {
		caps := adapter.GetCapabilities()
		if caps.ModelID != "sherpa-onnx" {
			t.Errorf("expected model_id 'sherpa-onnx', got %q", caps.ModelID)
		}
		if caps.ModelFamily != "sherpa_onnx" {
			t.Errorf("expected model_family 'sherpa_onnx', got %q", caps.ModelFamily)
		}
		if caps.RequiresGPU {
			t.Error("should not require GPU")
		}
		if !caps.Features["no_python"] {
			t.Error("expected no_python feature")
		}
		if !caps.Features["no_token_required"] {
			t.Error("expected no_token_required feature")
		}
	})

	t.Run("parameter_schema", func(t *testing.T) {
		schema := adapter.GetParameterSchema()
		names := make(map[string]bool)
		for _, p := range schema {
			names[p.Name] = true
		}
		for _, expected := range []string{"num_speakers", "threshold", "num_threads", "min_speakers", "max_speakers", "auto_convert_audio"} {
			if !names[expected] {
				t.Errorf("missing parameter %q in schema", expected)
			}
		}
	})

	t.Run("models_path", func(t *testing.T) {
		if adapter.modelsPath != "/tmp/models" {
			t.Errorf("expected modelsPath '/tmp/models', got %q", adapter.modelsPath)
		}
	})
}

// ---------------------------------------------------------------------------
// GetMaxSpeakers / GetMinSpeakers
// ---------------------------------------------------------------------------

func TestSpeakerLimits(t *testing.T) {
	adapter := NewSherpaOnnxAdapter("/tmp/models")
	if adapter.GetMaxSpeakers() != 20 {
		t.Errorf("expected max_speakers=20, got %d", adapter.GetMaxSpeakers())
	}
	if adapter.GetMinSpeakers() != 1 {
		t.Errorf("expected min_speakers=1, got %d", adapter.GetMinSpeakers())
	}
}

// ---------------------------------------------------------------------------
// Model paths
// ---------------------------------------------------------------------------

func TestModelPaths(t *testing.T) {
	adapter := NewSherpaOnnxAdapter("/data/models")

	segPath := adapter.segmentationModelPath()
	if !strings.HasSuffix(segPath, filepath.Join("sherpa-onnx-pyannote-segmentation-3-0", "model.onnx")) {
		t.Errorf("unexpected segmentation path: %s", segPath)
	}
	if !strings.HasPrefix(segPath, "/data/models") {
		t.Errorf("segmentation path should start with models dir: %s", segPath)
	}

	embPath := adapter.embeddingModelPath()
	if !strings.HasSuffix(embPath, "wespeaker_en_voxceleb_CAM++.onnx") {
		t.Errorf("unexpected embedding path: %s (expected wespeaker_en_voxceleb_CAM++.onnx)", embPath)
	}
	if !strings.HasPrefix(embPath, "/data/models") {
		t.Errorf("embedding path should start with models dir: %s", embPath)
	}
}

// ---------------------------------------------------------------------------
// IsReady
// ---------------------------------------------------------------------------

func TestIsReady(t *testing.T) {
	t.Run("not_ready_when_no_files", func(t *testing.T) {
		adapter := NewSherpaOnnxAdapter("/nonexistent/path")
		if adapter.IsReady(context.Background()) {
			t.Error("should not be ready when model files don't exist")
		}
	})

	t.Run("not_ready_when_files_too_small", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create model directories/files that are too small (<1MB)
		segDir := filepath.Join(tmpDir, segmentationModelDir)
		if err := os.MkdirAll(segDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(segDir, segmentationModelFile), []byte("small"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, embeddingModelFile), []byte("small"), 0644); err != nil {
			t.Fatal(err)
		}

		adapter := NewSherpaOnnxAdapter(tmpDir)
		if adapter.IsReady(context.Background()) {
			t.Error("should not be ready when model files are too small")
		}
	})

	t.Run("ready_when_files_large_enough", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create model files that are >1MB
		segDir := filepath.Join(tmpDir, segmentationModelDir)
		if err := os.MkdirAll(segDir, 0755); err != nil {
			t.Fatal(err)
		}
		bigData := make([]byte, 1024*1024+1) // 1MB+1 byte
		if err := os.WriteFile(filepath.Join(segDir, segmentationModelFile), bigData, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, embeddingModelFile), bigData, 0644); err != nil {
			t.Fatal(err)
		}

		adapter := NewSherpaOnnxAdapter(tmpDir)
		if !adapter.IsReady(context.Background()) {
			t.Error("should be ready when both model files exist and are >1MB")
		}
	})
}

// ---------------------------------------------------------------------------
// resolveNumClusters
// ---------------------------------------------------------------------------

func TestResolveNumClusters(t *testing.T) {
	adapter := NewSherpaOnnxAdapter("/tmp/models")

	tests := []struct {
		name     string
		params   map[string]interface{}
		expected int
	}{
		{
			name:     "auto_detect_when_empty",
			params:   map[string]interface{}{},
			expected: 0,
		},
		{
			name:     "auto_detect_when_zero",
			params:   map[string]interface{}{"num_speakers": 0},
			expected: 0,
		},
		{
			name:     "explicit_num_speakers",
			params:   map[string]interface{}{"num_speakers": 3},
			expected: 3,
		},
		{
			name:     "min_speakers_alone_does_not_force_clusters",
			params:   map[string]interface{}{"min_speakers": 2},
			expected: 0, // min_speakers is a hint, not a hard constraint
		},
		{
			name:     "max_speakers_does_not_force_clusters",
			params:   map[string]interface{}{"max_speakers": 4},
			expected: 0, // max_speakers is post-processing only, not NumClusters
		},
		{
			name:     "num_speakers_takes_priority_over_min",
			params:   map[string]interface{}{"num_speakers": 5, "min_speakers": 2},
			expected: 5,
		},
		{
			name:     "float_value_converts",
			params:   map[string]interface{}{"num_speakers": 4.0},
			expected: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adapter.resolveNumClusters(tt.params)
			if got != tt.expected {
				t.Errorf("resolveNumClusters(%v) = %d, want %d", tt.params, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveThreshold
// ---------------------------------------------------------------------------

func TestResolveThreshold(t *testing.T) {
	adapter := NewSherpaOnnxAdapter("/tmp/models")

	tests := []struct {
		name     string
		params   map[string]interface{}
		expected float32
	}{
		{
			name:     "default_when_empty",
			params:   map[string]interface{}{},
			expected: 0.50,
		},
		{
			name:     "custom_threshold",
			params:   map[string]interface{}{"threshold": 0.7},
			expected: 0.7,
		},
		{
			name:     "zero_when_num_speakers_set",
			params:   map[string]interface{}{"num_speakers": 3, "threshold": 0.8},
			expected: 0, // threshold ignored when num_clusters is explicit
		},
		{
			name:     "default_when_threshold_zero",
			params:   map[string]interface{}{"threshold": 0.0},
			expected: 0.50, // 0 is treated as unset, falls back to default
		},
		{
			name:     "threshold_still_used_with_max_speakers",
			params:   map[string]interface{}{"max_speakers": 5, "threshold": 0.6},
			expected: 0.6, // max_speakers doesn't disable threshold
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adapter.resolveThreshold(tt.params)
			if got != tt.expected {
				t.Errorf("resolveThreshold(%v) = %f, want %f", tt.params, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// mapToResult
// ---------------------------------------------------------------------------

func TestMapToResult(t *testing.T) {
	adapter := NewSherpaOnnxAdapter("/tmp/models")

	t.Run("empty_segments", func(t *testing.T) {
		result := adapter.mapToResult([]sherpa.OfflineSpeakerDiarizationSegment{})
		if result.SpeakerCount != 0 {
			t.Errorf("expected 0 speakers, got %d", result.SpeakerCount)
		}
		if len(result.Segments) != 0 {
			t.Errorf("expected 0 segments, got %d", len(result.Segments))
		}
		if len(result.Speakers) != 0 {
			t.Errorf("expected 0 speaker labels, got %d", len(result.Speakers))
		}
	})

	t.Run("single_speaker", func(t *testing.T) {
		segments := []sherpa.OfflineSpeakerDiarizationSegment{
			{Start: 0.0, End: 5.5, Speaker: 0},
			{Start: 6.0, End: 10.0, Speaker: 0},
		}
		result := adapter.mapToResult(segments)

		if result.SpeakerCount != 1 {
			t.Errorf("expected 1 speaker, got %d", result.SpeakerCount)
		}
		if len(result.Segments) != 2 {
			t.Errorf("expected 2 segments, got %d", len(result.Segments))
		}
		if result.Speakers[0] != "speaker_00" {
			t.Errorf("expected speaker_00, got %s", result.Speakers[0])
		}
	})

	t.Run("multiple_speakers", func(t *testing.T) {
		segments := []sherpa.OfflineSpeakerDiarizationSegment{
			{Start: 0.0, End: 3.0, Speaker: 0},
			{Start: 3.5, End: 7.0, Speaker: 1},
			{Start: 7.5, End: 12.0, Speaker: 2},
			{Start: 12.5, End: 15.0, Speaker: 0},
		}
		result := adapter.mapToResult(segments)

		if result.SpeakerCount != 3 {
			t.Errorf("expected 3 speakers, got %d", result.SpeakerCount)
		}
		if len(result.Segments) != 4 {
			t.Errorf("expected 4 segments, got %d", len(result.Segments))
		}

		// Check sorted speaker labels
		expected := []string{"speaker_00", "speaker_01", "speaker_02"}
		for i, exp := range expected {
			if result.Speakers[i] != exp {
				t.Errorf("speakers[%d] = %q, want %q", i, result.Speakers[i], exp)
			}
		}
	})

	t.Run("segment_fields_mapped_correctly", func(t *testing.T) {
		segments := []sherpa.OfflineSpeakerDiarizationSegment{
			{Start: 1.5, End: 4.25, Speaker: 3},
		}
		result := adapter.mapToResult(segments)

		seg := result.Segments[0]
		if seg.Start != 1.5 {
			t.Errorf("Start = %f, want 1.5", seg.Start)
		}
		if seg.End != 4.25 {
			t.Errorf("End = %f, want 4.25", seg.End)
		}
		if seg.Speaker != "speaker_03" {
			t.Errorf("Speaker = %q, want 'speaker_03'", seg.Speaker)
		}
		if seg.Confidence != 1.0 {
			t.Errorf("Confidence = %f, want 1.0", seg.Confidence)
		}
	})

	t.Run("speakers_sorted_deterministically", func(t *testing.T) {
		// Use non-sequential speaker IDs to verify sorting
		segments := []sherpa.OfflineSpeakerDiarizationSegment{
			{Start: 0, End: 1, Speaker: 5},
			{Start: 1, End: 2, Speaker: 2},
			{Start: 2, End: 3, Speaker: 9},
		}
		result := adapter.mapToResult(segments)

		if result.Speakers[0] != "speaker_02" {
			t.Errorf("speakers[0] = %q, want 'speaker_02'", result.Speakers[0])
		}
		if result.Speakers[1] != "speaker_05" {
			t.Errorf("speakers[1] = %q, want 'speaker_05'", result.Speakers[1])
		}
		if result.Speakers[2] != "speaker_09" {
			t.Errorf("speakers[2] = %q, want 'speaker_09'", result.Speakers[2])
		}
	})
}

// ---------------------------------------------------------------------------
// Constants / config defaults
// ---------------------------------------------------------------------------

func TestDiarizationConfigDefaults(t *testing.T) {
	t.Run("embedding_model_is_wespeaker", func(t *testing.T) {
		if embeddingModelFile != "wespeaker_en_voxceleb_CAM++.onnx" {
			t.Errorf("embeddingModelFile = %q, want 'wespeaker_en_voxceleb_CAM++.onnx'", embeddingModelFile)
		}
	})

	t.Run("embedding_download_url_matches_model", func(t *testing.T) {
		if !strings.Contains(embeddingDownloadURL, "wespeaker_en_voxceleb_CAM++") {
			t.Errorf("embeddingDownloadURL should reference wespeaker, got %q", embeddingDownloadURL)
		}
	})

	t.Run("min_duration_off_is_0_8", func(t *testing.T) {
		if defaultMinDurationOff != float32(0.8) {
			t.Errorf("defaultMinDurationOff = %f, want 0.8", defaultMinDurationOff)
		}
	})

	t.Run("default_threshold_is_0_50", func(t *testing.T) {
		if defaultThreshold != 0.50 {
			t.Errorf("defaultThreshold = %f, want 0.50", defaultThreshold)
		}
	})

	t.Run("schema_threshold_default_is_0_50", func(t *testing.T) {
		adapter := NewSherpaOnnxAdapter("/tmp/models")
		schema := adapter.GetParameterSchema()
		for _, p := range schema {
			if p.Name == "threshold" {
				val, ok := p.Default.(float64)
				if !ok {
					t.Errorf("threshold schema default is not float64: %T", p.Default)
					return
				}
				if val < 0.49 || val > 0.51 {
					t.Errorf("threshold schema default = %v, want ~0.50", val)
				}
				return
			}
		}
		t.Error("threshold parameter not found in schema")
	})
}

// ---------------------------------------------------------------------------
// limitSpeakers
// ---------------------------------------------------------------------------

func TestLimitSpeakers(t *testing.T) {
	adapter := NewSherpaOnnxAdapter("/tmp/models")

	t.Run("no_change_when_under_limit", func(t *testing.T) {
		result := &interfaces.DiarizationResult{
			Segments: []interfaces.DiarizationSegment{
				{Start: 0, End: 5, Speaker: "speaker_00"},
				{Start: 5, End: 10, Speaker: "speaker_01"},
			},
			SpeakerCount: 2,
			Speakers:     []string{"speaker_00", "speaker_01"},
		}
		limited := adapter.limitSpeakers(result, 3)
		if limited.SpeakerCount != 2 {
			t.Errorf("expected 2 speakers, got %d", limited.SpeakerCount)
		}
	})

	t.Run("merges_smallest_speaker", func(t *testing.T) {
		result := &interfaces.DiarizationResult{
			Segments: []interfaces.DiarizationSegment{
				{Start: 0, End: 10, Speaker: "speaker_00"},   // 10s
				{Start: 10, End: 20, Speaker: "speaker_01"},  // 10s
				{Start: 20, End: 21, Speaker: "speaker_02"},  // 1s — shortest, should be merged
				{Start: 21, End: 30, Speaker: "speaker_00"},  // 9s
			},
			SpeakerCount: 3,
			Speakers:     []string{"speaker_00", "speaker_01", "speaker_02"},
		}
		limited := adapter.limitSpeakers(result, 2)
		if limited.SpeakerCount != 2 {
			t.Errorf("expected 2 speakers, got %d", limited.SpeakerCount)
		}
		// speaker_02 (at t=20-21) should be merged into closest kept speaker
		for _, seg := range limited.Segments {
			if seg.Speaker == "speaker_02" {
				t.Error("speaker_02 should have been merged away")
			}
		}
	})

	t.Run("merges_20_speakers_to_3", func(t *testing.T) {
		// Simulate the real problem: 20 over-segmented speakers
		segments := make([]interfaces.DiarizationSegment, 0)
		speakers := make([]string, 0)
		for i := 0; i < 20; i++ {
			label := fmt.Sprintf("speaker_%02d", i)
			speakers = append(speakers, label)
			// First 3 speakers have 10s each, rest have 1s each
			dur := 1.0
			if i < 3 {
				dur = 10.0
			}
			segments = append(segments, interfaces.DiarizationSegment{
				Start:   float64(i) * 2,
				End:     float64(i)*2 + dur,
				Speaker: label,
			})
		}
		sort.Strings(speakers)

		result := &interfaces.DiarizationResult{
			Segments:     segments,
			SpeakerCount: 20,
			Speakers:     speakers,
		}
		limited := adapter.limitSpeakers(result, 3)
		if limited.SpeakerCount != 3 {
			t.Errorf("expected 3 speakers after limiting, got %d", limited.SpeakerCount)
		}
		// All segments should now belong to the top 3 speakers
		for _, seg := range limited.Segments {
			if seg.Speaker != "speaker_00" && seg.Speaker != "speaker_01" && seg.Speaker != "speaker_02" {
				t.Errorf("unexpected speaker after limit: %s", seg.Speaker)
			}
		}
	})

	t.Run("preserves_segment_times", func(t *testing.T) {
		result := &interfaces.DiarizationResult{
			Segments: []interfaces.DiarizationSegment{
				{Start: 1.5, End: 4.25, Speaker: "speaker_00", Confidence: 0.9},
				{Start: 5.0, End: 6.0, Speaker: "speaker_01", Confidence: 0.8},
				{Start: 6.5, End: 7.0, Speaker: "speaker_02", Confidence: 0.7},
			},
			SpeakerCount: 3,
			Speakers:     []string{"speaker_00", "speaker_01", "speaker_02"},
		}
		limited := adapter.limitSpeakers(result, 2)
		// Check that original segment times are preserved
		if limited.Segments[0].Start != 1.5 || limited.Segments[0].End != 4.25 {
			t.Errorf("segment times changed: got start=%f end=%f", limited.Segments[0].Start, limited.Segments[0].End)
		}
	})
}

// ---------------------------------------------------------------------------
// GetEstimatedProcessingTime
// ---------------------------------------------------------------------------

func TestGetEstimatedProcessingTime(t *testing.T) {
	adapter := NewSherpaOnnxAdapter("/tmp/models")

	t.Run("with_known_duration", func(t *testing.T) {
		input := interfaces.AudioInput{
			Duration: 10 * time.Minute,
		}
		est := adapter.GetEstimatedProcessingTime(input)

		// 10min * 0.08 = 48s
		expected := time.Duration(float64(10*time.Minute) * 0.08)
		if est != expected {
			t.Errorf("expected %v, got %v", expected, est)
		}
	})

	t.Run("zero_duration_uses_size_estimate", func(t *testing.T) {
		input := interfaces.AudioInput{
			Duration: 0,
			Size:     10 * 1024 * 1024, // 10MB
		}
		est := adapter.GetEstimatedProcessingTime(input)

		// 10MB → estimatedMinutes=10, audioDuration=10min, * 0.08 = 48s
		if est <= 0 {
			t.Error("expected positive processing time estimate")
		}
	})

	t.Run("short_audio", func(t *testing.T) {
		input := interfaces.AudioInput{
			Duration: 30 * time.Second,
		}
		est := adapter.GetEstimatedProcessingTime(input)

		// 30s * 0.08 = 2.4s
		if est <= 0 {
			t.Error("expected positive estimate for short audio")
		}
		if est > 5*time.Second {
			t.Errorf("estimate too high for 30s audio: %v", est)
		}
	})
}

// ---------------------------------------------------------------------------
// extractTarBz2 — path traversal protection
// ---------------------------------------------------------------------------

func TestExtractTarBz2_PathTraversal(t *testing.T) {
	// Create a malicious tar.bz2 with a path traversal entry
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "evil.tar.bz2")

	createTestTarBz2(t, archivePath, []testTarEntry{
		{name: "../../etc/passwd", content: "malicious", typeflag: tar.TypeReg},
	})

	destDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	err := extractTarBz2(archivePath, destDir)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "invalid tar path") {
		t.Errorf("expected 'invalid tar path' error, got: %v", err)
	}
}

func TestExtractTarBz2_ValidArchive(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "valid.tar.bz2")

	createTestTarBz2(t, archivePath, []testTarEntry{
		{name: "subdir/", typeflag: tar.TypeDir},
		{name: "subdir/file.txt", content: "hello world", typeflag: tar.TypeReg},
		{name: "root.txt", content: "top level", typeflag: tar.TypeReg},
	})

	destDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := extractTarBz2(archivePath, destDir); err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	// Verify extracted files
	data, err := os.ReadFile(filepath.Join(destDir, "subdir", "file.txt"))
	if err != nil {
		t.Fatalf("expected file.txt to exist: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("file.txt content = %q, want 'hello world'", string(data))
	}

	data, err = os.ReadFile(filepath.Join(destDir, "root.txt"))
	if err != nil {
		t.Fatalf("expected root.txt to exist: %v", err)
	}
	if string(data) != "top level" {
		t.Errorf("root.txt content = %q, want 'top level'", string(data))
	}
}

func TestExtractTarBz2_SymlinkSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "symlink.tar.bz2")

	createTestTarBz2(t, archivePath, []testTarEntry{
		{name: "normal.txt", content: "ok", typeflag: tar.TypeReg},
		{name: "link", typeflag: tar.TypeSymlink},
	})

	destDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Should succeed — symlinks are skipped, not rejected
	if err := extractTarBz2(archivePath, destDir); err != nil {
		t.Fatalf("extraction should skip symlinks, got error: %v", err)
	}

	// normal.txt should exist
	if _, err := os.Stat(filepath.Join(destDir, "normal.txt")); err != nil {
		t.Error("expected normal.txt to be extracted")
	}
	// symlink should NOT have been created
	if _, err := os.Lstat(filepath.Join(destDir, "link")); err == nil {
		t.Error("symlink 'link' should not have been extracted")
	}
}

func TestExtractTarBz2_OversizedEntry(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "oversized.tar.bz2")

	// Create a tar entry whose header claims a size above the limit
	createTestTarBz2WithCustomSize(t, archivePath, "big.bin", maxExtractFileSize+1)

	destDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	err := extractTarBz2(archivePath, destDir)
	if err == nil {
		t.Fatal("expected error for oversized entry, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds size limit") {
		t.Errorf("expected 'exceeds size limit' error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// extractTarFile
// ---------------------------------------------------------------------------

func TestExtractTarFile(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "output.txt")
	content := "test content for extraction"

	err := extractTarFile(target, strings.NewReader(content))
	if err != nil {
		t.Fatalf("extractTarFile failed: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("failed to read extracted file: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
}

func TestExtractTarFile_InvalidPath(t *testing.T) {
	err := extractTarFile("/nonexistent/dir/file.txt", strings.NewReader("data"))
	if err == nil {
		t.Error("expected error for invalid path, got nil")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type testTarEntry struct {
	name     string
	content  string
	typeflag byte
}

// createTestTarBz2 creates a real tar.bz2 archive with the given entries.
// We use bzip2 encoding by piping through a command since Go stdlib only has
// bzip2 reader, not writer. Instead we'll create a tar, then use compress/flate
// workaround: write uncompressed tar and wrap it so extractTarBz2 can read it.
//
// Actually, Go stdlib has bzip2.NewReader but no writer. We need to create
// a valid bzip2 file. Let's use the exec approach with bzip2 command, or
// alternatively create the test archive using the dsnet/compress library.
//
// Simplest approach: create a tar file and compress with external bzip2.
// Fallback: use a pipe with pbzip2/bzip2 command.
func createTestTarBz2(t *testing.T, archivePath string, entries []testTarEntry) {
	t.Helper()

	// Create tar in memory
	var tarBuf strings.Builder
	tw := tar.NewWriter(&tarBuf)

	for _, entry := range entries {
		hdr := &tar.Header{
			Name:     entry.name,
			Typeflag: entry.typeflag,
			Mode:     0644,
		}
		if entry.typeflag == tar.TypeDir {
			hdr.Mode = 0755
		}
		if entry.typeflag == tar.TypeReg {
			hdr.Size = int64(len(entry.content))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("failed to write tar header: %v", err)
		}
		if entry.typeflag == tar.TypeReg && len(entry.content) > 0 {
			if _, err := tw.Write([]byte(entry.content)); err != nil {
				t.Fatalf("failed to write tar content: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}

	// Compress with bzip2 via external command
	tarPath := archivePath + ".tar"
	if err := os.WriteFile(tarPath, []byte(tarBuf.String()), 0644); err != nil {
		t.Fatalf("failed to write tar file: %v", err)
	}
	defer os.Remove(tarPath)

	// Use bzip2 command to compress
	cmd := exec.Command("bzip2", "-k", tarPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bzip2 compression failed: %v: %s", err, out)
	}

	// bzip2 creates tarPath + ".bz2"
	bz2Path := tarPath + ".bz2"
	if err := os.Rename(bz2Path, archivePath); err != nil {
		t.Fatalf("failed to rename bz2 file: %v", err)
	}
}

func createTestTarBz2WithCustomSize(t *testing.T, archivePath string, filename string, headerSize int64) {
	t.Helper()

	// Create a tar with a header claiming a large size but minimal actual content
	var tarBuf strings.Builder
	tw := tar.NewWriter(&tarBuf)

	hdr := &tar.Header{
		Name:     filename,
		Typeflag: tar.TypeReg,
		Mode:     0644,
		Size:     headerSize,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("failed to write tar header: %v", err)
	}
	// Don't write full content — we just need the header to trigger the size check
	// Write enough padding to make tar happy (the extraction will reject based on header.Size)
	padding := make([]byte, 512) // minimal padding
	tw.Write(padding)
	tw.Close()

	tarPath := archivePath + ".tar"
	if err := os.WriteFile(tarPath, []byte(tarBuf.String()), 0644); err != nil {
		t.Fatalf("failed to write tar file: %v", err)
	}
	defer os.Remove(tarPath)

	cmd := exec.Command("bzip2", "-k", tarPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bzip2 compression failed: %v: %s", err, out)
	}

	bz2Path := tarPath + ".bz2"
	if err := os.Rename(bz2Path, archivePath); err != nil {
		t.Fatalf("failed to rename bz2 file: %v", err)
	}
}

// Ensure the imports of bzip2 / io are used (they're needed by extractTarBz2 which we test)
var _ = bzip2.NewReader
var _ io.Reader

// ---------------------------------------------------------------------------
// analyzeSamples — audio sample diagnostic
// ---------------------------------------------------------------------------

func TestAnalyzeSamples(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		stats := analyzeSamples(nil)
		if stats.rms != 0 || stats.nanCount != 0 {
			t.Errorf("expected zero stats for nil input, got %+v", stats)
		}
	})

	t.Run("normal_audio", func(t *testing.T) {
		// Simulate a sine-wave-like signal
		samples := make([]float32, 16000) // 1 second at 16kHz
		for i := range samples {
			samples[i] = float32(0.5 * float64(i%100-50) / 50.0) // triangle wave, amplitude 0.5
		}
		stats := analyzeSamples(samples)
		if stats.min >= 0 {
			t.Errorf("expected negative min, got %f", stats.min)
		}
		if stats.max <= 0 {
			t.Errorf("expected positive max, got %f", stats.max)
		}
		if stats.rms < 0.1 || stats.rms > 1.0 {
			t.Errorf("expected reasonable RMS, got %f", stats.rms)
		}
		if stats.nanCount != 0 || stats.infCount != 0 {
			t.Errorf("expected no NaN/Inf, got nan=%d inf=%d", stats.nanCount, stats.infCount)
		}
	})

	t.Run("all_zeros", func(t *testing.T) {
		samples := make([]float32, 1000)
		stats := analyzeSamples(samples)
		if stats.rms != 0 {
			t.Errorf("expected zero RMS for silence, got %f", stats.rms)
		}
		if stats.zeroFraction != 1.0 {
			t.Errorf("expected 100%% zeros, got %f", stats.zeroFraction)
		}
	})

	t.Run("contains_nan", func(t *testing.T) {
		samples := []float32{0.1, 0.2, float32(math.NaN()), 0.4}
		stats := analyzeSamples(samples)
		if stats.nanCount != 1 {
			t.Errorf("expected 1 NaN, got %d", stats.nanCount)
		}
	})

	t.Run("contains_inf", func(t *testing.T) {
		samples := []float32{0.1, float32(math.Inf(1)), 0.3}
		stats := analyzeSamples(samples)
		if stats.infCount != 1 {
			t.Errorf("expected 1 Inf, got %d", stats.infCount)
		}
	})
}
