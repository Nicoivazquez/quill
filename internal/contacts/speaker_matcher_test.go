package contacts

import (
	"math"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

// makeUnitBasis returns a length-n vector that is 1.0 at position idx and
// 0.0 everywhere else.  Two such vectors are orthogonal unless they share idx.
func makeUnitBasis(n, idx int) []float64 {
	v := make([]float64, n)
	if idx >= 0 && idx < n {
		v[idx] = 1.0
	}
	return v
}

// makeScaledVec returns a vector where every element equals scale.
func makeScaledVec(n int, scale float64) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = scale
	}
	return v
}

// nearlyIdentical returns a copy of v with a tiny epsilon nudge on index 0,
// producing cosine similarity very close to 1.0 (well above the 0.80 threshold).
func nearlyIdentical(v []float64) []float64 {
	cp := make([]float64, len(v))
	copy(cp, v)
	if len(cp) > 0 {
		cp[0] += 0.0001
	}
	return cp
}

// vecWithCosine constructs a 2-D unit vector whose cosine similarity against
// (1, 0) equals targetCos.  This gives a mathematically exact score.
// The returned vector has length 2; pad with zeros if needed for longer dims.
func vecWithCosine(targetCos float64) []float64 {
	sin := math.Sqrt(math.Max(0, 1.0-targetCos*targetCos))
	return []float64{targetCos, sin}
}

// ---- MatchSpeakers ----------------------------------------------------------

