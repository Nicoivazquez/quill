package adapters

import (
	"context"
	"embed"
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
	"quill/pkg/logger"
)

//go:embed py/pyannote/*
var pyannoteScripts embed.FS

const OutputFormatJSON = "json"

// PyAnnoteAdapter implements the DiarizationAdapter interface for PyAnnote
type PyAnnoteAdapter struct {
	*BaseAdapter
	envPath string
}

// NewPyAnnoteAdapter creates a new PyAnnote diarization adapter
func NewPyAnnoteAdapter(envPath string) *PyAnnoteAdapter {
	capabilities := interfaces.ModelCapabilities{
		ModelID:            "pyannote",
		ModelFamily:        "pyannote",
		DisplayName:        "PyAnnote Speaker Diarization Community 1",
		Description:        "PyAnnote community model for speaker diarization",
		Version:            "1.0.0",
		SupportedLanguages: []string{"*"}, // Language-agnostic
		SupportedFormats:   []string{"wav", "mp3", "flac", "m4a", "ogg"},
		RequiresGPU:        false, // Optional GPU support
		MemoryRequirement:  2048,  // 2GB recommended
		Features: map[string]bool{
			"speaker_detection":   true,
			"speaker_constraints": true,
			"confidence_scores":   true,
			"rttm_output":         true,
			"flexible_speakers":   true,
		},
		Metadata: map[string]string{
			"engine":    "pyannote_audio",
			"framework": "pytorch",
			"license":   "MIT",
			"requires":  "huggingface_token",
			"model_hub": "huggingface",
		},
	}

	schema := []interfaces.ParameterSchema{
		// Core settings
		{
			Name:        "hf_token",
			Type:        "string",
			Required:    false,
			Default:     nil,
			Description: "HuggingFace token for model access (optional if HF_TOKEN env var is set)",
			Group:       "basic",
		},
		{
			Name:        "model",
			Type:        "string",
			Required:    false,
			Default:     "pyannote/speaker-diarization-community-1",
			Options:     []string{"pyannote/speaker-diarization-community-1"},
			Description: "PyAnnote model to use",
			Group:       "basic",
		},

		// Speaker constraints
		{
			Name:        "min_speakers",
			Type:        "int",
			Required:    false,
			Default:     nil,
			Min:         &[]float64{1}[0],
			Max:         &[]float64{20}[0],
			Description: "Minimum number of speakers",
			Group:       "basic",
		},
		{
			Name:        "max_speakers",
			Type:        "int",
			Required:    false,
			Default:     nil,
			Min:         &[]float64{1}[0],
			Max:         &[]float64{20}[0],
			Description: "Maximum number of speakers",
			Group:       "basic",
		},

		// Output settings
		{
			Name:        "output_format",
			Type:        "string",
			Required:    false,
			Default:     "rttm",
			Options:     []string{"rttm", OutputFormatJSON},
			Description: "Output format for diarization results",
			Group:       "advanced",
		},

		// Performance settings
		{
			Name:        "use_auth_token",
			Type:        "bool",
			Required:    false,
			Default:     true,
			Description: "Use authentication token for model access",
			Group:       "advanced",
		},
		{
			Name:        "device",
			Type:        "string",
			Required:    false,
			Default:     "auto",
			Options:     []string{"auto", "cpu", "cuda"},
			Description: "Device to use for computation (auto, cpu, or cuda)",
			Group:       "advanced",
		},

		{
			Name:        "auto_convert_audio",
			Type:        "bool",
			Required:    false,
			Default:     true,
			Description: "Automatically convert audio to supported format",
			Group:       "advanced",
		},
	}

	baseAdapter := NewBaseAdapter("pyannote", envPath, capabilities, schema)

	adapter := &PyAnnoteAdapter{
		BaseAdapter: baseAdapter,
		envPath:     envPath,
	}

	return adapter
}

// GetMaxSpeakers returns the maximum number of speakers PyAnnote can handle
func (p *PyAnnoteAdapter) GetMaxSpeakers() int {
	return 20 // Practical limit for PyAnnote
}

// GetMinSpeakers returns the minimum number of speakers PyAnnote requires
func (p *PyAnnoteAdapter) GetMinSpeakers() int {
	return 1
}

