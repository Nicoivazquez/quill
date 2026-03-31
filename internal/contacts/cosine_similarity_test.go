package contacts

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// ---- CosineSimilarity -------------------------------------------------------

func TestCosineSimilarity_IdenticalVectors_ReturnsOne(t *testing.T) {
	a := []float64{0.1, 0.2, 0.3, 0.4}
	b := []float64{0.1, 0.2, 0.3, 0.4}

	got := CosineSimilarity(a, b)

	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("identical vectors: got %v, want 1.0", got)
	}
}

func TestCosineSimilarity_OppositeVectors_ReturnsNegativeOne(t *testing.T) {
	a := []float64{1.0, 2.0, 3.0}
	b := []float64{-1.0, -2.0, -3.0}

	got := CosineSimilarity(a, b)

	if math.Abs(got-(-1.0)) > 1e-9 {
		t.Errorf("opposite vectors: got %v, want -1.0", got)
	}
}

func TestCosineSimilarity_OrthogonalVectors_ReturnsZero(t *testing.T) {
	a := []float64{1.0, 0.0, 0.0}
	b := []float64{0.0, 1.0, 0.0}

	got := CosineSimilarity(a, b)

	if math.Abs(got) > 1e-9 {
		t.Errorf("orthogonal vectors: got %v, want 0.0", got)
	}
}

func TestCosineSimilarity_ZeroVectorA_ReturnsZero(t *testing.T) {
	a := []float64{0.0, 0.0, 0.0}
	b := []float64{1.0, 2.0, 3.0}

	got := CosineSimilarity(a, b)

	if math.IsNaN(got) {
		t.Fatal("zero vector a: returned NaN, want 0.0")
	}
	if got != 0.0 {
		t.Errorf("zero vector a: got %v, want 0.0", got)
	}
}

func TestCosineSimilarity_ZeroVectorB_ReturnsZero(t *testing.T) {
	a := []float64{1.0, 2.0, 3.0}
	b := []float64{0.0, 0.0, 0.0}

	got := CosineSimilarity(a, b)

	if math.IsNaN(got) {
		t.Fatal("zero vector b: returned NaN, want 0.0")
	}
	if got != 0.0 {
		t.Errorf("zero vector b: got %v, want 0.0", got)
	}
}

func TestCosineSimilarity_BothZeroVectors_ReturnsZero(t *testing.T) {
	a := []float64{0.0, 0.0, 0.0}
	b := []float64{0.0, 0.0, 0.0}

	got := CosineSimilarity(a, b)

	if math.IsNaN(got) {
		t.Fatal("both zero vectors: returned NaN, want 0.0")
	}
	if got != 0.0 {
		t.Errorf("both zero vectors: got %v, want 0.0", got)
	}
}

func TestCosineSimilarity_DifferentLengths_ReturnsZero(t *testing.T) {
	a := []float64{1.0, 2.0, 3.0}
	b := []float64{1.0, 2.0}

	got := CosineSimilarity(a, b)

	if got != 0.0 {
		t.Errorf("different-length vectors: got %v, want 0.0", got)
	}
}

func TestCosineSimilarity_EmptyVectors_ReturnsZero(t *testing.T) {
	got := CosineSimilarity([]float64{}, []float64{})

	if got != 0.0 {
		t.Errorf("empty vectors: got %v, want 0.0", got)
	}
}

func TestCosineSimilarity_NilVectors_ReturnsZero(t *testing.T) {
	got := CosineSimilarity(nil, nil)

	if got != 0.0 {
		t.Errorf("nil vectors: got %v, want 0.0", got)
	}
}

func TestCosineSimilarity_ResultBoundedMinusOneToOne(t *testing.T) {
	// Construct 256-dim vectors that represent a typical TitaNet-like distribution
	// (small floating-point values, some positive, some negative).
	a := make([]float64, 256)
	b := make([]float64, 256)
	for i := range a {
		a[i] = float64(i%17-8) * 0.01
		b[i] = float64(i%13-6) * 0.01
	}

	got := CosineSimilarity(a, b)

	if got < -1.0-1e-9 || got > 1.0+1e-9 {
		t.Errorf("256-dim result %v is outside [-1, 1]", got)
	}
}

func TestCosineSimilarity_RealWorldLike256Dim_KnownApproxSimilarity(t *testing.T) {
	// Two nearly-identical speakers: slight noise added to one vector.
	// Expected: high similarity close to but not exactly 1.0.
	a := make([]float64, 256)
	b := make([]float64, 256)
	for i := range a {
		a[i] = float64(i+1) * 0.001
		b[i] = float64(i+1)*0.001 + 0.0001 // tiny perturbation
	}

	got := CosineSimilarity(a, b)

	// Should be extremely close to 1.0 for nearly-identical vectors.
	if got < 0.999 {
		t.Errorf("nearly-identical 256-dim vectors: got similarity %v, want >= 0.999", got)
	}
}

func TestCosineSimilarity_KnownOrthogonalIn256Dim(t *testing.T) {
	// a has values only in even indices, b only in odd indices.
	a := make([]float64, 256)
	b := make([]float64, 256)
	for i := range a {
		if i%2 == 0 {
			a[i] = 1.0
		} else {
			b[i] = 1.0
		}
	}

	got := CosineSimilarity(a, b)

	if math.Abs(got) > 1e-9 {
		t.Errorf("orthogonal 256-dim vectors: got %v, want 0.0", got)
	}
}

// ---- LoadEmbeddingVector ----------------------------------------------------

