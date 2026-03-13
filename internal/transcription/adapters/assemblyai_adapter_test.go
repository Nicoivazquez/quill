package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	assemblyai "github.com/AssemblyAI/assemblyai-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"quill/internal/models"
	"quill/internal/repository"
	"quill/internal/transcription/interfaces"
)

// --- mock for CloudProviderConfigRepository ---

type mockCloudProviderRepo struct {
	config *models.CloudProviderConfig
	err    error
}

func (m *mockCloudProviderRepo) Upsert(_ context.Context, _ *models.CloudProviderConfig) error {
	return m.err
}

func (m *mockCloudProviderRepo) GetByProvider(_ context.Context, _ string) (*models.CloudProviderConfig, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.config, nil
}

func (m *mockCloudProviderRepo) ListAll(_ context.Context) ([]models.CloudProviderConfig, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.config != nil {
		return []models.CloudProviderConfig{*m.config}, nil
	}
	return nil, nil
}

func (m *mockCloudProviderRepo) ListActive(_ context.Context) ([]models.CloudProviderConfig, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.config != nil && m.config.IsActive {
		return []models.CloudProviderConfig{*m.config}, nil
	}
	return nil, nil
}

func (m *mockCloudProviderRepo) Delete(_ context.Context, _ string) error {
	return m.err
}

var _ repository.CloudProviderConfigRepository = (*mockCloudProviderRepo)(nil)

// --- helpers ---

func newTestAdapter(apiKey string) *AssemblyAIAdapter {
	var cfg *models.CloudProviderConfig
	if apiKey != "" {
		cfg = &models.CloudProviderConfig{
			Provider: "assemblyai",
			APIKey:   apiKey,
			IsActive: true,
		}
	}
	repo := &mockCloudProviderRepo{config: cfg}
	return NewAssemblyAIAdapter(repo)
}

// --- tests ---

func TestAssemblyAIAdapter_GetSupportedModels(t *testing.T) {
	adapter := newTestAdapter("test-key")
	models := adapter.GetSupportedModels()

	assert.Contains(t, models, "assemblyai-best", "should include assemblyai-best")
	assert.Contains(t, models, "assemblyai-nano", "should include assemblyai-nano")
	assert.Len(t, models, 2, "should expose exactly two model variants")
}

func TestAssemblyAIAdapter_GetMaxSpeakers(t *testing.T) {
	adapter := newTestAdapter("test-key")
	assert.Equal(t, 50, adapter.GetMaxSpeakers())
}

func TestAssemblyAIAdapter_GetMinSpeakers(t *testing.T) {
	adapter := newTestAdapter("test-key")
	assert.Equal(t, 1, adapter.GetMinSpeakers())
}

func TestAssemblyAIAdapter_GetCapabilities(t *testing.T) {
	adapter := newTestAdapter("test-key")
	caps := adapter.GetCapabilities()

	assert.Equal(t, "assemblyai", caps.ModelID)
	assert.Equal(t, "assemblyai", caps.ModelFamily)
	assert.True(t, caps.Features["transcription"])
	assert.True(t, caps.Features["diarization"])
	assert.True(t, caps.Features["timestamps"])
	assert.False(t, caps.RequiresGPU, "cloud adapter must not require a local GPU")
}

// TestAssemblyAIAdapter_IsAvailable checks readiness depending on whether an API key exists.
func TestAssemblyAIAdapter_IsAvailable(t *testing.T) {
	t.Run("available when API key present", func(t *testing.T) {
		adapter := newTestAdapter("sk-test-key")
		assert.True(t, adapter.IsAvailable(context.Background()))
	})

	t.Run("not available when no API key", func(t *testing.T) {
		repo := &mockCloudProviderRepo{err: errors.New("record not found")}
		adapter := NewAssemblyAIAdapter(repo)
		assert.False(t, adapter.IsAvailable(context.Background()))
	})
}

