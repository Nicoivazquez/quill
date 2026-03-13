package contacts

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"quill/pkg/binaries"
	"quill/pkg/logger"
)

// ClipWindow defines a start/end time range in seconds for speaker audio extraction.
type ClipWindow struct {
	Start float64
	End   float64
}

const (
	speakerClipTimeout = 30 * time.Second
	embeddingTimeout   = 2 * time.Minute
)

// ExtractSpeakerEmbeddings extracts voice embeddings for each speaker from the
// given audio file. For each speaker, it clips the relevant audio segment using
// FFmpeg, then runs the TitaNet model to produce a 256-dimensional embedding.
//
// Parameters:
//   - ctx: context with cancellation support
//   - audioPath: absolute path to the source audio file
//   - windows: map of speaker label → ClipWindow (time range to extract)
//   - whisperXEnv: base path for Python environments (TitaNet lives under whisperXEnv/parakeet)
//
// Returns a map of speaker label → embedding vector. Speakers for which extraction
// fails are omitted (logged as warnings).
func ExtractSpeakerEmbeddings(
	ctx context.Context,
	audioPath string,
	windows map[string]ClipWindow,
	whisperXEnv string,
) (map[string][]float64, error) {
	if len(windows) == 0 {
		return map[string][]float64{}, nil
	}

	// Create temp directory for clips and embeddings.
	tmpDir, err := os.MkdirTemp("", "quill-speaker-embeddings-*")
	if err != nil {
		return nil, fmt.Errorf("speaker_embedding: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	envPath := filepath.Join(whisperXEnv, "parakeet")

	// Prepare TitaNet runtime once for all speakers.
	if err := PrepareTitaNetRuntime(ctx, envPath); err != nil {
		return nil, fmt.Errorf("speaker_embedding: prepare runtime: %w", err)
	}

	result := make(map[string][]float64, len(windows))

	for speaker, window := range windows {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		safe := sanitizeFilename(speaker)
		clipPath := filepath.Join(tmpDir, fmt.Sprintf("%s.wav", safe))
		embeddingPath := filepath.Join(tmpDir, fmt.Sprintf("%s.embedding.json", safe))

		// Step 1: Extract audio clip with FFmpeg.
		if err := extractClipForEmbedding(ctx, audioPath, clipPath, window); err != nil {
			logger.Warn("speaker_embedding: clip extraction failed",
				"speaker", speaker, "error", err)
			continue
		}

		// Step 2: Run TitaNet embedding extraction.
		if err := runTitaNetExtraction(ctx, envPath, clipPath, embeddingPath); err != nil {
			logger.Warn("speaker_embedding: TitaNet extraction failed",
				"speaker", speaker, "error", err)
			continue
		}

		// Step 3: Load the embedding vector.
		vec, loadErr := LoadEmbeddingVector(embeddingPath)
		if loadErr != nil {
			logger.Warn("speaker_embedding: failed to load embedding",
				"speaker", speaker, "error", loadErr)
			continue
		}

		result[speaker] = vec
	}

	return result, nil
}

func extractClipForEmbedding(ctx context.Context, audioPath, outputPath string, window ClipWindow) error {
	start := math.Max(0, window.Start)
	end := math.Max(start+0.05, window.End)
	duration := end - start

	cmdCtx, cancel := context.WithTimeout(ctx, speakerClipTimeout)
	defer cancel()

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-ss", fmt.Sprintf("%.3f", start),
		"-t", fmt.Sprintf("%.3f", duration),
		"-i", audioPath,
		"-ac", "1",
		"-ar", "16000",
		"-vn",
		outputPath,
	}
	cmd := exec.CommandContext(cmdCtx, binaries.FFmpeg(), args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			trimmed = err.Error()
		}
		return fmt.Errorf("ffmpeg: %s", trimmed)
	}
	return nil
}

func runTitaNetExtraction(ctx context.Context, envPath, inputPath, outputPath string) error {
	if err := os.MkdirAll(envPath, 0o755); err != nil {
		return fmt.Errorf("prepare env dir: %w", err)
	}

	scriptPath := filepath.Join(envPath, "extract_titanet_embedding.py")
	if err := os.WriteFile(scriptPath, []byte(titanetEmbeddingScript), 0o755); err != nil {
		return fmt.Errorf("write embedding script: %w", err)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, embeddingTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx,
		binaries.UV(),
		"run", "--native-tls", "--project", envPath,
		"python", scriptPath,
		"--input", inputPath,
		"--output", outputPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			trimmed = err.Error()
		}
		return fmt.Errorf("titanet: %s", trimmed)
	}
	return nil
}

func sanitizeFilename(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_")
	return r.Replace(strings.TrimSpace(s))
}
