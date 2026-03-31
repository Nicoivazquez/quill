package pipeline

import (
	"math"
	"testing"

	"quill/internal/transcription/interfaces"
)

// --- SpeechRegion & VadManifest struct tests ---

func TestSpeechRegion_Duration(t *testing.T) {
	tests := []struct {
		name     string
		region   SpeechRegion
		expected float64
	}{
		{"simple region", SpeechRegion{Start: 1.0, End: 3.0}, 2.0},
		{"zero-length region", SpeechRegion{Start: 5.0, End: 5.0}, 0.0},
		{"fractional region", SpeechRegion{Start: 0.5, End: 2.75}, 2.25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.region.Duration()
			if math.Abs(got-tt.expected) > 1e-9 {
				t.Errorf("Duration() = %f, want %f", got, tt.expected)
			}
		})
	}
}

func TestVadManifest_StrippedDuration(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 0.5, End: 3.0},  // 2.5s
			{Start: 5.0, End: 8.0},  // 3.0s
			{Start: 9.0, End: 10.0}, // 1.0s
		},
		OriginalDuration: 12.0,
	}

	got := manifest.StrippedDuration()
	expected := 6.5
	if math.Abs(got-expected) > 1e-9 {
		t.Errorf("StrippedDuration() = %f, want %f", got, expected)
	}
}

func TestVadManifest_StrippedDuration_Empty(t *testing.T) {
	manifest := VadManifest{
		Regions:          []SpeechRegion{},
		OriginalDuration: 10.0,
	}

	got := manifest.StrippedDuration()
	if got != 0.0 {
		t.Errorf("StrippedDuration() = %f, want 0.0", got)
	}
}

// --- remapTimestamp tests ---

func TestRemapTimestamp_SingleRegion(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 2.0, End: 5.0}, // 3.0s speech
		},
		OriginalDuration: 10.0,
	}

	tests := []struct {
		name             string
		strippedTime     float64
		expectedOriginal float64
	}{
		{"start of region", 0.0, 2.0},
		{"middle of region", 1.5, 3.5},
		{"end of region", 3.0, 5.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remapTimestamp(tt.strippedTime, manifest)
			if math.Abs(got-tt.expectedOriginal) > 1e-6 {
				t.Errorf("remapTimestamp(%f) = %f, want %f", tt.strippedTime, got, tt.expectedOriginal)
			}
		})
	}
}

func TestRemapTimestamp_MultipleRegions(t *testing.T) {
	// Regions: [0.5-3.0] (2.5s), [5.0-8.0] (3.0s), [9.0-10.0] (1.0s)
	// Stripped offsets: [0-2.5], [2.5-5.5], [5.5-6.5]
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 0.5, End: 3.0},
			{Start: 5.0, End: 8.0},
			{Start: 9.0, End: 10.0},
		},
		OriginalDuration: 12.0,
	}

	tests := []struct {
		name             string
		strippedTime     float64
		expectedOriginal float64
	}{
		// First region: stripped [0, 2.5) → original [0.5, 3.0)
		{"first region start", 0.0, 0.5},
		{"first region middle", 1.0, 1.5},
		{"first region near end", 2.49, 2.99},

		// Boundary at 2.5: half-open intervals → belongs to second region
		{"boundary 1→2", 2.5, 5.0},

		// Second region: stripped [2.5, 5.5) → original [5.0, 8.0)
		{"second region quarter", 3.0, 5.5},
		{"second region near end", 5.49, 7.99},

		// Boundary at 5.5: belongs to third region
		{"boundary 2→3", 5.5, 9.0},

		// Third region: stripped [5.5, 6.5) → original [9.0, 10.0)
		{"third region middle", 6.0, 9.5},

		// Beyond all regions: clamp to end of last region
		{"beyond all", 6.5, 10.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remapTimestamp(tt.strippedTime, manifest)
			if math.Abs(got-tt.expectedOriginal) > 1e-6 {
				t.Errorf("remapTimestamp(%f) = %f, want %f", tt.strippedTime, got, tt.expectedOriginal)
			}
		})
	}
}

