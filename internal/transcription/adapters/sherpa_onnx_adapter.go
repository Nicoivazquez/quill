package adapters

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"

	"quill/internal/transcription/interfaces"
	"quill/pkg/downloader"
	"quill/pkg/logger"
)

const (
	segmentationModelDir  = "sherpa-onnx-pyannote-segmentation-3-0"
	segmentationModelFile = "model.onnx"
	embeddingModelFile = "wespeaker_en_voxceleb_CAM++.onnx"

	segmentationDownloadURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-segmentation-models/sherpa-onnx-pyannote-segmentation-3-0.tar.bz2"
	embeddingDownloadURL    = "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/wespeaker_en_voxceleb_CAM++.onnx"

	// Threshold for sherpa-onnx's internal HAC (complete linkage).
	// 0.8 is the highest non-crashing value; yields ~8 speakers on typical audio.
	// Post-processing centroid merging handles the remaining over-segmentation.
	defaultThreshold      float32 = 0.8
	defaultMinDurationOff float32 = 0.8

	// centroidMergeThreshold controls the post-processing centroid-linkage merge.
	// Sherpa-onnx uses complete-linkage HAC which is too conservative (one outlier
	// embedding prevents merging). We fix this by extracting per-segment embeddings,
	// computing speaker centroids, and merging clusters whose centroids are closer
	// than this cosine dissimilarity threshold.
	// Empirically validated: same-speaker centroid distances cluster at 0.02–0.04,
	// cross-speaker distances jump to 0.21+. Threshold 0.10 cleanly separates them.
	centroidMergeThreshold float64 = 0.10
)

// SherpaOnnxAdapter implements diarization using sherpa-onnx's native Go/C++ bindings.
// No Python, no HuggingFace token, no subprocess required.
type SherpaOnnxAdapter struct {
	*BaseAdapter
	modelsPath string
}

func floatPtr(f float64) *float64 { return &f }

// NewSherpaOnnxAdapter creates a new sherpa-onnx diarization adapter
func NewSherpaOnnxAdapter(modelsPath string) *SherpaOnnxAdapter {
	capabilities := interfaces.ModelCapabilities{
		ModelID:            "sherpa-onnx",
		ModelFamily:        "sherpa_onnx",
		DisplayName:        "sherpa-onnx Speaker Diarization",
		Description:        "Native ONNX Runtime diarization using pyannote-segmentation-3.0. No Python or HuggingFace token required.",
		Version:            "1.12.34",
		SupportedLanguages: []string{"*"},
		SupportedFormats:   []string{"wav", "mp3", "flac", "m4a", "ogg", "webm"},
		RequiresGPU:        false,
		MemoryRequirement:  512,
		Features: map[string]bool{
			"speaker_detection": true,
			"no_token_required": true,
			"no_python":         true,
			"fast_processing":   true,
		},
		Metadata: map[string]string{
			"engine":      "onnxruntime",
			"framework":   "sherpa-onnx",
			"license":     "Apache-2.0",
			"sample_rate": "16000",
			"no_auth":     "true",
		},
	}

	schema := []interfaces.ParameterSchema{
		{
			Name:        "num_speakers",
			Type:        "int",
			Required:    false,
			Default:     0,
			Min:         floatPtr(0),
			Max:         floatPtr(20),
			Description: "Known speaker count. 0 = auto-detect via clustering threshold",
			Group:       "basic",
		},
		{
			Name:        "threshold",
			Type:        "float",
			Required:    false,
			Default:     float64(defaultThreshold),
			Min:         floatPtr(0),
			Max:         floatPtr(2),
			Description: "Cosine dissimilarity threshold for auto-detect mode (num_speakers=0). Higher = fewer speakers. Range 0-2",
			Group:       "basic",
		},
		{
			Name:        "num_threads",
			Type:        "int",
			Required:    false,
			Default:     4,
			Min:         floatPtr(1),
			Max:         floatPtr(16),
			Description: "ONNX Runtime thread count",
			Group:       "advanced",
		},
		{
			Name:        "min_speakers",
			Type:        "int",
			Required:    false,
			Default:     0,
			Min:         floatPtr(0),
			Max:         floatPtr(20),
			Description: "Minimum number of speakers (hint)",
			Group:       "basic",
		},
		{
			Name:        "max_speakers",
			Type:        "int",
			Required:    false,
			Default:     0,
			Min:         floatPtr(0),
			Max:         floatPtr(20),
			Description: "Maximum number of speakers (hint)",
			Group:       "basic",
		},
		{
			Name:        "auto_convert_audio",
			Type:        "bool",
			Required:    false,
			Default:     true,
			Description: "Automatically convert audio to 16kHz mono WAV via FFmpeg",
			Group:       "advanced",
		},
	}

	baseAdapter := NewBaseAdapter("sherpa-onnx", modelsPath, capabilities, schema)

	return &SherpaOnnxAdapter{
		BaseAdapter: baseAdapter,
		modelsPath:  modelsPath,
	}
}

