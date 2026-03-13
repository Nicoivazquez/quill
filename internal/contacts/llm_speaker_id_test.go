package contacts

import (
	"math"
	"strings"
	"testing"
)

// ---- BuildSpeakerIDPrompt ---------------------------------------------------

// TestBuildSpeakerIDPrompt_IncludesAllSpeakerLabels verifies that every speaker
// label appears in the constructed prompt.
func TestBuildSpeakerIDPrompt_IncludesAllSpeakerLabels(t *testing.T) {
	speakers := []string{"speaker_00", "speaker_01", "speaker_02"}
	contacts := []string{"Alice", "Bob"}
	transcript := "speaker_00: Hello\nspeaker_01: Hi there"

	prompt := BuildSpeakerIDPrompt(transcript, speakers, contacts)

	for _, label := range speakers {
		if !strings.Contains(prompt, label) {
			t.Errorf("prompt missing speaker label %q", label)
		}
	}
}

// TestBuildSpeakerIDPrompt_IncludesAllContactNames verifies that every contact
// name appears in the constructed prompt.
func TestBuildSpeakerIDPrompt_IncludesAllContactNames(t *testing.T) {
	speakers := []string{"speaker_00", "speaker_01"}
	contacts := []string{"Alice", "Bob", "Carol"}
	transcript := "speaker_00: Hello\nspeaker_01: Hi there"

	prompt := BuildSpeakerIDPrompt(transcript, speakers, contacts)

	for _, name := range contacts {
		if !strings.Contains(prompt, name) {
			t.Errorf("prompt missing contact name %q", name)
		}
	}
}

// TestBuildSpeakerIDPrompt_IncludesTranscriptText verifies the transcript body
// is embedded verbatim in the prompt.
func TestBuildSpeakerIDPrompt_IncludesTranscriptText(t *testing.T) {
	speakers := []string{"speaker_00"}
	contacts := []string{"Alice"}
	transcript := "speaker_00: The quick brown fox jumps over the lazy dog"

	prompt := BuildSpeakerIDPrompt(transcript, speakers, contacts)

	if !strings.Contains(prompt, transcript) {
		t.Errorf("prompt does not contain the transcript text")
	}
}

// TestBuildSpeakerIDPrompt_EmptySpeakersList returns a non-empty prompt that
// contains the transcript and contacts even when no speakers are listed.
func TestBuildSpeakerIDPrompt_EmptySpeakersList(t *testing.T) {
	speakers := []string{}
	contacts := []string{"Alice", "Bob"}
	transcript := "No speakers here"

	prompt := BuildSpeakerIDPrompt(transcript, speakers, contacts)

	if len(prompt) == 0 {
		t.Fatal("prompt must not be empty even with no speakers")
	}
	if !strings.Contains(prompt, transcript) {
		t.Errorf("prompt does not contain the transcript text when speakers list is empty")
	}
}

// TestBuildSpeakerIDPrompt_EmptyContactsList returns a non-empty prompt that
// still contains the transcript and speaker labels.
func TestBuildSpeakerIDPrompt_EmptyContactsList(t *testing.T) {
	speakers := []string{"speaker_00", "speaker_01"}
	contacts := []string{}
	transcript := "speaker_00: Hello"

	prompt := BuildSpeakerIDPrompt(transcript, speakers, contacts)

	if len(prompt) == 0 {
		t.Fatal("prompt must not be empty even with no contacts")
	}
	for _, label := range speakers {
		if !strings.Contains(prompt, label) {
			t.Errorf("prompt missing speaker label %q when contacts list is empty", label)
		}
	}
}

// TestBuildSpeakerIDPrompt_RequestsJSONOutput verifies the prompt instructs the
// LLM to respond with a JSON array (the format parseable by ParseLLMSpeakerGuesses).
func TestBuildSpeakerIDPrompt_RequestsJSONOutput(t *testing.T) {
	speakers := []string{"speaker_00"}
	contacts := []string{"Alice"}
	transcript := "speaker_00: Hello"

	prompt := BuildSpeakerIDPrompt(transcript, speakers, contacts)

	// The prompt must mention JSON so the LLM knows the expected output format.
	if !strings.Contains(strings.ToLower(prompt), "json") {
		t.Error("prompt should instruct the LLM to output JSON")
	}
}

