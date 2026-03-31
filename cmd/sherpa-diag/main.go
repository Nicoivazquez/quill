// sherpa-diag: standalone diagnostic tool for sherpa-onnx diarization.
// Usage: go run ./cmd/sherpa-diag -audio <file> -seg-model <path> -emb-model <path> [-num-speakers N] [-threshold T]
//
// This bypasses the adapter layer to test sherpa-onnx directly,
// helping isolate whether diarization issues are in the adapter or the library.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

func main() {
	audioPath := flag.String("audio", "", "Path to audio file (any format — will be converted to WAV)")
	segModel := flag.String("seg-model", "", "Path to segmentation model (e.g., sherpa-onnx-pyannote-segmentation-3-0/model.onnx)")
	embModel := flag.String("emb-model", "", "Path to embedding model (e.g., wespeaker_en_voxceleb_CAM++.onnx)")
	numSpeakers := flag.Int("num-speakers", 0, "Known number of speakers (0 = auto-detect via threshold)")
	threshold := flag.Float64("threshold", 0.8, "Clustering threshold (only used when num-speakers=0)")
	mergeThreshold := flag.Float64("merge-threshold", 0.10, "Centroid-linkage merge threshold for post-processing (0=disable)")
	numThreads := flag.Int("threads", 4, "ONNX Runtime thread count")
	skipConvert := flag.Bool("skip-convert", false, "Skip FFmpeg conversion (input must already be 16kHz mono WAV)")
	flag.Parse()

	if *audioPath == "" || *segModel == "" || *embModel == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Verify model files exist
	for _, p := range []string{*segModel, *embModel} {
		if _, err := os.Stat(p); err != nil {
			fmt.Fprintf(os.Stderr, "Model file not found: %s\n", p)
			os.Exit(1)
		}
	}

	// Step 1: Convert audio to 16kHz mono WAV
	wavPath := *audioPath
	if !*skipConvert {
		tmpDir, err := os.MkdirTemp("", "sherpa-diag-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create temp dir: %v\n", err)
			os.Exit(1)
		}
		defer os.RemoveAll(tmpDir)

		wavPath = filepath.Join(tmpDir, "converted.wav")
		fmt.Printf("Converting audio to 16kHz mono WAV...\n")
		cmd := exec.Command("ffmpeg",
			"-i", *audioPath,
			"-ar", "16000",
			"-ac", "1",
			"-sample_fmt", "s16",
			"-f", "wav",
			"-y", wavPath,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "FFmpeg conversion failed: %v\n%s\n", err, string(out))
			os.Exit(1)
		}
		fi, _ := os.Stat(wavPath)
		fmt.Printf("Converted WAV: %s (%d bytes)\n", wavPath, fi.Size())
	}

	// Step 2: Read WAV
	fmt.Printf("\nReading WAV file: %s\n", wavPath)
	wave := sherpa.ReadWave(wavPath)
	if wave == nil {
		fmt.Fprintf(os.Stderr, "Failed to read WAV file\n")
		os.Exit(1)
	}

	fmt.Printf("  Samples:     %d\n", len(wave.Samples))
	fmt.Printf("  Sample rate: %d Hz\n", wave.SampleRate)
	fmt.Printf("  Duration:    %.2f seconds\n", float64(len(wave.Samples))/float64(wave.SampleRate))

	// Step 3: Analyze samples
	fmt.Printf("\nSample analysis:\n")
	var sMin, sMax float32
	var sum, sumSq float64
	var nanCount, infCount, zeroCount int
	if len(wave.Samples) > 0 {
		sMin, sMax = wave.Samples[0], wave.Samples[0]
	}
	for _, s := range wave.Samples {
		if math.IsNaN(float64(s)) {
			nanCount++
			continue
		}
		if math.IsInf(float64(s), 0) {
			infCount++
			continue
		}
		if s < sMin {
			sMin = s
		}
		if s > sMax {
			sMax = s
		}
		if s == 0 {
			zeroCount++
		}
		sum += float64(s)
		sumSq += float64(s) * float64(s)
	}
	valid := len(wave.Samples) - nanCount - infCount
	mean := sum / float64(valid)
	rms := math.Sqrt(sumSq / float64(valid))
	fmt.Printf("  Min:          %f\n", sMin)
	fmt.Printf("  Max:          %f\n", sMax)
	fmt.Printf("  Mean:         %f\n", mean)
	fmt.Printf("  RMS:          %f\n", rms)
	fmt.Printf("  Zero %%:       %.2f%%\n", float64(zeroCount)/float64(len(wave.Samples))*100)
	fmt.Printf("  NaN count:    %d\n", nanCount)
	fmt.Printf("  Inf count:    %d\n", infCount)

	if nanCount > 0 || infCount > 0 {
		fmt.Printf("\n*** WARNING: Audio contains %d NaN and %d Inf values! ***\n", nanCount, infCount)
	}

	// Step 4: Create diarization config
	numClusters := *numSpeakers
	clusterThreshold := float32(*threshold)
	if numClusters > 0 {
		clusterThreshold = 0 // threshold ignored when num_clusters is set
	}

	fmt.Printf("\nDiarization config:\n")
	fmt.Printf("  Segmentation model: %s\n", *segModel)
	fmt.Printf("  Embedding model:    %s\n", *embModel)
	fmt.Printf("  NumClusters:        %d (C API will convert 0 -> -1 via SHERPA_ONNX_OR)\n", numClusters)
	fmt.Printf("  Threshold:          %f (C API will convert 0.0 -> 0.5 via SHERPA_ONNX_OR)\n", clusterThreshold)
	fmt.Printf("  NumThreads:         %d\n", *numThreads)
	fmt.Printf("  MinDurationOn:      0.3 (default)\n")
	fmt.Printf("  MinDurationOff:     0.5 (default)\n")
	fmt.Printf("  Centroid merge:     %f\n", *mergeThreshold)

	config := sherpa.OfflineSpeakerDiarizationConfig{
		Segmentation: sherpa.OfflineSpeakerSegmentationModelConfig{
			Pyannote: sherpa.OfflineSpeakerSegmentationPyannoteModelConfig{
				Model: *segModel,
			},
			NumThreads: *numThreads,
			Debug:      1,
		},
		Embedding: sherpa.SpeakerEmbeddingExtractorConfig{
			Model:      *embModel,
			NumThreads: *numThreads,
			Debug:      1,
		},
		Clustering: sherpa.FastClusteringConfig{
			NumClusters: numClusters,
			Threshold:   clusterThreshold,
		},
		MinDurationOn:  0.3,
		MinDurationOff: 0.5,
	}

	sd := sherpa.NewOfflineSpeakerDiarization(&config)
	if sd == nil {
		fmt.Fprintf(os.Stderr, "Failed to create diarization instance — check model paths\n")
		os.Exit(1)
	}
	defer sherpa.DeleteOfflineSpeakerDiarization(sd)

	expectedSR := sd.SampleRate()
	fmt.Printf("  Diarizer expected sample rate: %d Hz\n", expectedSR)
	if wave.SampleRate != expectedSR {
		fmt.Printf("\n*** CRITICAL: Sample rate MISMATCH! WAV=%d, Diarizer=%d ***\n", wave.SampleRate, expectedSR)
		fmt.Printf("This would cause the segmentation model to misinterpret the audio.\n")
		os.Exit(1)
	}

	// Step 5: Process
	fmt.Printf("\nProcessing %d samples (%.1f seconds)...\n", len(wave.Samples), float64(len(wave.Samples))/float64(wave.SampleRate))
	segments := sd.Process(wave.Samples)

	fmt.Printf("\nResults (before centroid merge):\n")
	fmt.Printf("  Total segments: %d\n", len(segments))

	if len(segments) == 0 {
		fmt.Printf("  No segments returned.\n")
		return
	}

	// Count speakers
	speakerCounts := make(map[int]int)
	speakerDurations := make(map[int]float64)
	for _, seg := range segments {
		speakerCounts[seg.Speaker]++
		speakerDurations[seg.Speaker] += float64(seg.End - seg.Start)
	}
	fmt.Printf("  Unique speakers: %d\n", len(speakerCounts))
	printSpeakerBreakdown(speakerCounts, speakerDurations)
	printSegments(segments)

	// Step 6: Centroid-linkage post-processing merge
	if *mergeThreshold > 0 && numClusters == 0 && len(speakerCounts) > 1 {
		fmt.Printf("\n--- CENTROID-LINKAGE POST-PROCESSING ---\n")
		fmt.Printf("Merge threshold: %f (cosine dissimilarity)\n", *mergeThreshold)

		mergedSegments := centroidMerge(wave.Samples, wave.SampleRate, segments, *embModel, *numThreads, *mergeThreshold)
		if mergedSegments != nil {
			// Recount
			mergedCounts := make(map[int]int)
			mergedDurations := make(map[int]float64)
			for _, seg := range mergedSegments {
				mergedCounts[seg.Speaker]++
				mergedDurations[seg.Speaker] += float64(seg.End - seg.Start)
			}
			fmt.Printf("\nResults (after centroid merge):\n")
			fmt.Printf("  Total segments: %d\n", len(mergedSegments))
			fmt.Printf("  Unique speakers: %d (was %d)\n", len(mergedCounts), len(speakerCounts))
			printSpeakerBreakdown(mergedCounts, mergedDurations)
			printSegments(mergedSegments)

			// Update for summary
			speakerCounts = mergedCounts
		}
	}

	// Diagnostic summary
	fmt.Printf("\n--- DIAGNOSTIC SUMMARY ---\n")
	if len(speakerCounts) > 10 {
		fmt.Printf("PROBLEM: %d speakers detected — likely over-segmentation.\n", len(speakerCounts))
		fmt.Printf("Try adjusting -merge-threshold (lower = more aggressive merging).\n")
	} else {
		fmt.Printf("OK: %d speakers detected — appears reasonable.\n", len(speakerCounts))
	}
}

