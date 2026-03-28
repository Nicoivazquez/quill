package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"quill/internal/transcription/interfaces"
	"quill/pkg/binaries"
	"quill/pkg/downloader"
	"quill/pkg/logger"
)

const (
	// Default GGML model repository on HuggingFace.
	defaultGGMLModelRepo = "ggerganov/whisper.cpp"

	// Default quantisation suffix for GGML models.
	defaultGGMLQuant = "q5_0"
)

// WhisperCppAdapter implements TranscriptionAdapter using whisper.cpp.
// whisper.cpp is a cross-platform C++ inference engine for Whisper models.
// It uses Metal on macOS, CUDA on Linux/Windows, and CPU fallback everywhere.
type WhisperCppAdapter struct {
	*BaseAdapter
	envPath string // Directory for GGML model files
}

// NewWhisperCppAdapter creates a new whisper.cpp adapter.
func NewWhisperCppAdapter(envPath string) *WhisperCppAdapter {
	capabilities := interfaces.ModelCapabilities{
		ModelID:     "whisper_cpp",
		ModelFamily: "whisper_cpp",
		DisplayName: "Whisper.cpp",
		Description: "Cross-platform Whisper inference via whisper.cpp — uses Metal (macOS), CUDA (Linux/Windows), or CPU",
		Version:     "1.7.0",
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
		MemoryRequirement: 2048,
		Features: map[string]bool{
			"timestamps": true,
			"word_level": true,
			"metal":      runtime.GOOS == "darwin",
			"cuda":       runtime.GOOS == "linux" || runtime.GOOS == "windows",
		},
		Metadata: map[string]string{
			"engine":   "whisper.cpp",
			"format":   "ggml",
			"platform": "cross-platform",
		},
	}

	schema := []interfaces.ParameterSchema{
		{
			Name:        "model",
			Type:        "string",
			Required:    false,
			Default:     "large-v3-turbo",
			Options:     []string{"small", "small.en", "medium", "medium.en", "large-v3", "large-v3-turbo"},
			Description: "Whisper model size (GGML format)",
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
			Name:        "threads",
			Type:        "int",
			Required:    false,
			Default:     0,
			Description: "Number of CPU threads (0 = auto)",
			Group:       "advanced",
		},
		{
			Name:        "beam_size",
			Type:        "int",
			Required:    false,
			Default:     5,
			Description: "Beam size for decoding",
			Group:       "advanced",
		},
	}

	adapter := &WhisperCppAdapter{
		BaseAdapter: NewBaseAdapter("whisper_cpp", envPath, capabilities, schema),
		envPath:     envPath,
	}
	return adapter
}

// GetSupportedModels returns models supported by whisper.cpp.
func (w *WhisperCppAdapter) GetSupportedModels() []string {
	return []string{
		"small", "small.en",
		"medium", "medium.en",
		"large-v3", "large-v3-turbo",
	}
}

// PrepareEnvironment ensures the whisper.cpp binary is available and the model directory exists.
func (w *WhisperCppAdapter) PrepareEnvironment(ctx context.Context) error {
	return RunPrepareOnce("whisper-cpp-env:"+w.envPath, func() error {
		logger.Info("Preparing whisper.cpp environment", "env_path", w.envPath)

		// Verify whisper.cpp binary is reachable
		bin := binaries.WhisperCpp()
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("whisper.cpp binary not found (%s): %w — install it or set QUILL_WHISPER_CPP_BIN", bin, err)
		}

		// Ensure model cache directory exists
		modelsDir := filepath.Join(w.envPath, "models")
		if err := os.MkdirAll(modelsDir, 0755); err != nil {
			return fmt.Errorf("failed to create whisper.cpp models directory: %w", err)
		}

		w.initialized = true
		logger.Info("whisper.cpp environment ready")
		return nil
	})
}