// PrepareEnvironment sets up the dedicated PyAnnote environment
func (p *PyAnnoteAdapter) PrepareEnvironment(ctx context.Context) error {
	logger.Info("Preparing PyAnnote environment", "env_path", p.envPath)

	// Always ensure diarization script exists
	if err := p.copyDiarizationScript(); err != nil {
		return fmt.Errorf("failed to create diarization script: %w", err)
	}

	// Check if dependencies changed (e.g. pyannote version pin update)
	needsResync := p.pyprojectChanged()

	// Check if PyAnnote is already available (using cache to speed up repeated checks)
	if !needsResync && CheckEnvironmentReady(p.envPath, "from pyannote.audio import Pipeline") {
		logger.Info("PyAnnote already available in environment")
		// Still ensure script exists
		if err := p.copyDiarizationScript(); err != nil {
			return fmt.Errorf("failed to create diarization script: %w", err)
		}
		p.initialized = true
		return nil
	}

	if needsResync {
		logger.Info("PyAnnote pyproject.toml changed, re-syncing environment")
	}

	// Create environment if it doesn't exist or is incomplete
	if err := p.setupPyAnnoteEnvironment(); err != nil {
		return fmt.Errorf("failed to setup PyAnnote environment: %w", err)
	}

	// Verify PyAnnote is now available
	testCmd := exec.Command(binaries.UV(), "run", "--native-tls", "--project", p.envPath, "python", "-c", "from pyannote.audio import Pipeline")
	if testCmd.Run() != nil {
		logger.Warn("PyAnnote environment test still failed after setup")
	}

	p.initialized = true
	logger.Info("PyAnnote environment prepared successfully")
	return nil
}

// applyPyprojectTransforms applies all dynamic transformations to the embedded
// pyproject.toml content: CUDA URL replacement and platform-scoped environments
// to prevent cross-platform resolution failures (e.g. cu126 index lacks torch<2.6.0).
func applyPyprojectTransforms(content string) string {
	// Replace the hardcoded PyTorch URL with the dynamic one based on environment
	result := strings.Replace(content, "https://download.pytorch.org/whl/cu126", GetPyTorchWheelURL(), 1)

	// Add platform-specific environment constraint so uv only resolves for the
	// current OS. Without this, uv tries to resolve all platform splits and fails
	// when a CUDA index lacks compatible torch versions for our constraints.
	envConstraint := `"sys_platform == 'darwin'"`
	if runtime.GOOS == "linux" {
		envConstraint = `"sys_platform == 'linux'"`
	}
	result = strings.Replace(result, "[tool.uv.sources]",
		fmt.Sprintf("[tool.uv]\nenvironments = [%s]\n\n[tool.uv.sources]", envConstraint), 1)

	return result
}