func TestRemapTimestamp_BeyondStrippedDuration(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 1.0, End: 3.0}, // 2.0s
		},
		OriginalDuration: 10.0,
	}

	// A timestamp beyond the stripped audio should clamp to the end of the last region.
	got := remapTimestamp(5.0, manifest)
	expected := 3.0 // end of last region
	if math.Abs(got-expected) > 1e-6 {
		t.Errorf("remapTimestamp(5.0) = %f, want %f (clamped to last region end)", got, expected)
	}
}

func TestRemapTimestamp_EmptyManifest(t *testing.T) {
	manifest := VadManifest{
		Regions:          []SpeechRegion{},
		OriginalDuration: 10.0,
	}

	// With no regions, return the input unchanged (no remapping possible).
	got := remapTimestamp(3.0, manifest)
	if math.Abs(got-3.0) > 1e-6 {
		t.Errorf("remapTimestamp(3.0) with empty manifest = %f, want 3.0", got)
	}
}

func TestRemapTimestamp_NegativeInput(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 1.0, End: 3.0},
		},
		OriginalDuration: 5.0,
	}

	// Negative timestamps should clamp to start of first region.
	got := remapTimestamp(-1.0, manifest)
	expected := 1.0
	if math.Abs(got-expected) > 1e-6 {
		t.Errorf("remapTimestamp(-1.0) = %f, want %f", got, expected)
	}
}

// --- RemapTranscriptResult tests ---

func TestRemapTranscriptResult_Segments(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 2.0, End: 5.0},  // 3.0s → stripped [0, 3.0)
			{Start: 8.0, End: 11.0}, // 3.0s → stripped [3.0, 6.0)
		},
		OriginalDuration: 15.0,
	}

	result := &interfaces.TranscriptResult{
		Text:     "Hello world",
		Language: "en",
		Segments: []interfaces.TranscriptSegment{
			{Start: 0.0, End: 2.0, Text: "Hello"},    // maps to [2.0, 4.0]
			{Start: 3.5, End: 5.0, Text: "world"},    // maps to [8.5, 10.0]
		},
	}

	remapped := RemapTranscriptResult(result, manifest)

	if len(remapped.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(remapped.Segments))
	}

	// First segment: stripped [0.0, 2.0] → original [2.0, 4.0]
	assertFloat(t, "Segments[0].Start", remapped.Segments[0].Start, 2.0)
	assertFloat(t, "Segments[0].End", remapped.Segments[0].End, 4.0)
	if remapped.Segments[0].Text != "Hello" {
		t.Errorf("Segments[0].Text = %q, want %q", remapped.Segments[0].Text, "Hello")
	}

	// Second segment: stripped [3.5, 5.0] → original [8.5, 10.0]
	assertFloat(t, "Segments[1].Start", remapped.Segments[1].Start, 8.5)
	assertFloat(t, "Segments[1].End", remapped.Segments[1].End, 10.0)
}

func TestRemapTranscriptResult_WordSegments(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 1.0, End: 4.0}, // 3.0s
		},
		OriginalDuration: 10.0,
	}

	result := &interfaces.TranscriptResult{
		Text:     "Hello world",
		Language: "en",
		WordSegments: []interfaces.TranscriptWord{
			{Start: 0.0, End: 0.8, Word: "Hello", Score: 0.95},
			{Start: 1.2, End: 2.0, Word: "world", Score: 0.90},
		},
	}

	remapped := RemapTranscriptResult(result, manifest)

	if len(remapped.WordSegments) != 2 {
		t.Fatalf("expected 2 word segments, got %d", len(remapped.WordSegments))
	}

	// Word "Hello": stripped [0.0, 0.8] → original [1.0, 1.8]
	assertFloat(t, "WordSegments[0].Start", remapped.WordSegments[0].Start, 1.0)
	assertFloat(t, "WordSegments[0].End", remapped.WordSegments[0].End, 1.8)
	if remapped.WordSegments[0].Word != "Hello" {
		t.Errorf("WordSegments[0].Word = %q, want %q", remapped.WordSegments[0].Word, "Hello")
	}
	if remapped.WordSegments[0].Score != 0.95 {
		t.Errorf("WordSegments[0].Score = %f, want 0.95", remapped.WordSegments[0].Score)
	}

	// Word "world": stripped [1.2, 2.0] → original [2.2, 3.0]
	assertFloat(t, "WordSegments[1].Start", remapped.WordSegments[1].Start, 2.2)
	assertFloat(t, "WordSegments[1].End", remapped.WordSegments[1].End, 3.0)
}