func centroidMerge(samples []float32, sampleRate int, segments []sherpa.OfflineSpeakerDiarizationSegment, embModelPath string, numThreads int, mergeThresh float64) []sherpa.OfflineSpeakerDiarizationSegment {
	exConfig := &sherpa.SpeakerEmbeddingExtractorConfig{
		Model:      embModelPath,
		NumThreads: numThreads,
	}
	extractor := sherpa.NewSpeakerEmbeddingExtractor(exConfig)
	if extractor == nil {
		fmt.Printf("  ERROR: Failed to create embedding extractor\n")
		return nil
	}
	defer sherpa.DeleteSpeakerEmbeddingExtractor(extractor)

	dim := extractor.Dim()
	fmt.Printf("  Embedding dimension: %d\n", dim)

	// Extract per-segment embeddings
	type segEmb struct {
		idx       int
		speaker   int
		embedding []float32
	}
	var embeddings []segEmb

	for i, seg := range segments {
		startSample := int(seg.Start * float32(sampleRate))
		endSample := int(seg.End * float32(sampleRate))
		if startSample < 0 {
			startSample = 0
		}
		if endSample > len(samples) {
			endSample = len(samples)
		}
		if endSample-startSample < sampleRate/4 { // skip < 250ms
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
		embeddings = append(embeddings, segEmb{idx: i, speaker: seg.Speaker, embedding: emb})
	}

	fmt.Printf("  Extracted %d segment embeddings (skipped %d short segments)\n",
		len(embeddings), len(segments)-len(embeddings))

	if len(embeddings) == 0 {
		return nil
	}

	// Group by speaker and compute centroids
	speakerEmbs := make(map[int][][]float32)
	for _, se := range embeddings {
		speakerEmbs[se.speaker] = append(speakerEmbs[se.speaker], se.embedding)
	}

	centroids := make(map[int][]float64)
	for spk, embs := range speakerEmbs {
		centroid := make([]float64, dim)
		for _, emb := range embs {
			for i, v := range emb {
				centroid[i] += float64(v)
			}
		}
		n := float64(len(embs))
		for i := range centroid {
			centroid[i] /= n
		}
		centroids[spk] = centroid
	}

	// Iteratively merge closest pair
	mergeMap := make(map[int]int)

	resolveTarget := func(spk int) int {
		for {
			t, ok := mergeMap[spk]
			if !ok {
				return spk
			}
			spk = t
		}
	}

	mergeRound := 0
	for {
		var active []int
		for spk := range centroids {
			if _, merged := mergeMap[spk]; !merged {
				active = append(active, spk)
			}
		}
		sort.Ints(active)
		if len(active) <= 1 {
			break
		}

		bestDist := 2.0
		bestI, bestJ := -1, -1
		for i := 0; i < len(active); i++ {
			for j := i + 1; j < len(active); j++ {
				d := cosDissim(centroids[active[i]], centroids[active[j]])
				if d < bestDist {
					bestDist = d
					bestI = i
					bestJ = j
				}
			}
		}

		if bestDist >= mergeThresh {
			break
		}

		spkA, spkB := active[bestI], active[bestJ]
		countA := len(speakerEmbs[spkA])
		countB := len(speakerEmbs[spkB])
		var keeper, absorbed int
		if countA >= countB {
			keeper, absorbed = spkA, spkB
		} else {
			keeper, absorbed = spkB, spkA
		}

		mergeRound++
		fmt.Printf("  Merge %d: speaker_%02d (%d segs) → speaker_%02d (%d segs) [dist=%.4f]\n",
			mergeRound, absorbed, len(speakerEmbs[absorbed]), keeper, len(speakerEmbs[keeper]), bestDist)

		mergeMap[absorbed] = keeper

		// Recompute centroid
		allEmb := append(speakerEmbs[keeper], speakerEmbs[absorbed]...)
		speakerEmbs[keeper] = allEmb
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
		fmt.Printf("  No merges needed — all speakers sufficiently distinct.\n")
		return nil
	}

	// Apply merges
	result := make([]sherpa.OfflineSpeakerDiarizationSegment, len(segments))
	for i, seg := range segments {
		result[i] = seg
		result[i].Speaker = resolveTarget(seg.Speaker)
	}
	return result
}

func cosDissim(a, b []float64) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 2.0
	}
	return 1.0 - dot/(math.Sqrt(normA)*math.Sqrt(normB))
}

