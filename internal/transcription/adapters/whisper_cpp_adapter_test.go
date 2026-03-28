package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGGMLModelFilename(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		{"small", "ggml-small-q5_0.bin"},
		{"small.en", "ggml-small.en-q5_0.bin"},
		{"medium", "ggml-medium-q5_0.bin"},
		{"medium.en", "ggml-medium.en-q5_0.bin"},
		{"large-v3", "ggml-large-v3-q5_0.bin"},
		{"large-v3-turbo", "ggml-large-v3-turbo-q5_0.bin"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := ggmlModelFilename(tt.model)
			if got != tt.expected {
				t.Errorf("ggmlModelFilename(%q) = %q, want %q", tt.model, got, tt.expected)
			}
		})
	}
}

func TestGGMLModelURL(t *testing.T) {
	tests := []struct {
		model    string
		contains string
	}{
		{"small", "ggerganov/whisper.cpp/resolve/main/ggml-small-q5_0.bin"},
		{"large-v3-turbo", "ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo-q5_0.bin"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := ggmlModelURL(tt.model)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("ggmlModelURL(%q) = %q, want to contain %q", tt.model, got, tt.contains)
			}
			if !strings.HasPrefix(got, "https://huggingface.co/") {
				t.Errorf("ggmlModelURL(%q) = %q, expected https://huggingface.co/ prefix", tt.model, got)
			}
		})
	}
}

func TestGGMLModelFilenameExported(t *testing.T) {
	got := GGMLModelFilename("large-v3-turbo")
	if got != "ggml-large-v3-turbo-q5_0.bin" {
		t.Errorf("GGMLModelFilename = %q, want ggml-large-v3-turbo-q5_0.bin", got)
	}
}

func TestGGMLModelURLExported(t *testing.T) {
	got := GGMLModelURL("small")
	if !strings.Contains(got, "ggml-small-q5_0.bin") {
		t.Errorf("GGMLModelURL = %q, want to contain ggml-small-q5_0.bin", got)
	}
}

func TestWhisperCppAdapterCapabilities(t *testing.T) {
	adapter := NewWhisperCppAdapter("/tmp/test-env")
	caps := adapter.GetCapabilities()

	if caps.ModelID != "whisper_cpp" {
		t.Errorf("expected ModelID=whisper_cpp, got %q", caps.ModelID)
	}
	if caps.ModelFamily != "whisper_cpp" {
		t.Errorf("expected ModelFamily=whisper_cpp, got %q", caps.ModelFamily)
	}
	if !caps.Features["timestamps"] {
		t.Error("expected timestamps feature to be true")
	}
	if !caps.Features["word_level"] {
		t.Error("expected word_level feature to be true")
	}
	if caps.Metadata["engine"] != "whisper.cpp" {
		t.Errorf("expected engine=whisper.cpp, got %q", caps.Metadata["engine"])
	}
	if caps.Metadata["format"] != "ggml" {
		t.Errorf("expected format=ggml, got %q", caps.Metadata["format"])
	}
}

func TestWhisperCppGetSupportedModels(t *testing.T) {
	adapter := NewWhisperCppAdapter("/tmp/test-env")
	models := adapter.GetSupportedModels()

	expected := []string{"small", "small.en", "medium", "medium.en", "large-v3", "large-v3-turbo"}
	if len(models) != len(expected) {
		t.Fatalf("expected %d models, got %d: %v", len(expected), len(models), models)
	}
	for i, m := range expected {
		if models[i] != m {
			t.Errorf("model[%d] = %q, want %q", i, models[i], m)
		}
	}
}

