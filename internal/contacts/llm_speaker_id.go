package contacts

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"quill/pkg/logger"
)

// Fusion weight constants for the voice + LLM score combination formula.
//
//	combined = max(voice_score * voiceWeight + llm_score * llmWeight, voice_score)
//
// The max() ensures the LLM can only boost a voice match, never penalize it.
const (
	voiceWeight      = 0.6
	llmWeight        = 0.4
	minLLMConfidence = 0.50 // LLM guesses below this confidence are ignored
)

// LLMSpeakerGuess represents the LLM's guess for a speaker's identity.
type LLMSpeakerGuess struct {
	Speaker     string  // e.g., "speaker_00"
	GuessedName string  // The name the LLM thinks this is
	Confidence  float64 // 0.0 to 1.0
	Reasoning   string  // Brief explanation
}

// LLMSpeakerIDConfig holds configuration for the LLM speaker identification.
type LLMSpeakerIDConfig struct {
	Provider string // "openai" or "ollama"
	APIKey   string
	BaseURL  string
	Model    string // e.g., "gpt-4o-mini" or "llama3"
}

// BuildSpeakerIDPrompt constructs the prompt for the LLM to analyze a transcript
// and identify speakers based on conversational context and a list of known contacts.
//
// The prompt instructs the LLM to return a JSON array of objects with the shape:
//
//	[{"speaker": "speaker_00", "name": "Alice", "confidence": 0.8, "reasoning": "..."}]
//
// Only names from the provided contactNames list should be matched.
func BuildSpeakerIDPrompt(transcriptText string, speakerLabels []string, contactNames []string) string {
	var sb strings.Builder

	sb.WriteString("You are analyzing a transcript to identify which speaker labels correspond to which people.\n\n")

	// List known contacts.
	sb.WriteString("Known contacts (you may ONLY match speakers to names from this list):\n")
	if len(contactNames) == 0 {
		sb.WriteString("  (none provided)\n")
	} else {
		for _, name := range contactNames {
			fmt.Fprintf(&sb, "  - %s\n", name)
		}
	}
	sb.WriteString("\n")

	// List expected speaker labels.
	sb.WriteString("Speaker labels present in the transcript:\n")
	if len(speakerLabels) == 0 {
		sb.WriteString("  (none provided)\n")
	} else {
		for _, label := range speakerLabels {
			fmt.Fprintf(&sb, "  - %s\n", label)
		}
	}
	sb.WriteString("\n")

	// Embed the transcript.
	sb.WriteString("Transcript:\n")
	sb.WriteString("---\n")
	sb.WriteString(transcriptText)
	sb.WriteString("\n---\n\n")

	// Output instructions.
	sb.WriteString("Based on conversational context (names mentioned, self-introductions, topics discussed), ")
	sb.WriteString("identify which speaker label corresponds to which contact name.\n\n")
	sb.WriteString("Respond with ONLY a JSON array. Each element must have these fields:\n")
	sb.WriteString(`  {"speaker": "<label>", "name": "<contact name>", "confidence": <0.0-1.0>, "reasoning": "<brief explanation>"}`)
	sb.WriteString("\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("  1. Only use names from the Known contacts list above.\n")
	sb.WriteString("  2. Only include entries where you can make a reasonable identification.\n")
	sb.WriteString("  3. Set confidence to 0.0 if you are not confident at all.\n")
	sb.WriteString("  4. Output valid JSON only — no prose before or after the array.\n")

	return sb.String()
}

// jsonGuessEntry is the raw shape of each element in the LLM's JSON response.
type jsonGuessEntry struct {
	Speaker    string  `json:"speaker"`
	Name       string  `json:"name"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

// jsonArrayPattern matches the first JSON array found in a string, tolerating
// prose that surrounds the array (common LLM output pattern).
var jsonArrayPattern = regexp.MustCompile(`(?s)\[.*?\]`)

// ParseLLMSpeakerGuesses parses the LLM's response into structured guesses.
//
// The function is deliberately lenient:
//   - It extracts the first JSON array found in the response, ignoring
//     surrounding prose.
//   - Any entry whose speaker label is not in speakerLabels is filtered out.
//   - Confidence values outside [0, 1] are clamped to that range.
//   - On any parse error the function returns an empty (non-nil) slice.
func ParseLLMSpeakerGuesses(llmResponse string, speakerLabels []string) []LLMSpeakerGuess {
	result := []LLMSpeakerGuess{}

	if len(speakerLabels) == 0 {
		return result
	}

	// Build a fast lookup set of valid speaker labels.
	validLabels := make(map[string]struct{}, len(speakerLabels))
	for _, l := range speakerLabels {
		validLabels[l] = struct{}{}
	}

	// Extract the JSON array from the response, tolerating surrounding text.
	raw := jsonArrayPattern.FindString(llmResponse)
	if raw == "" {
		return result
	}

	var entries []jsonGuessEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return result
	}

	for _, e := range entries {
		// Filter out hallucinated speaker labels.
		if _, ok := validLabels[e.Speaker]; !ok {
			continue
		}
		result = append(result, LLMSpeakerGuess{
			Speaker:     e.Speaker,
			GuessedName: e.Name,
			Confidence:  clampConfidence(e.Confidence),
			Reasoning:   e.Reasoning,
		})
	}

	return result
}

// FuseScores combines voice matching scores with LLM confidence scores using
// a boost-only formula:
//
//	combined = max(voice_score * 0.6 + llm_score * 0.4, voice_score)
//
// The max() ensures the LLM can only boost a voice match, never penalize it.
//
// Fusion rules:
//   - LLM guesses with confidence below minLLMConfidence (0.50) are ignored.
//   - If the LLM's guessed name matches the voice-matched contact name (and
//     confidence >= threshold), the LLM confidence is used as the llm_score.
//   - If the LLM disagrees or has no guess, the voice score is preserved as-is.
//   - If a speaker appears in LLM guesses but has no voice match, an entry is
//     created with voice_score=0.0 (score = 0.4 * llm_confidence), only if
//     confidence >= threshold.
//   - If LLM guesses is empty, voice matches are returned unchanged.
//   - The Tier field of every returned entry is re-classified from the final
//     score using ClassifySpeakerMatch.
//
// The function never panics on nil or empty inputs.
func FuseScores(voiceMatches []SpeakerMatch, llmGuesses []LLMSpeakerGuess) []SpeakerMatch {
	result := []SpeakerMatch{}

	// No LLM input — return voice matches unchanged.
	if len(llmGuesses) == 0 {
		return append(result, voiceMatches...)
	}

	// Build a lookup: speaker label → LLMSpeakerGuess.
	llmBySpeaker := make(map[string]LLMSpeakerGuess, len(llmGuesses))
	for _, g := range llmGuesses {
		llmBySpeaker[g.Speaker] = g
	}

	// Track which speaker labels have been covered by a voice match.
	coveredSpeakers := make(map[string]struct{}, len(voiceMatches))

	// Process existing voice matches.
	for _, vm := range voiceMatches {
		coveredSpeakers[vm.Speaker] = struct{}{}

		combined := vm.Score // default: preserve voice score
		if guess, ok := llmBySpeaker[vm.Speaker]; ok {
			if guess.Confidence < minLLMConfidence {
				logger.Info("fuse-scores: LLM guess filtered (low confidence)",
					"speaker", vm.Speaker, "llm_name", guess.GuessedName,
					"llm_confidence", guess.Confidence, "threshold", minLLMConfidence,
					"voice_score", vm.Score)
			} else if strings.EqualFold(guess.GuessedName, vm.ContactName) {
				// LLM agrees — compute weighted average, but never go below voice score.
				weighted := vm.Score*voiceWeight + guess.Confidence*llmWeight
				if weighted > combined {
					combined = weighted
					logger.Info("fuse-scores: LLM boosted voice match",
						"speaker", vm.Speaker, "contact", vm.ContactName,
						"voice_score", vm.Score, "llm_confidence", guess.Confidence,
						"fused_score", combined)
				} else {
					logger.Info("fuse-scores: LLM agrees but no boost needed",
						"speaker", vm.Speaker, "contact", vm.ContactName,
						"voice_score", vm.Score, "llm_confidence", guess.Confidence)
				}
			} else {
				logger.Info("fuse-scores: LLM disagrees, voice score preserved",
					"speaker", vm.Speaker, "voice_contact", vm.ContactName,
					"llm_name", guess.GuessedName, "voice_score", vm.Score)
			}
		}

		result = append(result, SpeakerMatch{
			Speaker:     vm.Speaker,
			ContactID:   vm.ContactID,
			ContactName: vm.ContactName,
			Score:       combined,
			Tier:        ClassifySpeakerMatch(combined),
		})
	}

	// Handle LLM-only guesses (no voice match for this speaker).
	for _, guess := range llmGuesses {
		if _, covered := coveredSpeakers[guess.Speaker]; covered {
			continue
		}
		// Skip low-confidence LLM-only guesses.
		if guess.Confidence < minLLMConfidence {
			logger.Info("fuse-scores: LLM-only guess filtered (low confidence)",
				"speaker", guess.Speaker, "llm_name", guess.GuessedName,
				"llm_confidence", guess.Confidence, "threshold", minLLMConfidence)
			continue
		}
		llmOnlyScore := guess.Confidence * llmWeight
		result = append(result, SpeakerMatch{
			Speaker:     guess.Speaker,
			ContactID:   0, // unknown; caller must resolve via name lookup if needed
			ContactName: guess.GuessedName,
			Score:       llmOnlyScore,
			Tier:        ClassifySpeakerMatch(llmOnlyScore),
		})
	}

	return result
}

// clampConfidence returns v clamped to the range [0.0, 1.0].
func clampConfidence(v float64) float64 {
	if v < 0.0 {
		return 0.0
	}
	if v > 1.0 {
		return 1.0
	}
	return v
}
