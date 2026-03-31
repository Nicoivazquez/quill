package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMLXWhisperModelID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"small", "mlx-community/whisper-small-mlx"},
		{"small.en", "mlx-community/whisper-small.en-mlx"},
		{"medium", "mlx-community/whisper-medium-mlx"},
		{"medium.en", "mlx-community/whisper-medium.en-mlx"},
		{"large-v3", "mlx-community/whisper-large-v3-mlx"},
		{"large-v3-turbo", "mlx-community/whisper-large-v3-turbo"},
		{"large-v3-turbo-q4", "mlx-community/whisper-large-v3-turbo-q4"},
		// Unknown model falls back to convention
		{"tiny", "mlx-community/whisper-tiny-mlx"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mlxWhisperModelID(tt.input)
			if got != tt.expected {
				t.Errorf("mlxWhisperModelID(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestMLXWhisperModelSize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"small", "~490 MB"},
		{"small.en", "~490 MB"},
		{"medium", "~1.5 GB"},
		{"medium.en", "~1.5 GB"},
		{"large-v3", "~3.1 GB"},
		{"large-v3-turbo", "~1.6 GB"},
		{"large-v3-turbo-q4", "~442 MB"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mlxWhisperModelSize(tt.input)
			if got != tt.expected {
				t.Errorf("mlxWhisperModelSize(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestMLXWhisperAdapterCapabilities(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")
	caps := adapter.GetCapabilities()

	if caps.ModelID != "mlx_whisper" {
		t.Errorf("expected ModelID=mlx_whisper, got %q", caps.ModelID)
	}
	if caps.ModelFamily != "mlx_whisper" {
		t.Errorf("expected ModelFamily=mlx_whisper, got %q", caps.ModelFamily)
	}
	if !caps.Features["apple_metal"] {
		t.Error("expected apple_metal feature to be true")
	}
	if !caps.Features["word_level"] {
		t.Error("expected word_level feature to be true")
	}
	if caps.Metadata["engine"] != "mlx" {
		t.Errorf("expected engine=mlx, got %q", caps.Metadata["engine"])
	}
}

func TestMLXWhisperGetSupportedModels(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")
	models := adapter.GetSupportedModels()

	expected := []string{"small", "small.en", "medium", "medium.en", "large-v3", "large-v3-turbo", "large-v3-turbo-q4"}
	if len(models) != len(expected) {
		t.Fatalf("expected %d models, got %d: %v", len(expected), len(models), models)
	}
	for i, m := range expected {
		if models[i] != m {
			t.Errorf("model[%d] = %q, want %q", i, models[i], m)
		}
	}
}

func TestMLXWhisperParameterHelpers(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")

	params := map[string]interface{}{
		"model":           "medium",
		"word_timestamps": true,
		"temperature":     0.5,
	}

	if got := adapter.GetStringParameterExported(params, "model"); got != "medium" {
		t.Errorf("GetStringParameter(model) = %q, want %q", got, "medium")
	}
	if got := adapter.GetBoolParameterExported(params, "word_timestamps"); !got {
		t.Error("GetBoolParameter(word_timestamps) = false, want true")
	}
	if got := adapter.GetFloatParameterExported(params, "temperature"); got != 0.5 {
		t.Errorf("GetFloatParameter(temperature) = %f, want 0.5", got)
	}

	// Default values for missing params
	if got := adapter.GetStringParameterExported(params, "language"); got != "" {
		t.Errorf("GetStringParameter(language) = %q, want empty", got)
	}
}

func TestMLXWhisperBuildTranscribeScript(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")

	params := map[string]interface{}{
		"model":           "large-v3-turbo",
		"language":        "en",
		"task":            "transcribe",
		"word_timestamps": true,
		"temperature":     0.0,
	}

	script := adapter.buildTranscribeScript("/tmp/audio.wav", "/tmp/result.json", params)

	if !strings.Contains(script, "import json, mlx_whisper") {
		t.Error("script missing mlx_whisper import")
	}
	if !strings.Contains(script, "mlx-community/whisper-large-v3-turbo") {
		t.Error("script missing HuggingFace model ID")
	}
	if !strings.Contains(script, `language="en"`) {
		t.Error("script missing language parameter")
	}
	if !strings.Contains(script, "word_timestamps=True") {
		t.Error("script missing word_timestamps")
	}
	// task=transcribe should NOT add explicit task param (it's the default)
	if strings.Contains(script, `task="transcribe"`) {
		t.Error("script should not include default task=transcribe")
	}
}

func TestMLXWhisperBuildTranscribeScript_Translate(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")

	params := map[string]interface{}{
		"model":           "small",
		"task":            "translate",
		"word_timestamps": false,
	}

	script := adapter.buildTranscribeScript("/tmp/audio.wav", "/tmp/result.json", params)

	if !strings.Contains(script, `task="translate"`) {
		t.Error("script missing translate task")
	}
	if strings.Contains(script, "word_timestamps=True") {
		t.Error("script should not include word_timestamps=True when false")
	}
}

func TestMLXWhisperParseResult(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")

	resultJSON := map[string]interface{}{
		"text":     "Hello world",
		"language": "en",
		"segments": []map[string]interface{}{
			{"start": 0.0, "end": 1.5, "text": "Hello"},
			{"start": 1.5, "end": 3.0, "text": "world"},
		},
		"word_segments": []map[string]interface{}{
			{"start": 0.0, "end": 0.8, "word": "Hello", "score": 0.95},
			{"start": 1.5, "end": 2.5, "word": "world", "score": 0.92},
		},
	}

	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "result.json")
	data, _ := json.Marshal(resultJSON)
	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := adapter.parseResult(outputFile)
	if err != nil {
		t.Fatalf("parseResult failed: %v", err)
	}

	if result.Text != "Hello world" {
		t.Errorf("Text = %q, want %q", result.Text, "Hello world")
	}
	if result.Language != "en" {
		t.Errorf("Language = %q, want %q", result.Language, "en")
	}
	if len(result.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(result.Segments))
	}
	if result.Segments[0].Text != "Hello" {
		t.Errorf("Segments[0].Text = %q, want %q", result.Segments[0].Text, "Hello")
	}
	if len(result.WordSegments) != 2 {
		t.Fatalf("expected 2 word segments, got %d", len(result.WordSegments))
	}
	if result.WordSegments[0].Score != 0.95 {
		t.Errorf("WordSegments[0].Score = %f, want 0.95", result.WordSegments[0].Score)
	}
}

func TestMLXWhisperParseResult_MissingFile(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")
	_, err := adapter.parseResult("/nonexistent/result.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMLXWhisperBuildTranscribeScript_Q4(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")

	params := map[string]interface{}{
		"model":           "large-v3-turbo-q4",
		"word_timestamps": true,
	}

	script := adapter.buildTranscribeScript("/tmp/audio.wav", "/tmp/result.json", params)

	if !strings.Contains(script, "mlx-community/whisper-large-v3-turbo-q4") {
		t.Error("script should use quantized HuggingFace model ID for q4")
	}
}

func TestMLXWhisperParameterSchema_IncludesQ4(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")
	schema := adapter.GetParameterSchema()

	var modelSchema *struct{ Options []string }
	for _, s := range schema {
		if s.Name == "model" {
			opts := s.Options
			modelSchema = &struct{ Options []string }{opts}
			break
		}
	}
	if modelSchema == nil {
		t.Fatal("model parameter not found in schema")
	}

	found := false
	for _, opt := range modelSchema.Options {
		if opt == "large-v3-turbo-q4" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("large-v3-turbo-q4 not found in model options: %v", modelSchema.Options)
	}
}

func TestMLXWhisperFormatModelSize(t *testing.T) {
	adapter := NewMLXWhisperAdapter("/tmp/test-env")
	if got := adapter.FormatModelSize("large-v3-turbo"); got != "~1.6 GB" {
		t.Errorf("FormatModelSize(large-v3-turbo) = %q, want ~1.6 GB", got)
	}
}

func TestMLXModelIDExported(t *testing.T) {
	// Test the exported function used by runtime warmup
	if got := MLXModelID("large-v3"); got != "mlx-community/whisper-large-v3-mlx" {
		t.Errorf("MLXModelID(large-v3) = %q, want mlx-community/whisper-large-v3-mlx", got)
	}
}
