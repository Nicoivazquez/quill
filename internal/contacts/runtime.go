package contacts

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"quill/internal/transcription/adapters"
	"quill/pkg/binaries"
	"quill/pkg/logger"
)

const defaultTitaNetModel = "titanet_large"

// PrepareTitaNetRuntime ensures the shared NeMo runtime and the default TitaNet model are available.
func PrepareTitaNetRuntime(ctx context.Context, nvidiaEnvPath string) error {
	envPath := filepath.Clean(strings.TrimSpace(nvidiaEnvPath))
	if envPath == "" || envPath == "." {
		return fmt.Errorf("voice-signature runtime path is not configured")
	}

	if err := adapters.PrepareSharedNVIDIAEnv(ctx, envPath); err != nil {
		return fmt.Errorf("failed to prepare voice-signature runtime: %w", err)
	}

	warmKey := fmt.Sprintf("titanet-model:%s:%s", envPath, defaultTitaNetModel)
	if adapters.IsModelWarm(warmKey) {
		return nil
	}

	return adapters.RunModelWarmOnce(warmKey, func() error {
		if adapters.IsModelWarm(warmKey) {
			return nil
		}

		logger.Info("Warming TitaNet voice-signature model", "model", defaultTitaNetModel)
		cmd := exec.CommandContext(
			ctx,
			binaries.UV(),
			"run", "--native-tls", "--project", envPath,
			"python", "-c",
			"from nemo.collections.asr.models import EncDecSpeakerLabelModel; EncDecSpeakerLabelModel.from_pretrained(model_name='titanet_large')",
		)

		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to warm TitaNet model: %s", strings.TrimSpace(string(output)))
		}

		adapters.MarkModelWarm(warmKey)
		logger.Info("TitaNet voice-signature model ready")
		return nil
	})
}