// TestAssemblyAIAdapter_NoAPIKey verifies a clear error is returned when no key is configured.
func TestAssemblyAIAdapter_NoAPIKey(t *testing.T) {
	repo := &mockCloudProviderRepo{err: errors.New("record not found")}
	adapter := NewAssemblyAIAdapter(repo)

	_, err := adapter.Transcribe(context.Background(), interfaces.AudioInput{FilePath: "/fake/audio.mp3"}, nil, interfaces.ProcessingContext{JobID: "job-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assemblyai", "error should mention the provider")

	_, err = adapter.Diarize(context.Background(), interfaces.AudioInput{FilePath: "/fake/audio.mp3"}, nil, interfaces.ProcessingContext{JobID: "job-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assemblyai", "error should mention the provider")
}

// TestAssemblyAIAdapter_ConvertTranscriptResponse exercises the conversion helper with
// realistic AssemblyAI API response data.
func TestAssemblyAIAdapter_ConvertTranscriptResponse(t *testing.T) {
	speakerA := "A"
	speakerB := "B"
	startA := int64(0)
	endA := int64(3500)
	startB := int64(4000)
	endB := int64(8200)
	conf := 0.95
	langCode := assemblyai.TranscriptLanguageCode("en")
	text := "Hello world. How are you?"

	transcript := assemblyai.Transcript{
		Text:         &text,
		LanguageCode: langCode,
		Confidence:   &conf,
		Utterances: []assemblyai.TranscriptUtterance{
			{
				Start:      &startA,
				End:        &endA,
				Speaker:    &speakerA,
				Text:       strPtr("Hello world."),
				Confidence: float64Ptr(0.97),
			},
			{
				Start:      &startB,
				End:        &endB,
				Speaker:    &speakerB,
				Text:       strPtr("How are you?"),
				Confidence: float64Ptr(0.93),
			},
		},
	}

	result := convertAssemblyAITranscript(transcript, "assemblyai-best", time.Second)

	require.NotNil(t, result)
	assert.Equal(t, text, result.Text)
	assert.Equal(t, "en", result.Language)
	assert.InDelta(t, 0.95, result.Confidence, 0.001)
	assert.Equal(t, "assemblyai-best", result.ModelUsed)

	require.Len(t, result.Segments, 2)

	seg0 := result.Segments[0]
	assert.InDelta(t, 0.0, seg0.Start, 0.001)   // 0ms → 0.0s
	assert.InDelta(t, 3.5, seg0.End, 0.001)      // 3500ms → 3.5s
	assert.Equal(t, "Hello world.", seg0.Text)
	require.NotNil(t, seg0.Speaker)
	assert.Equal(t, "A", *seg0.Speaker)

	seg1 := result.Segments[1]
	assert.InDelta(t, 4.0, seg1.Start, 0.001)
	assert.InDelta(t, 8.2, seg1.End, 0.001)
	assert.Equal(t, "How are you?", seg1.Text)
	require.NotNil(t, seg1.Speaker)
	assert.Equal(t, "B", *seg1.Speaker)
}

// TestAssemblyAIAdapter_ConvertTranscriptResponse_EmptyUtterances handles responses
// where AssemblyAI returns no utterances (diarization disabled or mono audio).
func TestAssemblyAIAdapter_ConvertTranscriptResponse_EmptyUtterances(t *testing.T) {
	text := "Some transcribed text."
	conf := 0.88
	transcript := assemblyai.Transcript{
		Text:       &text,
		Confidence: &conf,
		Utterances: nil,
	}

	result := convertAssemblyAITranscript(transcript, "assemblyai-nano", 500*time.Millisecond)

	require.NotNil(t, result)
	assert.Equal(t, text, result.Text)
	assert.Empty(t, result.Segments)
}

// TestAssemblyAIAdapter_ConvertTranscriptResponse_NilConfidence ensures we handle
// missing confidence gracefully (AssemblyAI omits it for some models).
func TestAssemblyAIAdapter_ConvertTranscriptResponse_NilConfidence(t *testing.T) {
	text := "Transcript without confidence."
	transcript := assemblyai.Transcript{
		Text:       &text,
		Confidence: nil,
	}

	result := convertAssemblyAITranscript(transcript, "assemblyai-best", time.Second)

	require.NotNil(t, result)
	assert.Equal(t, 0.0, result.Confidence, "nil confidence should default to 0.0")
}

// TestAssemblyAIAdapter_ConvertDiarizationResponse tests the diarization-specific
// conversion path that builds a DiarizationResult from utterances.
func TestAssemblyAIAdapter_ConvertDiarizationResponse(t *testing.T) {
	speakerA := "A"
	speakerB := "B"
	speakerC := "C"

	transcript := assemblyai.Transcript{
		Utterances: []assemblyai.TranscriptUtterance{
			{Start: int64Ptr(0), End: int64Ptr(2000), Speaker: &speakerA},
			{Start: int64Ptr(2500), End: int64Ptr(5000), Speaker: &speakerB},
			{Start: int64Ptr(5500), End: int64Ptr(7000), Speaker: &speakerC},
			{Start: int64Ptr(7500), End: int64Ptr(9000), Speaker: &speakerA},
		},
	}

	result := convertAssemblyAIDiarization(transcript, "assemblyai-best", time.Second)

	require.NotNil(t, result)
	require.Len(t, result.Segments, 4)
	assert.Equal(t, 3, result.SpeakerCount, "should count 3 unique speakers")
	assert.ElementsMatch(t, []string{"A", "B", "C"}, result.Speakers)

	assert.Equal(t, "A", result.Segments[0].Speaker)
	assert.InDelta(t, 0.0, result.Segments[0].Start, 0.001)
	assert.InDelta(t, 2.0, result.Segments[0].End, 0.001)

	assert.Equal(t, "B", result.Segments[1].Speaker)
	assert.Equal(t, "A", result.Segments[3].Speaker)
}

// TestAssemblyAIAdapter_ConvertDiarizationResponse_NoUtterances handles audio with
// no identifiable speakers.
func TestAssemblyAIAdapter_ConvertDiarizationResponse_NoUtterances(t *testing.T) {
	transcript := assemblyai.Transcript{
		Utterances: []assemblyai.TranscriptUtterance{},
	}

	result := convertAssemblyAIDiarization(transcript, "assemblyai-best", time.Second)

	require.NotNil(t, result)
	assert.Empty(t, result.Segments)
	assert.Equal(t, 0, result.SpeakerCount)
	assert.Empty(t, result.Speakers)
}

// TestAssemblyAIAdapter_ConvertDiarizationResponse_SingleSpeaker validates that a
// single-speaker recording is handled without panicking.
func TestAssemblyAIAdapter_ConvertDiarizationResponse_SingleSpeaker(t *testing.T) {
	speakerA := "A"
	transcript := assemblyai.Transcript{
		Utterances: []assemblyai.TranscriptUtterance{
			{Start: int64Ptr(0), End: int64Ptr(10000), Speaker: &speakerA},
		},
	}

	result := convertAssemblyAIDiarization(transcript, "assemblyai-best", time.Second)

	assert.Equal(t, 1, result.SpeakerCount)
	assert.Equal(t, []string{"A"}, result.Speakers)
}

// TestAssemblyAIAdapter_SpeechModelMapping verifies assemblyai-best maps to "best" and
// assemblyai-nano maps to "nano".
func TestAssemblyAIAdapter_SpeechModelMapping(t *testing.T) {
	tests := []struct {
		modelID  string
		expected assemblyai.SpeechModel
	}{
		{"assemblyai-best", assemblyai.SpeechModelBest},
		{"assemblyai-nano", assemblyai.SpeechModelNano},
		{"unknown-model", assemblyai.SpeechModelBest}, // default
		{"", assemblyai.SpeechModelBest},              // empty → default
	}

	for _, tc := range tests {
		t.Run(tc.modelID, func(t *testing.T) {
			got := mapSpeechModel(tc.modelID)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestAssemblyAIAdapter_ParameterSchema checks the schema contains expected parameters.
func TestAssemblyAIAdapter_ParameterSchema(t *testing.T) {
	adapter := newTestAdapter("key")
	schema := adapter.GetParameterSchema()

	names := make(map[string]bool)
	for _, p := range schema {
		names[p.Name] = true
	}

	assert.True(t, names["model"], "schema should include 'model' parameter")
	assert.True(t, names["language_code"], "schema should include 'language_code' parameter")
	assert.True(t, names["speakers_expected"], "schema should include 'speakers_expected' parameter")
	assert.True(t, names["speaker_labels"], "schema should include 'speaker_labels' parameter")
}

// TestAssemblyAIAdapter_PrepareEnvironment verifies the adapter reports ready after
// PrepareEnvironment is called (no-op for cloud adapters).
func TestAssemblyAIAdapter_PrepareEnvironment(t *testing.T) {
	adapter := newTestAdapter("key")
	err := adapter.PrepareEnvironment(context.Background())
	require.NoError(t, err)
	// After PrepareEnvironment the base adapter flags initialized=true,
	// but IsAvailable still depends on the API key.
	assert.True(t, adapter.IsAvailable(context.Background()))
}

// TestAssemblyAIAdapter_IsReady mirrors IsAvailable — true when key exists.
func TestAssemblyAIAdapter_IsReady(t *testing.T) {
	t.Run("ready when key present", func(t *testing.T) {
		adapter := newTestAdapter("sk-ready")
		assert.True(t, adapter.IsReady(context.Background()))
	})

	t.Run("not ready when key absent", func(t *testing.T) {
		repo := &mockCloudProviderRepo{err: errors.New("not found")}
		adapter := NewAssemblyAIAdapter(repo)
		assert.False(t, adapter.IsReady(context.Background()))
	})
}

// TestAssemblyAIAdapter_ResolveModelID ensures the model ID defaults and overrides work.
func TestAssemblyAIAdapter_ResolveModelID(t *testing.T) {
	adapter := newTestAdapter("key")

	assert.Equal(t, "assemblyai-best", adapter.resolveModelID(nil))
	assert.Equal(t, "assemblyai-best", adapter.resolveModelID(map[string]interface{}{}))
	assert.Equal(t, "assemblyai-nano", adapter.resolveModelID(map[string]interface{}{"model": "assemblyai-nano"}))
	assert.Equal(t, "assemblyai-best", adapter.resolveModelID(map[string]interface{}{"model": "assemblyai-best"}))
}

// TestAssemblyAIAdapter_BuildTranscriptParams checks that params are correctly forwarded to SDK options.
func TestAssemblyAIAdapter_BuildTranscriptParams(t *testing.T) {
	adapter := newTestAdapter("key")

	t.Run("nano model maps to nano", func(t *testing.T) {
		opts := adapter.buildTranscriptParams(map[string]interface{}{"model": "assemblyai-nano"})
		assert.Equal(t, assemblyai.SpeechModelNano, opts.SpeechModel)
	})

	t.Run("language_code is forwarded", func(t *testing.T) {
		opts := adapter.buildTranscriptParams(map[string]interface{}{"language_code": "fr"})
		assert.Equal(t, assemblyai.TranscriptLanguageCode("fr"), opts.LanguageCode)
	})

	t.Run("language_detection is forwarded", func(t *testing.T) {
		opts := adapter.buildTranscriptParams(map[string]interface{}{"language_detection": true})
		require.NotNil(t, opts.LanguageDetection)
		assert.True(t, *opts.LanguageDetection)
	})

	t.Run("speakers_expected hint is forwarded", func(t *testing.T) {
		opts := adapter.buildTranscriptParams(map[string]interface{}{"speakers_expected": 3})
		require.NotNil(t, opts.SpeakersExpected)
		assert.Equal(t, int64(3), *opts.SpeakersExpected)
	})

	t.Run("zero speakers_expected is not forwarded", func(t *testing.T) {
		opts := adapter.buildTranscriptParams(map[string]interface{}{"speakers_expected": 0})
		assert.Nil(t, opts.SpeakersExpected)
	})

	t.Run("nil params returns default best model", func(t *testing.T) {
		opts := adapter.buildTranscriptParams(nil)
		assert.Equal(t, assemblyai.SpeechModelBest, opts.SpeechModel)
	})
}

// TestAssemblyAIAdapter_GetEstimatedProcessingTime verifies the time estimate is nonzero for real audio.
func TestAssemblyAIAdapter_GetEstimatedProcessingTime(t *testing.T) {
	adapter := newTestAdapter("key")
	input := interfaces.AudioInput{
		Duration: 10 * time.Minute,
		Size:     10 * 1024 * 1024,
	}
	est := adapter.GetEstimatedProcessingTime(input)
	assert.Greater(t, est, time.Duration(0))
}

// TestAssemblyAIAdapter_ValidateParameters checks required parameter validation via the BaseAdapter.
func TestAssemblyAIAdapter_ValidateParameters(t *testing.T) {
	adapter := newTestAdapter("key")

	t.Run("valid model option passes", func(t *testing.T) {
		err := adapter.ValidateParameters(map[string]interface{}{"model": "assemblyai-best"})
		assert.NoError(t, err)
	})

	t.Run("invalid model option fails", func(t *testing.T) {
		err := adapter.ValidateParameters(map[string]interface{}{"model": "not-a-real-model"})
		assert.Error(t, err)
	})
}

// TestAssemblyAIAdapter_MsToSeconds verifies millisecond to second conversion.
func TestAssemblyAIAdapter_MsToSeconds(t *testing.T) {
	assert.Equal(t, 0.0, msToSeconds(0))
	assert.Equal(t, 1.0, msToSeconds(1000))
	assert.Equal(t, 1.5, msToSeconds(1500))
	assert.Equal(t, 60.0, msToSeconds(60000))
}

// --- helper functions ---

func strPtr(s string) *string       { return &s }
func float64Ptr(f float64) *float64 { return &f }
func int64Ptr(i int64) *int64       { return &i }
