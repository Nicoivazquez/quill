package pipeline

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quill/internal/transcription/interfaces"
)

func TestBuildSileroVadScript(t *testing.T) {
	script := buildSileroVadScript("/tmp/audio.wav", "/tmp/stripped.wav", "/tmp/manifest.json")

	// Must import required modules.
	if !strings.Contains(script, "import torch") {
		t.Error("script missing torch import")
	}
	if !strings.Contains(script, "torchaudio") {
		t.Error("script missing torchaudio import")
	}
	if !strings.Contains(script, "json") {
		t.Error("script missing json import")
	}

	// Must load Silero VAD model.
	if !strings.Contains(script, "torch.hub.load") {
		t.Error("script missing torch.hub.load for Silero VAD")
	}
	if !strings.Contains(script, "silero-vad") {
		t.Error("script missing silero-vad model reference")
	}

	// Must reference input/output paths.
	if !strings.Contains(script, "/tmp/audio.wav") {
		t.Error("script missing input audio path")
	}
	if !strings.Contains(script, "/tmp/stripped.wav") {
		t.Error("script missing stripped audio output path")
	}
	if !strings.Contains(script, "/tmp/manifest.json") {
		t.Error("script missing manifest output path")
	}
}

func TestParseVadManifest(t *testing.T) {
	manifest := VadManifest{
		Regions: []SpeechRegion{
			{Start: 0.5, End: 3.0},
			{Start: 5.0, End: 8.0},
		},
		OriginalDuration: 10.0,
	}

	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseVadManifest(manifestPath)
	if err != nil {
		t.Fatalf("ParseVadManifest: %v", err)
	}

	if len(parsed.Regions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(parsed.Regions))
	}
	if math.Abs(parsed.Regions[0].Start-0.5) > 1e-6 {
		t.Errorf("Region[0].Start = %f, want 0.5", parsed.Regions[0].Start)
	}
	if math.Abs(parsed.Regions[1].End-8.0) > 1e-6 {
		t.Errorf("Region[1].End = %f, want 8.0", parsed.Regions[1].End)
	}
	if math.Abs(parsed.OriginalDuration-10.0) > 1e-6 {
		t.Errorf("OriginalDuration = %f, want 10.0", parsed.OriginalDuration)
	}
}

func TestParseVadManifest_MissingFile(t *testing.T) {
	_, err := ParseVadManifest("/nonexistent/manifest.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseVadManifest_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ParseVadManifest(manifestPath)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSileroVadPreprocessor_AppliesTo_Enabled(t *testing.T) {
	p := &SileroVadPreprocessor{Enabled: true}
	caps := interfaces.ModelCapabilities{
		ModelID:  "mlx_whisper",
		Features: map[string]bool{},
	}
	if !p.AppliesTo(caps) {
		t.Error("expected AppliesTo=true when Enabled=true")
	}
}

func TestSileroVadPreprocessor_AppliesTo_Disabled(t *testing.T) {
	p := &SileroVadPreprocessor{Enabled: false}
	caps := interfaces.ModelCapabilities{
		ModelID:  "mlx_whisper",
		Features: map[string]bool{},
	}
	if p.AppliesTo(caps) {
		t.Error("expected AppliesTo=false when Enabled=false")
	}
}

func TestSileroVadPreprocessor_GetRequiredFormats(t *testing.T) {
	p := &SileroVadPreprocessor{}
	formats := p.GetRequiredFormats()
	if len(formats) != 1 || formats[0] != "wav" {
		t.Errorf("expected [wav], got %v", formats)
	}
}
