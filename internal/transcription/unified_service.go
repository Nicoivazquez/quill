package transcription

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

	"quill/internal/database"
	"quill/internal/models"
	"quill/internal/repository"
	"quill/internal/sse"
	"quill/internal/transcription/interfaces"
	"quill/internal/transcription/pipeline"
	"quill/internal/transcription/registry"
	"quill/internal/webhook"
	"quill/pkg/binaries"
	"quill/pkg/logger"
)

const (
	ModelWhisperX              = "whisperx"
	ModelPyannote              = "pyannote"
	ModelParakeet              = "parakeet"
	ModelCanary                = "canary"
	ModelSortformer            = "sortformer"
	ModelOpenAI                = "openai_whisper"
	ModelVoxtral               = "voxtral"
	ModelDiarization31         = "pyannote/speaker-diarization-3.1"
	ModelDiarizationCommunity1 = "pyannote/speaker-diarization-community-1"
	FamilyNvidiaCanary         = "nvidia_canary"
	FamilyNvidiaParakeet       = "nvidia_parakeet"
	FamilyWhisper              = "whisper"
	FamilyOpenAI               = "openai"
	FamilyMistralVoxtral       = "mistral_voxtral"
	FamilyMLXWhisper           = "mlx_whisper"
	FamilyWhisperCpp           = "whisper_cpp"
	ModelMLXWhisper            = "mlx_whisper"
	ModelWhisperCpp            = "whisper_cpp"
	DiarizeSortformer          = "nvidia_sortformer"
	OutputFormatJSON           = "json"
)

// UnifiedTranscriptionService provides a unified interface for all transcription and diarization models
type UnifiedTranscriptionService struct {
	registry              *registry.ModelRegistry
	pipeline              *pipeline.ProcessingPipeline
	preprocessors         map[string]interfaces.Preprocessor
	postprocessors        map[string]interfaces.Postprocessor
	tempDirectory         string
	outputDirectory       string
	defaultModelIDs       map[string]string      // Default model IDs for each task type
	multiTrackTranscriber *MultiTrackTranscriber // For termination support
	jobRepo               repository.JobRepository
	webhookService        *webhook.Service
	broadcaster           *sse.Broadcaster
	postMaterializeHook   func(job *models.TranscriptionJob) // Called after successful artifact materialization
}

// NewUnifiedTranscriptionService creates a new unified transcription service
func NewUnifiedTranscriptionService(jobRepo repository.JobRepository, tempDir, outputDir string) *UnifiedTranscriptionService {
	return &UnifiedTranscriptionService{
		registry:        registry.GetRegistry(),
		pipeline:        pipeline.NewProcessingPipeline(),
		preprocessors:   make(map[string]interfaces.Preprocessor),
		postprocessors:  make(map[string]interfaces.Postprocessor),
		tempDirectory:   tempDir,
		outputDirectory: outputDir,
		defaultModelIDs: map[string]string{
			"transcription": ModelWhisperX,
			"diarization":   ModelPyannote,
		},
		jobRepo:        jobRepo,
		webhookService: webhook.NewService(),
	}
}

// SetBroadcaster sets the SSE broadcaster for the service
func (u *UnifiedTranscriptionService) SetBroadcaster(b *sse.Broadcaster) {
	u.broadcaster = b
}

// SetPostMaterializeHook registers a callback invoked after transcription artifacts
// are successfully written to disk. Used for auto-publish to Obsidian.
func (u *UnifiedTranscriptionService) SetPostMaterializeHook(fn func(job *models.TranscriptionJob)) {
	u.postMaterializeHook = fn
}

