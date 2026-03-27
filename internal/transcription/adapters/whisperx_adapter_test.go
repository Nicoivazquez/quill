package adapters

import (
	"strings"
	"testing"

	"quill/internal/transcription/interfaces"
)

func TestWhisperXReadinessImportStatementCoversRuntimeDependencies(t *testing.T) {
	statement := whisperXReadinessImportStatement()

	for _, expected := range []string{
		"import whisperx",
		`importlib.import_module("scipy.special._gufuncs")`,
		"from transformers import Pipeline",
	} {
		if !strings.Contains(statement, expected) {
			t.Fatalf("readiness probe missing %q in %q", expected, statement)
		}
	}
}

func TestBuildWhisperXArgs_PyAnnoteDiarization(t *testing.T) {
	adapter := NewWhisperXAdapter("/tmp/test-env")

	input := interfaces.AudioInput{
		FilePath: "/tmp/test.wav",
		Format:   "wav",
	}

	params := map[string]interface{}{
		"model":          "small",
		"device":         "cpu",
		"device_index":   0,
		"batch_size":     8,
		"compute_type":   "float32",
		"task":           "transcribe",
		"diarize":        true,
		"diarize_model":  "pyannote",
		"hf_token":       "hf_test_token_123",
		"vad_method":     "pyannote",
		"vad_onset":      0.5,
		"vad_offset":     0.363,
		"temperature":    0.0,
		"best_of":        5,
		"beam_size":      5,
		"patience":       1.0,
		"min_speakers":   2,
		"max_speakers":   4,
	}

	args, err := adapter.buildWhisperXArgs(input, params, "/tmp/output")
	if err != nil {
		t.Fatalf("buildWhisperXArgs failed: %v", err)
	}

	argsStr := strings.Join(args, " ")

	// Verify --diarize flag is present
	if !strings.Contains(argsStr, " --diarize ") {
		t.Error("expected --diarize flag in args")
	}

	// Verify pyannote was remapped to the community model
	if !strings.Contains(argsStr, "--diarize_model pyannote/speaker-diarization-community-1") {
		t.Errorf("expected diarize_model to be remapped to community model, got: %s", argsStr)
	}

	// Verify HF token is present
	if !strings.Contains(argsStr, "--hf_token hf_test_token_123") {
		t.Errorf("expected --hf_token in args, got: %s", argsStr)
	}

	// Verify speaker constraints
	if !strings.Contains(argsStr, "--min_speakers 2") {
		t.Errorf("expected --min_speakers 2 in args, got: %s", argsStr)
	}
	if !strings.Contains(argsStr, "--max_speakers 4") {
		t.Errorf("expected --max_speakers 4 in args, got: %s", argsStr)
	}
}

func TestBuildWhisperXArgs_SortformerDiarization(t *testing.T) {
	adapter := NewWhisperXAdapter("/tmp/test-env")

	input := interfaces.AudioInput{
		FilePath: "/tmp/test.wav",
		Format:   "wav",
	}

	params := map[string]interface{}{
		"model":          "small",
		"device":         "cpu",
		"device_index":   0,
		"batch_size":     8,
		"compute_type":   "float32",
		"task":           "transcribe",
		"diarize":        false, // WhisperX diarization is disabled for Sortformer
		"diarize_model":  "nvidia_sortformer",
		"vad_method":     "pyannote",
		"vad_onset":      0.5,
		"vad_offset":     0.363,
		"temperature":    0.0,
		"best_of":        5,
		"beam_size":      5,
		"patience":       1.0,
	}

	args, err := adapter.buildWhisperXArgs(input, params, "/tmp/output")
	if err != nil {
		t.Fatalf("buildWhisperXArgs failed: %v", err)
	}

	argsStr := strings.Join(args, " ")

	// Verify --diarize flag is NOT present (Sortformer handles diarization separately)
	if strings.Contains(argsStr, " --diarize ") || strings.Contains(argsStr, " --diarize_model ") {
		t.Errorf("expected NO --diarize flags for sortformer path, got: %s", argsStr)
	}
}

func TestBuildWhisperXArgs_NoHFTokenFallsBackToEnv(t *testing.T) {
	adapter := NewWhisperXAdapter("/tmp/test-env")

	input := interfaces.AudioInput{
		FilePath: "/tmp/test.wav",
		Format:   "wav",
	}

	params := map[string]interface{}{
		"model":          "small",
		"device":         "cpu",
		"device_index":   0,
		"batch_size":     8,
		"compute_type":   "float32",
		"task":           "transcribe",
		"diarize":        true,
		"diarize_model":  "pyannote",
		"vad_method":     "pyannote",
		"vad_onset":      0.5,
		"vad_offset":     0.363,
		"temperature":    0.0,
		"best_of":        5,
		"beam_size":      5,
		"patience":       1.0,
	}

	// No hf_token in params and no HF_TOKEN env var
	args, err := adapter.buildWhisperXArgs(input, params, "/tmp/output")
	if err != nil {
		t.Fatalf("buildWhisperXArgs failed: %v", err)
	}

	argsStr := strings.Join(args, " ")

	// Verify --diarize flag IS present (pyannote was requested)
	if !strings.Contains(argsStr, " --diarize ") {
		t.Error("expected --diarize flag even without token")
	}

	// Verify --hf_token is NOT present (no token provided)
	if strings.Contains(argsStr, "--hf_token") {
		t.Errorf("expected NO --hf_token when no token provided, got: %s", argsStr)
	}
}
