package adapters

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	assemblyai "github.com/AssemblyAI/assemblyai-go-sdk"

	"quill/internal/repository"
	"quill/internal/transcription/interfaces"
	"quill/pkg/logger"
)

// AssemblyAIAdapter implements both TranscriptionAdapter and DiarizationAdapter
// by delegating to the AssemblyAI REST API via the official Go SDK.
// It is a cloud adapter: it requires no local GPU and has no Python environment.
type AssemblyAIAdapter struct {
	*BaseAdapter
	cloudProviderRepo repository.CloudProviderConfigRepository
}

// Compile-time interface assertions.
var _ interfaces.TranscriptionAdapter = (*AssemblyAIAdapter)(nil)
var _ interfaces.DiarizationAdapter = (*AssemblyAIAdapter)(nil)

// NewAssemblyAIAdapter creates a new AssemblyAI adapter.
// The cloudProviderRepo is queried at request-time for the API key so that
// key rotation takes effect without restarting the server.
func NewAssemblyAIAdapter(cloudProviderRepo repository.CloudProviderConfigRepository) *AssemblyAIAdapter {
	capabilities := interfaces.ModelCapabilities{
		ModelID:     "assemblyai",
		ModelFamily: "assemblyai",
		DisplayName: "AssemblyAI",
		Description: "Cloud-based transcription and diarization by AssemblyAI (up to 50 speakers, $0.27/hr)",
		Version:     "1.0.0",
		SupportedLanguages: []string{
			"en", "en_au", "en_uk", "en_us",
			"es", "fr", "de", "it", "pt", "nl", "hi", "ja", "zh",
			"fi", "ko", "pl", "ru", "tr", "uk", "vi",
			"auto",
		},
		SupportedFormats:  []string{"mp3", "mp4", "wav", "flac", "ogg", "m4a", "webm"},
		RequiresGPU:       false,
		MemoryRequirement: 0, // Cloud: no local memory requirement
		Features: map[string]bool{
			"transcription":      true,
			"diarization":        true,
			"timestamps":         true,
			"word_level":         true,
			"language_detection": true,
			"cloud":              true,
		},
		Metadata: map[string]string{
			"provider":     "assemblyai",
			"engine":       "assemblyai_api",
			"pricing":      "$0.27/hr",
			"max_speakers": "50",
		},
	}

	schema := []interfaces.ParameterSchema{
		{
			Name:        "model",
			Type:        "string",
			Required:    false,
			Default:     "assemblyai-best",
			Options:     []string{"assemblyai-best", "assemblyai-nano"},
			Description: "AssemblyAI speech model to use (best = highest accuracy, nano = fastest/cheapest)",
			Group:       "basic",
		},
		{
			Name:        "language_code",
			Type:        "string",
			Required:    false,
			Default:     nil,
			Description: "BCP-47 language code (e.g. 'en', 'fr'). Auto-detected when omitted.",
			Group:       "basic",
		},
		{
			Name:        "speaker_labels",
			Type:        "bool",
			Required:    false,
			Default:     true,
			Description: "Enable speaker diarization",
			Group:       "basic",
		},
		{
			Name:        "speakers_expected",
			Type:        "int",
			Required:    false,
			Default:     nil,
			Min:         &[]float64{1}[0],
			Max:         &[]float64{50}[0],
			Description: "Hint for the number of expected speakers (1–50)",
			Group:       "advanced",
		},
		{
			Name:        "language_detection",
			Type:        "bool",
			Required:    false,
			Default:     false,
			Description: "Enable automatic language detection",
			Group:       "basic",
		},
	}

	base := NewBaseAdapter("assemblyai", "", capabilities, schema)

	return &AssemblyAIAdapter{
		BaseAdapter:       base,
		cloudProviderRepo: cloudProviderRepo,
	}
}

// GetSupportedModels returns the AssemblyAI speech model variants exposed by this adapter.
func (a *AssemblyAIAdapter) GetSupportedModels() []string {
	return []string{"assemblyai-best", "assemblyai-nano"}
}

// GetMaxSpeakers returns the maximum number of diarizable speakers (AssemblyAI supports up to 50).
func (a *AssemblyAIAdapter) GetMaxSpeakers() int {
	return 50
}

// GetMinSpeakers returns the minimum number of speakers required.
func (a *AssemblyAIAdapter) GetMinSpeakers() int {
	return 1
}