func (s *SherpaOnnxAdapter) GetMaxSpeakers() int { return 20 }
func (s *SherpaOnnxAdapter) GetMinSpeakers() int { return 1 }

func (s *SherpaOnnxAdapter) segmentationModelPath() string {
	return filepath.Join(s.modelsPath, segmentationModelDir, segmentationModelFile)
}

func (s *SherpaOnnxAdapter) embeddingModelPath() string {
	return filepath.Join(s.modelsPath, embeddingModelFile)
}

// IsReady checks if both ONNX model files exist on disk
func (s *SherpaOnnxAdapter) IsReady(ctx context.Context) bool {
	for _, path := range []string{s.segmentationModelPath(), s.embeddingModelPath()} {
		stat, err := os.Stat(path)
		if err != nil || stat.Size() < 1024*1024 {
			return false
		}
	}
	return true
}

// PrepareEnvironment downloads the ONNX models if they are not already present
func (s *SherpaOnnxAdapter) PrepareEnvironment(ctx context.Context) error {
	return RunPrepareOnce("sherpa-onnx-models:"+s.modelsPath, func() error {
		logger.Info("Preparing sherpa-onnx environment", "models_path", s.modelsPath)

		if s.IsReady(ctx) {
			logger.Info("sherpa-onnx models already present")
			return nil
		}

		if err := os.MkdirAll(s.modelsPath, 0755); err != nil {
			return fmt.Errorf("failed to create models directory: %w", err)
		}

		if err := s.downloadSegmentationModel(ctx); err != nil {
			return fmt.Errorf("failed to download segmentation model: %w", err)
		}

		if err := s.downloadEmbeddingModel(ctx); err != nil {
			return fmt.Errorf("failed to download embedding model: %w", err)
		}

		s.initialized = true
		logger.Info("sherpa-onnx environment ready")
		return nil
	})
}