// EnsureModel downloads the GGML model file if not already present.
func (w *WhisperCppAdapter) EnsureModel(ctx context.Context, modelName string) (string, error) {
	if strings.TrimSpace(modelName) == "" {
		modelName = "large-v3-turbo"
	}

	ggmlFile := ggmlModelFilename(modelName)
	modelPath := filepath.Join(w.envPath, "models", ggmlFile)

	if _, err := os.Stat(modelPath); err == nil {
		return modelPath, nil // Already downloaded
	}

	warmKey := fmt.Sprintf("whisper-cpp-model:%s:%s", w.envPath, modelName)
	err := RunModelWarmOnce(warmKey, func() error {
		// Check again inside singleflight
		if _, err := os.Stat(modelPath); err == nil {
			MarkModelWarm(warmKey)
			return nil
		}

		modelURL := ggmlModelURL(modelName)
		logger.Info("Downloading GGML model", "model", modelName, "url", modelURL, "dest", modelPath)

		if err := downloader.DownloadFile(ctx, modelURL, modelPath); err != nil {
			return fmt.Errorf("failed to download GGML model %s: %w", modelName, err)
		}

		MarkModelWarm(warmKey)
		logger.Info("GGML model downloaded", "model", modelName)
		return nil
	})
	if err != nil {
		return "", err
	}
	return modelPath, nil
}

// WarmModel ensures the whisper.cpp binary is ready and the GGML model is downloaded.
// This satisfies the simpleWarmable interface used by the runtime warmup system.
func (w *WhisperCppAdapter) WarmModel(ctx context.Context, modelName string) error {
	if err := w.PrepareEnvironment(ctx); err != nil {
		return err
	}
	_, err := w.EnsureModel(ctx, modelName)
	return err
}

