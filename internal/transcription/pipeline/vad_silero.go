package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"quill/internal/transcription/interfaces"
	"quill/pkg/binaries"
	"quill/pkg/logger"
)

// SileroVadPreprocessor runs Silero VAD to strip silence from audio before
// transcription, producing shorter audio that transcribes faster. A VadManifest
// is stored in AudioInput.Metadata["vad_manifest_path"] so callers can remap
// timestamps back to the original timeline after transcription.
type SileroVadPreprocessor struct {
	Enabled bool   // controlled by user setting
	EnvPath string // Python environment path (for uv run)
}

// AppliesTo returns true when VAD preprocessing is enabled by the user.
// It is model-agnostic — works with any transcription adapter.
func (s *SileroVadPreprocessor) AppliesTo(capabilities interfaces.ModelCapabilities) bool {
	return s.Enabled
}

// GetRequiredFormats returns the output formats this preprocessor produces.
func (s *SileroVadPreprocessor) GetRequiredFormats() []string {
	return []string{"wav"}
}

// Process runs Silero VAD on the input audio, strips silence, and returns a
// new AudioInput pointing to the stripped audio file. The manifest path is
// stored in Metadata so the caller can remap timestamps after transcription.
func (s *SileroVadPreprocessor) Process(ctx context.Context, input interfaces.AudioInput) (interfaces.AudioInput, error) {
	logger.Info("Running Silero VAD preprocessing", "file", input.FilePath)

	// Create output paths next to the input.
	dir := filepath.Dir(input.FilePath)
	base := strings.TrimSuffix(filepath.Base(input.FilePath), filepath.Ext(input.FilePath))
	strippedPath := filepath.Join(dir, base+"_vad_stripped.wav")
	manifestPath := filepath.Join(dir, base+"_vad_manifest.json")

	// Build and execute the Python VAD script.
	script := buildSileroVadScript(input.FilePath, strippedPath, manifestPath)
	cmd := exec.CommandContext(ctx, binaries.UV(), "run", "--native-tls", "--project", s.EnvPath, "python", "-c", script)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("Silero VAD failed", "output", string(output), "error", err)
		return input, fmt.Errorf("silero VAD preprocessing failed: %w", err)
	}

	// Parse the manifest to log summary.
	manifest, err := ParseVadManifest(manifestPath)
	if err != nil {
		logger.Error("Failed to parse VAD manifest", "error", err)
		return input, fmt.Errorf("VAD manifest parse error: %w", err)
	}

	strippedDur := manifest.StrippedDuration()
	savings := 0.0
	if manifest.OriginalDuration > 0 {
		savings = (1.0 - strippedDur/manifest.OriginalDuration) * 100
	}
	logger.Info("VAD preprocessing complete",
		"original_duration", manifest.OriginalDuration,
		"stripped_duration", strippedDur,
		"regions", len(manifest.Regions),
		"savings_pct", fmt.Sprintf("%.1f%%", savings))

	// Build the new audio input.
	result := interfaces.AudioInput{
		FilePath:     strippedPath,
		Format:       "wav",
		SampleRate:   input.SampleRate,
		Channels:     input.Channels,
		Duration:     input.Duration, // original duration preserved for metadata
		Size:         0,
		Metadata:     make(map[string]string),
		TempFilePath: strippedPath,
	}

	// Copy existing metadata.
	for k, v := range input.Metadata {
		result.Metadata[k] = v
	}
	// Store manifest path for post-transcription timestamp remapping.
	result.Metadata["vad_manifest_path"] = manifestPath
	// Store original audio path for diarization (which needs the full audio).
	result.Metadata["vad_original_audio"] = input.FilePath

	if stat, err := os.Stat(strippedPath); err == nil {
		result.Size = stat.Size()
	}

	return result, nil
}

// buildSileroVadScript generates a Python script that:
//  1. Loads the Silero VAD model via torch.hub
//  2. Detects speech regions in the input audio
//  3. Concatenates speech regions into a stripped WAV file
//  4. Writes a JSON manifest with speech region timestamps and original duration
func buildSileroVadScript(inputPath, strippedOutputPath, manifestOutputPath string) string {
	var sb strings.Builder
	sb.WriteString("import torch, torchaudio, json\n")
	sb.WriteString("\n")
	sb.WriteString("# Load Silero VAD model\n")
	sb.WriteString("model, utils = torch.hub.load('snakers4/silero-vad', 'silero_vad', trust_repo=True)\n")
	sb.WriteString("(get_speech_timestamps, _, read_audio, _, _) = utils\n")
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("SAMPLING_RATE = 16000\n"))
	sb.WriteString(fmt.Sprintf("audio_path = %q\n", inputPath))
	sb.WriteString(fmt.Sprintf("stripped_path = %q\n", strippedOutputPath))
	sb.WriteString(fmt.Sprintf("manifest_path = %q\n", manifestOutputPath))
	sb.WriteString("\n")
	sb.WriteString("# Read audio at 16kHz (Silero VAD requirement)\n")
	sb.WriteString("wav = read_audio(audio_path, sampling_rate=SAMPLING_RATE)\n")
	sb.WriteString("original_duration = len(wav) / SAMPLING_RATE\n")
	sb.WriteString("\n")
	sb.WriteString("# Detect speech timestamps\n")
	sb.WriteString("speech_timestamps = get_speech_timestamps(wav, model, sampling_rate=SAMPLING_RATE, return_seconds=False)\n")
	sb.WriteString("\n")
	sb.WriteString("# Build speech regions and concatenate speech audio\n")
	sb.WriteString("regions = []\n")
	sb.WriteString("chunks = []\n")
	sb.WriteString("for ts in speech_timestamps:\n")
	sb.WriteString("    start_sec = ts['start'] / SAMPLING_RATE\n")
	sb.WriteString("    end_sec = ts['end'] / SAMPLING_RATE\n")
	sb.WriteString("    regions.append({'start': round(start_sec, 6), 'end': round(end_sec, 6)})\n")
	sb.WriteString("    chunks.append(wav[ts['start']:ts['end']])\n")
	sb.WriteString("\n")
	sb.WriteString("if chunks:\n")
	sb.WriteString("    stripped = torch.cat(chunks)\n")
	sb.WriteString("else:\n")
	sb.WriteString("    stripped = wav  # No speech detected — keep original\n")
	sb.WriteString("    regions = [{'start': 0.0, 'end': float(original_duration)}]\n")
	sb.WriteString("\n")
	sb.WriteString("# Save stripped audio\n")
	sb.WriteString("torchaudio.save(stripped_path, stripped.unsqueeze(0), SAMPLING_RATE)\n")
	sb.WriteString("\n")
	sb.WriteString("# Write manifest\n")
	sb.WriteString("manifest = {\n")
	sb.WriteString("    'regions': regions,\n")
	sb.WriteString("    'original_duration': round(float(original_duration), 6),\n")
	sb.WriteString("}\n")
	sb.WriteString("with open(manifest_path, 'w') as f:\n")
	sb.WriteString("    json.dump(manifest, f)\n")

	return sb.String()
}

// ParseVadManifest reads and parses a VAD manifest JSON file.
func ParseVadManifest(path string) (VadManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VadManifest{}, fmt.Errorf("read VAD manifest: %w", err)
	}

	var manifest VadManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return VadManifest{}, fmt.Errorf("parse VAD manifest: %w", err)
	}

	return manifest, nil
}