func TestWhisperCppBuildArgs(t *testing.T) {
	adapter := NewWhisperCppAdapter("/tmp/test-env")

	tests := []struct {
		name     string
		params   map[string]interface{}
		contains []string
		excludes []string
	}{
		{
			name: "basic args",
			params: map[string]interface{}{
				"language": "en",
			},
			contains: []string{
				"-m", "/tmp/model.bin",
				"-f", "/tmp/input.wav",
				"-oj",
				"-of", "/tmp/output",
				"-l", "en",
			},
			excludes: []string{"--translate"},
		},
		{
			name: "auto language",
			params: map[string]interface{}{},
			contains: []string{
				"-l", "auto",
			},
		},
		{
			name: "translate task",
			params: map[string]interface{}{
				"task": "translate",
			},
			contains: []string{"--translate"},
		},
		{
			name: "custom threads and beam size",
			params: map[string]interface{}{
				"threads":   4,
				"beam_size": 8,
			},
			contains: []string{
				"-t", "4",
				"-bs", "8",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := adapter.buildArgs("/tmp/input.wav", "/tmp/model.bin", "/tmp/output", tt.params)
			argsStr := strings.Join(args, " ")

			for _, expected := range tt.contains {
				if !strings.Contains(argsStr, expected) {
					t.Errorf("expected %q in args: %s", expected, argsStr)
				}
			}
			for _, excluded := range tt.excludes {
				if strings.Contains(argsStr, excluded) {
					t.Errorf("unexpected %q in args: %s", excluded, argsStr)
				}
			}
		})
	}
}

func TestWhisperCppParseResult(t *testing.T) {
	adapter := NewWhisperCppAdapter("/tmp/test-env")

	// whisper.cpp JSON format with transcription array
	cppJSON := map[string]interface{}{
		"transcription": []map[string]interface{}{
			{
				"timestamps": map[string]string{"from": "00:00:00,000", "to": "00:00:02,500"},
				"offsets":    map[string]int64{"from": 0, "to": 2500},
				"text":       " Hello world",
				"tokens": []map[string]interface{}{
					{
						"text":       "Hello",
						"offsets":    map[string]int64{"from": 0, "to": 1200},
						"timestamps": map[string]string{"from": "00:00:00,000", "to": "00:00:01,200"},
						"p":          0.95,
					},
					{
						"text":       "world",
						"offsets":    map[string]int64{"from": 1200, "to": 2500},
						"timestamps": map[string]string{"from": "00:00:01,200", "to": "00:00:02,500"},
						"p":          0.88,
					},
				},
			},
			{
				"timestamps": map[string]string{"from": "00:00:03,000", "to": "00:00:05,000"},
				"offsets":    map[string]int64{"from": 3000, "to": 5000},
				"text":       " Testing",
				"tokens": []map[string]interface{}{
					{
						"text":       "Testing",
						"offsets":    map[string]int64{"from": 3000, "to": 5000},
						"timestamps": map[string]string{"from": "00:00:03,000", "to": "00:00:05,000"},
						"p":          0.92,
					},
				},
			},
		},
	}

	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "output.json")
	data, _ := json.Marshal(cppJSON)
	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := adapter.parseResult(outputFile)
	if err != nil {
		t.Fatalf("parseResult failed: %v", err)
	}

	// Check segments
	if len(result.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(result.Segments))
	}

	// First segment: offsets.from=0 → 0.0s, offsets.to=2500 → 2.5s
	if result.Segments[0].Start != 0.0 {
		t.Errorf("Segments[0].Start = %f, want 0.0", result.Segments[0].Start)
	}
	if result.Segments[0].End != 2.5 {
		t.Errorf("Segments[0].End = %f, want 2.5", result.Segments[0].End)
	}
	if result.Segments[0].Text != "Hello world" {
		t.Errorf("Segments[0].Text = %q, want %q", result.Segments[0].Text, "Hello world")
	}

	// Second segment
	if result.Segments[1].Start != 3.0 {
		t.Errorf("Segments[1].Start = %f, want 3.0", result.Segments[1].Start)
	}

	// Check word segments (special tokens like [_BEG_] should be filtered out)
	if len(result.WordSegments) != 3 {
		t.Fatalf("expected 3 word segments, got %d", len(result.WordSegments))
	}

	// First word: offsets.from=0 → 0.0s
	if result.WordSegments[0].Word != "Hello" {
		t.Errorf("WordSegments[0].Word = %q, want %q", result.WordSegments[0].Word, "Hello")
	}
	if result.WordSegments[0].Start != 0.0 {
		t.Errorf("WordSegments[0].Start = %f, want 0.0", result.WordSegments[0].Start)
	}
	if result.WordSegments[0].End != 1.2 {
		t.Errorf("WordSegments[0].End = %f, want 1.2", result.WordSegments[0].End)
	}
	if result.WordSegments[0].Score != 0.95 {
		t.Errorf("WordSegments[0].Score = %f, want 0.95", result.WordSegments[0].Score)
	}

	// Full text should be joined
	if result.Text != "Hello world Testing" {
		t.Errorf("Text = %q, want %q", result.Text, "Hello world Testing")
	}
}

