package adapters

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	listenv1rest "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/rest"
	sdkinterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/rest/interfaces"
	dginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/interfaces"
	listenclient "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/listen"

	"quill/internal/repository"
	"quill/internal/transcription/interfaces"
	"quill/pkg/logger"
)

// DeepgramAdapter implements both TranscriptionAdapter and DiarizationAdapter
// using the Deepgram Nova-3 REST API via the official Go SDK.
// It is a cloud adapter: no local GPU or Python environment required.
type DeepgramAdapter struct {
	*BaseAdapter
	cloudProviderRepo repository.CloudProviderConfigRepository
}

// Compile-time interface assertions.
var _ interfaces.TranscriptionAdapter = (*DeepgramAdapter)(nil)
var _ interfaces.DiarizationAdapter = (*DeepgramAdapter)(nil)

// NewDeepgramAdapter creates a new Deepgram adapter.
// The cloudProviderRepo is queried at request-time for the API key so that
// key rotation takes effect without restarting the server.
func NewDeepgramAdapter(cloudProviderRepo repository.CloudProviderConfigRepository) *DeepgramAdapter {
	capabilities := interfaces.ModelCapabilities{
		ModelID:     "deepgram",
		ModelFamily: "deepgram",
		DisplayName: "Deepgram",
		Description: "Cloud-based transcription and diarization by Deepgram Nova-3 (up to 100 speakers, $0.26/hr)",
		Version:     "1.0.0",
		SupportedLanguages: []string{
			"en", "en-US", "en-AU", "en-GB", "en-NZ", "en-IN",
			"es", "es-419", "fr", "fr-CA", "de", "it", "pt", "pt-BR",
			"nl", "hi", "ja", "ko", "zh", "zh-CN", "zh-TW",
			"da", "fi", "id", "ms", "no", "pl", "ru", "sv",
			"ta", "th", "tr", "uk", "vi",
			"auto",
		},
		SupportedFormats:  []string{"mp3", "mp4", "wav", "flac", "ogg", "m4a", "webm", "opus"},
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
			"provider":     "deepgram",
			"engine":       "nova-3",
			"pricing":      "$0.26/hr",
			"max_speakers": "100",
		},
	}

	schema := []interfaces.ParameterSchema{
		{
			Name:        "model",
			Type:        "string",
			Required:    false,
			Default:     "deepgram-nova-3",
			Options:     []string{"deepgram-nova-3", "deepgram-nova-2", "deepgram-whisper"},
			Description: "Deepgram model to use (nova-3 = best accuracy, nova-2 = previous gen, whisper = OpenAI Whisper-based)",
			Group:       "basic",
		},
		{
			Name:        "language",
			Type:        "string",
			Required:    false,
			Default:     nil,
			Description: "BCP-47 language code (e.g. 'en', 'fr', 'es'). Auto-detected when omitted.",
			Group:       "basic",
		},
		{
			Name:        "diarize",
			Type:        "bool",
			Required:    false,
			Default:     true,
			Description: "Enable speaker diarization",
			Group:       "basic",
		},
		{
			Name:        "smart_format",
			Type:        "bool",
			Required:    false,
			Default:     true,
			Description: "Apply smart formatting (punctuation, numbers, etc.)",
			Group:       "basic",
		},
		{
			Name:        "detect_language",
			Type:        "bool",
			Required:    false,
			Default:     false,
			Description: "Enable automatic language detection",
			Group:       "basic",
		},
	}

	base := NewBaseAdapter("deepgram", "", capabilities, schema)

	return &DeepgramAdapter{
		BaseAdapter:       base,
		cloudProviderRepo: cloudProviderRepo,
	}
}

// GetSupportedModels returns the Deepgram model variants exposed by this adapter.
func (d *DeepgramAdapter) GetSupportedModels() []string {
	return []string{"deepgram-nova-3", "deepgram-nova-2", "deepgram-whisper"}
}

// GetMaxSpeakers returns the maximum number of diarizable speakers.
// Deepgram does not impose a hard speaker limit; 100 is a conservative practical ceiling.
func (d *DeepgramAdapter) GetMaxSpeakers() int {
	return 100
}

// GetMinSpeakers returns the minimum number of speakers required.
func (d *DeepgramAdapter) GetMinSpeakers() int {
	return 1
}