// setupPyAnnoteEnvironment creates the Python environment
func (p *PyAnnoteAdapter) setupPyAnnoteEnvironment() error {
	if err := os.MkdirAll(p.envPath, 0755); err != nil {
		return fmt.Errorf("failed to create pyannote directory: %w", err)
	}

	// Read pyproject.toml for PyAnnote
	pyprojectContent, err := pyannoteScripts.ReadFile("py/pyannote/pyproject.toml")
	if err != nil {
		return fmt.Errorf("failed to read embedded pyproject.toml: %w", err)
	}

	contentStr := applyPyprojectTransforms(string(pyprojectContent))

	pyprojectPath := filepath.Join(p.envPath, "pyproject.toml")
	if err := os.WriteFile(pyprojectPath, []byte(contentStr), 0644); err != nil {
		return fmt.Errorf("failed to write pyproject.toml: %w", err)
	}

	// Remove stale lock file to force fresh resolution with new constraints
	lockPath := filepath.Join(p.envPath, "uv.lock")
	if _, err := os.Stat(lockPath); err == nil {
		os.Remove(lockPath)
	}

	// Run uv sync
	logger.Info("Installing PyAnnote dependencies")
	cmd := exec.Command(binaries.UV(), "sync", "--native-tls")
	cmd.Dir = p.envPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uv sync failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// pyprojectChanged returns true if the embedded pyproject.toml differs from the
// one on disk, indicating dependencies need to be re-synced.
func (p *PyAnnoteAdapter) pyprojectChanged() bool {
	embeddedContent, err := pyannoteScripts.ReadFile("py/pyannote/pyproject.toml")
	if err != nil {
		return false
	}

	expected := applyPyprojectTransforms(string(embeddedContent))

	diskContent, err := os.ReadFile(filepath.Join(p.envPath, "pyproject.toml"))
	if err != nil {
		return true // file missing, needs setup
	}

	return string(diskContent) != expected
}

// copyDiarizationScript creates the Python script for PyAnnote diarization
func (p *PyAnnoteAdapter) copyDiarizationScript() error {
	// Ensure the directory exists first
	if err := os.MkdirAll(p.envPath, 0755); err != nil {
		return fmt.Errorf("failed to create pyannote directory: %w", err)
	}

	scriptContent, err := pyannoteScripts.ReadFile("py/pyannote/pyannote_diarize.py")
	if err != nil {
		return fmt.Errorf("failed to read embedded pyannote_diarize.py: %w", err)
	}

	scriptPath := filepath.Join(p.envPath, "pyannote_diarize.py")
	if err := os.WriteFile(scriptPath, scriptContent, 0755); err != nil {
		return fmt.Errorf("failed to write diarization script: %w", err)
	}

	return nil
}

// Diarize processes audio using PyAnnote
func (p *PyAnnoteAdapter) Diarize(ctx context.Context, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext) (*interfaces.DiarizationResult, error) {
	startTime := time.Now()
	p.LogProcessingStart(input, procCtx)
	defer func() {
		p.LogProcessingEnd(procCtx, time.Since(startTime), nil)
	}()

	// Validate input
	if err := p.ValidateAudioInput(input); err != nil {
		return nil, fmt.Errorf("invalid audio input: %w", err)
	}

	// Validate parameters
	if err := p.ValidateParameters(params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	// Check for HF token - use param first, then fall back to environment variable
	hfToken := p.GetStringParameter(params, "hf_token")
	if hfToken == "" {
		hfToken = os.Getenv("HF_TOKEN")
	}
	if hfToken == "" {
		return nil, fmt.Errorf("HuggingFace token is required for PyAnnote diarization. Set HF_TOKEN environment variable or provide it in the UI")
	}
	// Store resolved token in params for buildPyAnnoteArgs
	params["hf_token"] = hfToken

	// Create temporary directory
	tempDir, err := p.CreateTempDirectory(procCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer p.CleanupTempDirectory(tempDir)

	// Build command arguments
	args, err := p.buildPyAnnoteArgs(input, params, tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to build command: %w", err)
	}

	// Execute PyAnnote
	cmd := exec.CommandContext(ctx, binaries.UV(), args...)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	// Setup log file — write diagnostic markers and capture stderr separately
	// so we can see errors even when the Python process crashes.
	logPath := filepath.Join(procCtx.OutputDirectory, "transcription.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Warn("Failed to create log file", "error", err)
	} else {
		defer logFile.Close()
		fmt.Fprintf(logFile, "\n[diarization] Starting PyAnnote speaker diarization...\n")
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	logger.Info("Executing PyAnnote command", "args", strings.Join(args, " "))

	if err := cmd.Run(); err != nil {
		errMsg := fmt.Sprintf("PyAnnote execution failed: %v", err)
		if logFile != nil {
			fmt.Fprintf(logFile, "\n[diarization] ERROR: %s\n", errMsg)
		}

		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("diarization was cancelled")
		}

		logTail, readErr := p.ReadLogTail(logPath, 2048)
		if readErr != nil {
			logger.Warn("Failed to read log tail", "error", readErr)
		}

		logger.Error("PyAnnote execution failed", "error", err)
		return nil, fmt.Errorf("PyAnnote execution failed: %w\nLogs:\n%s", err, logTail)
	}

	if logFile != nil {
		fmt.Fprintf(logFile, "\n[diarization] PyAnnote completed successfully\n")
	}

	// Parse result
	result, err := p.parseResult(tempDir, input, params)
	if err != nil {
		return nil, fmt.Errorf("failed to parse result: %w", err)
	}

	result.ProcessingTime = time.Since(startTime)
	result.ModelUsed = p.GetStringParameter(params, "model")
	result.Metadata = p.CreateDefaultMetadata(params)

	logger.Info("PyAnnote diarization completed",
		"segments", len(result.Segments),
		"speakers", result.SpeakerCount,
		"processing_time", result.ProcessingTime)

	return result, nil
}

// buildPyAnnoteArgs builds the command arguments for PyAnnote
func (p *PyAnnoteAdapter) buildPyAnnoteArgs(input interfaces.AudioInput, params map[string]interface{}, tempDir string) ([]string, error) {
	outputFormat := p.GetStringParameter(params, "output_format")
	var outputFile string
	if outputFormat == OutputFormatJSON {
		outputFile = filepath.Join(tempDir, "result.json")
	} else {
		outputFile = filepath.Join(tempDir, "result.rttm")
	}

	scriptPath := filepath.Join(p.envPath, "pyannote_diarize.py")
	args := []string{
		"run", "--native-tls", "--project", p.envPath, "python", scriptPath,
		input.FilePath,
		"--output", outputFile,
		"--hf-token", p.GetStringParameter(params, "hf_token"),
	}

	// Add model
	if model := p.GetStringParameter(params, "model"); model != "" {
		args = append(args, "--model", model)
	}

	// Add speaker constraints
	if minSpeakers := p.GetIntParameter(params, "min_speakers"); minSpeakers > 0 {
		args = append(args, "--min-speakers", strconv.Itoa(minSpeakers))
	}
	if maxSpeakers := p.GetIntParameter(params, "max_speakers"); maxSpeakers > 0 {
		args = append(args, "--max-speakers", strconv.Itoa(maxSpeakers))
	}

	// Add output format
	args = append(args, "--output-format", outputFormat)

	// Device is handled automatically by the script

	return args, nil
}

// parseResult parses the PyAnnote output
func (p *PyAnnoteAdapter) parseResult(tempDir string, input interfaces.AudioInput, params map[string]interface{}) (*interfaces.DiarizationResult, error) {
	outputFormat := p.GetStringParameter(params, "output_format")

	if outputFormat == OutputFormatJSON {
		return p.parseJSONResult(tempDir)
	}
	return p.parseRTTMResult(tempDir, input)
}

// parseJSONResult parses JSON format output
func (p *PyAnnoteAdapter) parseJSONResult(tempDir string) (*interfaces.DiarizationResult, error) {
	resultFile := filepath.Join(tempDir, "result.json")

	data, err := os.ReadFile(resultFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read result file: %w", err)
	}

	var pyannoteResult struct {
		AudioFile string `json:"audio_file"`
		Model     string `json:"model"`
		Segments  []struct {
			Start      float64 `json:"start"`
			End        float64 `json:"end"`
			Speaker    string  `json:"speaker"`
			Confidence float64 `json:"confidence"`
			Duration   float64 `json:"duration"`
		} `json:"segments"`
		Speakers      []string `json:"speakers"`
		SpeakerCount  int      `json:"speaker_count"`
		TotalDuration float64  `json:"total_duration"`
	}

	if err := json.Unmarshal(data, &pyannoteResult); err != nil {
		return nil, fmt.Errorf("failed to parse JSON result: %w", err)
	}

	// Convert to standard format
	result := &interfaces.DiarizationResult{
		Segments:     make([]interfaces.DiarizationSegment, len(pyannoteResult.Segments)),
		SpeakerCount: pyannoteResult.SpeakerCount,
		Speakers:     pyannoteResult.Speakers,
	}

	for i, seg := range pyannoteResult.Segments {
		result.Segments[i] = interfaces.DiarizationSegment{
			Start:      seg.Start,
			End:        seg.End,
			Speaker:    seg.Speaker,
			Confidence: seg.Confidence,
		}
	}

	return result, nil
}

// parseRTTMResult parses RTTM format output
func (p *PyAnnoteAdapter) parseRTTMResult(tempDir string, input interfaces.AudioInput) (*interfaces.DiarizationResult, error) {
	resultFile := filepath.Join(tempDir, "result.rttm")

	data, err := os.ReadFile(resultFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read result file: %w", err)
	}

	var segments []interfaces.DiarizationSegment
	speakers := make(map[string]bool)

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "SPEAKER") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}

		start, err := strconv.ParseFloat(parts[3], 64)
		if err != nil {
			continue
		}

		duration, err := strconv.ParseFloat(parts[4], 64)
		if err != nil {
			continue
		}

		end := start + duration
		speaker := strings.ToLower(parts[7])
		speakers[speaker] = true

		segments = append(segments, interfaces.DiarizationSegment{
			Start:      start,
			End:        end,
			Speaker:    speaker,
			Confidence: 1.0, // RTTM doesn't include confidence scores
		})
	}

	// Convert speakers map to slice
	speakerList := make([]string, 0, len(speakers))
	for speaker := range speakers {
		speakerList = append(speakerList, speaker)
	}

	result := &interfaces.DiarizationResult{
		Segments:     segments,
		SpeakerCount: len(speakers),
		Speakers:     speakerList,
	}

	return result, nil
}

// GetEstimatedProcessingTime provides PyAnnote-specific time estimation
func (p *PyAnnoteAdapter) GetEstimatedProcessingTime(input interfaces.AudioInput) time.Duration {
	// PyAnnote is typically faster than real-time for diarization
	baseTime := p.BaseAdapter.GetEstimatedProcessingTime(input)

	// PyAnnote typically processes at about 10-15% of audio duration
	return time.Duration(float64(baseTime) * 0.5)
}