func TestLoadEmbeddingVector_ValidFile_ReturnsVector(t *testing.T) {
	dir := t.TempDir()
	vector := []float64{0.1, -0.2, 0.3, 0.4, -0.5}
	writeEmbeddingFile(t, dir, "embedding.json", 1, "titanet", len(vector), vector)

	got, err := LoadEmbeddingVector(filepath.Join(dir, "embedding.json"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(got) != len(vector) {
		t.Fatalf("expected vector length %d, got %d", len(vector), len(got))
	}
	for i, v := range vector {
		if math.Abs(got[i]-v) > 1e-12 {
			t.Errorf("vector[%d]: got %v, want %v", i, got[i], v)
		}
	}
}

func TestLoadEmbeddingVector_256DimVector_ReturnsAllComponents(t *testing.T) {
	dir := t.TempDir()
	vector := make([]float64, 256)
	for i := range vector {
		vector[i] = float64(i) * 0.001
	}
	writeEmbeddingFile(t, dir, "embedding256.json", 1, "titanet", 256, vector)

	got, err := LoadEmbeddingVector(filepath.Join(dir, "embedding256.json"))

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(got) != 256 {
		t.Fatalf("expected 256-dim vector, got %d dims", len(got))
	}
}

func TestLoadEmbeddingVector_MissingFile_ReturnsError(t *testing.T) {
	_, err := LoadEmbeddingVector("/nonexistent/path/embedding.json")

	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadEmbeddingVector_InvalidJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("this is not json"), 0o644); err != nil {
		t.Fatalf("write bad json: %v", err)
	}

	_, err := LoadEmbeddingVector(path)

	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadEmbeddingVector_MissingVectorField_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-vector.json")
	// Valid JSON but no "vector" key.
	payload := `{"version": 1, "model": "titanet", "dimension": 3}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write no-vector json: %v", err)
	}

	_, err := LoadEmbeddingVector(path)

	if err == nil {
		t.Fatal("expected error when vector field is missing, got nil")
	}
}

func TestLoadEmbeddingVector_EmptyVectorField_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty-vector.json")
	payload := `{"version": 1, "model": "titanet", "dimension": 0, "vector": []}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write empty-vector json: %v", err)
	}

	_, err := LoadEmbeddingVector(path)

	if err == nil {
		t.Fatal("expected error for empty vector, got nil")
	}
}

func TestLoadEmbeddingVector_EmptyFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	_, err := LoadEmbeddingVector(path)

	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

// ---- ClassifySpeakerMatch ---------------------------------------------------

func TestClassifySpeakerMatch_HighConfidence_AutoAssign(t *testing.T) {
	cases := []float64{0.95, 0.85, 0.80}
	for _, score := range cases {
		got := ClassifySpeakerMatch(score)
		if got != TierAutoAssign {
			t.Errorf("score %.2f: got %q, want TierAutoAssign", score, got)
		}
	}
}

func TestClassifySpeakerMatch_BoundaryAutoAssign_ExactlyPoint50(t *testing.T) {
	got := ClassifySpeakerMatch(0.50)
	if got != TierAutoAssign {
		t.Errorf("score 0.50 (boundary): got %q, want TierAutoAssign", got)
	}
}

func TestClassifySpeakerMatch_MidRange_Suggest(t *testing.T) {
	cases := []float64{0.49, 0.45, 0.40, 0.35}
	for _, score := range cases {
		got := ClassifySpeakerMatch(score)
		if got != TierSuggest {
			t.Errorf("score %.2f: got %q, want TierSuggest", score, got)
		}
	}
}

func TestClassifySpeakerMatch_BoundarySuggest_ExactlyPoint35(t *testing.T) {
	got := ClassifySpeakerMatch(0.35)
	if got != TierSuggest {
		t.Errorf("score 0.35 (boundary): got %q, want TierSuggest", got)
	}
}

func TestClassifySpeakerMatch_LowConfidence_Unknown(t *testing.T) {
	cases := []float64{0.34, 0.20, 0.0}
	for _, score := range cases {
		got := ClassifySpeakerMatch(score)
		if got != TierUnknown {
			t.Errorf("score %.2f: got %q, want TierUnknown", score, got)
		}
	}
}

func TestClassifySpeakerMatch_NegativeScore_Unknown(t *testing.T) {
	got := ClassifySpeakerMatch(-0.5)
	if got != TierUnknown {
		t.Errorf("score -0.5: got %q, want TierUnknown", got)
	}
}

func TestClassifySpeakerMatch_AboveOne_AutoAssign(t *testing.T) {
	// Floating-point arithmetic might produce scores fractionally above 1.0;
	// those are still a match.
	got := ClassifySpeakerMatch(1.0000001)
	if got != TierAutoAssign {
		t.Errorf("score slightly above 1.0: got %q, want TierAutoAssign", got)
	}
}

func TestClassifySpeakerMatch_TierValuesAreCorrectStrings(t *testing.T) {
	if TierAutoAssign != "auto" {
		t.Errorf("TierAutoAssign = %q, want %q", TierAutoAssign, "auto")
	}
	if TierSuggest != "suggest" {
		t.Errorf("TierSuggest = %q, want %q", TierSuggest, "suggest")
	}
	if TierUnknown != "unknown" {
		t.Errorf("TierUnknown = %q, want %q", TierUnknown, "unknown")
	}
}

// ---- helpers ----------------------------------------------------------------

// writeEmbeddingFile creates a JSON embedding file in dir with the given fields.
func writeEmbeddingFile(t *testing.T, dir, name string, version int, model string, dimension int, vector []float64) {
	t.Helper()
	ef := EmbeddingFile{
		Version:   version,
		Model:     model,
		Dimension: dimension,
		Vector:    vector,
	}
	data, err := json.Marshal(ef)
	if err != nil {
		t.Fatalf("marshal embedding file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write embedding file %s: %v", name, err)
	}
}