// IsAvailable reports whether a valid API key is stored for this provider.
func (d *DeepgramAdapter) IsAvailable(ctx context.Context) bool {
	_, err := d.apiKey(ctx)
	return err == nil
}

// PrepareEnvironment is a no-op for cloud adapters — there is nothing to install.
func (d *DeepgramAdapter) PrepareEnvironment(ctx context.Context) error {
	d.initialized = true
	return nil
}

// IsReady returns true when an API key is configured.
func (d *DeepgramAdapter) IsReady(ctx context.Context) bool {
	return d.IsAvailable(ctx)
}

// Transcribe uploads the audio file to Deepgram, waits for the transcript, then
// converts the response to Quill's internal TranscriptResult format.
func (d *DeepgramAdapter) Transcribe(
	ctx context.Context,
	input interfaces.AudioInput,
	params map[string]interface{},
	procCtx interfaces.ProcessingContext,
) (*interfaces.TranscriptResult, error) {
	start := time.Now()
	d.LogProcessingStart(input, procCtx)

	key, err := d.apiKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("deepgram: API key not configured — %w", err)
	}

	opts := d.buildTranscriptionOptions(params)
	// Always enable diarization and utterances for combined transcription+diarization.
	opts.Diarize = true
	opts.Utterances = true
	opts.Punctuate = true

	logger.Info("Deepgram: uploading and transcribing", "job_id", procCtx.JobID, "file", input.FilePath)

	sdkResp, err := d.callAPI(ctx, key, input.FilePath, opts)
	if err != nil {
		return nil, fmt.Errorf("deepgram: transcription failed: %w", err)
	}

	dgResp := flattenSDKResponse(sdkResp)
	result := convertDeepgramTranscript(dgResp, d.resolveModelID(params), time.Since(start))
	d.LogProcessingEnd(procCtx, result.ProcessingTime, nil)
	return result, nil
}

// Diarize uploads the audio file to Deepgram with diarization enabled and
// returns a DiarizationResult containing per-utterance speaker assignments.
func (d *DeepgramAdapter) Diarize(
	ctx context.Context,
	input interfaces.AudioInput,
	params map[string]interface{},
	procCtx interfaces.ProcessingContext,
) (*interfaces.DiarizationResult, error) {
	start := time.Now()
	d.LogProcessingStart(input, procCtx)

	key, err := d.apiKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("deepgram: API key not configured — %w", err)
	}

	opts := d.buildTranscriptionOptions(params)
	opts.Diarize = true
	opts.Utterances = true

	logger.Info("Deepgram: uploading for diarization", "job_id", procCtx.JobID, "file", input.FilePath)

	sdkResp, err := d.callAPI(ctx, key, input.FilePath, opts)
	if err != nil {
		return nil, fmt.Errorf("deepgram: diarization failed: %w", err)
	}

	dgResp := flattenSDKResponse(sdkResp)
	result := convertDeepgramDiarization(dgResp, d.resolveModelID(params), time.Since(start))
	d.LogProcessingEnd(procCtx, result.ProcessingTime, nil)
	return result, nil
}

// --- internal response types (flattened from SDK for testability) ---

// deepgramResponse is a simplified, flattened view of the Deepgram API response
// used internally for conversion. Keeping it separate from the SDK types allows
// conversion helpers to be tested without constructing deeply-nested SDK structs.
type deepgramResponse struct {
	Transcript string
	Language   string
	Confidence float64
	Words      []deepgramWord
	Utterances []deepgramUtterance
}

// deepgramWord mirrors the SDK's Word type for internal use.
type deepgramWord struct {
	Word           string
	PunctuatedWord string
	Start          float64
	End            float64
	Confidence     float64
	Speaker        *int
}

// deepgramUtterance mirrors the SDK's Utterance type for internal use.
type deepgramUtterance struct {
	Start      float64
	End        float64
	Transcript string
	Speaker    *int
	Confidence float64
}

// --- conversion helpers ---

