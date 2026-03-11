package adapters

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"quill/pkg/binaries"
	"quill/pkg/logger"
)

const nvidiaASRImportStatement = "import nemo.collections.asr"

func sharedNVIDIAEnvKey(envPath string) string {
	return "nvidia-env:" + envPath
}

// PrepareSharedNVIDIAEnv installs the shared NeMo runtime used by local NVIDIA-based features.
func PrepareSharedNVIDIAEnv(ctx context.Context, envPath string) error {
	return RunPrepareOnce(sharedNVIDIAEnvKey(envPath), func() error {
		if CheckEnvironmentReady(envPath, nvidiaASRImportStatement) {
			return nil
		}

		if err := os.MkdirAll(envPath, 0o755); err != nil {
			return fmt.Errorf("failed to create NVIDIA runtime directory: %w", err)
		}

		pyprojectContent, err := nvidiaScripts.ReadFile("py/nvidia/pyproject.toml")
		if err != nil {
			return fmt.Errorf("failed to read embedded pyproject.toml: %w", err)
		}

		contentStr := strings.Replace(
			string(pyprojectContent),
			"https://download.pytorch.org/whl/cu126",
			GetPyTorchWheelURL(),
			1,
		)

		pyprojectPath := filepath.Join(envPath, "pyproject.toml")
		if err := os.WriteFile(pyprojectPath, []byte(contentStr), 0o644); err != nil {
			return fmt.Errorf("failed to write pyproject.toml: %w", err)
		}

		logger.Info("Installing shared NVIDIA speech runtime", "env_path", envPath)
		cmd := exec.CommandContext(ctx, binaries.UV(), "sync", "--native-tls")
		cmd.Dir = envPath
		cmd.Env = append(os.Environ(), "UV_PYTHON=3.11")

		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("uv sync failed: %w: %s", err, strings.TrimSpace(string(output)))
		}

		return nil
	})
}
