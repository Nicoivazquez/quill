package transcription

import (
	"regexp"
	"strconv"
)

var speakerKeyRe = regexp.MustCompile(`(?i)^speaker[_ ](\d+)$`)

// FormatSpeakerLabel converts raw speaker keys like "speaker_0", "SPEAKER_2",
// or "speaker 1" to human-friendly labels like "Speaker A", "Speaker B".
// Returns the original value unchanged if it doesn't match a known pattern.
// This mirrors the frontend's formatSpeakerLabel() in speaker-utils.ts.
func FormatSpeakerLabel(rawSpeaker string) string {
	match := speakerKeyRe.FindStringSubmatch(rawSpeaker)
	if match != nil {
		index, _ := strconv.Atoi(match[1])
		return speakerIndexToLabel(index)
	}
	return rawSpeaker
}

// speakerIndexToLabel converts a 0-based index to an alphabetic label.
// 0 → "Speaker A", 1 → "Speaker B", ..., 25 → "Speaker Z", 26 → "Speaker AA".
func speakerIndexToLabel(index int) string {
	n := index + 1
	label := ""
	for n > 0 {
		rem := (n - 1) % 26
		label = string(rune('A'+rem)) + label
		n = (n - 1) / 26
	}
	return "Speaker " + label
}
