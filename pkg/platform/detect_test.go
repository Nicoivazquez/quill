package platform

import (
	"runtime"
	"testing"
)

func TestDetect_ReturnsConsistentValues(t *testing.T) {
	info := Detect()

	if info.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", info.OS, runtime.GOOS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", info.Arch, runtime.GOARCH)
	}

	expected := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	if info.AppleSilicon != expected {
		t.Errorf("AppleSilicon = %v, want %v", info.AppleSilicon, expected)
	}
}

func TestDetect_IsCached(t *testing.T) {
	a := Detect()
	b := Detect()
	if a != b {
		t.Error("Detect() returned different values on repeated calls")
	}
}

func TestBestTranscriptionFamily(t *testing.T) {
	family := BestTranscriptionFamily()
	switch {
	case family == "mlx_whisper":
		if !Detect().AppleSilicon {
			t.Error("mlx_whisper returned on non-Apple-Silicon platform")
		}
	case family == "whisper":
		if Detect().AppleSilicon {
			t.Error("whisper returned on Apple Silicon — should prefer mlx_whisper")
		}
	case family == "whisper_cpp":
		// acceptable fallback
	default:
		t.Errorf("unexpected family %q", family)
	}
}