// Transcribe processes audio using whisper.cpp.
func (w *WhisperCppAdapter) Transcribe(ctx context.Context, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext) (*interfaces.TranscriptResult, error) {
	startTime := time.Now()
	w.LogProcessingStart(input, procCtx)
	defer func() {
		w.LogProcessingEnd(procCtx, time.Since(startTime), nil)
	}()

	if err := w.ValidateAudioInput(input); err != nil {
		return nil, fmt.Errorf("invalid audio input: %w", err)
	}
	if err := w.ValidateParameters(params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	if err := w.PrepareEnvironment(ctx); err != nil {
		return nil, err
	}

	modelName := w.GetStringParameter(params, "model")
	modelPath, err := w.EnsureModel(ctx, modelName)
	if err != nil {
		return nil, err
	}

	tempDir, err := w.CreateTempDirectory(procCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer w.CleanupTempDirectory(tempDir)

	// Convert audio to 16kHz mono WAV (whisper.cpp requirement)
	wavPath := filepath.Join(tempDir, "input.wav")
	if err := w.convertToWav(ctx, input.FilePath, wavPath); err != nil {
		return nil, fmt.Errorf("failed to convert audio to WAV: %w", err)
	}

	// Build whisper.cpp command
	outputBase := filepath.Join(tempDir, "output")
	args := w.buildArgs(wavPath, modelPath, outputBase, params)

	cmd := exec.CommandContext(ctx, binaries.WhisperCpp(), args...)

	logFile, err := os.OpenFile(filepath.Join(procCtx.OutputDirectory, "transcription.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Warn("Failed to create log file", "error", err)
	} else {
		defer logFile.Close()
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	logger.Info("Executing whisper.cpp", "model", modelName, "args", strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("transcription was cancelled")
		}
		logPath := filepath.Join(procCtx.OutputDirectory, "transcription.log")
		logTail, _ := w.ReadLogTail(logPath, 2048)
		return nil, fmt.Errorf("whisper.cpp execution failed: %w\nLogs:\n%s", err, logTail)
	}

	result, err := w.parseResult(outputBase + ".json")
	if err != nil {
		return nil, fmt.Errorf("failed to parse result: %w", err)
	}

	result.ProcessingTime = time.Since(startTime)
	result.ModelUsed = modelName
	result.Metadata = w.CreateDefaultMetadata(params)

	logger.Info("whisper.cpp transcription completed",
		"segments", len(result.Segments),
		"words", len(result.WordSegments),
		"processing_time", result.ProcessingTime)

	return result, nil
}

// convertToWav uses FFmpeg to convert any audio to 16kHz mono WAV.
func (w *WhisperCppAdapter) convertToWav(ctx context.Context, inputPath, outputPath string) error {
	cmd := exec.CommandContext(ctx, binaries.FFmpeg(),
		"-i", inputPath,
		"-ar", "16000",
		"-ac", "1",
		"-c:a", "pcm_s16le",
		"-y",
		outputPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg conversion failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// buildArgs constructs the whisper.cpp CLI arguments.
func (w *WhisperCppAdapter) buildArgs(wavPath, modelPath, outputBase string, params map[string]interface{}) []string {
	args := []string{
		"-m", modelPath,
		"-f", wavPath,
		"-oj",                // JSON output
		"-of", outputBase,    // Output file prefix
		"--print-progress",   // Show progress
	}

	if language := w.GetStringParameter(params, "language"); language != "" {
		args = append(args, "-l", language)
	} else {
		args = append(args, "-l", "auto")
	}

	if task := w.GetStringParameter(params, "task"); task == "translate" {
		args = append(args, "--translate")
	}

	if threads := w.GetIntParameter(params, "threads"); threads > 0 {
		args = append(args, "-t", strconv.Itoa(threads))
	}

	if beamSize := w.GetIntParameter(params, "beam_size"); beamSize > 0 {
		args = append(args, "-bs", strconv.Itoa(beamSize))
	}

	return args
}

// parseResult reads the whisper.cpp JSON output.
func (w *WhisperCppAdapter) parseResult(jsonPath string) (*interfaces.TranscriptResult, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read whisper.cpp JSON output: %w", err)
	}

	// whisper.cpp JSON format
	var cppResult struct {
		Transcription []struct {
			Timestamps struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"timestamps"`
			Offsets struct {
				From int64 `json:"from"`
				To   int64 `json:"to"`
			} `json:"offsets"`
			Text   string `json:"text"`
			Tokens []struct {
				Text      string  `json:"text"`
				Offsets   struct {
					From int64 `json:"from"`
					To   int64 `json:"to"`
				} `json:"offsets"`
				Timestamps struct {
					From string `json:"from"`
					To   string `json:"to"`
				} `json:"timestamps"`
				P float64 `json:"p"`
			} `json:"tokens"`
		} `json:"transcription"`
	}

	if err := json.Unmarshal(data, &cppResult); err != nil {
		return nil, fmt.Errorf("failed to parse whisper.cpp JSON: %w", err)
	}

	result := &interfaces.TranscriptResult{
		Segments:     make([]interfaces.TranscriptSegment, 0, len(cppResult.Transcription)),
		WordSegments: make([]interfaces.TranscriptWord, 0),
	}

	var textParts []string
	for _, seg := range cppResult.Transcription {
		startSec := float64(seg.Offsets.From) / 1000.0
		endSec := float64(seg.Offsets.To) / 1000.0

		result.Segments = append(result.Segments, interfaces.TranscriptSegment{
			Start: startSec,
			End:   endSec,
			Text:  strings.TrimSpace(seg.Text),
		})
		textParts = append(textParts, strings.TrimSpace(seg.Text))

		// Extract word-level timestamps from tokens
		for _, tok := range seg.Tokens {
			word := strings.TrimSpace(tok.Text)
			if word == "" || strings.HasPrefix(word, "[") {
				continue // Skip special tokens
			}
			result.WordSegments = append(result.WordSegments, interfaces.TranscriptWord{
				Start: float64(tok.Offsets.From) / 1000.0,
				End:   float64(tok.Offsets.To) / 1000.0,
				Word:  word,
				Score: tok.P,
			})
		}
	}

	result.Text = strings.Join(textParts, " ")

	return result, nil
}

// ggmlModelFilename returns the GGML model filename for a given model name.
func ggmlModelFilename(model string) string {
	return fmt.Sprintf("ggml-%s-%s.bin", model, defaultGGMLQuant)
}

// ggmlModelURL returns the HuggingFace download URL for a GGML model.
func ggmlModelURL(model string) string {
	filename := ggmlModelFilename(model)
	// The whisper.cpp HuggingFace repo organises quantised models under the root.
	return fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", defaultGGMLModelRepo, filename)
}

// GGMLModelFilename exports the filename helper for use in desktop runtime seed.
func GGMLModelFilename(model string) string {
	return ggmlModelFilename(model)
}

// GGMLModelURL exports the URL helper for use in desktop runtime seed.
func GGMLModelURL(model string) string {
	return ggmlModelURL(model)
}

// GetEstimatedProcessingTime provides whisper.cpp-specific time estimation.
func (w *WhisperCppAdapter) GetEstimatedProcessingTime(input interfaces.AudioInput) time.Duration {
	return w.BaseAdapter.GetEstimatedProcessingTime(input)
}