// ---- ParseLLMSpeakerGuesses -------------------------------------------------

// TestParseLLMSpeakerGuesses_ValidJSON parses a well-formed JSON array and
// returns the expected LLMSpeakerGuess slice.
func TestParseLLMSpeakerGuesses_ValidJSON(t *testing.T) {
	speakerLabels := []string{"speaker_00", "speaker_01"}
	response := `[
		{"speaker": "speaker_00", "name": "Alice", "confidence": 0.9, "reasoning": "Introduces herself"},
		{"speaker": "speaker_01", "name": "Bob",   "confidence": 0.7, "reasoning": "Mentioned by Alice"}
	]`

	guesses := ParseLLMSpeakerGuesses(response, speakerLabels)

	if len(guesses) != 2 {
		t.Fatalf("expected 2 guesses, got %d", len(guesses))
	}

	// Verify first guess
	if guesses[0].Speaker != "speaker_00" {
		t.Errorf("guesses[0].Speaker = %q, want speaker_00", guesses[0].Speaker)
	}
	if guesses[0].GuessedName != "Alice" {
		t.Errorf("guesses[0].GuessedName = %q, want Alice", guesses[0].GuessedName)
	}
	if math.Abs(guesses[0].Confidence-0.9) > 1e-9 {
		t.Errorf("guesses[0].Confidence = %f, want 0.9", guesses[0].Confidence)
	}
	if guesses[0].Reasoning == "" {
		t.Error("guesses[0].Reasoning must not be empty")
	}

	// Verify second guess
	if guesses[1].Speaker != "speaker_01" {
		t.Errorf("guesses[1].Speaker = %q, want speaker_01", guesses[1].Speaker)
	}
	if guesses[1].GuessedName != "Bob" {
		t.Errorf("guesses[1].GuessedName = %q, want Bob", guesses[1].GuessedName)
	}
}

// TestParseLLMSpeakerGuesses_ExtraTextAroundJSON handles LLM responses that
// wrap the JSON array in prose (e.g., "Here is the result: [...]").
func TestParseLLMSpeakerGuesses_ExtraTextAroundJSON(t *testing.T) {
	speakerLabels := []string{"speaker_00"}
	response := `Sure! Here are my best guesses:
[{"speaker": "speaker_00", "name": "Alice", "confidence": 0.85, "reasoning": "Said her name"}]
I hope that helps!`

	guesses := ParseLLMSpeakerGuesses(response, speakerLabels)

	if len(guesses) != 1 {
		t.Fatalf("expected 1 guess from response with surrounding text, got %d", len(guesses))
	}
	if guesses[0].GuessedName != "Alice" {
		t.Errorf("GuessedName = %q, want Alice", guesses[0].GuessedName)
	}
}

// TestParseLLMSpeakerGuesses_InvalidJSON returns an empty slice without panic
// when the LLM returns something that cannot be parsed as JSON at all.
func TestParseLLMSpeakerGuesses_InvalidJSON(t *testing.T) {
	speakerLabels := []string{"speaker_00", "speaker_01"}
	response := "I cannot identify the speakers in this transcript."

	guesses := ParseLLMSpeakerGuesses(response, speakerLabels)

	if guesses == nil {
		t.Fatal("expected non-nil slice for invalid JSON, got nil")
	}
	if len(guesses) != 0 {
		t.Errorf("expected 0 guesses for invalid JSON, got %d: %+v", len(guesses), guesses)
	}
}

// TestParseLLMSpeakerGuesses_UnknownSpeakerLabel filters out any guesses that
// reference a speaker label not present in the provided speakerLabels list.
func TestParseLLMSpeakerGuesses_UnknownSpeakerLabel(t *testing.T) {
	speakerLabels := []string{"speaker_00"}
	// LLM hallucinates "speaker_99" which is not a real label.
	response := `[
		{"speaker": "speaker_00", "name": "Alice", "confidence": 0.8, "reasoning": "Known speaker"},
		{"speaker": "speaker_99", "name": "Ghost", "confidence": 0.5, "reasoning": "Hallucinated"}
	]`

	guesses := ParseLLMSpeakerGuesses(response, speakerLabels)

	if len(guesses) != 1 {
		t.Fatalf("expected 1 guess (unknown label filtered), got %d: %+v", len(guesses), guesses)
	}
	if guesses[0].Speaker != "speaker_00" {
		t.Errorf("surviving guess has wrong speaker: %q", guesses[0].Speaker)
	}
}