func TestWhisperCppParseResult_SkipsSpecialTokens(t *testing.T) {
	adapter := NewWhisperCppAdapter("/tmp/test-env")

	cppJSON := map[string]interface{}{
		"transcription": []map[string]interface{}{
			{
				"timestamps": map[string]string{"from": "00:00:00,000", "to": "00:00:02,000"},
				"offsets":    map[string]int64{"from": 0, "to": 2000},
				"text":       " Hello",
				"tokens": []map[string]interface{}{
					{
						"text":       "[_BEG_]",
						"offsets":    map[string]int64{"from": 0, "to": 0},
						"timestamps": map[string]string{"from": "00:00:00,000", "to": "00:00:00,000"},
						"p":          0.0,
					},
					{
						"text":       "Hello",
						"offsets":    map[string]int64{"from": 0, "to": 1000},
						"timestamps": map[string]string{"from": "00:00:00,000", "to": "00:00:01,000"},
						"p":          0.9,
					},
					{
						"text":       "",
						"offsets":    map[string]int64{"from": 1000, "to": 2000},
						"timestamps": map[string]string{"from": "00:00:01,000", "to": "00:00:02,000"},
						"p":          0.0,
					},
				},
			},
		},
	}

	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "output.json")
	data, _ := json.Marshal(cppJSON)
	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := adapter.parseResult(outputFile)
	if err != nil {
		t.Fatalf("parseResult failed: %v", err)
	}

	// Should only have "Hello" — [_BEG_] and empty strings filtered
	if len(result.WordSegments) != 1 {
		t.Fatalf("expected 1 word segment (special tokens filtered), got %d", len(result.WordSegments))
	}
	if result.WordSegments[0].Word != "Hello" {
		t.Errorf("WordSegments[0].Word = %q, want %q", result.WordSegments[0].Word, "Hello")
	}
}

func TestWhisperCppParseResult_MissingFile(t *testing.T) {
	adapter := NewWhisperCppAdapter("/tmp/test-env")
	_, err := adapter.parseResult("/nonexistent/output.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestWhisperCppParseResult_InvalidJSON(t *testing.T) {
	adapter := NewWhisperCppAdapter("/tmp/test-env")
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "bad.json")
	if err := os.WriteFile(outputFile, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := adapter.parseResult(outputFile)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWhisperCppParseResult_EmptyTranscription(t *testing.T) {
	adapter := NewWhisperCppAdapter("/tmp/test-env")

	cppJSON := map[string]interface{}{
		"transcription": []map[string]interface{}{},
	}

	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "output.json")
	data, _ := json.Marshal(cppJSON)
	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := adapter.parseResult(outputFile)
	if err != nil {
		t.Fatalf("parseResult failed: %v", err)
	}

	if len(result.Segments) != 0 {
		t.Errorf("expected 0 segments, got %d", len(result.Segments))
	}
	if result.Text != "" {
		t.Errorf("expected empty text, got %q", result.Text)
	}
}