func printSpeakerBreakdown(counts map[int]int, durations map[int]float64) {
	var ids []int
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	fmt.Printf("\n  Speaker breakdown:\n")
	for _, id := range ids {
		fmt.Printf("    Speaker %02d: %3d segments, %.1f seconds\n", id, counts[id], durations[id])
	}
}

func printSegments(segments []sherpa.OfflineSpeakerDiarizationSegment) {
	showCount := 10
	if showCount > len(segments) {
		showCount = len(segments)
	}
	fmt.Printf("\n  First %d segments:\n", showCount)
	for i := 0; i < showCount; i++ {
		seg := segments[i]
		fmt.Printf("    [%3d] %.3f - %.3f (%.3fs) speaker_%02d\n",
			i, seg.Start, seg.End, seg.End-seg.Start, seg.Speaker)
	}
	if len(segments) > 10 {
		fmt.Printf("    ... (%d more segments)\n", len(segments)-10)
		fmt.Printf("\n  Last 5 segments:\n")
		start := len(segments) - 5
		if start < 10 {
			start = 10
		}
		for i := start; i < len(segments); i++ {
			seg := segments[i]
			fmt.Printf("    [%3d] %.3f - %.3f (%.3fs) speaker_%02d\n",
				i, seg.Start, seg.End, seg.End-seg.Start, seg.Speaker)
		}
	}
}