// TestParseLLMSpeakerGuesses_ConfidenceClampedToZeroOne ensures confidence
// values outside [0, 1] are clamped to the valid range.
func TestParseLLMSpeakerGuesses_ConfidenceClampedToZeroOne(t *testing.T) {
	speakerLabels := []string{"speaker_00", "speaker_01"}
	response := `[
		{"speaker": "speaker_00", "name": "Alice", "confidence": 1.5, "reasoning": "Over-confident"},
		{"speaker": "speaker_01", "name": "Bob",   "confidence": -0.3, "reasoning": "Negative"}
	]`

	guesses := ParseLLMSpeakerGuesses(response, speakerLabels)

	if len(guesses) != 2 {
		t.Fatalf("expected 2 guesses, got %d", len(guesses))
	}

	if guesses[0].Confidence > 1.0 {
		t.Errorf("confidence > 1.0 not clamped: got %f", guesses[0].Confidence)
	}
	if guesses[1].Confidence < 0.0 {
		t.Errorf("confidence < 0.0 not clamped: got %f", guesses[1].Confidence)
	}
}

// TestParseLLMSpeakerGuesses_EmptySpeakerLabels returns empty when the allowed
// speaker label set is empty (nothing can pass the filter).
func TestParseLLMSpeakerGuesses_EmptySpeakerLabels(t *testing.T) {
	speakerLabels := []string{}
	response := `[{"speaker": "speaker_00", "name": "Alice", "confidence": 0.9, "reasoning": "Hello"}]`

	guesses := ParseLLMSpeakerGuesses(response, speakerLabels)

	if len(guesses) != 0 {
		t.Errorf("expected 0 guesses when speakerLabels is empty, got %d", len(guesses))
	}
}

// ---- FuseScores -------------------------------------------------------------

// TestFuseScores_VoiceAndLLMAgreeSuggestTier tests case 1 from the spec:
// voice=0.75, LLM=0.80 matching same contact → combined=0.75*0.6+0.80*0.4=0.77 → TierSuggest.
func TestFuseScores_VoiceAndLLMAgreeSuggestTier(t *testing.T) {
	voiceMatches := []SpeakerMatch{
		{Speaker: "speaker_00", ContactID: 1, ContactName: "Alice", Score: 0.75, Tier: TierSuggest},
	}
	llmGuesses := []LLMSpeakerGuess{
		{Speaker: "speaker_00", GuessedName: "Alice", Confidence: 0.80},
	}

	result := FuseScores(voiceMatches, llmGuesses)

	if len(result) != 1 {
		t.Fatalf("expected 1 fused match, got %d", len(result))
	}

	wantScore := 0.75*0.6 + 0.80*0.4 // 0.77
	if math.Abs(result[0].Score-wantScore) > 1e-9 {
		t.Errorf("fused score = %f, want %f", result[0].Score, wantScore)
	}
	if result[0].Tier != TierSuggest {
		t.Errorf("tier = %q, want TierSuggest (score=%.4f)", result[0].Tier, result[0].Score)
	}
}

// TestFuseScores_LLMBoostNearAutoThreshold tests case 2:
// voice=0.70, LLM=0.90 → combined=0.70*0.6+0.90*0.4=0.78 → still TierSuggest (just below 0.80).
func TestFuseScores_LLMBoostNearAutoThreshold(t *testing.T) {
	voiceMatches := []SpeakerMatch{
		{Speaker: "speaker_00", ContactID: 1, ContactName: "Alice", Score: 0.70, Tier: TierSuggest},
	}
	llmGuesses := []LLMSpeakerGuess{
		{Speaker: "speaker_00", GuessedName: "Alice", Confidence: 0.90},
	}

	result := FuseScores(voiceMatches, llmGuesses)

	if len(result) != 1 {
		t.Fatalf("expected 1 fused match, got %d", len(result))
	}

	wantScore := 0.70*0.6 + 0.90*0.4 // 0.78
	if math.Abs(result[0].Score-wantScore) > 1e-9 {
		t.Errorf("fused score = %f, want %f", result[0].Score, wantScore)
	}
	// 0.78 < 0.80 → still TierSuggest
	if result[0].Tier != TierSuggest {
		t.Errorf("tier = %q, want TierSuggest (combined score is 0.78 < 0.80)", result[0].Tier)
	}
}

