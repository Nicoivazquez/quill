package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"quill/internal/transcription/adapters"
	"quill/internal/transcription/interfaces"
	"quill/pkg/binaries"
)

const runtimeSeedManifestName = ".quill-runtime-seed.json"

type runtimeSeedManifest struct {
	Version            int      `json:"version"`
	SeedID             string   `json:"seed_id"`
	PreparedAt         string   `json:"prepared_at"`
	PyTorchCUDA        string   `json:"pytorch_cuda_version"`
	WhisperModels      []string `json:"whisper_models,omitempty"`
	IncludesWhisper    bool     `json:"includes_whisperx"`
	IncludesParakeet   bool     `json:"includes_parakeet"`
	IncludesSortformer bool     `json:"includes_sortformer"`
	IncludesCanary     bool     `json:"includes_canary"`
	IncludesTitaNet    bool     `json:"includes_titanet"`
}

func main() {
	var (
		outputDir         string
		whisperModelsRaw  string
		sampleAudio       string
		includeWhisperX   bool
		includeParakeet   bool
		includeSortformer bool
		includeCanary     bool
		includeTitaNet    bool
	)

	flag.StringVar(&outputDir, "output", "", "output directory for the prepared desktop runtime seed")
	flag.StringVar(&whisperModelsRaw, "whisper-models", "small", "comma-separated WhisperX models to prefetch")
	flag.StringVar(&sampleAudio, "sample-audio", "tests/data/AMI-Corpus-IB4002.Mix-Headset-clip.wav", "audio fixture used to prefetch WhisperX model caches")
	flag.BoolVar(&includeWhisperX, "include-whisperx", true, "prepare the WhisperX environment and prefetch configured Whisper models")
	flag.BoolVar(&includeParakeet, "include-parakeet", true, "prepare the shared NVIDIA NeMo runtime and bundle the Parakeet model")
	flag.BoolVar(&includeSortformer, "include-sortformer", true, "bundle the Sortformer diarization model into the shared NVIDIA runtime")
	flag.BoolVar(&includeCanary, "include-canary", false, "bundle the Canary model into the shared NVIDIA runtime")
	flag.BoolVar(&includeTitaNet, "include-titanet", true, "prefetch the TitaNet speaker embedding model into the shared NVIDIA runtime cache")
	flag.Parse()

	if strings.TrimSpace(outputDir) == "" {
		exitf("missing required --output")
	}

	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		exitf("resolve output path: %v", err)
	}
	if err := os.RemoveAll(absOutput); err != nil {
		exitf("reset output directory: %v", err)
	}
	if err := os.MkdirAll(absOutput, 0o755); err != nil {
		exitf("create output directory: %v", err)
	}

	absSampleAudio, err := filepath.Abs(strings.TrimSpace(sampleAudio))
	if err != nil {
		exitf("resolve sample audio path: %v", err)
	}
	if _, err := os.Stat(absSampleAudio); err != nil {
		exitf("sample audio is not accessible: %v", err)
	}

	whisperModels := normalizeCSV(whisperModelsRaw)
	if includeWhisperX && len(whisperModels) == 0 {
		exitf("at least one Whisper model is required when --include-whisperx is enabled")
	}
	if includeTitaNet && !includeParakeet {
		exitf("--include-titanet requires --include-parakeet because it uses the shared NVIDIA runtime")
	}

	configureRuntimeCacheEnv(absOutput)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	if includeWhisperX {
		if err := warmWhisperX(ctx, absOutput, absSampleAudio, whisperModels); err != nil {
			exitf("prepare WhisperX runtime: %v", err)
		}
	}

	nvidiaEnvPath := filepath.Join(absOutput, "parakeet")
	if includeParakeet {
		if err := warmParakeet(ctx, nvidiaEnvPath); err != nil {
			exitf("prepare Parakeet runtime: %v", err)
		}
	}
	if includeSortformer {
		if err := warmSortformer(ctx, nvidiaEnvPath); err != nil {
			exitf("prepare Sortformer runtime: %v", err)
		}
	}
	if includeCanary {
		if err := warmCanary(ctx, nvidiaEnvPath); err != nil {
			exitf("prepare Canary runtime: %v", err)
		}
	}
	if includeTitaNet {
		if err := prefetchTitaNet(ctx, nvidiaEnvPath); err != nil {
			exitf("prepare TitaNet cache: %v", err)
		}
	}

	manifest := runtimeSeedManifest{
		Version:            1,
		SeedID:             buildSeedID(whisperModels, includeWhisperX, includeParakeet, includeSortformer, includeCanary, includeTitaNet),
		PreparedAt:         time.Now().UTC().Format(time.RFC3339),
		PyTorchCUDA:        adapters.GetPyTorchCUDAVersion(),
		WhisperModels:      whisperModels,
		IncludesWhisper:    includeWhisperX,
		IncludesParakeet:   includeParakeet,
		IncludesSortformer: includeSortformer,
		IncludesCanary:     includeCanary,
		IncludesTitaNet:    includeTitaNet,
	}
	if err := writeManifest(absOutput, manifest); err != nil {
		exitf("write runtime seed manifest: %v", err)
	}
}

