package pipeline

import (
	"quill/internal/transcription/interfaces"
)

// SpeechRegion represents a detected speech region in the original audio.
type SpeechRegion struct {
	Start float64 `json:"start"` // seconds in original audio
	End   float64 `json:"end"`   // seconds in original audio
}

// Duration returns the length of this speech region in seconds.
func (r SpeechRegion) Duration() float64 {
	return r.End - r.Start
}

// VadManifest describes the speech regions detected by VAD, used to remap
// timestamps from stripped (silence-removed) audio back to the original.
type VadManifest struct {
	Regions          []SpeechRegion `json:"regions"`
	OriginalDuration float64        `json:"original_duration"` // seconds
}

// StrippedDuration returns the total duration of all speech regions combined
// (i.e., the duration of the silence-stripped audio).
func (m VadManifest) StrippedDuration() float64 {
	var total float64
	for _, r := range m.Regions {
		total += r.Duration()
	}
	return total
}

// remapTimestamp converts a timestamp from stripped (silence-removed) audio
// coordinates back to the original audio timeline.
//
// The algorithm walks through the speech regions, accumulating their durations
// to find which region the stripped timestamp falls in, then adds the offset
// within that region to the region's original start time.
//
// Edge cases:
//   - Empty manifest: returns strippedTime unchanged (no remapping possible).
//   - Negative strippedTime: clamps to start of first region.
//   - strippedTime beyond total stripped duration: clamps to end of last region.
func remapTimestamp(strippedTime float64, manifest VadManifest) float64 {
	if len(manifest.Regions) == 0 {
		return strippedTime
	}

	if strippedTime <= 0 {
		return manifest.Regions[0].Start
	}

	var accumulated float64
	for _, region := range manifest.Regions {
		regionDur := region.Duration()
		if strippedTime < accumulated+regionDur {
			// The timestamp falls within this region.
			offset := strippedTime - accumulated
			return region.Start + offset
		}
		accumulated += regionDur
	}

	// Beyond all regions — clamp to end of last region.
	return manifest.Regions[len(manifest.Regions)-1].End
}

// RemapTranscriptResult creates a new TranscriptResult with all timestamps
// remapped from stripped audio coordinates back to the original timeline.
//
// The original result is not modified; a new copy is returned.
// If result is nil, returns nil.
// If the manifest has no regions, timestamps pass through unchanged.
func RemapTranscriptResult(result *interfaces.TranscriptResult, manifest VadManifest) *interfaces.TranscriptResult {
	if result == nil {
		return nil
	}

	remapped := &interfaces.TranscriptResult{
		Text:           result.Text,
		Language:       result.Language,
		Confidence:     result.Confidence,
		ProcessingTime: result.ProcessingTime,
		ModelUsed:      result.ModelUsed,
		Metadata:       result.Metadata,
	}

	// Remap segments.
	remapped.Segments = make([]interfaces.TranscriptSegment, len(result.Segments))
	for i, seg := range result.Segments {
		remapped.Segments[i] = interfaces.TranscriptSegment{
			Start:    remapTimestamp(seg.Start, manifest),
			End:      remapTimestamp(seg.End, manifest),
			Text:     seg.Text,
			Speaker:  seg.Speaker,
			Language: seg.Language,
		}
	}

	// Remap word segments.
	remapped.WordSegments = make([]interfaces.TranscriptWord, len(result.WordSegments))
	for i, word := range result.WordSegments {
		remapped.WordSegments[i] = interfaces.TranscriptWord{
			Start:   remapTimestamp(word.Start, manifest),
			End:     remapTimestamp(word.End, manifest),
			Word:    word.Word,
			Score:   word.Score,
			Speaker: word.Speaker,
		}
	}

	return remapped
}