// Initialize prepares all registered models for use
func (u *UnifiedTranscriptionService) Initialize(ctx context.Context) error {
	logger.Info("Initializing unified transcription service")

	// Create necessary directories
	if err := os.MkdirAll(u.tempDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	if err := os.MkdirAll(u.outputDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Initialize all registered models
	if err := u.registry.InitializeModels(ctx); err != nil {
		return fmt.Errorf("failed to initialize models: %w", err)
	}

	logger.Info("Unified transcription service initialized successfully")
	return nil
}

// ProcessJob processes a transcription job using the new adapter architecture
//
//nolint:gocyclo // Complex orchestration required
func (u *UnifiedTranscriptionService) ProcessJob(ctx context.Context, jobID string) error {
	startTime := time.Now()
	logger.Info("Processing job with unified service", "job_id", jobID)

	// Get the job from database
	// Get the job from database
	job, err := u.jobRepo.FindWithAssociations(ctx, jobID)
	if err != nil {
		return fmt.Errorf("failed to get job: %w", err)
	}

	// Create execution record
	execution := &models.TranscriptionJobExecution{
		TranscriptionJobID: jobID,
		StartedAt:          startTime,
		ActualParameters:   job.Parameters,
		Status:             models.StatusProcessing,
	}

	if err := u.jobRepo.CreateExecution(ctx, execution); err != nil {
		return fmt.Errorf("failed to create execution record: %w", err)
	}

	// Broadcast initial processing status
	if u.broadcaster != nil {
		u.broadcaster.Broadcast(jobID, "job_update", map[string]interface{}{
			"job_id": jobID,
			"status": models.StatusProcessing,
		})
	}

	// Helper function to update execution status
	updateExecutionStatus := func(status models.JobStatus, errorMsg string) {
		completedAt := time.Now()
		execution.CompletedAt = &completedAt
		execution.Status = status
		execution.CalculateProcessingDuration()

		if errorMsg != "" {
			execution.ErrorMessage = &errorMsg
		}

		_ = u.jobRepo.UpdateExecution(ctx, execution)

		// Broadcast update via SSE
		if u.broadcaster != nil {
			u.broadcaster.Broadcast(jobID, "job_update", map[string]interface{}{
				"job_id": jobID,
				"status": status,
				"error":  errorMsg,
			})
		}

		// Trigger webhook if callback URL is present
		if job.Parameters.CallbackURL != nil && *job.Parameters.CallbackURL != "" {
			payload := webhook.WebhookPayload{
				JobID:        job.ID,
				Status:       status,
				AudioPath:    job.AudioPath,
				Transcript:   job.Transcript,
				Summary:      job.Summary,
				ErrorMessage: execution.ErrorMessage,
				CompletedAt:  completedAt,
				Metadata: map[string]interface{}{
					"model":        job.Parameters.Model,
					"model_family": job.Parameters.ModelFamily,
					"duration_ms":  execution.ProcessingDuration,
				},
			}

			// Send webhook asynchronously to not block the main process
			go func() {
				// Create a new context with timeout for the webhook
				webhookCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				if err := u.webhookService.SendWebhook(webhookCtx, *job.Parameters.CallbackURL, payload); err != nil {
					logger.Error("Failed to send webhook", "job_id", job.ID, "error", err)
				}
			}()
		}
	}

	// Check for multi-track processing
	if job.IsMultiTrack && job.Parameters.IsMultiTrackEnabled {
		logger.Info("Processing multi-track job", "job_id", jobID)
		if err := u.processMultiTrackJob(ctx, job); err != nil {
			errMsg := fmt.Sprintf("multi-track processing failed: %v", err)
			updateExecutionStatus(models.StatusFailed, errMsg)
			return fmt.Errorf("%s", errMsg)
		}
	} else {
		// Process single track
		if err := u.processSingleTrackJob(ctx, job); err != nil {
			errMsg := fmt.Sprintf("single-track processing failed: %v", err)
			updateExecutionStatus(models.StatusFailed, errMsg)
			return fmt.Errorf("%s", errMsg)
		}
	}

	// Success
	updateExecutionStatus(models.StatusCompleted, "")
	logger.Info("Job processed successfully", "job_id", jobID, "duration", time.Since(startTime))
	return nil
}

// processSingleTrackJob handles single audio file transcription
//
//nolint:gocyclo // Orchestrator function with multiple steps
func (u *UnifiedTranscriptionService) processSingleTrackJob(ctx context.Context, job *models.TranscriptionJob) error {
	logger.Info("Processing single-track job", "job_id", job.ID, "model_family", job.Parameters.ModelFamily)

	// Create processing context
	procCtx := interfaces.ProcessingContext{
		JobID:           job.ID,
		OutputDirectory: filepath.Join(u.outputDirectory, job.ID),
		TempDirectory:   u.tempDirectory,
		Metadata:        map[string]string{},
	}

	// Create output directory
	if err := os.MkdirAll(procCtx.OutputDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create audio input
	audioInput, err := u.createAudioInput(job.AudioPath)
	if err != nil {
		return fmt.Errorf("failed to create audio input: %w", err)
	}

	// Determine models to use first
	transcriptionModelID, diarizationModelID, err := u.selectModels(job.Parameters)
	if err != nil {
		return fmt.Errorf("failed to select models: %w", err)
	}

	// Apply preprocessing to ensure audio is in correct format (mono 16kHz)
	var preprocessedInput interfaces.AudioInput
	var tempFilesToCleanup []string

	// Get model capabilities for preprocessing decisions
	var capabilities interfaces.ModelCapabilities
	if transcriptionModelID != "" {
		if adapter, err := u.registry.GetTranscriptionAdapter(transcriptionModelID); err == nil {
			capabilities = adapter.GetCapabilities()
		}
	} else if diarizationModelID != "" {
		if adapter, err := u.registry.GetDiarizationAdapter(diarizationModelID); err == nil {
			capabilities = adapter.GetCapabilities()
		}
	}

	// Apply preprocessing
	preprocessedInput, err = u.pipeline.ProcessAudio(ctx, audioInput, capabilities)
	if err != nil {
		logger.Warn("Audio preprocessing failed, using original", "error", err)
		preprocessedInput = audioInput
	} else {
		// Track temporary file for cleanup if preprocessing created one
		if preprocessedInput.TempFilePath != "" && preprocessedInput.TempFilePath != audioInput.FilePath {
			tempFilesToCleanup = append(tempFilesToCleanup, preprocessedInput.TempFilePath)
			logger.Info("Audio preprocessing completed",
				"original", audioInput.FilePath,
				"converted", preprocessedInput.TempFilePath,
				"original_sr", audioInput.SampleRate,
				"converted_sr", preprocessedInput.SampleRate,
				"original_channels", audioInput.Channels,
				"converted_channels", preprocessedInput.Channels)
		}
	}

	// Ensure cleanup of temporary files when function exits
	defer func() {
		for _, tempFile := range tempFilesToCleanup {
			if err := os.Remove(tempFile); err != nil {
				logger.Warn("Failed to clean up temporary file", "file", tempFile, "error", err)
			} else {
				logger.Info("Cleaned up temporary file", "file", tempFile)
			}
		}
	}()

	var transcriptResult *interfaces.TranscriptResult
	var diarizationResult *interfaces.DiarizationResult

	// Perform transcription using the preprocessed audio
	if transcriptionModelID != "" {
		logger.Info("Running transcription", "model_id", transcriptionModelID)
		transcriptionAdapter, err := u.registry.GetTranscriptionAdapter(transcriptionModelID)
		if err != nil {
			return fmt.Errorf("failed to get transcription adapter: %w", err)
		}
		if !transcriptionAdapter.IsReady(ctx) {
			logger.Info("Preparing transcription model environment on demand", "model_id", transcriptionModelID)
			if err := transcriptionAdapter.PrepareEnvironment(ctx); err != nil {
				return fmt.Errorf("failed to prepare transcription model %s: %w", transcriptionModelID, err)
			}
		}

		// Convert parameters for this specific model
		params := u.convertParametersForModel(job.Parameters, transcriptionModelID)

		transcriptResult, err = transcriptionAdapter.Transcribe(ctx, preprocessedInput, params, procCtx)
		if err != nil {
			return fmt.Errorf("transcription failed: %w", err)
		}
	}

	// Perform diarization if requested and not already done by transcription
	if job.Parameters.Diarize && diarizationModelID != "" {
		// Convert parameters for diarization model
		diarizationParams := u.convertParametersForModel(job.Parameters, diarizationModelID)

		if !u.transcriptionIncludesDiarization(transcriptionModelID, job.Parameters) {
			logger.Info("Running separate diarization", "model_id", diarizationModelID)
			diarizationAdapter, err := u.registry.GetDiarizationAdapter(diarizationModelID)
			if err != nil {
				return fmt.Errorf("failed to get diarization adapter: %w", err)
			}
			if !diarizationAdapter.IsReady(ctx) {
				logger.Info("Preparing diarization model environment on demand", "model_id", diarizationModelID)
				if err := diarizationAdapter.PrepareEnvironment(ctx); err != nil {
					return fmt.Errorf("failed to prepare diarization model %s: %w", diarizationModelID, err)
				}
			}

			// Use the same preprocessed audio for diarization
			diarizationResult, err = diarizationAdapter.Diarize(ctx, preprocessedInput, diarizationParams, procCtx)
			if err != nil {
				return fmt.Errorf("diarization failed: %w", err)
			}

			// Merge diarization results with transcription
			if transcriptResult != nil && diarizationResult != nil {
				transcriptResult = u.mergeDiarizationWithTranscription(transcriptResult, diarizationResult)
			}
		}
	}

	// Save results to database
	if transcriptResult != nil {
		if err := u.saveTranscriptionResults(job.ID, transcriptResult); err != nil {
			return fmt.Errorf("failed to save transcription results: %w", err)
		}
	}

	return nil
}

// processMultiTrackJob handles multi-track audio processing
func (u *UnifiedTranscriptionService) processMultiTrackJob(ctx context.Context, job *models.TranscriptionJob) error {
	logger.Info("Processing multi-track job", "job_id", job.ID, "track_count", len(job.MultiTrackFiles))

	// Create unified processor for this service
	unifiedProcessor := &UnifiedJobProcessor{
		unifiedService: u,
	}

	// Create multi-track transcriber with unified processor and store reference for termination
	transcriber := NewMultiTrackTranscriber(unifiedProcessor)
	u.multiTrackTranscriber = transcriber

	// Process the multi-track transcription
	return transcriber.ProcessMultiTrackTranscription(ctx, job.ID)
}

// TerminateMultiTrackJob terminates a multi-track job and all its individual track jobs
func (u *UnifiedTranscriptionService) TerminateMultiTrackJob(jobID string) error {
	if u.multiTrackTranscriber == nil {
		return fmt.Errorf("no multi-track transcriber available")
	}
	return u.multiTrackTranscriber.TerminateMultiTrackJob(jobID)
}

// IsMultiTrackJob checks if a job is a multi-track job
func (u *UnifiedTranscriptionService) IsMultiTrackJob(jobID string) bool {
	job, err := u.jobRepo.FindByID(context.Background(), jobID)
	if err != nil || job == nil {
		return false
	}
	return job.IsMultiTrack
}

// selectModels determines which models to use based on job parameters
func (u *UnifiedTranscriptionService) selectModels(params models.WhisperXParams) (transcriptionModelID, diarizationModelID string, err error) {
	// Determine transcription model
	switch params.ModelFamily {
	case FamilyNvidiaParakeet:
		transcriptionModelID = ModelParakeet
	case FamilyNvidiaCanary:
		transcriptionModelID = ModelCanary
	case FamilyWhisper:
		transcriptionModelID = ModelWhisperX
	case FamilyOpenAI:
		transcriptionModelID = ModelOpenAI
	case FamilyMistralVoxtral:
		transcriptionModelID = ModelVoxtral
	case FamilyMLXWhisper:
		transcriptionModelID = ModelMLXWhisper
	case FamilyWhisperCpp:
		transcriptionModelID = ModelWhisperCpp
	default:
		transcriptionModelID = ModelWhisperX // Default fallback
	}

	// Determine diarization model if needed
	if params.Diarize {
		switch params.DiarizeModel {
		case DiarizeSortformer:
			diarizationModelID = ModelSortformer
		case ModelPyannote, ModelDiarization31, ModelDiarizationCommunity1:
			diarizationModelID = ModelPyannote
		default:
			diarizationModelID = ModelPyannote // Default fallback
		}
	}

	logger.Info("Selected models",
		"transcription", transcriptionModelID,
		"diarization", diarizationModelID,
		"original_family", params.ModelFamily,
		"original_diarize_model", params.DiarizeModel)

	return transcriptionModelID, diarizationModelID, nil
}

// transcriptionIncludesDiarization checks if the transcription model already includes diarization
func (u *UnifiedTranscriptionService) transcriptionIncludesDiarization(modelID string, params models.WhisperXParams) bool {
	// WhisperX includes diarization when enabled
	// WhisperX includes diarization when enabled
	if modelID == ModelWhisperX {
		if params.Diarize {
			// Check if it's using nvidia_sortformer (which requires separate processing)
			if params.DiarizeModel == DiarizeSortformer {
				return false
			}
			return true
		}
	}

	return false
}

// ffprobeOutput represents the JSON output from ffprobe
type ffprobeOutput struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		SampleRate string `json:"sample_rate"`
		Channels   int    `json:"channels"`
		Duration   string `json:"duration"`
		CodecName  string `json:"codec_name"`
		BitRate    string `json:"bit_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		Size     string `json:"size"`
	} `json:"format"`
}

// createAudioInput creates an AudioInput from a file path with real metadata
func (u *UnifiedTranscriptionService) createAudioInput(audioPath string) (interfaces.AudioInput, error) {
	// Get file info
	fileInfo, err := os.Stat(audioPath)
	if err != nil {
		return interfaces.AudioInput{}, fmt.Errorf("failed to stat audio file: %w", err)
	}

	// Determine format from extension
	ext := strings.ToLower(filepath.Ext(audioPath))
	format := strings.TrimPrefix(ext, ".")

	// Use ffprobe to get actual audio metadata
	audioInput := interfaces.AudioInput{
		FilePath: audioPath,
		Format:   format,
		Size:     fileInfo.Size(),
		Metadata: map[string]string{},
	}

	// Run ffprobe to get audio metadata
	cmd := exec.Command(binaries.FFprobe(),
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		audioPath)

	output, err := cmd.Output()
	if err != nil {
		logger.Warn("Failed to run ffprobe, using defaults", "error", err, "file", audioPath)
		// Fallback to defaults
		audioInput.SampleRate = 16000
		audioInput.Channels = 1
		audioInput.Duration = time.Duration(float64(fileInfo.Size()/32000)) * time.Second
		return audioInput, nil
	}

	// Parse ffprobe output
	var probeData ffprobeOutput
	if err := json.Unmarshal(output, &probeData); err != nil {
		logger.Warn("Failed to parse ffprobe output, using defaults", "error", err)
		audioInput.SampleRate = 16000
		audioInput.Channels = 1
		audioInput.Duration = time.Duration(float64(fileInfo.Size()/32000)) * time.Second
		return audioInput, nil
	}

	// Find the audio stream
	for _, stream := range probeData.Streams {
		if stream.CodecType == "audio" {
			// Parse sample rate
			if sampleRate, err := strconv.Atoi(stream.SampleRate); err == nil {
				audioInput.SampleRate = sampleRate
			} else {
				audioInput.SampleRate = 16000 // Default
			}

			// Set channels
			audioInput.Channels = stream.Channels
			if audioInput.Channels == 0 {
				audioInput.Channels = 1 // Default to mono
			}

			// Parse duration
			if duration, err := strconv.ParseFloat(stream.Duration, 64); err == nil {
				audioInput.Duration = time.Duration(duration * float64(time.Second))
			} else if duration, err := strconv.ParseFloat(probeData.Format.Duration, 64); err == nil {
				audioInput.Duration = time.Duration(duration * float64(time.Second))
			} else {
				// Fallback calculation
				audioInput.Duration = time.Duration(float64(fileInfo.Size()/32000)) * time.Second
			}

			// Store additional metadata
			audioInput.Metadata["codec"] = stream.CodecName
			if stream.BitRate != "" {
				audioInput.Metadata["bitrate"] = stream.BitRate
			}

			break
		}
	}

	// Set defaults if no audio stream found
	if audioInput.SampleRate == 0 {
		audioInput.SampleRate = 16000
	}
	if audioInput.Channels == 0 {
		audioInput.Channels = 1
	}

	logger.Info("Audio metadata extracted",
		"file", audioPath,
		"sample_rate", audioInput.SampleRate,
		"channels", audioInput.Channels,
		"duration", audioInput.Duration,
		"size", audioInput.Size)

	return audioInput, nil
}

// parametersToMap converts WhisperXParams to a generic parameter map
// convertParametersForModel converts WhisperX parameters to model-specific parameters
func (u *UnifiedTranscriptionService) convertParametersForModel(params models.WhisperXParams, modelID string) map[string]interface{} {
	switch modelID {
	case ModelParakeet:
		return u.convertToParakeetParams(params)
	case ModelCanary:
		return u.convertToCanaryParams(params)
	case ModelWhisperX:
		return u.convertToWhisperXParams(params)
	case ModelPyannote:
		return u.convertToPyannoteParams(params)
	case ModelSortformer:
		return u.convertToSortformerParams(params)
	case ModelOpenAI:
		return u.convertToOpenAIParams(params)
	case ModelVoxtral:
		return u.convertToVoxtralParams(params)
	case ModelMLXWhisper:
		return u.convertToMLXWhisperParams(params)
	case ModelWhisperCpp:
		return u.convertToWhisperCppParams(params)
	default:
		// Fallback to legacy conversion
		return u.parametersToMap(params)
	}
}

// convertToOpenAIParams converts to OpenAI-specific parameters
func (u *UnifiedTranscriptionService) convertToOpenAIParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		"model":       params.Model,
		"temperature": params.Temperature,
	}

	if params.Language != nil {
		paramMap["language"] = *params.Language
	}
	if params.InitialPrompt != nil {
		paramMap["prompt"] = *params.InitialPrompt
	}

	// Add API key if provided in params (e.g. from UI override)
	if params.APIKey != nil && *params.APIKey != "" {
		paramMap["api_key"] = *params.APIKey
	}

	return paramMap
}

// convertToVoxtralParams converts to Voxtral-specific parameters
func (u *UnifiedTranscriptionService) convertToVoxtralParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{}

	// Language
	if params.Language != nil {
		paramMap["language"] = *params.Language
	} else {
		paramMap["language"] = "en"
	}

	// Max new tokens
	if params.MaxNewTokens != nil {
		paramMap["max_new_tokens"] = *params.MaxNewTokens
	}

	return paramMap
}

// convertToMLXWhisperParams converts to MLX Whisper-specific parameters
func (u *UnifiedTranscriptionService) convertToMLXWhisperParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		"model":     params.Model,
		"task":      params.Task,
		"beam_size": params.BeamSize,
	}

	if params.Language != nil {
		paramMap["language"] = *params.Language
	}

	return paramMap
}

// convertToWhisperCppParams converts to whisper.cpp-specific parameters
func (u *UnifiedTranscriptionService) convertToWhisperCppParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		"model":     params.Model,
		"task":      params.Task,
		"threads":   params.Threads,
		"beam_size": params.BeamSize,
	}

	if params.Language != nil {
		paramMap["language"] = *params.Language
	}

	return paramMap
}

// convertToParakeetParams converts to Parakeet-specific parameters
func (u *UnifiedTranscriptionService) convertToParakeetParams(params models.WhisperXParams) map[string]interface{} {
	return map[string]interface{}{
		"timestamps":         true,
		"context_left":       params.AttentionContextLeft,
		"context_right":      params.AttentionContextRight,
		"output_format":      OutputFormatJSON,
		"auto_convert_audio": true,
	}
}

// convertToCanaryParams converts to Canary-specific parameters
func (u *UnifiedTranscriptionService) convertToCanaryParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		"timestamps":         true,
		"output_format":      OutputFormatJSON,
		"auto_convert_audio": true,
		"task":               params.Task,
	}

	// Set source language
	if params.Language != nil {
		paramMap["source_lang"] = *params.Language
	} else {
		paramMap["source_lang"] = "en"
	}

	// Set target language for translation
	if params.Task == "translate" {
		paramMap["target_lang"] = "en"
	}

	return paramMap
}

// convertToWhisperXParams converts to WhisperX-specific parameters
func (u *UnifiedTranscriptionService) convertToWhisperXParams(params models.WhisperXParams) map[string]interface{} {
	useWhisperXDiarization := params.Diarize && params.DiarizeModel != DiarizeSortformer

	// For WhisperX, we use the standard WhisperX parameters (no NVIDIA-specific ones)
	paramMap := map[string]interface{}{
		// Core parameters
		"model":        params.Model,
		"device":       params.Device,
		"device_index": params.DeviceIndex,
		"batch_size":   params.BatchSize,
		"compute_type": params.ComputeType,
		"threads":      params.Threads,

		// Task and language
		"task": params.Task,

		// Diarization
		"diarize": useWhisperXDiarization,

		// Quality settings
		"temperature": params.Temperature,
		"best_of":     params.BestOf,
		"beam_size":   params.BeamSize,
		"patience":    params.Patience,

		// VAD settings
		"vad_method": params.VadMethod,
		"vad_onset":  params.VadOnset,
		"vad_offset": params.VadOffset,
	}

	// WhisperX only supports pyannote diarization models. When Sortformer is selected,
	// diarization is run as a separate adapter step, so we intentionally omit diarize_model.
	if useWhisperXDiarization {
		paramMap["diarize_model"] = params.DiarizeModel
	}

	// Handle pointer fields - only add if not nil
	if params.Language != nil {
		paramMap["language"] = *params.Language
	}
	if params.MinSpeakers != nil {
		paramMap["min_speakers"] = *params.MinSpeakers
	}
	if params.MaxSpeakers != nil {
		paramMap["max_speakers"] = *params.MaxSpeakers
	}
	if params.HfToken != nil {
		paramMap["hf_token"] = *params.HfToken
	}
	if params.ModelDir != nil {
		paramMap["model_dir"] = *params.ModelDir
	}
	if params.AlignModel != nil {
		paramMap["align_model"] = *params.AlignModel
	}
	if params.SuppressTokens != nil {
		paramMap["suppress_tokens"] = *params.SuppressTokens
	}
	if params.InitialPrompt != nil {
		paramMap["initial_prompt"] = *params.InitialPrompt
	}

	return paramMap
}

// convertToPyannoteParams converts to PyAnnote-specific parameters
func (u *UnifiedTranscriptionService) convertToPyannoteParams(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		"output_format":      OutputFormatJSON,
		"auto_convert_audio": true,
		"device":             "auto",
	}

	if params.MinSpeakers != nil {
		paramMap["min_speakers"] = *params.MinSpeakers
	}
	if params.MaxSpeakers != nil {
		paramMap["max_speakers"] = *params.MaxSpeakers
	}
	if params.HfToken != nil {
		paramMap["hf_token"] = *params.HfToken
	}

	// Map VAD thresholds to Pyannote segmentation parameters
	// These control voice activity detection sensitivity for diarization
	if params.VadOnset > 0 {
		paramMap["segmentation_onset"] = params.VadOnset
	}
	if params.VadOffset > 0 {
		paramMap["segmentation_offset"] = params.VadOffset
	}

	return paramMap
}

// convertToSortformerParams converts to Sortformer-specific parameters
func (u *UnifiedTranscriptionService) convertToSortformerParams(params models.WhisperXParams) map[string]interface{} {
	return map[string]interface{}{
		"output_format":      OutputFormatJSON,
		"auto_convert_audio": true,
		// Sortformer is optimized for 4 speakers, no additional config needed
	}
}

func (u *UnifiedTranscriptionService) parametersToMap(params models.WhisperXParams) map[string]interface{} {
	paramMap := map[string]interface{}{
		// Core parameters
		"model":        params.Model,
		"device":       params.Device,
		"device_index": params.DeviceIndex,
		"batch_size":   params.BatchSize,
		"compute_type": params.ComputeType,
		"threads":      params.Threads,

		// Language and task
		"task": params.Task,

		// Diarization
		"diarize":       params.Diarize,
		"diarize_model": params.DiarizeModel,
	}

	// Handle pointer fields - only add if not nil
	if params.Language != nil {
		paramMap["language"] = *params.Language
	}
	if params.MinSpeakers != nil {
		paramMap["min_speakers"] = *params.MinSpeakers
	}
	if params.MaxSpeakers != nil {
		paramMap["max_speakers"] = *params.MaxSpeakers
	}
	if params.HfToken != nil {
		paramMap["hf_token"] = *params.HfToken
	}
	if params.ModelDir != nil {
		paramMap["model_dir"] = *params.ModelDir
	}
	if params.AlignModel != nil {
		paramMap["align_model"] = *params.AlignModel
	}
	if params.SuppressTokens != nil {
		paramMap["suppress_tokens"] = *params.SuppressTokens
	}
	if params.InitialPrompt != nil {
		paramMap["initial_prompt"] = *params.InitialPrompt
	}

	// Add remaining non-pointer fields
	paramMap["temperature"] = params.Temperature
	paramMap["best_of"] = params.BestOf
	paramMap["beam_size"] = params.BeamSize
	paramMap["patience"] = params.Patience
	paramMap["vad_method"] = params.VadMethod
	paramMap["vad_onset"] = params.VadOnset
	paramMap["vad_offset"] = params.VadOffset
	paramMap["context_left"] = params.AttentionContextLeft
	paramMap["context_right"] = params.AttentionContextRight
	paramMap["timestamps"] = true
	paramMap["output_format"] = OutputFormatJSON
	paramMap["auto_convert_audio"] = true

	// For Canary model, set source and target languages
	if params.ModelFamily == FamilyNvidiaCanary {
		if params.Language != nil {
			paramMap["source_lang"] = *params.Language
		} else {
			paramMap["source_lang"] = "en"
		}

		if params.Task == "translate" {
			paramMap["target_lang"] = "en" // Default target for translation
		} else {
			paramMap["target_lang"] = paramMap["source_lang"]
		}
	}

	return paramMap
}

// mergeDiarizationWithTranscription combines diarization results with transcription
func (u *UnifiedTranscriptionService) mergeDiarizationWithTranscription(transcript *interfaces.TranscriptResult, diarization *interfaces.DiarizationResult) *interfaces.TranscriptResult {
	logger.Info("Merging diarization with transcription",
		"transcript_segments", len(transcript.Segments),
		"diarization_segments", len(diarization.Segments))

	// Create a copy of the transcript to avoid modifying the original
	mergedTranscript := *transcript
	mergedTranscript.Segments = make([]interfaces.TranscriptSegment, len(transcript.Segments))
	copy(mergedTranscript.Segments, transcript.Segments)

	// Assign speakers to transcript segments based on timing overlap
	for i := range mergedTranscript.Segments {
		segment := &mergedTranscript.Segments[i]
		bestSpeaker := u.findBestSpeakerForSegment(segment.Start, segment.End, diarization.Segments)
		if bestSpeaker != "" {
			segment.Speaker = &bestSpeaker
		}
	}

	// Also assign speakers to words if available
	if len(transcript.WordSegments) > 0 {
		mergedTranscript.WordSegments = make([]interfaces.TranscriptWord, len(transcript.WordSegments))
		copy(mergedTranscript.WordSegments, transcript.WordSegments)

		for i := range mergedTranscript.WordSegments {
			word := &mergedTranscript.WordSegments[i]
			bestSpeaker := u.findBestSpeakerForSegment(word.Start, word.End, diarization.Segments)
			if bestSpeaker != "" {
				word.Speaker = &bestSpeaker
			}
		}
	}

	return &mergedTranscript
}

// findBestSpeakerForSegment finds the speaker with maximum overlap for a given time segment
func (u *UnifiedTranscriptionService) findBestSpeakerForSegment(start, end float64, diarizationSegments []interfaces.DiarizationSegment) string {
	maxOverlap := 0.0
	bestSpeaker := ""

	for _, diarSeg := range diarizationSegments {
		// Calculate overlap
		overlapStart := max(start, diarSeg.Start)
		overlapEnd := min(end, diarSeg.End)
		overlap := max(0, overlapEnd-overlapStart)

		if overlap > maxOverlap {
			maxOverlap = overlap
			bestSpeaker = diarSeg.Speaker
		}
	}

	return bestSpeaker
}

// saveTranscriptionResults saves the transcription results to the database
func (u *UnifiedTranscriptionService) saveTranscriptionResults(jobID string, result *interfaces.TranscriptResult) error {
	// Convert result to JSON string for database storage
	resultJSON, err := u.convertTranscriptResultToJSON(result)
	if err != nil {
		return fmt.Errorf("failed to convert result to JSON: %w", err)
	}

	// Update the job in the database
	if err := u.jobRepo.UpdateTranscript(context.Background(), jobID, resultJSON); err != nil {
		return fmt.Errorf("failed to update job transcript: %w", err)
	}

	if err := u.materializeTranscriptArtifacts(context.Background(), jobID, resultJSON); err != nil {
		logger.Warn("Failed to materialize transcript artifacts", "job_id", jobID, "error", err)
	}

	logger.Info("Saved transcription results", "job_id", jobID, "text_length", len(result.Text))
	return nil
}

// convertTranscriptResultToJSON converts the interface result to JSON format
func (u *UnifiedTranscriptionService) convertTranscriptResultToJSON(result *interfaces.TranscriptResult) (string, error) {
	// Now that the struct fields match the JSON field names, we can directly marshal
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}

// GetSupportedModels returns all supported models through the new architecture
func (u *UnifiedTranscriptionService) GetSupportedModels() map[string]interfaces.ModelCapabilities {
	return u.registry.GetAllCapabilities()
}

// GetModelStatus returns the status of all models
func (u *UnifiedTranscriptionService) GetModelStatus(ctx context.Context) map[string]bool {
	return u.registry.GetModelStatus(ctx)
}

// ValidateModelParameters validates parameters for a specific model
func (u *UnifiedTranscriptionService) ValidateModelParameters(modelID string, params map[string]interface{}) error {
	return u.registry.ValidateModelParameters(modelID, params)
}

// Helper functions
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

type markdownTranscriptSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker *string `json:"speaker,omitempty"`
}

type markdownTranscriptPayload struct {
	Text     string                      `json:"text"`
	Segments []markdownTranscriptSegment `json:"segments,omitempty"`
}

func shortID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func formatMMSS(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds)
	minutes := total / 60
	secs := total % 60
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

func renderMarkdownTranscript(job *models.TranscriptionJob, payload *markdownTranscriptPayload, speakerNames map[string]string) string {
	title := "Untitled"
	if job.Title != nil && strings.TrimSpace(*job.Title) != "" {
		title = strings.TrimSpace(*job.Title)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("id: %s\n", job.ID))
	b.WriteString(fmt.Sprintf("title: %q\n", title))
	b.WriteString(fmt.Sprintf("status: %s\n", job.Status))
	b.WriteString(fmt.Sprintf("created_at: %s\n", job.CreatedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("updated_at: %s\n", job.UpdatedAt.Format(time.RFC3339)))
	b.WriteString("format: transcript-markdown-v1\n")
	b.WriteString("---\n\n")
	b.WriteString(fmt.Sprintf("# %s\n\n", title))

	if len(payload.Segments) == 0 {
		b.WriteString(strings.TrimSpace(payload.Text))
		b.WriteString("\n")
		return b.String()
	}

	for _, segment := range payload.Segments {
		prefix := fmt.Sprintf("[%s - %s]", formatMMSS(segment.Start), formatMMSS(segment.End))
		if segment.Speaker != nil && strings.TrimSpace(*segment.Speaker) != "" {
			speaker := strings.TrimSpace(*segment.Speaker)
			if name, ok := speakerNames[speaker]; ok && name != "" {
				speaker = name
			}
			prefix += " " + speaker + ":"
		}
		b.WriteString(prefix)
		b.WriteString(" ")
		b.WriteString(strings.TrimSpace(segment.Text))
		b.WriteString("\n\n")
	}
	return b.String()
}

func (u *UnifiedTranscriptionService) materializeTranscriptArtifacts(ctx context.Context, jobID string, transcriptJSON string) error {
	job, err := u.jobRepo.FindByID(ctx, jobID)
	if err != nil {
		return err
	}

	var activeVault models.Vault
	vaultErr := database.DB.WithContext(ctx).Where("is_active = ?", true).First(&activeVault).Error

	var targetDir string
	if vaultErr == nil {
		title := "transcript"
		if job.Title != nil && strings.TrimSpace(*job.Title) != "" {
			title = *job.Title
		}
		targetDir = BundleTargetDir(activeVault.Path, title, job.ID)
		job.VaultID = &activeVault.ID
	} else {
		targetDir = filepath.Join(u.outputDirectory, job.ID)
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	jsonPath := filepath.Join(targetDir, "transcript.json")
	mdPath := filepath.Join(targetDir, "transcript.md")

	var pretty interface{}
	if err := json.Unmarshal([]byte(transcriptJSON), &pretty); err == nil {
		if payload, marshalErr := json.MarshalIndent(pretty, "", "  "); marshalErr == nil {
			if err := os.WriteFile(jsonPath, payload, 0644); err != nil {
				return err
			}
		}
	} else {
		if err := os.WriteFile(jsonPath, []byte(transcriptJSON), 0644); err != nil {
			return err
		}
	}

	var markdownPayload markdownTranscriptPayload
	if err := json.Unmarshal([]byte(transcriptJSON), &markdownPayload); err != nil {
		return err
	}

	// Load speaker mappings so markdown uses custom names instead of SPEAKER_00
	speakerNames := make(map[string]string)
	smRepo := repository.NewSpeakerMappingRepository(database.DB)
	if mappings, smErr := smRepo.ListByJob(ctx, jobID); smErr == nil {
		for _, m := range mappings {
			if m.CustomName != "" && m.CustomName != m.OriginalSpeaker {
				speakerNames[m.OriginalSpeaker] = m.CustomName
			}
		}
	}

	markdown := renderMarkdownTranscript(job, &markdownPayload, speakerNames)
	if err := os.WriteFile(mdPath, []byte(markdown), 0644); err != nil {
		return err
	}

	// Move audio file into the bundle directory for self-contained artifacts.
	// This is fatal — a bundle without audio is broken.
	if job.AudioPath != "" {
		newAudioPath, moveErr := MoveAudioToBundle(job.AudioPath, targetDir)
		if moveErr != nil {
			// Fallback: try copy instead of move (source may be on a different filesystem or read-only)
			newAudioPath, moveErr = CopyAudioToBundle(job.AudioPath, targetDir)
		}
		if moveErr != nil {
			return fmt.Errorf("failed to move/copy audio to bundle: %w", moveErr)
		}
		// Verify the audio file actually landed in the bundle
		if verifyErr := VerifyAudioInBundle(targetDir); verifyErr != nil {
			return fmt.Errorf("audio verification failed after move: %w", verifyErr)
		}
		job.AudioPath = newAudioPath
	}

	job.ArtifactDir = &targetDir
	job.TranscriptJSONPath = &jsonPath
	job.TranscriptMarkdownPath = &mdPath

	// Write initial metadata.json sidecar (no summaries/notes at creation time)
	meta := BuildMetadataFromJob(job, nil, nil, nil)
	if metaErr := WriteMetadata(targetDir, meta); metaErr != nil {
		logger.Warn("materialize: failed to write initial metadata.json",
			"job_id", jobID, "dir", targetDir, "error", metaErr)
	}

	if err := u.jobRepo.Update(ctx, job); err != nil {
		return err
	}

	// Invoke post-materialization hook (e.g. auto-publish to Obsidian)
	if u.postMaterializeHook != nil {
		u.postMaterializeHook(job)
	}

	return nil
}