// convertDeepgramTranscript converts a deepgramResponse to Quill's TranscriptResult.
// Segments are built by grouping consecutive words from the same speaker.
func convertDeepgramTranscript(
	resp *deepgramResponse,
	modelID string,
	processingTime time.Duration,
) *interfaces.TranscriptResult {
	result := &interfaces.TranscriptResult{
		ProcessingTime: processingTime,
		ModelUsed:      modelID,
		Metadata: map[string]string{
			"provider": "deepgram",
		},
	}

	if resp == nil {
		result.Segments = []interfaces.TranscriptSegment{}
		return result
	}

	result.Text = resp.Transcript
	result.Language = resp.Language
	result.Confidence = resp.Confidence

	// Prefer utterance-level segments (present when diarize=true + utterances=true).
	// Fall back to word-level grouping when utterances are absent.
	if len(resp.Utterances) > 0 {
		result.Segments = utterancesToTranscriptSegments(resp.Utterances)
	} else if len(resp.Words) > 0 {
		result.Segments = groupWordsIntoSegments(resp.Words)
	} else {
		result.Segments = []interfaces.TranscriptSegment{}
	}

	return result
}

// convertDeepgramDiarization converts a deepgramResponse to Quill's DiarizationResult.
func convertDeepgramDiarization(
	resp *deepgramResponse,
	modelID string,
	processingTime time.Duration,
) *interfaces.DiarizationResult {
	result := &interfaces.DiarizationResult{
		Segments:       []interfaces.DiarizationSegment{},
		Speakers:       []string{},
		ProcessingTime: processingTime,
		ModelUsed:      modelID,
		Metadata: map[string]string{
			"provider": "deepgram",
		},
	}

	if resp == nil {
		return result
	}

	if len(resp.Utterances) == 0 {
		return result
	}

	speakerSet := make(map[string]struct{})
	result.Segments = make([]interfaces.DiarizationSegment, 0, len(resp.Utterances))

	for _, u := range resp.Utterances {
		label := speakerLabel(u.Speaker)
		speakerSet[label] = struct{}{}

		result.Segments = append(result.Segments, interfaces.DiarizationSegment{
			Start:      u.Start,
			End:        u.End,
			Speaker:    label,
			Confidence: u.Confidence,
		})
	}

	// Build sorted speaker list for deterministic output.
	speakers := make([]string, 0, len(speakerSet))
	for s := range speakerSet {
		speakers = append(speakers, s)
	}
	sort.Strings(speakers)

	result.Speakers = speakers
	result.SpeakerCount = len(speakers)
	return result
}

// groupWordsIntoSegments groups consecutive words with the same speaker into
// TranscriptSegments. Words without a speaker label are grouped under SPEAKER_UNKNOWN.
// PunctuatedWord is used for the segment text when available; Word is the fallback.
func groupWordsIntoSegments(words []deepgramWord) []interfaces.TranscriptSegment {
	if len(words) == 0 {
		return []interfaces.TranscriptSegment{}
	}

	var segments []interfaces.TranscriptSegment
	var currentWords []deepgramWord

	flush := func() {
		if len(currentWords) == 0 {
			return
		}
		first := currentWords[0]
		last := currentWords[len(currentWords)-1]

		var parts []string
		for _, w := range currentWords {
			text := w.PunctuatedWord
			if text == "" {
				text = w.Word
			}
			parts = append(parts, text)
		}
		segText := strings.Join(parts, " ")
		label := speakerLabel(first.Speaker)

		segments = append(segments, interfaces.TranscriptSegment{
			Start:   first.Start,
			End:     last.End,
			Text:    segText,
			Speaker: &label,
		})
		currentWords = nil
	}

	for i, w := range words {
		if i == 0 {
			currentWords = append(currentWords, w)
			continue
		}
		prev := currentWords[len(currentWords)-1]
		if speakerLabel(w.Speaker) != speakerLabel(prev.Speaker) {
			flush()
		}
		currentWords = append(currentWords, w)
	}
	flush()

	return segments
}

// utterancesToTranscriptSegments converts utterance-level data to TranscriptSegments
// (one utterance = one segment).
func utterancesToTranscriptSegments(utterances []deepgramUtterance) []interfaces.TranscriptSegment {
	segments := make([]interfaces.TranscriptSegment, 0, len(utterances))
	for _, u := range utterances {
		label := speakerLabel(u.Speaker)
		segments = append(segments, interfaces.TranscriptSegment{
			Start:   u.Start,
			End:     u.End,
			Text:    u.Transcript,
			Speaker: &label,
		})
	}
	return segments
}

