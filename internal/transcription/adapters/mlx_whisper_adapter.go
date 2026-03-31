package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"quill/internal/transcription/interfaces"
	"quill/pkg/binaries"
	"quill/pkg/logger"
)

// mlxWhisperPyprojectTOML is the inline pyproject.toml for the MLX Whisper UV project.
const mlxWhisperPyprojectTOML = `[project]
name = "quill-mlx-whisper"
version = "0.1.0"
requires-python = ">=3.10"
dependencies = [
    "mlx-whisper>=0.4.0",
]
`

// MLXWhisperAdapter implements TranscriptionAdapter using mlx-whisper on Apple Silicon.
type MLXWhisperAdapter struct {
	*BaseAdapter
	envPath string
}

// NewMLXWhisperAdapter creates a new MLX Whisper adapter.
func NewMLXWhisperAdapter(envPath string) *MLXWhisperAdapter {
	capabilities := interfaces.ModelCapabilities{
		ModelID:     "mlx_whisper",
		ModelFamily: "mlx_whisper",
		DisplayName: "MLX Whisper (Apple Silicon)",
		Description: "Whisper optimized for Apple Silicon via MLX framework — 1.2-2x faster than whisper.cpp on M-series chips",
		Version:     "0.5.0",
		SupportedLanguages: []string{
			"en", "zh", "de", "es", "ru", "ko", "fr", "ja", "pt", "tr",
			"pl", "ca", "nl", "ar", "sv", "it", "id", "hi", "fi", "vi",
			"he", "uk", "el", "ms", "cs", "ro", "da", "hu", "ta", "no",
			"th", "ur", "hr", "bg", "lt", "la", "mi", "ml", "cy", "sk",
			"te", "fa", "lv", "bn", "sr", "az", "sl", "kn", "et", "mk",
			"br", "eu", "is", "hy", "ne", "mn", "bs", "kk", "sq", "sw",
			"gl", "mr", "pa", "si", "km", "sn", "yo", "so", "af", "oc",
			"ka", "be", "tg", "sd", "gu", "am", "yi", "lo", "uz", "fo",
			"ht", "ps", "tk", "nn", "mt", "sa", "lb", "my", "bo", "tl",
			"mg", "as", "tt", "haw", "ln", "ha", "ba", "jw", "su",
		},
		SupportedFormats:  []string{"wav", "mp3", "flac", "m4a", "ogg", "opus"},
		RequiresGPU:       false,
		MemoryRequirement: 3072,
		Features: map[string]bool{
			"timestamps":  true,
			"word_level":  true,
			"translation": true,
			"apple_metal": true,
		},
		Metadata: map[string]string{
			"engine":       "mlx",
			"framework":    "mlx-whisper",
			"platform":     "apple_silicon",
			"acceleration": "metal",
		},
	}

	schema := []interfaces.ParameterSchema{
		{
			Name:        "model",
			Type:        "string",
			Required:    false,
			Default:     "large-v3-turbo-q4",
			Options:     []string{"small", "small.en", "medium", "medium.en", "large-v3", "large-v3-turbo", "large-v3-turbo-q4"},
			Description: "Whisper model size",
			Group:       "basic",
		},
		{
			Name:        "language",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "Language code (empty for auto-detect)",
			Group:       "basic",
		},
		{
			Name:        "task",
			Type:        "string",
			Required:    false,
			Default:     "transcribe",
			Options:     []string{"transcribe", "translate"},
			Description: "Task to perform",
			Group:       "basic",
		},
		{
			Name:        "temperature",
			Type:        "float",
			Required:    false,
			Default:     0.0,
			Description: "Sampling temperature",
			Group:       "advanced",
		},
		{
			Name:        "word_timestamps",
			Type:        "bool",
			Required:    false,
			Default:     true,
			Description: "Include word-level timestamps",
			Group:       "basic",
		},
	}

	adapter := &MLXWhisperAdapter{
		BaseAdapter: NewBaseAdapter("mlx_whisper", envPath, capabilities, schema),
		envPath:     envPath,
	}
	return adapter
}