// TestMatchSpeakers_SingleSpeaker_SingleContact_HighScore tests that a speaker
// with a near-identical voice embedding to a contact is auto-assigned.
func TestMatchSpeakers_SingleSpeaker_SingleContact_HighScore(t *testing.T) {
	base := makeScaledVec(256, 0.01)
	speakerVec := nearlyIdentical(base)

	contacts := []ContactEmbedding{
		{ContactID: 1, ContactName: "Alice", Vector: base},
	}
	speakers := map[string][]float64{
		"speaker_00": speakerVec,
	}

	result := MatchSpeakers(speakers, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
	m := result.Matches[0]
	if m.Speaker != "speaker_00" {
		t.Errorf("speaker: got %q, want %q", m.Speaker, "speaker_00")
	}
	if m.ContactID != 1 {
		t.Errorf("contact_id: got %d, want 1", m.ContactID)
	}
	if m.ContactName != "Alice" {
		t.Errorf("contact_name: got %q, want %q", m.ContactName, "Alice")
	}
	if m.Tier != TierAutoAssign {
		t.Errorf("tier: got %q, want %q", m.Tier, TierAutoAssign)
	}
	if m.Score < 0.80 {
		t.Errorf("score: got %f, want >= 0.80", m.Score)
	}
	if len(result.Unmatched) != 0 {
		t.Errorf("unmatched: got %v, want none", result.Unmatched)
	}
}

// TestMatchSpeakers_SingleSpeaker_SingleContact_MidScore tests the suggest tier.
// Uses vecWithCosine(0.70) so the similarity is guaranteed to land in [0.60, 0.80).
func TestMatchSpeakers_SingleSpeaker_SingleContact_MidScore(t *testing.T) {
	// contactVec = (1, 0): the reference direction.
	// speakerVec = (0.70, sin(arccos(0.70))): cosine similarity with contactVec = 0.70.
	contactVec := []float64{1.0, 0.0}
	speakerVec := vecWithCosine(0.70)

	score := CosineSimilarity(contactVec, speakerVec)
	if score < 0.60 || score >= 0.80 {
		t.Fatalf("test setup error: expected score in [0.60, 0.80), got %f", score)
	}

	speakers := map[string][]float64{
		"speaker_00": speakerVec,
	}
	contacts := []ContactEmbedding{
		{ContactID: 2, ContactName: "Bob", Vector: contactVec},
	}

	result := MatchSpeakers(speakers, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
	if result.Matches[0].Tier != TierSuggest {
		t.Errorf("tier: got %q, want %q (score=%f)", result.Matches[0].Tier, TierSuggest, score)
	}
	if len(result.Unmatched) != 0 {
		t.Errorf("unmatched: got %v, want none", result.Unmatched)
	}
}

// TestMatchSpeakers_SingleSpeaker_SingleContact_LowScore tests the unknown tier.
// A score below 0.60 means the speaker should appear in Unmatched, not Matches.
func TestMatchSpeakers_SingleSpeaker_SingleContact_LowScore(t *testing.T) {
	// Orthogonal vectors → cosine = 0.0, well below 0.60.
	vecA := makeUnitBasis(4, 0) // (1, 0, 0, 0)
	vecB := makeUnitBasis(4, 1) // (0, 1, 0, 0)

	speakers := map[string][]float64{
		"speaker_00": vecA,
	}
	contacts := []ContactEmbedding{
		{ContactID: 3, ContactName: "Carol", Vector: vecB},
	}

	result := MatchSpeakers(speakers, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 0 {
		t.Errorf("expected 0 matches for low-score pair, got %d: %+v", len(result.Matches), result.Matches)
	}
	if len(result.Unmatched) != 1 || result.Unmatched[0] != "speaker_00" {
		t.Errorf("unmatched: got %v, want [speaker_00]", result.Unmatched)
	}
}

// TestMatchSpeakers_TwoSpeakers_TwoContacts_ClearMatches verifies that each
// speaker is matched to the correct contact when the vectors are clearly distinct.
func TestMatchSpeakers_TwoSpeakers_TwoContacts_ClearMatches(t *testing.T) {
	// speaker_00 ↔ Alice: identical vectors (score ≈ 1.0)
	// speaker_01 ↔ Bob:   identical vectors (score ≈ 1.0)
	// Cross-matches: orthogonal (score = 0.0)
	aliceVec := makeUnitBasis(4, 0)
	bobVec := makeUnitBasis(4, 1)

	speakers := map[string][]float64{
		"speaker_00": nearlyIdentical(aliceVec),
		"speaker_01": nearlyIdentical(bobVec),
	}
	contacts := []ContactEmbedding{
		{ContactID: 10, ContactName: "Alice", Vector: aliceVec},
		{ContactID: 11, ContactName: "Bob", Vector: bobVec},
	}

	result := MatchSpeakers(speakers, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Unmatched) != 0 {
		t.Errorf("unmatched: got %v, want none", result.Unmatched)
	}
	if len(result.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(result.Matches), result.Matches)
	}

	byContact := make(map[uint]string)
	for _, m := range result.Matches {
		byContact[m.ContactID] = m.Speaker
	}

	if byContact[10] != "speaker_00" {
		t.Errorf("Alice (contact 10): got speaker %q, want speaker_00", byContact[10])
	}
	if byContact[11] != "speaker_01" {
		t.Errorf("Bob (contact 11): got speaker %q, want speaker_01", byContact[11])
	}
}

// TestMatchSpeakers_TwoSpeakers_OneContact_OnlyBestWins verifies that when two
// speakers both match the same contact, only the higher-scoring speaker is
// recorded in Matches; the other goes to Unmatched.
func TestMatchSpeakers_TwoSpeakers_OneContact_OnlyBestWins(t *testing.T) {
	base := makeScaledVec(256, 0.01)

	// speaker_00 is nearly identical → higher score.
	// speaker_01 is the same vector → also identical.
	// Both score ≈ 1.0, but only one contact exists.
	// The one NOT assigned must end up in Unmatched.
	speakers := map[string][]float64{
		"speaker_00": nearlyIdentical(base),
		"speaker_01": nearlyIdentical(base),
	}
	contacts := []ContactEmbedding{
		{ContactID: 20, ContactName: "Dave", Vector: base},
	}

	result := MatchSpeakers(speakers, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected exactly 1 match (only best wins), got %d: %+v", len(result.Matches), result.Matches)
	}
	if len(result.Unmatched) != 1 {
		t.Fatalf("expected exactly 1 unmatched speaker, got %d: %+v", len(result.Unmatched), result.Unmatched)
	}
}

// TestMatchSpeakers_NoSpeakerEmbeddings returns an empty result without panic.
func TestMatchSpeakers_NoSpeakerEmbeddings(t *testing.T) {
	contacts := []ContactEmbedding{
		{ContactID: 1, ContactName: "Eve", Vector: makeScaledVec(4, 0.5)},
	}

	result := MatchSpeakers(map[string][]float64{}, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(result.Matches))
	}
	if len(result.Unmatched) != 0 {
		t.Errorf("expected 0 unmatched, got %d", len(result.Unmatched))
	}
}

// TestMatchSpeakers_NoContactEmbeddings places all speakers in Unmatched.
func TestMatchSpeakers_NoContactEmbeddings(t *testing.T) {
	speakers := map[string][]float64{
		"speaker_00": makeScaledVec(4, 0.5),
		"speaker_01": makeScaledVec(4, 0.3),
	}

	result := MatchSpeakers(speakers, []ContactEmbedding{})

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(result.Matches))
	}
	if len(result.Unmatched) != 2 {
		t.Errorf("expected 2 unmatched, got %d: %v", len(result.Unmatched), result.Unmatched)
	}
}

// TestMatchSpeakers_NilInputs does not panic and returns an empty result.
func TestMatchSpeakers_NilInputs(t *testing.T) {
	result := MatchSpeakers(nil, nil)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 0 {
		t.Errorf("expected 0 matches for nil inputs, got %d", len(result.Matches))
	}
	if len(result.Unmatched) != 0 {
		t.Errorf("expected 0 unmatched for nil inputs, got %d", len(result.Unmatched))
	}
}