// IsAvailable reports whether a valid API key is stored for this provider.
func (a *AssemblyAIAdapter) IsAvailable(ctx context.Context) bool {
	_, err := a.apiKey(ctx)
	return err == nil
}

// PrepareEnvironment is a no-op for cloud adapters — there is nothing to install.
func (a *AssemblyAIAdapter) PrepareEnvironment(ctx context.Context) error {
	a.initialized = true
	return nil
}

// IsReady returns true when an API key is configured.
func (a *AssemblyAIAdapter) IsReady(ctx context.Context) bool {
	return a.IsAvailable(ctx)
}

// Transcribe uploads the audio file to AssemblyAI, waits for the transcript, then
// converts the response to Quill's internal TranscriptResult format.
func (a *AssemblyAIAdapter) Transcribe(
	ctx context.Context,
	input interfaces.AudioInput,
	params map[string]interface{},
	procCtx interfaces.ProcessingContext,
) (*interfaces.TranscriptResult, error) {
	start := time.Now()
	a.LogProcessingStart(input, procCtx)

	key, err := a.apiKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("assemblyai: API key not configured — %w", err)
	}

	client := assemblyai.NewClient(key)

	// Build options from params
	opts := a.buildTranscriptParams(params)
	// Always enable speaker labels for combined transcription+diarization
	opts.SpeakerLabels = assemblyai.Bool(true)

	f, err := os.Open(input.FilePath)
	if err != nil {
		return nil, fmt.Errorf("assemblyai: failed to open audio file %s: %w", input.FilePath, err)
	}
	defer f.Close()

	logger.Info("AssemblyAI: uploading and transcribing", "job_id", procCtx.JobID, "file", input.FilePath)

	transcript, err := client.Transcripts.TranscribeFromReader(ctx, f, opts)
	if err != nil {
		return nil, fmt.Errorf("assemblyai: transcription failed: %w", err)
	}

	result := convertAssemblyAITranscript(transcript, a.resolveModelID(params), time.Since(start))
	a.LogProcessingEnd(procCtx, result.ProcessingTime, nil)
	return result, nil
}

// Diarize uploads the audio file to AssemblyAI with speaker labels enabled and
// returns a DiarizationResult containing per-utterance speaker assignments.
func (a *AssemblyAIAdapter) Diarize(
	ctx context.Context,
	input interfaces.AudioInput,
	params map[string]interface{},
	procCtx interfaces.ProcessingContext,
) (*interfaces.DiarizationResult, error) {
	start := time.Now()
	a.LogProcessingStart(input, procCtx)

	key, err := a.apiKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("assemblyai: API key not configured — %w", err)
	}

	client := assemblyai.NewClient(key)

	opts := a.buildTranscriptParams(params)
	opts.SpeakerLabels = assemblyai.Bool(true)

	f, err := os.Open(input.FilePath)
	if err != nil {
		return nil, fmt.Errorf("assemblyai: failed to open audio file %s: %w", input.FilePath, err)
	}
	defer f.Close()

	logger.Info("AssemblyAI: uploading for diarization", "job_id", procCtx.JobID, "file", input.FilePath)

	transcript, err := client.Transcripts.TranscribeFromReader(ctx, f, opts)
	if err != nil {
		return nil, fmt.Errorf("assemblyai: diarization failed: %w", err)
	}

	result := convertAssemblyAIDiarization(transcript, a.resolveModelID(params), time.Since(start))
	a.LogProcessingEnd(procCtx, result.ProcessingTime, nil)
	return result, nil
}

// --- conversion helpers (exported for unit testing without API calls) ---

// convertAssemblyAITranscript converts an AssemblyAI Transcript to Quill's TranscriptResult.
// Utterance timestamps are stored in milliseconds in the API; we convert to seconds.
func convertAssemblyAITranscript(
	transcript assemblyai.Transcript,
	modelID string,
	processingTime time.Duration,
) *interfaces.TranscriptResult {
	result := &interfaces.TranscriptResult{
		Text:           assemblyai.ToString(transcript.Text),
		Language:       string(transcript.LanguageCode),
		Confidence:     assemblyai.ToFloat64(transcript.Confidence),
		ProcessingTime: processingTime,
		ModelUsed:      modelID,
		Metadata: map[string]string{
			"provider": "assemblyai",
		},
	}

	if len(transcript.Utterances) == 0 {
		result.Segments = []interfaces.TranscriptSegment{}
		return result
	}

	result.Segments = make([]interfaces.TranscriptSegment, 0, len(transcript.Utterances))
	for _, u := range transcript.Utterances {
		seg := interfaces.TranscriptSegment{
			Start:   msToSeconds(assemblyai.ToInt64(u.Start)),
			End:     msToSeconds(assemblyai.ToInt64(u.End)),
			Text:    assemblyai.ToString(u.Text),
			Speaker: u.Speaker,
		}
		result.Segments = append(result.Segments, seg)
	}

	return result
}

