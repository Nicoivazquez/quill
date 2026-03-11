package api

import (
	"math"
	"testing"
)

func TestBuildSpeakerClipWindows_MergesAndExtends(t *testing.T) {
	s0 := "SPEAKER_0"
	s1 := "speaker_1"

	segments := []transcriptSegment{
		{Start: 0.0, End: 1.0, Speaker: &s0},
		{Start: 1.2, End: 2.6, Speaker: &s0}, // Merges with previous segment.
		{Start: 10.0, End: 11.0, Speaker: &s0},
		{Start: 5.0, End: 5.05, Speaker: &s1}, // Too short, ignored.
	}

	windows := buildSpeakerClipWindows(segments)
	window, ok := windows["speaker_0"]
	if !ok {
		t.Fatalf("expected speaker_0 window to exist")
	}

	if math.Abs(window.Start-0.0) > 0.001 {
		t.Fatalf("expected window start close to 0.0, got %.3f", window.Start)
	}
	if math.Abs(window.End-8.0) > 0.001 {
		t.Fatalf("expected window end close to 8.0, got %.3f", window.End)
	}

	if _, exists := windows["speaker_1"]; exists {
		t.Fatalf("did not expect speaker_1 window because segment duration was below threshold")
	}
}

func TestMergeClipSpans(t *testing.T) {
	input := []clipSpan{
		{Start: 0.0, End: 1.0},
		{Start: 1.4, End: 2.0},
		{Start: 3.5, End: 4.0},
	}

	merged := mergeClipSpans(input, 0.5)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged spans, got %d", len(merged))
	}

	if math.Abs(merged[0].Start-0.0) > 0.001 || math.Abs(merged[0].End-2.0) > 0.001 {
		t.Fatalf("unexpected first merged span: %#v", merged[0])
	}
	if math.Abs(merged[1].Start-3.5) > 0.001 || math.Abs(merged[1].End-4.0) > 0.001 {
		t.Fatalf("unexpected second merged span: %#v", merged[1])
	}
}
