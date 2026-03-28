package platform

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Info holds detected platform characteristics.
type Info struct {
	OS           string // "darwin", "linux", "windows"
	Arch         string // "arm64", "amd64"
	AppleSilicon bool   // True on darwin/arm64 (M1–M4+)
	HasCUDA      bool   // True when nvidia-smi is reachable
}

var (
	once     sync.Once
	detected Info
)

// Detect returns cached platform information.
func Detect() Info {
	once.Do(func() {
		detected = Info{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		}
		detected.AppleSilicon = detected.OS == "darwin" && detected.Arch == "arm64"
		detected.HasCUDA = probeCUDA()
	})
	return detected
}

// BestTranscriptionFamily returns the recommended model family for the current platform.
// Priority: Apple Silicon → mlx_whisper, CUDA → whisper (WhisperX), otherwise → whisper_cpp.
func BestTranscriptionFamily() string {
	info := Detect()
	if info.AppleSilicon {
		return "mlx_whisper"
	}
	if info.HasCUDA {
		return "whisper" // WhisperX with GPU
	}
	return "whisper_cpp"
}

func probeCUDA() bool {
	out, err := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}