// GetSupportedModels returns the list of models supported by MLX Whisper.
func (m *MLXWhisperAdapter) GetSupportedModels() []string {
	return []string{
		"small", "small.en",
		"medium", "medium.en",
		"large-v3", "large-v3-turbo", "large-v3-turbo-q4",
	}
}

// PrepareEnvironment sets up the MLX Whisper UV project.
func (m *MLXWhisperAdapter) PrepareEnvironment(ctx context.Context) error {
	return RunPrepareOnce("mlx-whisper-env:"+m.envPath, func() error {
		logger.Info("Preparing MLX Whisper environment", "env_path", m.envPath)

		projectDir := filepath.Join(m.envPath, "mlx-whisper")

		if CheckEnvironmentReady(projectDir, "import mlx_whisper; print('ok')") {
			logger.Info("MLX Whisper environment already ready")
			m.initialized = true
			return nil
		}

		if err := os.MkdirAll(projectDir, 0755); err != nil {
			return fmt.Errorf("failed to create MLX Whisper project directory: %w", err)
		}

		// Write pyproject.toml
		pyprojectPath := filepath.Join(projectDir, "pyproject.toml")
		if err := os.WriteFile(pyprojectPath, []byte(mlxWhisperPyprojectTOML), 0644); err != nil {
			return fmt.Errorf("failed to write pyproject.toml: %w", err)
		}

		// UV sync
		cmd := exec.CommandContext(ctx, binaries.UV(), "sync", "--native-tls")
		cmd.Dir = projectDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("uv sync failed for mlx-whisper: %w: %s", err, strings.TrimSpace(string(out)))
		}

		m.initialized = true
		logger.Info("MLX Whisper environment prepared successfully")
		return nil
	})
}

// WarmModel ensures the requested model weights are cached locally.
func (m *MLXWhisperAdapter) WarmModel(ctx context.Context, modelName string) error {
	if strings.TrimSpace(modelName) == "" {
		modelName = "large-v3-turbo-q4"
	}

	if err := m.PrepareEnvironment(ctx); err != nil {
		return err
	}

	warmKey := fmt.Sprintf("mlx-whisper-model:%s:%s", m.envPath, modelName)
	if IsModelWarm(warmKey) {
		return nil
	}

	return RunModelWarmOnce(warmKey, func() error {
		if IsModelWarm(warmKey) {
			return nil
		}

		projectDir := filepath.Join(m.envPath, "mlx-whisper")
		hfModel := mlxWhisperModelID(modelName)
		logger.Info("Warming MLX Whisper model cache", "model", modelName, "hf_model", hfModel)

		// Trigger model download by importing it
		cmd := exec.CommandContext(
			ctx,
			binaries.UV(),
			"run", "--native-tls", "--project", projectDir,
			"python", "-c",
			fmt.Sprintf("from huggingface_hub import snapshot_download; snapshot_download('%s')", hfModel),
		)
		cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

		if output, err := cmd.CombinedOutput(); err != nil {
			trimmed := strings.TrimSpace(string(output))
			if trimmed == "" {
				trimmed = err.Error()
			}
			return fmt.Errorf("failed to warm MLX Whisper model %s: %s", modelName, trimmed)
		}

		MarkModelWarm(warmKey)
		logger.Info("MLX Whisper model cache ready", "model", modelName)
		return nil
	})
}

