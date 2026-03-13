package adapters

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"quill/internal/models"
	"quill/internal/transcription/interfaces"
)

// --- helpers ---

func newDeepgramTestAdapter(apiKey string) *DeepgramAdapter {
	var cfg *models.CloudProviderConfig
	if apiKey != "" {
		cfg = &models.CloudProviderConfig{
			Provider: "deepgram",
			APIKey:   apiKey,
			IsActive: true,
		}
	}
	repo := &mockCloudProviderRepo{config: cfg}
	return NewDeepgramAdapter(repo)
}

// --- tests ---

func TestDeepgramAdapter_GetSupportedModels(t *testing.T) {
	adapter := newDeepgramTestAdapter("test-key")
	models := adapter.GetSupportedModels()

	assert.Contains(t, models, "deepgram-nova-3", "should include deepgram-nova-3")
	assert.Contains(t, models, "deepgram-nova-2", "should include deepgram-nova-2")
	assert.Contains(t, models, "deepgram-whisper", "should include deepgram-whisper")
	assert.Len(t, models, 3, "should expose exactly three model variants")
}

func TestDeepgramAdapter_GetMaxSpeakers(t *testing.T) {
	adapter := newDeepgramTestAdapter("test-key")
	assert.Equal(t, 100, adapter.GetMaxSpeakers())
}

func TestDeepgramAdapter_GetMinSpeakers(t *testing.T) {
	adapter := newDeepgramTestAdapter("test-key")
	assert.Equal(t, 1, adapter.GetMinSpeakers())
}

func TestDeepgramAdapter_GetCapabilities(t *testing.T) {
	adapter := newDeepgramTestAdapter("test-key")
	caps := adapter.GetCapabilities()

	assert.Equal(t, "deepgram", caps.ModelID)
	assert.Equal(t, "deepgram", caps.ModelFamily)
	assert.True(t, caps.Features["transcription"])
	assert.True(t, caps.Features["diarization"])
	assert.True(t, caps.Features["timestamps"])
	assert.True(t, caps.Features["cloud"])
	assert.False(t, caps.RequiresGPU, "cloud adapter must not require a local GPU")
}

// TestDeepgramAdapter_IsReady checks readiness depending on whether an API key exists.
func TestDeepgramAdapter_IsReady(t *testing.T) {
	t.Run("ready when API key present", func(t *testing.T) {
		adapter := newDeepgramTestAdapter("dg-test-key")
		assert.True(t, adapter.IsReady(context.Background()))
	})

	t.Run("not ready when no API key", func(t *testing.T) {
		repo := &mockCloudProviderRepo{err: errors.New("record not found")}
		adapter := NewDeepgramAdapter(repo)
		assert.False(t, adapter.IsReady(context.Background()))
	})
}