// TestFuseScores_LowVoiceHighLLMStillSuggest tests case 3:
// voice=0.65, LLM=0.95 → combined=0.65*0.6+0.95*0.4=0.77 → TierSuggest.
func TestFuseScores_LowVoiceHighLLMStillSuggest(t *testing.T) {
	voiceMatches := []SpeakerMatch{
		{Speaker: "speaker_00", ContactID: 2, ContactName: "Bob", Score: 0.65, Tier: TierSuggest},
	}
	llmGuesses := []LLMSpeakerGuess{
		{Speaker: "speaker_00", GuessedName: "Bob", Confidence: 0.95},
	}

	result := FuseScores(voiceMatches, llmGuesses)

	if len(result) != 1 {
		t.Fatalf("expected 1 fused match, got %d", len(result))
	}

	wantScore := 0.65*0.6 + 0.95*0.4 // 0.77
	if math.Abs(result[0].Score-wantScore) > 1e-9 {
		t.Errorf("fused score = %f, want %f", result[0].Score, wantScore)
	}
	if result[0].Tier != TierSuggest {
		t.Errorf("tier = %q, want TierSuggest (score=%.4f)", result[0].Tier, result[0].Score)
	}
}

// TestFuseScores_HighVoiceLLMConfirmsAutoTier tests case 4:
// voice=0.82, LLM=0.90 → combined=0.82*0.6+0.90*0.4=0.852 → TierAutoAssign.
func TestFuseScores_HighVoiceLLMConfirmsAutoTier(t *testing.T) {
	voiceMatches := []SpeakerMatch{
		{Speaker: "speaker_00", ContactID: 3, ContactName: "Carol", Score: 0.82, Tier: TierAutoAssign},
	}
	llmGuesses := []LLMSpeakerGuess{
		{Speaker: "speaker_00", GuessedName: "Carol", Confidence: 0.90},
	}

	result := FuseScores(voiceMatches, llmGuesses)

	if len(result) != 1 {
		t.Fatalf("expected 1 fused match, got %d", len(result))
	}

	wantScore := 0.82*0.6 + 0.90*0.4 // 0.852
	if math.Abs(result[0].Score-wantScore) > 1e-9 {
		t.Errorf("fused score = %f, want %f", result[0].Score, wantScore)
	}
	if result[0].Tier != TierAutoAssign {
		t.Errorf("tier = %q, want TierAutoAssign (score=%.4f >= 0.80)", result[0].Tier, result[0].Score)
	}
}

// TestFuseScores_LLMDisagrees tests case 5: when the LLM guesses a different
// name than the voice-matched contact, only the voice score is used (LLM score
// for that contact is effectively 0.0).
func TestFuseScores_LLMDisagrees(t *testing.T) {
	voiceMatches := []SpeakerMatch{
		{Speaker: "speaker_00", ContactID: 1, ContactName: "Alice", Score: 0.75, Tier: TierSuggest},
	}
	// LLM thinks it's Dave, not Alice — disagreement means LLM score for Alice = 0.
	llmGuesses := []LLMSpeakerGuess{
		{Speaker: "speaker_00", GuessedName: "Dave", Confidence: 0.80},
	}

	result := FuseScores(voiceMatches, llmGuesses)

	if len(result) != 1 {
		t.Fatalf("expected 1 fused match, got %d", len(result))
	}

	// LLM disagrees → llm_score for Alice = 0.0 → combined = 0.75*0.6 + 0.0*0.4 = 0.45
	// 0.45 < 0.60 → TierUnknown; the entry should still exist so the caller can decide
	// or the voice match is used unchanged (design choice: use voice score only).
	// Per spec: "only voice score used (LLM=0.0 for that contact)" → combined = 0.75*0.6 = 0.45
	wantScore := 0.75*0.6 + 0.0*0.4 // 0.45
	if math.Abs(result[0].Score-wantScore) > 1e-9 {
		t.Errorf("fused score = %f, want %f (disagreement → llm_score=0)", result[0].Score, wantScore)
	}
}

