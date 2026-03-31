package contacts

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// mockLLMCaller returns a fixed response regardless of prompt. The speaker
// labels and contact names embedded in the fixture let us control which
// speakers the LLM "agrees" or "disagrees" with.
func mockLLMCaller(response string) LLMCaller {
	return func(_ context.Context, _ string) (string, error) {
		return response, nil
	}
}

// TestLabelSpeakers_NilLLMCaller_VoiceOnly verifies that when no LLMCaller is
// set, LabelSpeakers behaves identically to the voice-only path (no regression).
func TestLabelSpeakers_NilLLMCaller_VoiceOnly(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	vaultPath := filepath.Join(t.TempDir(), "vault")
	seedVaultAndJob(t, db, vaultPath, "job-nil-llm")

	// Contact "Alice" with unit vector at index 0.
	seedContactWithEmbedding(t, db, vaultPath, 1, "Alice", makeUnitBasisForAutoLabel(256, 0))

	// Speaker embedding nearly identical to Alice's — should auto-assign.
	speakerEmb := makeUnitBasisForAutoLabel(256, 0)
	speakerEmb[1] = 0.01

	// LLMCaller is nil (default) — voice-only.
	result, err := svc.LabelSpeakers(context.Background(), 1, vaultPath, "job-nil-llm",
		map[string][]float64{"speaker_00": speakerEmb}, "some transcript text")
	if err != nil {
		t.Fatalf("LabelSpeakers: %v", err)
	}

	if len(result.AutoAssigned) != 1 {
		t.Fatalf("expected 1 auto-assigned, got %d", len(result.AutoAssigned))
	}
	if result.AutoAssigned[0].ContactName != "Alice" {
		t.Errorf("expected contact name Alice, got %q", result.AutoAssigned[0].ContactName)
	}
}

// TestLabelSpeakers_LLMFusion_BoostsSuggestToAuto verifies that when the LLM
// agrees with a voice match that is in the suggest tier, the fused score is
// high enough to promote it to auto-assign.
//
// Setup: voice cosine = ~0.45 (suggest tier).
// LLM agrees with confidence 1.0 → combined = 0.45 * 0.6 + 1.0 * 0.4 = 0.67 → auto.
func TestLabelSpeakers_LLMFusion_BoostsSuggestToAuto(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	vaultPath := filepath.Join(t.TempDir(), "vault")
	seedVaultAndJob(t, db, vaultPath, "job-llm-boost")

	// Create contact "Alice" with a known embedding.
	contactVec := makeUnitBasisForAutoLabel(256, 0)
	seedContactWithEmbedding(t, db, vaultPath, 1, "Alice", contactVec)

	// Build a speaker embedding that produces ~0.45 cosine similarity with Alice.
	// Strategy: mix unit basis 0 with unit basis 1 to get partial overlap.
	speakerEmb := makeMixedVectorForFusion(256, 0, 1, 0.45)

	// Verify our embedding is in the suggest tier before fusion.
	voiceSim := CosineSimilarity(speakerEmb, contactVec)
	voiceTier := ClassifySpeakerMatch(voiceSim)
	if voiceTier != TierSuggest {
		t.Fatalf("pre-check: voice similarity %.4f is tier %q, want suggest (0.35-0.49)",
			voiceSim, voiceTier)
	}

	// LLM response: agrees that speaker_00 is Alice with high confidence.
	llmResponse := `[{"speaker": "speaker_00", "name": "Alice", "confidence": 1.0, "reasoning": "self-introduced"}]`
	svc.SetLLMCaller(mockLLMCaller(llmResponse))

	result, err := svc.LabelSpeakers(context.Background(), 1, vaultPath, "job-llm-boost",
		map[string][]float64{"speaker_00": speakerEmb},
		"Alice: Hello everyone, I'm Alice.")
	if err != nil {
		t.Fatalf("LabelSpeakers: %v", err)
	}

	// After fusion, the suggest-tier match should be promoted to auto-assign.
	if len(result.AutoAssigned) != 1 {
		t.Errorf("expected 1 auto-assigned after LLM boost, got %d (suggestions: %d, unmatched: %v)",
			len(result.AutoAssigned), len(result.Suggestions), result.Unmatched)
	}
	if len(result.Suggestions) != 0 {
		t.Errorf("expected 0 suggestions after LLM boost, got %d", len(result.Suggestions))
	}

	// Verify persisted mapping reflects fused score.
	mappings, err := svc.speakerMapRepo.ListByJob(context.Background(), "job-llm-boost")
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 persisted mapping, got %d", len(mappings))
	}
	m := mappings[0]
	// Fused score should be approximately voiceSim * 0.6 + 1.0 * 0.4
	expectedMin := voiceSim*0.6 + 1.0*0.4 - 0.01
	if m.ConfidenceScore < expectedMin {
		t.Errorf("fused ConfidenceScore %f should be >= %f", m.ConfidenceScore, expectedMin)
	}
	if m.MatchTier != "auto" {
		t.Errorf("MatchTier: got %q, want %q", m.MatchTier, "auto")
	}
}