// Transcribe processes audio using MLX Whisper.
func (m *MLXWhisperAdapter) Transcribe(ctx context.Context, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext) (*interfaces.TranscriptResult, error) {
	startTime := time.Now()
	m.LogProcessingStart(input, procCtx)
	defer func() {
		m.LogProcessingEnd(procCtx, time.Since(startTime), nil)
	}()

	if err := m.ValidateAudioInput(input); err != nil {
		return nil, fmt.Errorf("invalid audio input: %w", err)
	}
	if err := m.ValidateParameters(params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	modelName := m.GetStringParameter(params, "model")
	if err := m.WarmModel(ctx, modelName); err != nil {
		return nil, err
	}

	tempDir, err := m.CreateTempDirectory(procCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer m.CleanupTempDirectory(tempDir)

	outputFile := filepath.Join(tempDir, "result.json")
	script := m.buildTranscribeScript(input.FilePath, outputFile, params)
	projectDir := filepath.Join(m.envPath, "mlx-whisper")

	cmd := exec.CommandContext(
		ctx,
		binaries.UV(),
		"run", "--native-tls", "--project", projectDir,
		"python", "-c", script,
	)

	env := os.Environ()
	ffmpegBinary := binaries.FFmpeg()
	env = append(env, "FFMPEG_BINARY="+ffmpegBinary)
	if strings.Contains(ffmpegBinary, string(os.PathSeparator)) {
		if dir := filepath.Dir(ffmpegBinary); dir != "" && dir != "." {
			env = append(env, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}
	cmd.Env = append(env, "PYTHONUNBUFFERED=1")

	logFile, err := os.OpenFile(filepath.Join(procCtx.OutputDirectory, "transcription.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Warn("Failed to create log file", "error", err)
	} else {
		defer logFile.Close()
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	logger.Info("Executing MLX Whisper transcription", "model", modelName)
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("transcription was cancelled")
		}
		logPath := filepath.Join(procCtx.OutputDirectory, "transcription.log")
		logTail, _ := m.ReadLogTail(logPath, 2048)
		return nil, fmt.Errorf("MLX Whisper execution failed: %w\nLogs:\n%s", err, logTail)
	}

	result, err := m.parseResult(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to parse result: %w", err)
	}

	result.ProcessingTime = time.Since(startTime)
	result.ModelUsed = modelName
	result.Metadata = m.CreateDefaultMetadata(params)

	logger.Info("MLX Whisper transcription completed",
		"segments", len(result.Segments),
		"words", len(result.WordSegments),
		"processing_time", result.ProcessingTime)

	return result, nil
}

// buildTranscribeScript builds the Python script that runs mlx_whisper.transcribe.
func (m *MLXWhisperAdapter) buildTranscribeScript(audioPath, outputFile string, params map[string]interface{}) string {
	modelName := m.GetStringParameter(params, "model")
	hfModel := mlxWhisperModelID(modelName)
	language := m.GetStringParameter(params, "language")
	task := m.GetStringParameter(params, "task")
	wordTimestamps := m.GetBoolParameter(params, "word_timestamps")
	temperature := m.GetFloatParameter(params, "temperature")

	// Build Python script that outputs WhisperX-compatible JSON
	var sb strings.Builder
	sb.WriteString("import json, mlx_whisper\n")
	sb.WriteString(fmt.Sprintf("result = mlx_whisper.transcribe(%q, path_or_hf_repo=%q", audioPath, hfModel))

	if language != "" {
		sb.WriteString(fmt.Sprintf(", language=%q", language))
	}
	if task != "" && task != "transcribe" {
		sb.WriteString(fmt.Sprintf(", task=%q", task))
	}
	if wordTimestamps {
		sb.WriteString(", word_timestamps=True")
	}
	if temperature > 0 {
		sb.WriteString(fmt.Sprintf(", temperature=%.2f", temperature))
	}
	sb.WriteString(")\n")

	// Convert mlx_whisper output to our standard JSON format
	sb.WriteString(`
segments = []
word_segments = []
for seg in result.get("segments", []):
    segments.append({"start": seg["start"], "end": seg["end"], "text": seg["text"]})
    for w in seg.get("words", []):
        word_segments.append({
            "start": w.get("start", 0.0),
            "end": w.get("end", 0.0),
            "word": w.get("word", ""),
            "score": w.get("probability", 0.0),
        })

output = {
    "text": result.get("text", ""),
    "language": result.get("language", ""),
    "segments": segments,
    "word_segments": word_segments,
}
`)
	sb.WriteString(fmt.Sprintf("with open(%q, 'w') as f:\n    json.dump(output, f)\n", outputFile))

	return sb.String()
}

// parseResult reads and converts the JSON output from the Python script.
func (m *MLXWhisperAdapter) parseResult(outputFile string) (*interfaces.TranscriptResult, error) {
	data, err := os.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read result file: %w", err)
	}

	var raw struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
		WordSegments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Word  string  `json:"word"`
			Score float64 `json:"score"`
		} `json:"word_segments"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse JSON result: %w", err)
	}

	result := &interfaces.TranscriptResult{
		Text:         raw.Text,
		Language:     raw.Language,
		Segments:     make([]interfaces.TranscriptSegment, len(raw.Segments)),
		WordSegments: make([]interfaces.TranscriptWord, len(raw.WordSegments)),
	}

	for i, seg := range raw.Segments {
		result.Segments[i] = interfaces.TranscriptSegment{
			Start: seg.Start,
			End:   seg.End,
			Text:  seg.Text,
		}
	}
	for i, w := range raw.WordSegments {
		result.WordSegments[i] = interfaces.TranscriptWord{
			Start: w.Start,
			End:   w.End,
			Word:  w.Word,
			Score: w.Score,
		}
	}

	return result, nil
}

// mlxWhisperModelID maps short model names to HuggingFace model IDs for MLX.
func mlxWhisperModelID(model string) string {
	switch model {
	case "small":
		return "mlx-community/whisper-small-mlx"
	case "small.en":
		return "mlx-community/whisper-small.en-mlx"
	case "medium":
		return "mlx-community/whisper-medium-mlx"
	case "medium.en":
		return "mlx-community/whisper-medium.en-mlx"
	case "large-v3":
		return "mlx-community/whisper-large-v3-mlx"
	case "large-v3-turbo":
		return "mlx-community/whisper-large-v3-turbo"
	case "large-v3-turbo-q4":
		return "mlx-community/whisper-large-v3-turbo-q4"
	default:
		return "mlx-community/whisper-" + model + "-mlx"
	}
}

// GetEstimatedProcessingTime provides MLX Whisper-specific time estimation.
func (m *MLXWhisperAdapter) GetEstimatedProcessingTime(input interfaces.AudioInput) time.Duration {
	// MLX Whisper is ~1.5-2x faster than WhisperX on Apple Silicon
	baseTime := m.BaseAdapter.GetEstimatedProcessingTime(input)
	return baseTime / 2
}

// mlxWhisperModelSize returns approximate model size for download estimation (unused by adapter but useful for warmup UI).
func mlxWhisperModelSize(model string) string {
	switch {
	case strings.HasPrefix(model, "small"):
		return "~490 MB"
	case strings.HasPrefix(model, "medium"):
		return "~1.5 GB"
	case model == "large-v3-turbo-q4":
		return "~442 MB"
	case model == "large-v3-turbo":
		return "~1.6 GB"
	case strings.HasPrefix(model, "large"):
		return "~3.1 GB"
	default:
		return "unknown"
	}
}

// FormatModelSize returns human-readable size for a model name.
func (m *MLXWhisperAdapter) FormatModelSize(model string) string {
	return mlxWhisperModelSize(model)
}

// MLXModelID exports the model ID mapping for use in runtime warmup.
func MLXModelID(model string) string {
	return mlxWhisperModelID(model)
}

// GetStringParameterExported wraps the base method for testing.
func (m *MLXWhisperAdapter) GetStringParameterExported(params map[string]interface{}, key string) string {
	return m.GetStringParameter(params, key)
}

// GetBoolParameterExported wraps the base method for testing.
func (m *MLXWhisperAdapter) GetBoolParameterExported(params map[string]interface{}, key string) bool {
	return m.GetBoolParameter(params, key)
}

// GetFloatParameterExported wraps the base method for testing.
func (m *MLXWhisperAdapter) GetFloatParameterExported(params map[string]interface{}, key string) float64 {
	return m.GetFloatParameter(params, key)
}

// GetIntParameterExported wraps the base method for testing.
func (m *MLXWhisperAdapter) GetIntParameterExported(params map[string]interface{}, key string) int {
	// MLX adapter doesn't have int parameters currently but included for completeness
	v, ok := params[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(val)
		return n
	default:
		return 0
	}
}