// convertAssemblyAIDiarization converts an AssemblyAI Transcript to Quill's DiarizationResult.
func convertAssemblyAIDiarization(
	transcript assemblyai.Transcript,
	modelID string,
	processingTime time.Duration,
) *interfaces.DiarizationResult {
	result := &interfaces.DiarizationResult{
		Segments:       []interfaces.DiarizationSegment{},
		Speakers:       []string{},
		ProcessingTime: processingTime,
		ModelUsed:      modelID,
		Metadata: map[string]string{
			"provider": "assemblyai",
		},
	}

	if len(transcript.Utterances) == 0 {
		return result
	}

	speakerSet := make(map[string]struct{})
	result.Segments = make([]interfaces.DiarizationSegment, 0, len(transcript.Utterances))

	for _, u := range transcript.Utterances {
		speaker := assemblyai.ToString(u.Speaker)
		if speaker != "" {
			speakerSet[speaker] = struct{}{}
		}

		seg := interfaces.DiarizationSegment{
			Start:   msToSeconds(assemblyai.ToInt64(u.Start)),
			End:     msToSeconds(assemblyai.ToInt64(u.End)),
			Speaker: speaker,
		}
		result.Segments = append(result.Segments, seg)
	}

	// Build sorted speaker list for deterministic output
	speakers := make([]string, 0, len(speakerSet))
	for s := range speakerSet {
		speakers = append(speakers, s)
	}
	sort.Strings(speakers)

	result.Speakers = speakers
	result.SpeakerCount = len(speakers)
	return result
}

// mapSpeechModel converts a Quill model ID string to an AssemblyAI SpeechModel constant.
func mapSpeechModel(modelID string) assemblyai.SpeechModel {
	switch modelID {
	case "assemblyai-nano":
		return assemblyai.SpeechModelNano
	default:
		return assemblyai.SpeechModelBest
	}
}

// --- private helpers ---

// apiKey fetches the AssemblyAI API key from the repository.
func (a *AssemblyAIAdapter) apiKey(ctx context.Context) (string, error) {
	cfg, err := a.cloudProviderRepo.GetByProvider(ctx, "assemblyai")
	if err != nil {
		return "", err
	}
	if cfg == nil || cfg.APIKey == "" {
		return "", fmt.Errorf("API key is empty")
	}
	return cfg.APIKey, nil
}

// buildTranscriptParams converts Quill params map to AssemblyAI TranscriptOptionalParams.
func (a *AssemblyAIAdapter) buildTranscriptParams(params map[string]interface{}) *assemblyai.TranscriptOptionalParams {
	opts := &assemblyai.TranscriptOptionalParams{}

	modelID := a.resolveModelID(params)
	speechModel := mapSpeechModel(modelID)
	opts.SpeechModel = speechModel

	// Language code
	if lc := a.GetStringParameter(params, "language_code"); lc != "" {
		opts.LanguageCode = assemblyai.TranscriptLanguageCode(lc)
	}

	// Language detection
	if a.GetBoolParameter(params, "language_detection") {
		opts.LanguageDetection = assemblyai.Bool(true)
	}

	// Speakers expected hint
	if n := a.GetIntParameter(params, "speakers_expected"); n > 0 {
		n64 := int64(n)
		opts.SpeakersExpected = &n64
	}

	return opts
}

// resolveModelID reads the "model" parameter falling back to the default.
func (a *AssemblyAIAdapter) resolveModelID(params map[string]interface{}) string {
	if m := a.GetStringParameter(params, "model"); m != "" {
		return m
	}
	return "assemblyai-best"
}

// msToSeconds converts milliseconds (int64) to seconds (float64).
func msToSeconds(ms int64) float64 {
	return float64(ms) / 1000.0
}