// TestFuseScores_NoVoiceMatchLLMOnly tests case 6:
// No voice match exists, but LLM suggests a speaker → score = 0.0*0.6 + llm_score*0.4.
func TestFuseScores_NoVoiceMatchLLMOnly(t *testing.T) {
	voiceMatches := []SpeakerMatch{} // nothing from voice matching
	llmGuesses := []LLMSpeakerGuess{
		{Speaker: "speaker_00", GuessedName: "Alice", Confidence: 0.85},
	}

	result := FuseScores(voiceMatches, llmGuesses)

	// Should produce one entry for the LLM-only suggestion.
	if len(result) != 1 {
		t.Fatalf("expected 1 LLM-only entry, got %d: %+v", len(result), result)
	}

	wantScore := 0.0*0.6 + 0.85*0.4 // 0.34
	if math.Abs(result[0].Score-wantScore) > 1e-9 {
		t.Errorf("LLM-only score = %f, want %f", result[0].Score, wantScore)
	}
	if result[0].Speaker != "speaker_00" {
		t.Errorf("Speaker = %q, want speaker_00", result[0].Speaker)
	}
	if result[0].ContactName != "Alice" {
		t.Errorf("ContactName = %q, want Alice", result[0].ContactName)
	}
}

// TestFuseScores_EmptyLLMGuesses returns voice matches unchanged when there are
// no LLM guesses at all.
func TestFuseScores_EmptyLLMGuesses(t *testing.T) {
	voiceMatches := []SpeakerMatch{
		{Speaker: "speaker_00", ContactID: 1, ContactName: "Alice", Score: 0.75, Tier: TierSuggest},
		{Speaker: "speaker_01", ContactID: 2, ContactName: "Bob", Score: 0.85, Tier: TierAutoAssign},
	}
	llmGuesses := []LLMSpeakerGuess{}

	result := FuseScores(voiceMatches, llmGuesses)

	if len(result) != 2 {
		t.Fatalf("expected 2 matches unchanged, got %d", len(result))
	}

	// Scores and tiers must be identical to the original voice matches.
	scoreByContact := make(map[uint]float64)
	for _, m := range result {
		scoreByContact[m.ContactID] = m.Score
	}

	for _, vm := range voiceMatches {
		if math.Abs(scoreByContact[vm.ContactID]-vm.Score) > 1e-9 {
			t.Errorf("voice match for contact %d: score changed from %f to %f",
				vm.ContactID, vm.Score, scoreByContact[vm.ContactID])
		}
	}
}

// TestFuseScores_EmptyVoiceMatchesWithLLMGuesses tests case 8:
// No voice matches at all, multiple LLM guesses → each becomes an entry with
// score = 0.4 * llm_confidence.
func TestFuseScores_EmptyVoiceMatchesWithLLMGuesses(t *testing.T) {
	voiceMatches := []SpeakerMatch{}
	llmGuesses := []LLMSpeakerGuess{
		{Speaker: "speaker_00", GuessedName: "Alice", Confidence: 0.90},
		{Speaker: "speaker_01", GuessedName: "Bob", Confidence: 0.70},
	}

	result := FuseScores(voiceMatches, llmGuesses)

	if len(result) != 2 {
		t.Fatalf("expected 2 LLM-only entries, got %d: %+v", len(result), result)
	}

	// Build a map for easy lookup.
	byLabel := make(map[string]SpeakerMatch)
	for _, r := range result {
		byLabel[r.Speaker] = r
	}

	wantAlice := 0.0*0.6 + 0.90*0.4 // 0.36
	wantBob := 0.0*0.6 + 0.70*0.4   // 0.28

	if math.Abs(byLabel["speaker_00"].Score-wantAlice) > 1e-9 {
		t.Errorf("speaker_00 score = %f, want %f", byLabel["speaker_00"].Score, wantAlice)
	}
	if math.Abs(byLabel["speaker_01"].Score-wantBob) > 1e-9 {
		t.Errorf("speaker_01 score = %f, want %f", byLabel["speaker_01"].Score, wantBob)
	}
}