func (s *SherpaOnnxAdapter) downloadSegmentationModel(ctx context.Context) error {
	modelPath := s.segmentationModelPath()
	if stat, err := os.Stat(modelPath); err == nil && stat.Size() > 1024*1024 {
		logger.Info("Segmentation model already exists", "path", modelPath)
		return nil
	}

	logger.Info("Downloading sherpa-onnx segmentation model")

	dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	tarPath := filepath.Join(s.modelsPath, "segmentation.tar.bz2")
	if err := downloader.DownloadFile(dlCtx, segmentationDownloadURL, tarPath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(tarPath)

	if err := extractTarBz2(tarPath, s.modelsPath); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	if stat, err := os.Stat(modelPath); err != nil || stat.Size() < 1024*1024 {
		return fmt.Errorf("segmentation model not found after extraction at %s", modelPath)
	}

	logger.Info("Segmentation model ready", "path", modelPath)
	return nil
}

func (s *SherpaOnnxAdapter) downloadEmbeddingModel(ctx context.Context) error {
	modelPath := s.embeddingModelPath()
	if stat, err := os.Stat(modelPath); err == nil && stat.Size() > 1024*1024 {
		logger.Info("Embedding model already exists", "path", modelPath)
		return nil
	}

	logger.Info("Downloading sherpa-onnx speaker embedding model")

	dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	if err := downloader.DownloadFile(dlCtx, embeddingDownloadURL, modelPath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if stat, err := os.Stat(modelPath); err != nil || stat.Size() < 1024*1024 {
		return fmt.Errorf("embedding model appears incomplete")
	}

	logger.Info("Embedding model ready", "path", modelPath)
	return nil
}

// Diarize performs speaker diarization using sherpa-onnx
func (s *SherpaOnnxAdapter) Diarize(ctx context.Context, input interfaces.AudioInput, params map[string]interface{}, procCtx interfaces.ProcessingContext) (result *interfaces.DiarizationResult, err error) {
	startTime := time.Now()
	s.LogProcessingStart(input, procCtx)
	defer func() {
		s.LogProcessingEnd(procCtx, time.Since(startTime), err)
	}()

	if err := s.ValidateAudioInput(input); err != nil {
		return nil, fmt.Errorf("invalid audio input: %w", err)
	}

	if err := s.ValidateParameters(params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	tempDir, err := s.CreateTempDirectory(procCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer s.CleanupTempDirectory(tempDir)

	// Convert audio to 16kHz mono WAV
	wavPath := input.FilePath
	if s.GetBoolParameter(params, "auto_convert_audio") {
		converted, err := s.convertToWav(ctx, input.FilePath, tempDir)
		if err != nil {
			return nil, fmt.Errorf("audio conversion failed: %w", err)
		}
		wavPath = converted
	}

	// Read WAV file using sherpa-onnx's built-in reader
	wave := sherpa.ReadWave(wavPath)
	if wave == nil {
		return nil, fmt.Errorf("failed to read WAV file: %s", wavPath)
	}

	if len(wave.Samples) == 0 {
		return nil, fmt.Errorf("WAV file contains no audio samples")
	}

	// Diagnostic: analyze audio samples for corruption
	sampleStats := analyzeSamples(wave.Samples)
	logger.Info("Audio loaded for diarization",
		"samples", len(wave.Samples),
		"sample_rate", wave.SampleRate,
		"duration_s", float64(len(wave.Samples))/float64(wave.SampleRate),
		"sample_min", sampleStats.min,
		"sample_max", sampleStats.max,
		"sample_mean", sampleStats.mean,
		"sample_rms", sampleStats.rms,
		"zero_fraction", sampleStats.zeroFraction,
		"nan_count", sampleStats.nanCount,
		"inf_count", sampleStats.infCount,
	)

	if sampleStats.nanCount > 0 || sampleStats.infCount > 0 {
		logger.Error("DIAGNOSTIC: Audio samples contain NaN/Inf values — this will corrupt diarization",
			"nan_count", sampleStats.nanCount,
			"inf_count", sampleStats.infCount)
	}

	if sampleStats.rms < 0.001 {
		logger.Warn("DIAGNOSTIC: Audio RMS is extremely low — may be silence or near-silence",
			"rms", sampleStats.rms)
	}

	// Configure sherpa-onnx diarization
	numThreads := s.GetIntParameter(params, "num_threads")
	if numThreads == 0 {
		numThreads = 4
	}

	config := sherpa.OfflineSpeakerDiarizationConfig{
		Segmentation: sherpa.OfflineSpeakerSegmentationModelConfig{
			Pyannote: sherpa.OfflineSpeakerSegmentationPyannoteModelConfig{
				Model: s.segmentationModelPath(),
			},
			NumThreads: numThreads,
			Debug:      1, // Enable debug output for segmentation
		},
		Embedding: sherpa.SpeakerEmbeddingExtractorConfig{
			Model:      s.embeddingModelPath(),
			NumThreads: numThreads,
			Debug:      1, // Enable debug output for embedding extraction
		},
		Clustering: sherpa.FastClusteringConfig{
			NumClusters: s.resolveNumClusters(params),
			Threshold:   s.resolveThreshold(params),
		},
		MinDurationOn:  0.3,
		MinDurationOff: defaultMinDurationOff,
	}

	sd := sherpa.NewOfflineSpeakerDiarization(&config)
	if sd == nil {
		return nil, fmt.Errorf("failed to create sherpa-onnx diarization instance (check model paths)")
	}
	defer sherpa.DeleteOfflineSpeakerDiarization(sd)

	// Diagnostic: verify sample rate matches
	expectedSR := sd.SampleRate()
	if wave.SampleRate != expectedSR {
		logger.Error("DIAGNOSTIC: Sample rate MISMATCH — this causes incorrect diarization",
			"wav_sample_rate", wave.SampleRate,
			"diarizer_expected_rate", expectedSR)
		return nil, fmt.Errorf("sample rate mismatch: WAV has %d Hz but diarizer expects %d Hz", wave.SampleRate, expectedSR)
	}

	logger.Info("Running sherpa-onnx diarization",
		"num_clusters", config.Clustering.NumClusters,
		"threshold", config.Clustering.Threshold,
		"diarizer_sample_rate", expectedSR,
		"min_duration_on", config.MinDurationOn,
		"min_duration_off", config.MinDurationOff,
		"segmentation_model", s.segmentationModelPath(),
		"embedding_model", s.embeddingModelPath())

	// Process audio
	segments := sd.Process(wave.Samples)

	// Diagnostic: dump raw segment info
	speakerCounts := make(map[int]int)
	for _, seg := range segments {
		speakerCounts[seg.Speaker]++
	}
	logger.Info("DIAGNOSTIC: Raw sherpa-onnx output",
		"total_segments", len(segments),
		"unique_speakers", len(speakerCounts),
		"speaker_segment_counts", fmt.Sprintf("%v", speakerCounts))

	// Log first and last few segments for debugging
	for i, seg := range segments {
		if i < 5 || i >= len(segments)-3 {
			logger.Debug("DIAGNOSTIC: segment",
				"index", i,
				"start", seg.Start,
				"end", seg.End,
				"speaker", seg.Speaker,
				"duration", seg.End-seg.Start)
		}
	}

	if len(segments) == 0 {
		logger.Warn("sherpa-onnx returned no segments")
		return &interfaces.DiarizationResult{
			Segments:       []interfaces.DiarizationSegment{},
			SpeakerCount:   0,
			Speakers:       []string{},
			ProcessingTime: time.Since(startTime),
			ModelUsed:      "sherpa-onnx",
			Metadata:       s.CreateDefaultMetadata(params),
		}, nil
	}

	result = s.mapToResult(segments)

	// Post-processing: centroid-linkage merging to fix over-segmentation.
	// sherpa-onnx uses complete-linkage HAC which is too conservative — a single
	// outlier embedding prevents merging. We extract per-segment embeddings,
	// compute speaker centroids, and iteratively merge the closest pair until
	// no pair is within centroidMergeThreshold. This mimics pyannote's native
	// centroid-linkage clustering.
	if s.resolveNumClusters(params) == 0 && result.SpeakerCount > 1 {
		merged, mergeErr := s.mergeSpeakersByCentroid(wave.Samples, wave.SampleRate, segments, result)
		if mergeErr != nil {
			logger.Warn("Centroid merge failed, using raw results", "error", mergeErr)
		} else {
			logger.Info("Centroid merge reduced speakers",
				"before", result.SpeakerCount,
				"after", merged.SpeakerCount)
			result = merged
		}
	}

	// Post-processing: cap speakers at max_speakers by merging smallest speakers
	if maxSpeakers := s.GetIntParameter(params, "max_speakers"); maxSpeakers > 0 && result.SpeakerCount > maxSpeakers {
		logger.Info("Merging over-segmented speakers",
			"detected", result.SpeakerCount,
			"max_speakers", maxSpeakers)
		result = s.limitSpeakers(result, maxSpeakers)
	}

	result.ProcessingTime = time.Since(startTime)
	result.ModelUsed = "sherpa-onnx"
	result.Metadata = s.CreateDefaultMetadata(params)

	logger.Info("sherpa-onnx diarization completed",
		"segments", len(result.Segments),
		"speakers", result.SpeakerCount,
		"processing_time", result.ProcessingTime)

	return result, nil
}

func (s *SherpaOnnxAdapter) resolveNumClusters(params map[string]interface{}) int {
	// Only num_speakers forces exact cluster count.
	// max_speakers is NOT used here — it's applied as post-processing in Diarize().
	if n := s.GetIntParameter(params, "num_speakers"); n > 0 {
		return n
	}
	return 0 // auto-detect via threshold
}

func (s *SherpaOnnxAdapter) resolveThreshold(params map[string]interface{}) float32 {
	if n := s.GetIntParameter(params, "num_speakers"); n > 0 {
		return 0 // threshold is ignored when num_clusters is set
	}
	threshold := s.GetFloatParameter(params, "threshold")
	if threshold > 0 {
		return float32(threshold)
	}
	return defaultThreshold
}

func (s *SherpaOnnxAdapter) mapToResult(segments []sherpa.OfflineSpeakerDiarizationSegment) *interfaces.DiarizationResult {
	speakerSet := make(map[int]bool)
	diarSegments := make([]interfaces.DiarizationSegment, len(segments))

	for i, seg := range segments {
		speakerLabel := fmt.Sprintf("speaker_%02d", seg.Speaker)
		speakerSet[seg.Speaker] = true
		diarSegments[i] = interfaces.DiarizationSegment{
			Start:      float64(seg.Start),
			End:        float64(seg.End),
			Speaker:    speakerLabel,
			Confidence: 1.0,
		}
	}

	speakers := make([]string, 0, len(speakerSet))
	for id := range speakerSet {
		speakers = append(speakers, fmt.Sprintf("speaker_%02d", id))
	}
	sort.Strings(speakers)

	return &interfaces.DiarizationResult{
		Segments:     diarSegments,
		SpeakerCount: len(speakerSet),
		Speakers:     speakers,
	}
}

// limitSpeakers merges the least-frequent speakers into the most similar remaining
// speakers until speaker count <= maxSpeakers. Similarity is approximated by
// temporal proximity (speakers that appear close together are likely the same).
func (s *SherpaOnnxAdapter) limitSpeakers(result *interfaces.DiarizationResult, maxSpeakers int) *interfaces.DiarizationResult {
	if result.SpeakerCount <= maxSpeakers {
		return result
	}

	// Count total duration per speaker
	type speakerInfo struct {
		label    string
		duration float64
	}
	durationMap := make(map[string]float64)
	for _, seg := range result.Segments {
		durationMap[seg.Speaker] += seg.End - seg.Start
	}

	speakers := make([]speakerInfo, 0, len(durationMap))
	for label, dur := range durationMap {
		speakers = append(speakers, speakerInfo{label, dur})
	}
	// Sort by duration descending — keep the speakers with most speaking time
	sort.Slice(speakers, func(i, j int) bool {
		return speakers[i].duration > speakers[j].duration
	})

	// The top maxSpeakers are kept; the rest get merged into the nearest kept speaker
	keepSet := make(map[string]bool, maxSpeakers)
	for i := 0; i < maxSpeakers && i < len(speakers); i++ {
		keepSet[speakers[i].label] = true
	}

	// For each discarded speaker, find the kept speaker with closest temporal overlap
	mergeMap := make(map[string]string) // discarded -> kept
	for _, sp := range speakers {
		if keepSet[sp.label] {
			continue
		}
		// Find kept speaker with minimum average time distance
		bestTarget := speakers[0].label
		bestDist := 1e18
		// Collect midpoints for the discarded speaker
		var discardedMids []float64
		for _, seg := range result.Segments {
			if seg.Speaker == sp.label {
				discardedMids = append(discardedMids, (seg.Start+seg.End)/2)
			}
		}
		for _, kept := range speakers[:maxSpeakers] {
			var keptMids []float64
			for _, seg := range result.Segments {
				if seg.Speaker == kept.label {
					keptMids = append(keptMids, (seg.Start+seg.End)/2)
				}
			}
			// Average minimum distance from each discarded segment to nearest kept segment
			totalDist := 0.0
			for _, dm := range discardedMids {
				minD := 1e18
				for _, km := range keptMids {
					d := dm - km
					if d < 0 {
						d = -d
					}
					if d < minD {
						minD = d
					}
				}
				totalDist += minD
			}
			avgDist := totalDist / float64(len(discardedMids))
			if avgDist < bestDist {
				bestDist = avgDist
				bestTarget = kept.label
			}
		}
		mergeMap[sp.label] = bestTarget
	}

	// Apply merges
	newSegments := make([]interfaces.DiarizationSegment, len(result.Segments))
	for i, seg := range result.Segments {
		newSegments[i] = seg
		if target, ok := mergeMap[seg.Speaker]; ok {
			newSegments[i].Speaker = target
		}
	}

	// Rebuild speaker list
	speakerSet := make(map[string]bool)
	for _, seg := range newSegments {
		speakerSet[seg.Speaker] = true
	}
	newSpeakers := make([]string, 0, len(speakerSet))
	for sp := range speakerSet {
		newSpeakers = append(newSpeakers, sp)
	}
	sort.Strings(newSpeakers)

	return &interfaces.DiarizationResult{
		Segments:     newSegments,
		SpeakerCount: len(newSpeakers),
		Speakers:     newSpeakers,
	}
}

// mergeSpeakersByCentroid extracts per-segment embeddings, computes speaker
// centroids, and iteratively merges the two closest speakers (by cosine
// dissimilarity of centroids) until no pair is closer than centroidMergeThreshold.
func (s *SherpaOnnxAdapter) mergeSpeakersByCentroid(
	samples []float32,
	sampleRate int,
	rawSegments []sherpa.OfflineSpeakerDiarizationSegment,
	result *interfaces.DiarizationResult,
) (*interfaces.DiarizationResult, error) {
	// Create a standalone embedding extractor
	exConfig := &sherpa.SpeakerEmbeddingExtractorConfig{
		Model:      s.embeddingModelPath(),
		NumThreads: 4,
	}
	extractor := sherpa.NewSpeakerEmbeddingExtractor(exConfig)
	if extractor == nil {
		return nil, fmt.Errorf("failed to create embedding extractor")
	}
	defer sherpa.DeleteSpeakerEmbeddingExtractor(extractor)

	dim := extractor.Dim()
	logger.Info("Extracting per-segment embeddings for centroid merge",
		"segments", len(rawSegments), "embedding_dim", dim)

	// Extract embedding for each segment
	type segmentEmbedding struct {
		speaker   int
		embedding []float32
	}
	var segEmbeddings []segmentEmbedding

	for _, seg := range rawSegments {
		startSample := int(seg.Start * float32(sampleRate))
		endSample := int(seg.End * float32(sampleRate))
		if startSample < 0 {
			startSample = 0
		}
		if endSample > len(samples) {
			endSample = len(samples)
		}
		if endSample-startSample < sampleRate/4 { // skip segments shorter than 250ms
			continue
		}

		stream := extractor.CreateStream()
		stream.AcceptWaveform(sampleRate, samples[startSample:endSample])
		stream.InputFinished()

		if !extractor.IsReady(stream) {
			sherpa.DeleteOnlineStream(stream)
			continue
		}

		emb := extractor.Compute(stream)
		sherpa.DeleteOnlineStream(stream)

		segEmbeddings = append(segEmbeddings, segmentEmbedding{
			speaker:   seg.Speaker,
			embedding: emb,
		})
	}

	if len(segEmbeddings) == 0 {
		return nil, fmt.Errorf("no valid segment embeddings extracted")
	}

	// Group embeddings by speaker and compute centroids
	speakerEmbeddings := make(map[int][][]float32)
	for _, se := range segEmbeddings {
		speakerEmbeddings[se.speaker] = append(speakerEmbeddings[se.speaker], se.embedding)
	}

	centroids := make(map[int][]float64) // speaker ID -> centroid (float64 for precision)
	for spk, embeddings := range speakerEmbeddings {
		centroid := make([]float64, dim)
		for _, emb := range embeddings {
			for i, v := range emb {
				centroid[i] += float64(v)
			}
		}
		n := float64(len(embeddings))
		for i := range centroid {
			centroid[i] /= n
		}
		centroids[spk] = centroid
	}

	logger.Info("Computed speaker centroids", "speakers", len(centroids))

	// Build merge map: iteratively merge closest pair
	mergeMap := make(map[int]int) // merged speaker -> target speaker

	// resolveTarget follows the merge chain to find the final target
	resolveTarget := func(spk int) int {
		for {
			t, ok := mergeMap[spk]
			if !ok {
				return spk
			}
			spk = t
		}
	}

	for {
		// Find active speakers (not yet merged)
		var active []int
		for spk := range centroids {
			if _, merged := mergeMap[spk]; !merged {
				active = append(active, spk)
			}
		}
		if len(active) <= 1 {
			break
		}

		// Find closest pair by cosine dissimilarity
		bestDist := 2.0 // max possible cosine dissimilarity
		bestI, bestJ := -1, -1
		for i := 0; i < len(active); i++ {
			for j := i + 1; j < len(active); j++ {
				d := cosineDissimilarity(centroids[active[i]], centroids[active[j]])
				if d < bestDist {
					bestDist = d
					bestI = i
					bestJ = j
				}
			}
		}

		if bestDist >= centroidMergeThreshold {
			break // no more merges possible
		}

		spkA, spkB := active[bestI], active[bestJ]

		// Merge the speaker with fewer segments into the one with more
		countA := len(speakerEmbeddings[spkA])
		countB := len(speakerEmbeddings[spkB])
		var keeper, absorbed int
		if countA >= countB {
			keeper, absorbed = spkA, spkB
		} else {
			keeper, absorbed = spkB, spkA
		}

		logger.Debug("Merging speakers by centroid",
			"absorbed", absorbed, "into", keeper,
			"cosine_dissimilarity", bestDist)

		mergeMap[absorbed] = keeper

		// Recompute keeper's centroid with absorbed embeddings
		allEmb := append(speakerEmbeddings[keeper], speakerEmbeddings[absorbed]...)
		speakerEmbeddings[keeper] = allEmb
		newCentroid := make([]float64, dim)
		for _, emb := range allEmb {
			for i, v := range emb {
				newCentroid[i] += float64(v)
			}
		}
		n := float64(len(allEmb))
		for i := range newCentroid {
			newCentroid[i] /= n
		}
		centroids[keeper] = newCentroid
	}

	if len(mergeMap) == 0 {
		return result, nil // no merges needed
	}

	// Apply merges to result segments
	newSegments := make([]interfaces.DiarizationSegment, len(result.Segments))
	for i, seg := range result.Segments {
		newSegments[i] = seg
		// Parse speaker index from label
		var spkIdx int
		fmt.Sscanf(seg.Speaker, "speaker_%d", &spkIdx)
		finalTarget := resolveTarget(spkIdx)
		if finalTarget != spkIdx {
			newSegments[i].Speaker = fmt.Sprintf("speaker_%02d", finalTarget)
		}
	}

	// Rebuild speaker list
	speakerSet := make(map[string]bool)
	for _, seg := range newSegments {
		speakerSet[seg.Speaker] = true
	}
	newSpeakers := make([]string, 0, len(speakerSet))
	for sp := range speakerSet {
		newSpeakers = append(newSpeakers, sp)
	}
	sort.Strings(newSpeakers)

	return &interfaces.DiarizationResult{
		Segments:     newSegments,
		SpeakerCount: len(newSpeakers),
		Speakers:     newSpeakers,
	}, nil
}

// cosineDissimilarity computes 1 - cosine_similarity between two vectors.
func cosineDissimilarity(a, b []float64) float64 {
	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 2.0 // max dissimilarity for zero vectors
	}
	similarity := dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
	return 1.0 - similarity
}

// convertToWav converts any audio format to 16kHz mono WAV using FFmpeg
func (s *SherpaOnnxAdapter) convertToWav(ctx context.Context, inputPath, tempDir string) (string, error) {
	outputPath := filepath.Join(tempDir, "converted.wav")
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", inputPath,
		"-ar", "16000",
		"-ac", "1",
		"-sample_fmt", "s16",
		"-f", "wav",
		"-y", outputPath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg conversion failed: %w: %s", err, string(out))
	}

	logger.Info("Audio converted to 16kHz mono WAV", "output", outputPath)
	return outputPath, nil
}

// GetEstimatedProcessingTime estimates processing time for sherpa-onnx
func (s *SherpaOnnxAdapter) GetEstimatedProcessingTime(input interfaces.AudioInput) time.Duration {
	audioDuration := input.Duration
	if audioDuration == 0 {
		estimatedMinutes := float64(input.Size) / (1024 * 1024)
		audioDuration = time.Duration(estimatedMinutes * float64(time.Minute))
	}
	// sherpa-onnx is very fast — roughly 5-10% of audio duration
	return time.Duration(float64(audioDuration) * 0.08)
}

// maxExtractFileSize is the per-file size limit during tar extraction (2 GiB).
const maxExtractFileSize = 2 << 30

// extractTarBz2 extracts a .tar.bz2 archive to the destination directory
func extractTarBz2(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	bzReader := bzip2.NewReader(f)
	tarReader := tar.NewReader(bzReader)
	cleanDest := filepath.Clean(destDir) + string(os.PathSeparator)

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		target := filepath.Join(destDir, filepath.FromSlash(header.Name))

		// Prevent path traversal
		if !strings.HasPrefix(target+string(os.PathSeparator), cleanDest) {
			return fmt.Errorf("invalid tar path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size > maxExtractFileSize {
				return fmt.Errorf("tar entry %q exceeds size limit (%d bytes)", header.Name, header.Size)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := extractTarFile(target, tarReader); err != nil {
				return err
			}
		default:
			logger.Warn("Skipping unsupported tar entry type", "name", header.Name, "type", header.Typeflag)
		}
	}

	return nil
}

func extractTarFile(target string, r io.Reader) error {
	outFile, err := os.Create(target)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(outFile, io.LimitReader(r, maxExtractFileSize))
	closeErr := outFile.Close()
	if copyErr != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(target), copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing %s: %w", filepath.Base(target), closeErr)
	}
	return nil
}

// sampleStats holds diagnostic statistics about audio samples.
type sampleStats struct {
	min          float32
	max          float32
	mean         float64
	rms          float64
	zeroFraction float64
	nanCount     int
	infCount     int
}

// analyzeSamples computes diagnostic statistics for audio sample data.
func analyzeSamples(samples []float32) sampleStats {
	if len(samples) == 0 {
		return sampleStats{}
	}

	stats := sampleStats{
		min: samples[0],
		max: samples[0],
	}

	var sum, sumSq float64
	var zeroCount int

	for _, s := range samples {
		if math.IsNaN(float64(s)) {
			stats.nanCount++
			continue
		}
		if math.IsInf(float64(s), 0) {
			stats.infCount++
			continue
		}
		if s < stats.min {
			stats.min = s
		}
		if s > stats.max {
			stats.max = s
		}
		if s == 0 {
			zeroCount++
		}
		sum += float64(s)
		sumSq += float64(s) * float64(s)
	}

	validCount := len(samples) - stats.nanCount - stats.infCount
	if validCount > 0 {
		stats.mean = sum / float64(validCount)
		stats.rms = math.Sqrt(sumSq / float64(validCount))
	}
	stats.zeroFraction = float64(zeroCount) / float64(len(samples))

	return stats
}