// speakerLabel converts a Deepgram integer speaker index to a Quill-style label.
// nil speaker indices (undiarized words) produce "SPEAKER_UNKNOWN".
func speakerLabel(speaker *int) string {
	if speaker == nil {
		return "SPEAKER_UNKNOWN"
	}
	return fmt.Sprintf("SPEAKER_%d", *speaker)
}

// deepgramAPIModel maps a Quill model ID to the Deepgram API model string.
func deepgramAPIModel(modelID string) string {
	switch modelID {
	case "deepgram-nova-2":
		return "nova-2"
	case "deepgram-whisper":
		return "whisper"
	default:
		return "nova-3"
	}
}

// flattenSDKResponse converts the Deepgram SDK PreRecordedResponse to our internal type.
func flattenSDKResponse(sdkResp *sdkinterfaces.PreRecordedResponse) *deepgramResponse {
	resp := &deepgramResponse{}

	if sdkResp == nil || sdkResp.Results == nil {
		return resp
	}

	results := sdkResp.Results

	// Extract transcript and metadata from the first channel's first alternative.
	if len(results.Channels) > 0 {
		ch := results.Channels[0]
		if len(ch.Alternatives) > 0 {
			alt := ch.Alternatives[0]
			resp.Transcript = alt.Transcript
			resp.Confidence = alt.Confidence

			resp.Words = make([]deepgramWord, 0, len(alt.Words))
			for _, w := range alt.Words {
				dw := deepgramWord{
					Word:           w.Word,
					PunctuatedWord: w.PunctuatedWord,
					Start:          w.Start,
					End:            w.End,
					Confidence:     w.Confidence,
					Speaker:        w.Speaker,
				}
				resp.Words = append(resp.Words, dw)
			}
		}
		resp.Language = ch.DetectedLanguage
	}

	// Extract utterances (present when diarize=true + utterances=true).
	if len(results.Utterances) > 0 {
		resp.Utterances = make([]deepgramUtterance, 0, len(results.Utterances))
		for _, u := range results.Utterances {
			du := deepgramUtterance{
				Start:      u.Start,
				End:        u.End,
				Transcript: u.Transcript,
				Speaker:    u.Speaker,
				Confidence: u.Confidence,
			}
			resp.Utterances = append(resp.Utterances, du)
		}
	}

	return resp
}

// --- private helpers ---

// apiKey fetches the Deepgram API key from the repository.
func (d *DeepgramAdapter) apiKey(ctx context.Context) (string, error) {
	cfg, err := d.cloudProviderRepo.GetByProvider(ctx, "deepgram")
	if err != nil {
		return "", err
	}
	if cfg == nil || cfg.APIKey == "" {
		return "", fmt.Errorf("API key is empty")
	}
	return cfg.APIKey, nil
}

// buildTranscriptionOptions converts Quill params map to Deepgram PreRecordedTranscriptionOptions.
func (d *DeepgramAdapter) buildTranscriptionOptions(params map[string]interface{}) *dginterfaces.PreRecordedTranscriptionOptions {
	opts := &dginterfaces.PreRecordedTranscriptionOptions{
		Model:       deepgramAPIModel(d.resolveModelID(params)),
		SmartFormat: true,
		Punctuate:   true,
	}

	if lang := d.GetStringParameter(params, "language"); lang != "" {
		opts.Language = lang
	}

	if d.GetBoolParameter(params, "detect_language") {
		opts.DetectLanguage = true
	}

	return opts
}

// resolveModelID reads the "model" parameter, falling back to the nova-3 default.
func (d *DeepgramAdapter) resolveModelID(params map[string]interface{}) string {
	if m := d.GetStringParameter(params, "model"); m != "" {
		return m
	}
	return "deepgram-nova-3"
}

// callAPI sends audio to the Deepgram REST API and returns the SDK response.
// A per-request client is created so API key rotation takes effect immediately.
func (d *DeepgramAdapter) callAPI(
	ctx context.Context,
	apiKey string,
	filePath string,
	opts *dginterfaces.PreRecordedTranscriptionOptions,
) (*sdkinterfaces.PreRecordedResponse, error) {
	dgClient := listenv1rest.New(listenclient.NewREST(apiKey, &dginterfaces.ClientOptions{}))

	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file %s: %w", filePath, err)
	}
	defer f.Close()

	return dgClient.FromStream(ctx, f, opts)
}