func TestRemapTranscriptResult_PreservesMetadata(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 0.0, End: 5.0},
		},
		OriginalDuration: 5.0,
	}

	result := &interfaces.TranscriptResult{
		Text:       "Hello",
		Language:   "en",
		Confidence: 0.95,
		ModelUsed:  "mlx_whisper",
		Metadata:   map[string]string{"key": "value"},
		Segments: []interfaces.TranscriptSegment{
			{Start: 0.0, End: 1.0, Text: "Hello"},
		},
	}

	remapped := RemapTranscriptResult(result, manifest)

	if remapped.Text != "Hello" {
		t.Errorf("Text = %q, want %q", remapped.Text, "Hello")
	}
	if remapped.Language != "en" {
		t.Errorf("Language = %q, want %q", remapped.Language, "en")
	}
	if remapped.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", remapped.Confidence)
	}
	if remapped.ModelUsed != "mlx_whisper" {
		t.Errorf("ModelUsed = %q, want %q", remapped.ModelUsed, "mlx_whisper")
	}
	if remapped.Metadata["key"] != "value" {
		t.Errorf("Metadata[key] = %q, want %q", remapped.Metadata["key"], "value")
	}
}

func TestRemapTranscriptResult_NilResult(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 0.0, End: 5.0},
		},
		OriginalDuration: 5.0,
	}

	remapped := RemapTranscriptResult(nil, manifest)
	if remapped != nil {
		t.Error("expected nil result for nil input")
	}
}

func TestRemapTranscriptResult_EmptyManifest(t *testing.T) {
	manifest := VadManifest{
		Regions:          []SpeechRegion{},
		OriginalDuration: 10.0,
	}

	result := &interfaces.TranscriptResult{
		Text: "Hello",
		Segments: []interfaces.TranscriptSegment{
			{Start: 1.0, End: 2.0, Text: "Hello"},
		},
	}

	// With empty manifest, timestamps should pass through unchanged.
	remapped := RemapTranscriptResult(result, manifest)
	assertFloat(t, "Segments[0].Start", remapped.Segments[0].Start, 1.0)
	assertFloat(t, "Segments[0].End", remapped.Segments[0].End, 2.0)
}

func TestRemapTranscriptResult_PreservesSpeakerLabels(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 0.0, End: 5.0},
		},
		OriginalDuration: 5.0,
	}

	speaker := "speaker_00"
	result := &interfaces.TranscriptResult{
		Segments: []interfaces.TranscriptSegment{
			{Start: 0.0, End: 1.0, Text: "Hello", Speaker: &speaker},
		},
		WordSegments: []interfaces.TranscriptWord{
			{Start: 0.0, End: 0.5, Word: "Hello", Speaker: &speaker},
		},
	}

	remapped := RemapTranscriptResult(result, manifest)

	if remapped.Segments[0].Speaker == nil || *remapped.Segments[0].Speaker != "speaker_00" {
		t.Error("expected speaker label to be preserved on segment")
	}
	if remapped.WordSegments[0].Speaker == nil || *remapped.WordSegments[0].Speaker != "speaker_00" {
		t.Error("expected speaker label to be preserved on word segment")
	}
}

// --- helpers ---

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-4 {
		t.Errorf("%s = %f, want %f", name, got, want)
	}
}