// TestMatchSpeakers_TwoSpeakers_SameContact_HigherScoreWins verifies the
// conflict-resolution rule: when both speakers best-match the same contact,
// the speaker with the higher cosine score claims the contact.
func TestMatchSpeakers_TwoSpeakers_SameContact_HigherScoreWins(t *testing.T) {
	// contactVec = (1, 0): the reference direction.
	// speaker_high = vecWithCosine(0.95) → score ≈ 0.95 → auto tier
	// speaker_low  = vecWithCosine(0.65) → score ≈ 0.65 → suggest tier
	contactVec := []float64{1.0, 0.0}
	highVec := vecWithCosine(0.95)
	lowVec := vecWithCosine(0.65)

	scoreHigh := CosineSimilarity(contactVec, highVec)
	scoreLow := CosineSimilarity(contactVec, lowVec)

	if scoreHigh <= scoreLow {
		t.Fatalf("test setup error: high score (%f) not greater than low score (%f)", scoreHigh, scoreLow)
	}
	if scoreLow < 0.60 {
		t.Fatalf("test setup error: low score %f is below 0.60 threshold", scoreLow)
	}

	speakers := map[string][]float64{
		"speaker_high": highVec,
		"speaker_low":  lowVec,
	}
	contacts := []ContactEmbedding{
		{ContactID: 30, ContactName: "Frank", Vector: contactVec},
	}

	result := MatchSpeakers(speakers, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(result.Matches), result.Matches)
	}
	if result.Matches[0].Speaker != "speaker_high" {
		t.Errorf("higher-score speaker should win: got %q, want speaker_high", result.Matches[0].Speaker)
	}
	if len(result.Unmatched) != 1 || result.Unmatched[0] != "speaker_low" {
		t.Errorf("lower-score speaker should be unmatched: got %v", result.Unmatched)
	}
}

// TestMatchSpeakers_EmptyVectors does not panic and treats empty vectors as
// zero similarity (below threshold → unknown tier → unmatched).
func TestMatchSpeakers_EmptyVectors(t *testing.T) {
	speakers := map[string][]float64{
		"speaker_00": {},
	}
	contacts := []ContactEmbedding{
		{ContactID: 5, ContactName: "Grace", Vector: []float64{}},
	}

	result := MatchSpeakers(speakers, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Empty vectors → cosine = 0 → below threshold → unmatched.
	if len(result.Matches) != 0 {
		t.Errorf("expected 0 matches for empty vectors, got %d", len(result.Matches))
	}
	if len(result.Unmatched) != 1 {
		t.Errorf("expected 1 unmatched for empty vectors, got %d: %v", len(result.Unmatched), result.Unmatched)
	}
}

// TestMatchSpeakers_ResultStructure verifies the struct fields are populated
// consistently: Score, Tier, ContactID, ContactName, Speaker.
func TestMatchSpeakers_ResultStructure(t *testing.T) {
	base := makeScaledVec(64, 0.1)

	speakers := map[string][]float64{
		"speaker_00": nearlyIdentical(base),
	}
	contacts := []ContactEmbedding{
		{ContactID: 99, ContactName: "Hugo", Vector: base},
	}

	result := MatchSpeakers(speakers, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
	m := result.Matches[0]
	if m.Speaker == "" {
		t.Error("Speaker field must not be empty")
	}
	if m.ContactID == 0 {
		t.Error("ContactID field must not be zero")
	}
	if m.ContactName == "" {
		t.Error("ContactName field must not be empty")
	}
	if m.Score <= 0 {
		t.Errorf("Score must be positive, got %f", m.Score)
	}
	tier := ClassifySpeakerMatch(m.Score)
	if m.Tier != tier {
		t.Errorf("Tier %q does not match ClassifySpeakerMatch(%f) = %q", m.Tier, m.Score, tier)
	}
}

// TestMatchSpeakers_SuggestTierWithKnownScores uses vecWithCosine to construct
// a speaker vector whose cosine similarity against the contact is exactly 0.70.
func TestMatchSpeakers_SuggestTierWithKnownScores(t *testing.T) {
	// contactVec = (1, 0): the reference unit axis.
	// speakerVec = (0.70, sinθ) where sinθ = sqrt(1 - 0.70^2).
	// cosine(contactVec, speakerVec) = 0.70 → TierSuggest.
	contactVec := []float64{1.0, 0.0}
	speakerVec := vecWithCosine(0.70)

	score := CosineSimilarity(contactVec, speakerVec)
	if score < 0.60 || score >= 0.80 {
		t.Fatalf("test setup error: expected score in [0.60, 0.80), got %f", score)
	}

	speakers := map[string][]float64{"speaker_00": speakerVec}
	contacts := []ContactEmbedding{
		{ContactID: 7, ContactName: "Iris", Vector: contactVec},
	}

	result := MatchSpeakers(speakers, contacts)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
	if result.Matches[0].Tier != TierSuggest {
		t.Errorf("tier: got %q, want TierSuggest (score=%f)", result.Matches[0].Tier, score)
	}
}
