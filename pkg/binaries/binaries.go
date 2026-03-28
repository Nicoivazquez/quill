package binaries

import "os"

func resolve(primaryEnvKey, fallback string) string {
	if value := os.Getenv(primaryEnvKey); value != "" {
		return value
	}
	return fallback
}

// UV returns the configured uv executable path.
func UV() string {
	return resolve("QUILL_UV_BIN", "uv")
}

// FFmpeg returns the configured ffmpeg executable path.
func FFmpeg() string {
	return resolve("QUILL_FFMPEG_BIN", "ffmpeg")
}

// FFprobe returns the configured ffprobe executable path.
func FFprobe() string {
	return resolve("QUILL_FFPROBE_BIN", "ffprobe")
}

// YtDLP returns the configured yt-dlp executable path.
func YtDLP() string {
	return resolve("QUILL_YTDLP_BIN", "yt-dlp")
}

// WhisperCpp returns the configured whisper.cpp executable path.
func WhisperCpp() string {
	return resolve("QUILL_WHISPER_CPP_BIN", "whisper-cpp")
}