// TestLabelSpeakers_LLMFusion_EmptyTranscript_FallsBackToVoice verifies that
// when LLMCaller is set but transcriptText is empty, the service skips the LLM
// call and falls back to voice-only matching.
func TestLabelSpeakers_LLMFusion_EmptyTranscript_FallsBackToVoice(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	vaultPath := filepath.Join(t.TempDir(), "vault")
	seedVaultAndJob(t, db, vaultPath, "job-empty-txt")

	contactVec := makeUnitBasisForAutoLabel(256, 0)
	seedContactWithEmbedding(t, db, vaultPath, 1, "Alice", contactVec)

	// Speaker with suggest-tier similarity.
	speakerEmb := makeMixedVectorForFusion(256, 0, 1, 0.45)

	// Set LLM caller that should NOT be called.
	called := false
	svc.SetLLMCaller(func(_ context.Context, _ string) (string, error) {
		called = true
		return `[{"speaker": "speaker_00", "name": "Alice", "confidence": 1.0, "reasoning": "test"}]`, nil
	})

	result, err := svc.LabelSpeakers(context.Background(), 1, vaultPath, "job-empty-txt",
		map[string][]float64{"speaker_00": speakerEmb},
		"") // empty transcript
	if err != nil {
		t.Fatalf("LabelSpeakers: %v", err)
	}

	if called {
		t.Error("LLMCaller should NOT be called when transcriptText is empty")
	}

	// Voice-only: should be in suggest tier, not auto-assigned.
	if len(result.AutoAssigned) != 0 {
		t.Errorf("expected 0 auto-assigned (voice-only fallback), got %d", len(result.AutoAssigned))
	}
	if len(result.Suggestions) != 1 {
		t.Errorf("expected 1 suggestion (voice-only), got %d", len(result.Suggestions))
	}
}

// TestLabelSpeakers_LLMFusion_Disagreement_PreservesVoice verifies that when
// the LLM disagrees with a voice match, the voice score is preserved unchanged
// (boost-only: LLM can never penalize a voice match).
func TestLabelSpeakers_LLMFusion_Disagreement_PreservesVoice(t *testing.T) {
	db, svc := setupAutoLabelTestHarness(t)

	vaultPath := filepath.Join(t.TempDir(), "vault")
	seedVaultAndJob(t, db, vaultPath, "job-llm-disagree")

	contactVec := makeUnitBasisForAutoLabel(256, 0)
	seedContactWithEmbedding(t, db, vaultPath, 1, "Alice", contactVec)

	// Speaker with a score just above auto threshold (~0.55).
	speakerEmb := makeMixedVectorForFusion(256, 0, 1, 0.55)

	// Verify pre-fusion voice score is auto-assign tier.
	voiceSim := CosineSimilarity(speakerEmb, contactVec)
	if ClassifySpeakerMatch(voiceSim) != TierAutoAssign {
		t.Fatalf("pre-check: voice similarity %.4f should be auto tier", voiceSim)
	}

	// LLM disagrees: thinks speaker_00 is "Bob" (not Alice).
	llmResponse := `[{"speaker": "speaker_00", "name": "Bob", "confidence": 0.9, "reasoning": "mentioned Bob"}]`
	svc.SetLLMCaller(mockLLMCaller(llmResponse))

	result, err := svc.LabelSpeakers(context.Background(), 1, vaultPath, "job-llm-disagree",
		map[string][]float64{"speaker_00": speakerEmb},
		"Bob: Hi I'm Bob.")
	if err != nil {
		t.Fatalf("LabelSpeakers: %v", err)
	}

	// Boost-only: LLM disagreement preserves voice score unchanged.
	// voiceSim ~0.55 → auto tier preserved.
	if len(result.AutoAssigned) != 1 {
		t.Errorf("expected 1 auto-assigned (voice score preserved), got %d (suggestions=%d, unmatched=%v)",
			len(result.AutoAssigned), len(result.Suggestions), result.Unmatched)
	}
	if len(result.Unmatched) != 0 {
		t.Errorf("expected 0 unmatched (voice score preserved), got %v", result.Unmatched)
	}
}

// --- helpers ---

// makeMixedVectorForFusion creates a 256-dim vector that has approximately
// targetSimilarity cosine similarity with a unit basis vector at index primary.
// It mixes the primary basis with a secondary basis to achieve the target.
func makeMixedVectorForFusion(dim, primary, secondary int, targetSimilarity float64) []float64 {
	// For unit vectors: cos(v, e_primary) = v[primary] / ||v||
	// If v = a * e_primary + b * e_secondary, then ||v|| = sqrt(a² + b²)
	// cos = a / sqrt(a² + b²) = targetSimilarity
	// Solving: a² = target² * (a² + b²)
	// => a² (1 - target²) = target² * b²
	// => (a/b)² = target² / (1 - target²)
	// Set b = 1, then a = target / sqrt(1 - target²)
	if targetSimilarity >= 1.0 {
		return makeUnitBasisForAutoLabel(dim, primary)
	}
	if targetSimilarity <= 0.0 {
		return makeUnitBasisForAutoLabel(dim, secondary)
	}

	a := targetSimilarity / sqrt1MinusSquared(targetSimilarity)
	b := 1.0

	v := make([]float64, dim)
	if primary < dim {
		v[primary] = a
	}
	if secondary < dim {
		v[secondary] = b
	}
	return v
}

func sqrt1MinusSquared(x float64) float64 {
	val := 1.0 - x*x
	if val <= 0 {
		return 1e-10
	}
	return sqrt(val)
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton's method.
	z := x
	for i := 0; i < 100; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// verifyVectorSimilarity is a debugging helper (not used in tests but useful
// for development).
func verifyVectorSimilarity(t *testing.T, a, b []float64, label string) {
	t.Helper()
	sim := CosineSimilarity(a, b)
	tier := ClassifySpeakerMatch(sim)
	fmt.Printf("  %s: cosine=%.4f tier=%s\n", label, sim, tier)
}