// TestDeepgramAdapter_NoAPIKey verifies a clear error is returned when no key is configured.
func TestDeepgramAdapter_NoAPIKey(t *testing.T) {
	repo := &mockCloudProviderRepo{err: errors.New("record not found")}
	adapter := NewDeepgramAdapter(repo)

	_, err := adapter.Transcribe(context.Background(), interfaces.AudioInput{FilePath: "/fake/audio.mp3"}, nil, interfaces.ProcessingContext{JobID: "job-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deepgram", "error should mention the provider")

	_, err = adapter.Diarize(context.Background(), interfaces.AudioInput{FilePath: "/fake/audio.mp3"}, nil, interfaces.ProcessingContext{JobID: "job-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deepgram", "error should mention the provider")
}

// TestDeepgramAdapter_ConvertResponse exercises the conversion helper with
// realistic Deepgram API response data (word-level speaker labels).
func TestDeepgramAdapter_ConvertResponse(t *testing.T) {
	speakerA := 0
	speakerB := 1

	words := []deepgramWord{
		{Word: "Hello", Start: 0.0, End: 0.5, Confidence: 0.99, Speaker: &speakerA, PunctuatedWord: "Hello"},
		{Word: "world", Start: 0.6, End: 1.1, Confidence: 0.98, Speaker: &speakerA, PunctuatedWord: "world."},
		{Word: "How", Start: 2.0, End: 2.3, Confidence: 0.95, Speaker: &speakerB, PunctuatedWord: "How"},
		{Word: "are", Start: 2.4, End: 2.6, Confidence: 0.97, Speaker: &speakerB, PunctuatedWord: "are"},
		{Word: "you", Start: 2.7, End: 3.0, Confidence: 0.96, Speaker: &speakerB, PunctuatedWord: "you?"},
	}

	resp := buildDeepgramResponse("Hello world. How are you?", "en-US", 0.97, words, nil)
	result := convertDeepgramTranscript(resp, "deepgram-nova-3", time.Second)

	require.NotNil(t, result)
	assert.Equal(t, "Hello world. How are you?", result.Text)
	assert.Equal(t, "en-US", result.Language)
	assert.Equal(t, "deepgram-nova-3", result.ModelUsed)
	assert.InDelta(t, 0.97, result.Confidence, 0.001)

	require.Len(t, result.Segments, 2, "two speakers should produce two segments")

	seg0 := result.Segments[0]
	assert.InDelta(t, 0.0, seg0.Start, 0.001)
	assert.InDelta(t, 1.1, seg0.End, 0.001)
	assert.Equal(t, "Hello world.", seg0.Text)
	require.NotNil(t, seg0.Speaker)
	assert.Equal(t, "SPEAKER_0", *seg0.Speaker)

	seg1 := result.Segments[1]
	assert.InDelta(t, 2.0, seg1.Start, 0.001)
	assert.InDelta(t, 3.0, seg1.End, 0.001)
	assert.Equal(t, "How are you?", seg1.Text)
	require.NotNil(t, seg1.Speaker)
	assert.Equal(t, "SPEAKER_1", *seg1.Speaker)
}

// TestDeepgramAdapter_ConvertResponse_EmptyWords handles responses where there are no words.
func TestDeepgramAdapter_ConvertResponse_EmptyWords(t *testing.T) {
	resp := buildDeepgramResponse("Some transcribed text.", "en", 0.88, nil, nil)
	result := convertDeepgramTranscript(resp, "deepgram-nova-3", 500*time.Millisecond)

	require.NotNil(t, result)
	assert.Equal(t, "Some transcribed text.", result.Text)
	assert.Empty(t, result.Segments)
}

// TestDeepgramAdapter_ConvertResponse_NilResponse handles a nil response gracefully.
func TestDeepgramAdapter_ConvertResponse_NilResponse(t *testing.T) {
	result := convertDeepgramTranscript(nil, "deepgram-nova-3", time.Second)

	require.NotNil(t, result)
	assert.Equal(t, "", result.Text)
	assert.Empty(t, result.Segments)
}

// TestDeepgramAdapter_ConvertDiarization tests the diarization-specific
// conversion path that builds a DiarizationResult from utterances.
func TestDeepgramAdapter_ConvertDiarization(t *testing.T) {
	speakerA := 0
	speakerB := 1
	speakerC := 2

	utterances := []deepgramUtterance{
		{Start: 0.0, End: 2.0, Transcript: "Hello there.", Speaker: &speakerA, Confidence: 0.99},
		{Start: 2.5, End: 5.0, Transcript: "Hi back.", Speaker: &speakerB, Confidence: 0.95},
		{Start: 5.5, End: 7.0, Transcript: "Nice to meet you.", Speaker: &speakerC, Confidence: 0.92},
		{Start: 7.5, End: 9.0, Transcript: "You too.", Speaker: &speakerA, Confidence: 0.98},
	}

	resp := buildDeepgramResponse("", "en", 0.96, nil, utterances)
	result := convertDeepgramDiarization(resp, "deepgram-nova-3", time.Second)

	require.NotNil(t, result)
	require.Len(t, result.Segments, 4)
	assert.Equal(t, 3, result.SpeakerCount, "should count 3 unique speakers")
	assert.ElementsMatch(t, []string{"SPEAKER_0", "SPEAKER_1", "SPEAKER_2"}, result.Speakers)

	assert.Equal(t, "SPEAKER_0", result.Segments[0].Speaker)
	assert.InDelta(t, 0.0, result.Segments[0].Start, 0.001)
	assert.InDelta(t, 2.0, result.Segments[0].End, 0.001)

	assert.Equal(t, "SPEAKER_1", result.Segments[1].Speaker)
	assert.Equal(t, "SPEAKER_0", result.Segments[3].Speaker)
}

// TestDeepgramAdapter_ConvertDiarization_NoUtterances handles audio with no utterances.
func TestDeepgramAdapter_ConvertDiarization_NoUtterances(t *testing.T) {
	resp := buildDeepgramResponse("", "en", 0.0, nil, []deepgramUtterance{})
	result := convertDeepgramDiarization(resp, "deepgram-nova-3", time.Second)

	require.NotNil(t, result)
	assert.Empty(t, result.Segments)
	assert.Equal(t, 0, result.SpeakerCount)
	assert.Empty(t, result.Speakers)
}

// TestDeepgramAdapter_ConvertDiarization_NilResponse handles a nil response.
func TestDeepgramAdapter_ConvertDiarization_NilResponse(t *testing.T) {
	result := convertDeepgramDiarization(nil, "deepgram-nova-3", time.Second)

	require.NotNil(t, result)
	assert.Empty(t, result.Segments)
	assert.Equal(t, 0, result.SpeakerCount)
}

// TestDeepgramAdapter_ConvertDiarization_SingleSpeaker validates single-speaker recordings.
func TestDeepgramAdapter_ConvertDiarization_SingleSpeaker(t *testing.T) {
	speakerA := 0
	utterances := []deepgramUtterance{
		{Start: 0.0, End: 10.0, Transcript: "Long monologue.", Speaker: &speakerA, Confidence: 0.99},
	}

	resp := buildDeepgramResponse("Long monologue.", "en", 0.99, nil, utterances)
	result := convertDeepgramDiarization(resp, "deepgram-nova-3", time.Second)

	assert.Equal(t, 1, result.SpeakerCount)
	assert.Equal(t, []string{"SPEAKER_0"}, result.Speakers)
}

// TestDeepgramAdapter_GroupWordsIntoSegments verifies consecutive words from the same
// speaker are merged into one segment, and a speaker change triggers a new segment.
func TestDeepgramAdapter_GroupWordsIntoSegments(t *testing.T) {
	speakerA := 0
	speakerB := 1

	tests := []struct {
		name         string
		words        []deepgramWord
		wantSegments int
		wantTexts    []string
	}{
		{
			name:         "empty words produces no segments",
			words:        []deepgramWord{},
			wantSegments: 0,
		},
		{
			name: "single word is one segment",
			words: []deepgramWord{
				{PunctuatedWord: "Hello.", Start: 0, End: 0.5, Speaker: &speakerA},
			},
			wantSegments: 1,
			wantTexts:    []string{"Hello."},
		},
		{
			name: "consecutive same-speaker words merge",
			words: []deepgramWord{
				{PunctuatedWord: "Hello", Start: 0, End: 0.5, Speaker: &speakerA},
				{PunctuatedWord: "there.", Start: 0.6, End: 1.0, Speaker: &speakerA},
				{PunctuatedWord: "Goodbye.", Start: 1.5, End: 2.0, Speaker: &speakerA},
			},
			wantSegments: 1,
			wantTexts:    []string{"Hello there. Goodbye."},
		},
		{
			name: "speaker change creates new segment",
			words: []deepgramWord{
				{PunctuatedWord: "Hello.", Start: 0, End: 0.5, Speaker: &speakerA},
				{PunctuatedWord: "Hi.", Start: 1.0, End: 1.4, Speaker: &speakerB},
			},
			wantSegments: 2,
			wantTexts:    []string{"Hello.", "Hi."},
		},
		{
			name: "alternating speakers create multiple segments",
			words: []deepgramWord{
				{PunctuatedWord: "Yes.", Start: 0, End: 0.3, Speaker: &speakerA},
				{PunctuatedWord: "No.", Start: 0.5, End: 0.8, Speaker: &speakerB},
				{PunctuatedWord: "Maybe.", Start: 1.0, End: 1.5, Speaker: &speakerA},
			},
			wantSegments: 3,
			wantTexts:    []string{"Yes.", "No.", "Maybe."},
		},
		{
			name: "word without speaker label is assigned SPEAKER_UNKNOWN",
			words: []deepgramWord{
				{PunctuatedWord: "Hello.", Start: 0, End: 0.5, Speaker: nil},
			},
			wantSegments: 1,
			wantTexts:    []string{"Hello."},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			segments := groupWordsIntoSegments(tc.words)
			assert.Len(t, segments, tc.wantSegments)
			if len(tc.wantTexts) > 0 {
				for i, seg := range segments {
					assert.Equal(t, tc.wantTexts[i], seg.Text)
				}
			}
		})
	}
}

// TestDeepgramAdapter_GroupWordsIntoSegments_Timing checks start/end times are correct.
func TestDeepgramAdapter_GroupWordsIntoSegments_Timing(t *testing.T) {
	speakerA := 0
	speakerB := 1
	words := []deepgramWord{
		{PunctuatedWord: "First", Start: 1.0, End: 1.5, Speaker: &speakerA},
		{PunctuatedWord: "second.", Start: 1.6, End: 2.0, Speaker: &speakerA},
		{PunctuatedWord: "Third.", Start: 3.0, End: 3.5, Speaker: &speakerB},
	}

	segments := groupWordsIntoSegments(words)
	require.Len(t, segments, 2)

	assert.InDelta(t, 1.0, segments[0].Start, 0.001, "first segment starts at first word start")
	assert.InDelta(t, 2.0, segments[0].End, 0.001, "first segment ends at last word end")
	assert.InDelta(t, 3.0, segments[1].Start, 0.001, "second segment starts at its first word")
	assert.InDelta(t, 3.5, segments[1].End, 0.001, "second segment ends at its last word")
}

// TestDeepgramAdapter_SpeakerLabel verifies the speaker int→string conversion.
func TestDeepgramAdapter_SpeakerLabel(t *testing.T) {
	tests := []struct {
		speaker  *int
		expected string
	}{
		{intPtr(0), "SPEAKER_0"},
		{intPtr(1), "SPEAKER_1"},
		{intPtr(9), "SPEAKER_9"},
		{nil, "SPEAKER_UNKNOWN"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			got := speakerLabel(tc.speaker)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestDeepgramAdapter_ResolveModelID ensures the model ID defaults and overrides work.
func TestDeepgramAdapter_ResolveModelID(t *testing.T) {
	adapter := newDeepgramTestAdapter("key")

	assert.Equal(t, "deepgram-nova-3", adapter.resolveModelID(nil))
	assert.Equal(t, "deepgram-nova-3", adapter.resolveModelID(map[string]interface{}{}))
	assert.Equal(t, "deepgram-nova-2", adapter.resolveModelID(map[string]interface{}{"model": "deepgram-nova-2"}))
	assert.Equal(t, "deepgram-whisper", adapter.resolveModelID(map[string]interface{}{"model": "deepgram-whisper"}))
}

// TestDeepgramAdapter_PrepareEnvironment verifies the adapter reports ready after PrepareEnvironment.
func TestDeepgramAdapter_PrepareEnvironment(t *testing.T) {
	adapter := newDeepgramTestAdapter("key")
	err := adapter.PrepareEnvironment(context.Background())
	require.NoError(t, err)
	assert.True(t, adapter.IsReady(context.Background()))
}

// TestDeepgramAdapter_ParameterSchema checks the schema contains expected parameters.
func TestDeepgramAdapter_ParameterSchema(t *testing.T) {
	adapter := newDeepgramTestAdapter("key")
	schema := adapter.GetParameterSchema()

	names := make(map[string]bool)
	for _, p := range schema {
		names[p.Name] = true
	}

	assert.True(t, names["model"], "schema should include 'model' parameter")
	assert.True(t, names["language"], "schema should include 'language' parameter")
	assert.True(t, names["diarize"], "schema should include 'diarize' parameter")
}

// TestDeepgramAdapter_GetEstimatedProcessingTime verifies the time estimate is nonzero.
func TestDeepgramAdapter_GetEstimatedProcessingTime(t *testing.T) {
	adapter := newDeepgramTestAdapter("key")
	input := interfaces.AudioInput{
		Duration: 10 * time.Minute,
		Size:     10 * 1024 * 1024,
	}
	est := adapter.GetEstimatedProcessingTime(input)
	assert.Greater(t, est, time.Duration(0))
}

// TestDeepgramAdapter_ValidateParameters checks parameter validation via the BaseAdapter.
func TestDeepgramAdapter_ValidateParameters(t *testing.T) {
	adapter := newDeepgramTestAdapter("key")

	t.Run("valid model option passes", func(t *testing.T) {
		err := adapter.ValidateParameters(map[string]interface{}{"model": "deepgram-nova-3"})
		assert.NoError(t, err)
	})

	t.Run("invalid model option fails", func(t *testing.T) {
		err := adapter.ValidateParameters(map[string]interface{}{"model": "not-a-real-model"})
		assert.Error(t, err)
	})
}

// TestDeepgramAdapter_BuildAPIModel checks the model string passed to the Deepgram API.
func TestDeepgramAdapter_BuildAPIModel(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"deepgram-nova-3", "nova-3"},
		{"deepgram-nova-2", "nova-2"},
		{"deepgram-whisper", "whisper"},
		{"unknown-model", "nova-3"}, // default
		{"", "nova-3"},              // empty → default
	}

	for _, tc := range tests {
		t.Run(tc.modelID, func(t *testing.T) {
			got := deepgramAPIModel(tc.modelID)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestDeepgramAdapter_ConvertResponse_WithUtterances verifies that when utterances are
// present, they are used directly as segments rather than word-grouping.
func TestDeepgramAdapter_ConvertResponse_WithUtterances(t *testing.T) {
	speakerA := 0
	speakerB := 1
	utterances := []deepgramUtterance{
		{Start: 0.0, End: 2.5, Transcript: "Hello, how are you?", Speaker: &speakerA, Confidence: 0.98},
		{Start: 3.0, End: 5.0, Transcript: "I am fine thanks.", Speaker: &speakerB, Confidence: 0.96},
	}

	resp := buildDeepgramResponse("Hello, how are you? I am fine thanks.", "en", 0.97, nil, utterances)
	result := convertDeepgramTranscript(resp, "deepgram-nova-3", time.Second)

	require.NotNil(t, result)
	require.Len(t, result.Segments, 2, "utterances should map 1:1 to segments")

	assert.InDelta(t, 0.0, result.Segments[0].Start, 0.001)
	assert.InDelta(t, 2.5, result.Segments[0].End, 0.001)
	assert.Equal(t, "Hello, how are you?", result.Segments[0].Text)
	require.NotNil(t, result.Segments[0].Speaker)
	assert.Equal(t, "SPEAKER_0", *result.Segments[0].Speaker)

	assert.InDelta(t, 3.0, result.Segments[1].Start, 0.001)
	assert.InDelta(t, 5.0, result.Segments[1].End, 0.001)
	assert.Equal(t, "I am fine thanks.", result.Segments[1].Text)
	require.NotNil(t, result.Segments[1].Speaker)
	assert.Equal(t, "SPEAKER_1", *result.Segments[1].Speaker)
}

// TestDeepgramAdapter_BuildTranscriptionOptions checks that params are correctly
// forwarded to Deepgram API options.
func TestDeepgramAdapter_BuildTranscriptionOptions(t *testing.T) {
	adapter := newDeepgramTestAdapter("key")

	t.Run("nova-2 model maps to nova-2", func(t *testing.T) {
		opts := adapter.buildTranscriptionOptions(map[string]interface{}{"model": "deepgram-nova-2"})
		assert.Equal(t, "nova-2", opts.Model)
	})

	t.Run("whisper model maps to whisper", func(t *testing.T) {
		opts := adapter.buildTranscriptionOptions(map[string]interface{}{"model": "deepgram-whisper"})
		assert.Equal(t, "whisper", opts.Model)
	})

	t.Run("language is forwarded", func(t *testing.T) {
		opts := adapter.buildTranscriptionOptions(map[string]interface{}{"language": "fr"})
		assert.Equal(t, "fr", opts.Language)
	})

	t.Run("detect_language is forwarded", func(t *testing.T) {
		opts := adapter.buildTranscriptionOptions(map[string]interface{}{"detect_language": true})
		assert.True(t, opts.DetectLanguage)
	})

	t.Run("nil params returns default nova-3 model", func(t *testing.T) {
		opts := adapter.buildTranscriptionOptions(nil)
		assert.Equal(t, "nova-3", opts.Model)
	})

	t.Run("smart_format is enabled by default", func(t *testing.T) {
		opts := adapter.buildTranscriptionOptions(nil)
		assert.True(t, opts.SmartFormat)
	})

	t.Run("punctuate is enabled by default", func(t *testing.T) {
		opts := adapter.buildTranscriptionOptions(nil)
		assert.True(t, opts.Punctuate)
	})
}

// TestDeepgramAdapter_ConvertResponse_WordsFallback verifies that when there are no
// utterances but words exist, word grouping is used for segments.
func TestDeepgramAdapter_ConvertResponse_WordsFallback(t *testing.T) {
	speakerA := 0
	words := []deepgramWord{
		{PunctuatedWord: "Word", Start: 0.0, End: 0.5, Confidence: 0.99, Speaker: &speakerA},
		{PunctuatedWord: "two.", Start: 0.6, End: 1.0, Confidence: 0.98, Speaker: &speakerA},
	}
	resp := buildDeepgramResponse("Word two.", "en", 0.98, words, nil)
	result := convertDeepgramTranscript(resp, "deepgram-nova-3", time.Second)

	require.NotNil(t, result)
	require.Len(t, result.Segments, 1, "consecutive words from same speaker should merge")
	assert.Equal(t, "Word two.", result.Segments[0].Text)
}

// --- test helpers ---

// deepgramWord and deepgramUtterance are the internal types used by the adapter.
// buildDeepgramResponse constructs a minimal response for testing without hitting the API.
func buildDeepgramResponse(
	transcript string,
	language string,
	confidence float64,
	words []deepgramWord,
	utterances []deepgramUtterance,
) *deepgramResponse {
	return &deepgramResponse{
		Transcript: transcript,
		Language:   language,
		Confidence: confidence,
		Words:      words,
		Utterances: utterances,
	}
}

func intPtr(i int) *int { return &i }

// Compile-time interface assertions ensure DeepgramAdapter satisfies the required interfaces.
var _ interfaces.TranscriptionAdapter = (*DeepgramAdapter)(nil)
var _ interfaces.DiarizationAdapter = (*DeepgramAdapter)(nil)

// TestDeepgramAdapter_WordsWithNilSpeaker checks that words missing a speaker label do not panic.
func TestDeepgramAdapter_WordsWithNilSpeaker(t *testing.T) {
	words := []deepgramWord{
		{PunctuatedWord: "Hello.", Start: 0.0, End: 0.5, Speaker: nil},
		{PunctuatedWord: "World.", Start: 0.6, End: 1.0, Speaker: nil},
	}
	resp := buildDeepgramResponse("Hello. World.", "en", 0.9, words, nil)
	result := convertDeepgramTranscript(resp, "deepgram-nova-3", time.Second)

	require.NotNil(t, result)
	// Both nil-speaker words should merge into one segment under SPEAKER_UNKNOWN
	require.Len(t, result.Segments, 1)
	assert.Equal(t, "SPEAKER_UNKNOWN", *result.Segments[0].Speaker)
}

// TestDeepgramAdapter_MetadataIncludesProvider ensures the metadata map always has a provider key.
func TestDeepgramAdapter_MetadataIncludesProvider(t *testing.T) {
	resp := buildDeepgramResponse("test", "en", 0.9, nil, nil)

	transcriptResult := convertDeepgramTranscript(resp, "deepgram-nova-3", time.Second)
	assert.Equal(t, "deepgram", transcriptResult.Metadata["provider"])

	diarizationResult := convertDeepgramDiarization(resp, "deepgram-nova-3", time.Second)
	assert.Equal(t, "deepgram", diarizationResult.Metadata["provider"])
}

// TestDeepgramAdapter_LargeNumberOfSpeakers verifies no panic or miscounting with many speakers.
func TestDeepgramAdapter_LargeNumberOfSpeakers(t *testing.T) {
	numSpeakers := 20
	var utterances []deepgramUtterance
	for i := 0; i < numSpeakers; i++ {
		idx := i // capture loop variable
		utterances = append(utterances, deepgramUtterance{
			Start:      float64(i) * 2,
			End:        float64(i)*2 + 1.5,
			Transcript: fmt.Sprintf("Speaker %d says hello.", i),
			Speaker:    &idx,
			Confidence: 0.95,
		})
	}

	resp := buildDeepgramResponse("", "en", 0.95, nil, utterances)
	result := convertDeepgramDiarization(resp, "deepgram-nova-3", time.Second)

	require.NotNil(t, result)
	assert.Equal(t, numSpeakers, result.SpeakerCount)
	assert.Len(t, result.Speakers, numSpeakers)
	assert.Len(t, result.Segments, numSpeakers)
}
