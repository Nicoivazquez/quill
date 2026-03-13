package contacts

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// SpeakerMatchTier represents the confidence level of a speaker-to-contact match.
type SpeakerMatchTier string

const (
	// TierAutoAssign means the match score is >= 0.80: assign automatically.
	TierAutoAssign SpeakerMatchTier = "auto"
	// TierSuggest means the match score is 0.60–0.79: surface as a suggestion.
	TierSuggest SpeakerMatchTier = "suggest"
	// TierUnknown means the match score is < 0.60: not confident enough to act.
	TierUnknown SpeakerMatchTier = "unknown"
)

// EmbeddingFile mirrors the JSON schema produced by the TitaNet extraction script.
type EmbeddingFile struct {
	Version   int       `json:"version"`
	Model     string    `json:"model"`
	Dimension int       `json:"dimension"`
	Vector    []float64 `json:"vector"`
}

// CosineSimilarity returns the cosine similarity between two equal-length
// float64 vectors. The result is in the range [-1, 1].
//
// Edge-case guarantees (no panics, no NaN returns):
//   - Vectors of different lengths → 0.0
//   - Either or both zero-magnitude vectors → 0.0
//   - Nil or empty inputs → 0.0
func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	if len(a) != len(b) {
		return 0.0
	}

	var dot, magA, magB float64
	for i := range a {
		dot += a[i] * b[i]
		magA += a[i] * a[i]
		magB += b[i] * b[i]
	}

	denom := math.Sqrt(magA) * math.Sqrt(magB)
	if denom == 0.0 {
		return 0.0
	}
	return dot / denom
}

// LoadEmbeddingVector reads an EmbeddingFile from filePath and returns its
// vector. Returns an error if the file is missing, the JSON is malformed, or
// the vector field is absent or empty.
func LoadEmbeddingVector(filePath string) ([]float64, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read embedding file: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("embedding file is empty: %s", filePath)
	}

	var ef EmbeddingFile
	if err := json.Unmarshal(data, &ef); err != nil {
		return nil, fmt.Errorf("parse embedding file: %w", err)
	}
	if len(ef.Vector) == 0 {
		return nil, fmt.Errorf("embedding file has no vector: %s", filePath)
	}
	return ef.Vector, nil
}

// ClassifySpeakerMatch maps a cosine similarity score to a SpeakerMatchTier.
//
// Thresholds:
//   - score >= 0.80 → TierAutoAssign
//   - 0.60 <= score < 0.80 → TierSuggest
//   - score < 0.60 → TierUnknown
func ClassifySpeakerMatch(score float64) SpeakerMatchTier {
	switch {
	case score >= 0.80:
		return TierAutoAssign
	case score >= 0.60:
		return TierSuggest
	default:
		return TierUnknown
	}
}