func warmWhisperX(ctx context.Context, envRoot string, sampleAudio string, models []string) error {
	adapter := adapters.NewWhisperXAdapter(envRoot)
	if err := adapter.PrepareEnvironment(ctx); err != nil {
		return err
	}

	audioInfo, err := os.Stat(sampleAudio)
	if err != nil {
		return err
	}
	tempRoot := filepath.Join(envRoot, ".seed-temp")
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)

	audioInput := interfaces.AudioInput{
		FilePath: sampleAudio,
		Format:   strings.TrimPrefix(strings.ToLower(filepath.Ext(sampleAudio)), "."),
		Size:     audioInfo.Size(),
	}
	procCtx := interfaces.ProcessingContext{
		JobID:         "desktop-runtime-seed",
		TempDirectory: tempRoot,
	}

	for _, model := range models {
		params := map[string]interface{}{
			"model":        model,
			"device":       "cpu",
			"batch_size":   1,
			"compute_type": "float32",
			"language":     "en",
			"diarize":      false,
			"vad_method":   "silero",
			"task":         "transcribe",
			"best_of":      1,
			"beam_size":    1,
			"temperature":  0.0,
			"patience":     1.0,
		}
		if _, err := adapter.Transcribe(ctx, audioInput, params, procCtx); err != nil {
			return fmt.Errorf("prefetch whisper model %q: %w", model, err)
		}
	}
	return nil
}

func warmParakeet(ctx context.Context, envPath string) error {
	return adapters.NewParakeetAdapter(envPath).PrepareEnvironment(ctx)
}

func warmSortformer(ctx context.Context, envPath string) error {
	return adapters.NewSortformerAdapter(envPath).PrepareEnvironment(ctx)
}

func warmCanary(ctx context.Context, envPath string) error {
	return adapters.NewCanaryAdapter(envPath).PrepareEnvironment(ctx)
}

func prefetchTitaNet(ctx context.Context, envPath string) error {
	cmd := exec.CommandContext(
		ctx,
		binaries.UV(),
		"run", "--native-tls", "--project", envPath,
		"python", "-c",
		"from nemo.collections.asr.models import EncDecSpeakerLabelModel; EncDecSpeakerLabelModel.from_pretrained(model_name='titanet_large')",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uv run prefetch failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func configureRuntimeCacheEnv(envRoot string) {
	cacheRoot := filepath.Join(envRoot, "cache")
	setEnv("HF_HOME", filepath.Join(cacheRoot, "huggingface"))
	setEnv("HUGGINGFACE_HUB_CACHE", filepath.Join(cacheRoot, "huggingface", "hub"))
	setEnv("XDG_CACHE_HOME", filepath.Join(cacheRoot, "xdg"))
	setEnv("TORCH_HOME", filepath.Join(cacheRoot, "torch"))
	setEnv("NEMO_HOME", filepath.Join(cacheRoot, "nemo"))
	setEnv("HF_HUB_DISABLE_SYMLINKS_WARNING", "1")
}

func setEnv(key string, value string) {
	_ = os.Setenv(key, value)
}

func writeManifest(root string, manifest runtimeSeedManifest) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, runtimeSeedManifestName), payload, 0o644)
}

func buildSeedID(whisperModels []string, includeWhisperX bool, includeParakeet bool, includeSortformer bool, includeCanary bool, includeTitaNet bool) string {
	parts := []string{
		"v1",
		"cuda=" + adapters.GetPyTorchCUDAVersion(),
		fmt.Sprintf("whisperx=%t", includeWhisperX),
		"whisper-models=" + strings.Join(whisperModels, ","),
		fmt.Sprintf("parakeet=%t", includeParakeet),
		fmt.Sprintf("sortformer=%t", includeSortformer),
		fmt.Sprintf("canary=%t", includeCanary),
		fmt.Sprintf("titanet=%t", includeTitaNet),
	}
	return strings.Join(parts, ";")
}

func normalizeCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	models := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		models = append(models, trimmed)
	}
	return models
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