// TestFuseScores_TierReclassifiedAfterFusion verifies that after score fusion
// the Tier field reflects the NEW combined score, not the original voice tier.
func TestFuseScores_TierReclassifiedAfterFusion(t *testing.T) {
	// voice=0.78 → TierSuggest; after LLM boost: 0.78*0.6+0.90*0.4=0.828 → TierAutoAssign.
	voiceMatches := []SpeakerMatch{
		{Speaker: "speaker_00", ContactID: 1, ContactName: "Alice", Score: 0.78, Tier: TierSuggest},
	}
	llmGuesses := []LLMSpeakerGuess{
		{Speaker: "speaker_00", GuessedName: "Alice", Confidence: 0.90},
	}

	result := FuseScores(voiceMatches, llmGuesses)

	if len(result) != 1 {
		t.Fatalf("expected 1 fused match, got %d", len(result))
	}

	wantScore := 0.78*0.6 + 0.90*0.4 // 0.828
	if math.Abs(result[0].Score-wantScore) > 1e-9 {
		t.Errorf("fused score = %f, want %f", result[0].Score, wantScore)
	}
	// 0.828 >= 0.80 → should be promoted to TierAutoAssign.
	if result[0].Tier != TierAutoAssign {
		t.Errorf("tier after boost = %q, want TierAutoAssign (score=%.4f)", result[0].Tier, result[0].Score)
	}
}

// TestFuseScores_BothEmpty returns empty slice without panic.
func TestFuseScores_BothEmpty(t *testing.T) {
	result := FuseScores([]SpeakerMatch{}, []LLMSpeakerGuess{})
	if result == nil {
		t.Fatal("expected non-nil slice for empty inputs")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 results for both-empty inputs, got %d", len(result))
	}
}

// TestFuseScores_NilInputs returns empty slice without panic when both inputs
// are nil.
func TestFuseScores_NilInputs(t *testing.T) {
	result := FuseScores(nil, nil)
	if result == nil {
		t.Fatal("expected non-nil slice for nil inputs")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 results for nil inputs, got %d", len(result))
	}
}

// TestFuseScores_OriginalVoiceMatchFieldsPreserved verifies that ContactID,
// ContactName, and Speaker are preserved through the fusion step.
func TestFuseScores_OriginalVoiceMatchFieldsPreserved(t *testing.T) {
	voiceMatches := []SpeakerMatch{
		{Speaker: "speaker_00", ContactID: 42, ContactName: "Zara", Score: 0.82, Tier: TierAutoAssign},
	}
	llmGuesses := []LLMSpeakerGuess{
		{Speaker: "speaker_00", GuessedName: "Zara", Confidence: 0.88},
	}

	result := FuseScores(voiceMatches, llmGuesses)

	if len(result) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result))
	}
	if result[0].Speaker != "speaker_00" {
		t.Errorf("Speaker = %q, want speaker_00", result[0].Speaker)
	}
	if result[0].ContactID != 42 {
		t.Errorf("ContactID = %d, want 42", result[0].ContactID)
	}
	if result[0].ContactName != "Zara" {
		t.Errorf("ContactName = %q, want Zara", result[0].ContactName)
	}
}

// ---- LLMSpeakerIDConfig struct -----------------------------------------------

// TestLLMSpeakerIDConfig_FieldsAccessible verifies the config struct can be
// created and its fields read without issues.
func TestLLMSpeakerIDConfig_FieldsAccessible(t *testing.T) {
	cfg := LLMSpeakerIDConfig{
		Provider: "openai",
		APIKey:   "sk-test",
		BaseURL:  "https://api.openai.com",
		Model:    "gpt-4o-mini",
	}

	if cfg.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", cfg.Provider)
	}
	if cfg.APIKey != "sk-test" {
		t.Errorf("APIKey = %q, want sk-test", cfg.APIKey)
	}
	if cfg.BaseURL != "https://api.openai.com" {
		t.Errorf("BaseURL = %q, want https://api.openai.com", cfg.BaseURL)
	}
	if cfg.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want gpt-4o-mini", cfg.Model)
	}
}
