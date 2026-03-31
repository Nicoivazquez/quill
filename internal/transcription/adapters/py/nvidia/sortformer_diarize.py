#!/usr/bin/env python3
"""
NVIDIA Sortformer speaker diarization script.
Uses diar_streaming_sortformer_4spk-v2.1 for optimized up-to-4-speaker diarization.
"""

import argparse
import json
import sys
import os
import tempfile
from pathlib import Path
import torch

try:
    from nemo.collections.asr.models import SortformerEncLabelModel
except ImportError:
    print("Error: NeMo not found. Please install nemo_toolkit[asr]")
    sys.exit(1)


def diarize_audio(
    audio_path: str,
    output_file: str,
    batch_size: int = 1,
    device: str = None,
    max_speakers: int = 4,
    output_format: str = "rttm",
    streaming_mode: bool = False,
    chunk_length_s: float = 30.0,
):
    """
    Perform speaker diarization using NVIDIA's Sortformer model.
    """
    if device is None or device == "auto":
        if torch.cuda.is_available():
            device = "cuda"

        else:
            device = "cpu"

    print(f"Using device: {device}")
    print(f"Loading NVIDIA Sortformer diarization model...")

    # Determine model path
    model_filename = "diar_streaming_sortformer_4spk-v2.1.nemo"
    model_path = None

    # Locate project root: derived from VIRTUAL_ENV, which is set by `uv run` to path/.venv
    virtual_env = os.environ.get("VIRTUAL_ENV")
    if not virtual_env:
        print("Error: VIRTUAL_ENV environment variable not set. Script must be run with 'uv run'.")
        sys.exit(1)

    project_root = os.path.dirname(virtual_env)
    model_path = os.path.join(project_root, model_filename)

    try:
        if not os.path.exists(model_path):
            print(f"Error: Model file not found: {model_filename} in project root: {project_root}")
            sys.exit(1)

        # Load from local file
        print(f"Loading model from path: {model_path}")
        diar_model = SortformerEncLabelModel.restore_from(
            restore_path=model_path,
            map_location=device,
            strict=False,
        )

        # Switch to inference mode
        diar_model.eval()
        print("Model loaded successfully")

    except Exception as e:
        print(f"Error loading model: {e}")
        sys.exit(1)

    print(f"Processing audio file: {audio_path}")

    # Verify audio file exists
    if not os.path.exists(audio_path):
        print(f"Error: Audio file not found: {audio_path}")
        sys.exit(1)

    try:
        # Run diarization
        print(f"Running diarization with batch_size={batch_size}, max_speakers={max_speakers}")

        # The Sortformer 4spk model always has 4 output heads (hardcoded in the
        # neural network architecture). It cannot be told "only output N speakers"
        # at the model level. Instead, NeMo uses onset/offset thresholds during
        # post-processing to decide whether a speaker slot is active.
        #
        # NVIDIA recommends onset=0.64, offset=0.74 for Sortformer (optimized on
        # DIHARD3). The NeMo defaults of 0.5/0.5 are too permissive and produce
        # ghost speakers. We ALWAYS pass tuned thresholds — higher when fewer
        # speakers are expected.
        postprocessing_yaml_path = _create_postprocessing_yaml(max_speakers)
        print(f"Using tuned postprocessing thresholds for max_speakers={max_speakers}")

        diarize_kwargs = {
            "audio": audio_path,
            "batch_size": batch_size,
            "postprocessing_yaml": postprocessing_yaml_path,
        }

        predicted_segments = diar_model.diarize(**diarize_kwargs)

        # Clean up temp yaml
        if postprocessing_yaml_path and os.path.exists(postprocessing_yaml_path):
            os.unlink(postprocessing_yaml_path)

        print(f"Diarization completed. Found segments: {len(predicted_segments)}")

        # Process and save results — pass max_speakers as a safety net in case
        # the threshold tuning wasn't aggressive enough for this particular audio.
        save_results(predicted_segments, output_file, audio_path, output_format, max_speakers)

    except Exception as e:
        print(f"Error during diarization: {e}")
        sys.exit(1)


def _create_postprocessing_yaml(max_speakers: int) -> str:
    """
    Create a temporary postprocessing YAML with tuned onset/offset thresholds.

    The Sortformer model outputs sigmoid probabilities for each of its 4 speaker
    heads. The onset threshold controls how high the probability must be before
    a speaker is considered "active". By raising it when fewer speakers are
    expected, we make the model more conservative and suppress weak/spurious
    speaker activations.

    Default NeMo thresholds: onset=0.5, offset=0.5
    For fewer speakers we raise both, plus increase min_duration_on to filter
    out very short false activations.
    """
    # NVIDIA-recommended baseline for Sortformer: onset=0.64, offset=0.74
    # (optimized on DIHARD3). We raise further when fewer speakers are expected.
    if max_speakers <= 1:
        onset, offset = 0.80, 0.80
        min_duration_on = 0.3
    elif max_speakers == 2:
        onset, offset = 0.70, 0.75
        min_duration_on = 0.2
    elif max_speakers == 3:
        onset, offset = 0.65, 0.72
        min_duration_on = 0.15
    else:  # max_speakers == 4 (default) — use NVIDIA baseline
        onset, offset = 0.64, 0.74
        min_duration_on = 0.1

    yaml_content = (
        f"parameters:\n"
        f"  onset: {onset}\n"
        f"  offset: {offset}\n"
        f"  pad_onset: 0.06\n"
        f"  pad_offset: 0.0\n"
        f"  min_duration_on: {min_duration_on}\n"
        f"  min_duration_off: 0.15\n"
    )

    fd, path = tempfile.mkstemp(suffix=".yaml", prefix="sortformer_pp_")
    with os.fdopen(fd, "w") as f:
        f.write(yaml_content)
    print(f"Post-processing config: onset={onset}, offset={offset}, min_duration_on={min_duration_on}")
    return path


def filter_ghost_speakers(segments, min_activity_ratio=0.05, min_absolute_seconds=1.0):
    """
    Remove ghost speakers with negligible activity (always-on filter).

    The Sortformer 4spk model always outputs 4 speaker heads. When audio has
    fewer actual speakers, unused heads produce ghost speakers with near-zero
    activity. This filter removes them based on two thresholds — a speaker must
    pass BOTH to be kept:

      1. Relative: total speaking time >= min_activity_ratio of total audio duration
      2. Absolute: total speaking time >= min_absolute_seconds

    Ghost segments are reassigned to the nearest remaining speaker by temporal
    proximity (same strategy as limit_speakers). At least one speaker is always
    kept.

    Based on NVIDIA recommendation (5-10% activity threshold) and community
    best practices:
      - NVIDIA: onset=0.64/offset=0.74 + filter speakers with <5-10% activity
      - NeMo has no built-in total-duration filter (only per-frame onset threshold)
      - pyannote uses cluster -2 for zero-activity speakers
      - diart uses tau_active=0.5-0.62 per-chunk
      - Application-level filtering at 0.5-1.0s is the standard approach
    """
    if not segments:
        return segments

    # Calculate total speaking time per speaker
    speaker_time = {}
    for seg in segments:
        spk = seg["speaker"]
        speaker_time[spk] = speaker_time.get(spk, 0.0) + seg["duration"]

    if len(speaker_time) <= 1:
        return segments

    # Total audio duration (from earliest start to latest end)
    total_duration = max(seg["end"] for seg in segments) - min(seg["start"] for seg in segments)
    if total_duration <= 0:
        return segments

    # Determine which speakers to keep: must pass BOTH thresholds
    sorted_speakers = sorted(speaker_time.items(), key=lambda x: x[1], reverse=True)
    kept_speakers = set()
    ghost_speakers = set()

    for spk, duration in sorted_speakers:
        passes_ratio = duration >= (total_duration * min_activity_ratio)
        passes_absolute = duration >= min_absolute_seconds
        if passes_ratio and passes_absolute:
            kept_speakers.add(spk)
        else:
            ghost_speakers.add(spk)

    # Always keep at least the most active speaker
    if not kept_speakers:
        kept_speakers.add(sorted_speakers[0][0])
        ghost_speakers.discard(sorted_speakers[0][0])

    # Nothing to filter
    if not ghost_speakers:
        return segments

    print(f"Filtering ghost speakers: keeping {sorted(kept_speakers)}, "
          f"removing {sorted(ghost_speakers)}")
    for spk in ghost_speakers:
        print(f"  {spk}: {speaker_time[spk]:.2f}s "
              f"({speaker_time[spk]/total_duration*100:.1f}% of {total_duration:.1f}s)")

    # Reassign ghost segments to nearest kept speaker (by temporal proximity)
    kept_segments = [s for s in segments if s["speaker"] in kept_speakers]

    result = []
    for seg in segments:
        if seg["speaker"] in kept_speakers:
            result.append(seg)
        else:
            seg_mid = (seg["start"] + seg["end"]) / 2.0
            best_speaker = None
            best_dist = float("inf")
            for ks in kept_segments:
                ks_mid = (ks["start"] + ks["end"]) / 2.0
                dist = abs(seg_mid - ks_mid)
                if dist < best_dist:
                    best_dist = dist
                    best_speaker = ks["speaker"]
            if best_speaker is None:
                best_speaker = sorted_speakers[0][0]
            seg["speaker"] = best_speaker
            result.append(seg)

    # Merge adjacent segments with the same speaker
    result.sort(key=lambda x: x["start"])
    merged = []
    for seg in result:
        if merged and merged[-1]["speaker"] == seg["speaker"] and seg["start"] - merged[-1]["end"] < 0.1:
            merged[-1]["end"] = seg["end"]
            merged[-1]["duration"] = merged[-1]["end"] - merged[-1]["start"]
        else:
            merged.append(seg)

    return merged


def limit_speakers(segments, max_speakers):
    """
    Limit the number of speakers by merging excess speakers into the most active ones.

    Strategy: keep the top `max_speakers` speakers by total speaking time, reassign
    dropped speakers' segments to the kept speaker with the closest temporal overlap.
    """
    if not segments or max_speakers < 1:
        return segments

    # Collect unique speakers and their total speaking time
    speaker_time = {}
    for seg in segments:
        spk = seg["speaker"]
        speaker_time[spk] = speaker_time.get(spk, 0.0) + seg["duration"]

    if len(speaker_time) <= max_speakers:
        return segments

    # Keep the top max_speakers speakers by total speaking time
    sorted_speakers = sorted(speaker_time.items(), key=lambda x: x[1], reverse=True)
    kept_speakers = {spk for spk, _ in sorted_speakers[:max_speakers]}
    dropped_speakers = {spk for spk, _ in sorted_speakers[max_speakers:]}

    print(f"Limiting speakers from {len(speaker_time)} to {max_speakers}")
    print(f"Keeping: {sorted(kept_speakers)}, dropping: {sorted(dropped_speakers)}")

    # Build a list of kept segments for temporal proximity lookup
    kept_segments = [s for s in segments if s["speaker"] in kept_speakers]

    # Reassign dropped speaker segments to the closest kept speaker
    result = []
    for seg in segments:
        if seg["speaker"] in kept_speakers:
            result.append(seg)
        else:
            # Find the kept speaker with the closest segment in time
            seg_mid = (seg["start"] + seg["end"]) / 2.0
            best_speaker = None
            best_dist = float("inf")
            for ks in kept_segments:
                ks_mid = (ks["start"] + ks["end"]) / 2.0
                dist = abs(seg_mid - ks_mid)
                if dist < best_dist:
                    best_dist = dist
                    best_speaker = ks["speaker"]
            if best_speaker is None:
                # Fallback: assign to the most active kept speaker
                best_speaker = sorted_speakers[0][0]
            seg["speaker"] = best_speaker
            result.append(seg)

    # Merge adjacent segments with the same speaker
    result.sort(key=lambda x: x["start"])
    merged = []
    for seg in result:
        if merged and merged[-1]["speaker"] == seg["speaker"] and seg["start"] - merged[-1]["end"] < 0.1:
            merged[-1]["end"] = seg["end"]
            merged[-1]["duration"] = merged[-1]["end"] - merged[-1]["start"]
        else:
            merged.append(seg)

    return merged


def save_results(segments, output_file: str, audio_path: str, output_format: str, max_speakers: int = 4):
    """
    Save diarization results to output file.
    Supports both JSON and RTTM formats based on output_format parameter.
    """
    output_path = Path(output_file)

    if output_format == "rttm":
        save_rttm_format(segments, output_file, audio_path, max_speakers)
    else:
        save_json_format(segments, output_file, audio_path, max_speakers)


def save_json_format(segments, output_file: str, audio_path: str, max_speakers: int = 4):
    """Save results in JSON format."""
    results = {
        "audio_file": audio_path,
        "model": "nvidia/diar_streaming_sortformer_4spk-v2.1",
        "segments": [],
    }

    # Handle the case where segments is a list containing a single list of string entries
    if len(segments) == 1 and isinstance(segments[0], list):
        segments = segments[0]

    # Convert segments to JSON format
    speakers = set()
    for i, segment in enumerate(segments):
        try:
            # Handle different possible segment formats
            if isinstance(segment, str):
                # String format: "start end speaker_id"
                parts = segment.strip().split()
                if len(parts) >= 3:
                    segment_data = {
                        "start": float(parts[0]),
                        "end": float(parts[1]),
                        "speaker": str(parts[2]),
                        "duration": float(parts[1]) - float(parts[0]),
                        "confidence": 1.0,
                    }
                else:
                    print(f"Warning: Invalid string segment format: {segment}")
                    continue
            elif hasattr(segment, 'start') and hasattr(segment, 'end') and hasattr(segment, 'label'):
                # Standard pyannote-like format
                segment_data = {
                    "start": float(segment.start),
                    "end": float(segment.end),
                    "speaker": str(segment.label),
                    "duration": float(segment.end - segment.start),
                    "confidence": getattr(segment, 'confidence', 1.0),
                }
            elif isinstance(segment, (list, tuple)) and len(segment) >= 3:
                # List/tuple format: [start, end, speaker]
                segment_data = {
                    "start": float(segment[0]),
                    "end": float(segment[1]),
                    "speaker": str(segment[2]),
                    "duration": float(segment[1] - segment[0]),
                    "confidence": 1.0,
                }
            elif isinstance(segment, dict):
                # Dictionary format
                segment_data = {
                    "start": float(segment.get('start', 0)),
                    "end": float(segment.get('end', 0)),
                    "speaker": str(segment.get('speaker', segment.get('label', f'speaker_{i}'))),
                    "duration": float(segment.get('end', 0) - segment.get('start', 0)),
                    "confidence": float(segment.get('confidence', 1.0)),
                }
            else:
                # Fallback: try to extract attributes dynamically
                segment_data = {
                    "start": float(getattr(segment, 'start', 0)),
                    "end": float(getattr(segment, 'end', 0)),
                    "speaker": str(getattr(segment, 'label', getattr(segment, 'speaker', f'speaker_{i}'))),
                    "duration": float(getattr(segment, 'end', 0) - getattr(segment, 'start', 0)),
                    "confidence": float(getattr(segment, 'confidence', 1.0)),
                }

            results["segments"].append(segment_data)
            speakers.add(segment_data["speaker"])

        except Exception as e:
            print(f"Warning: Could not process segment {i}: {e}")
            print(f"Segment: {segment}")

    # Sort by start time
    if results["segments"]:
        results["segments"].sort(key=lambda x: x["start"])

    # Filter ghost speakers (always-on, runs before max_speakers limit)
    if results["segments"]:
        results["segments"] = filter_ghost_speakers(results["segments"])
        speakers = {seg["speaker"] for seg in results["segments"]}

    # Enforce max_speakers limit by merging excess speakers
    if len(speakers) > max_speakers and results["segments"]:
        results["segments"] = limit_speakers(results["segments"], max_speakers)
        speakers = {seg["speaker"] for seg in results["segments"]}

    # Add summary statistics
    results["speakers"] = sorted(speakers)
    results["speaker_count"] = len(speakers)
    results["total_segments"] = len(results["segments"])
    results["total_duration"] = max(seg["end"] for seg in results["segments"]) if results["segments"] else 0

    with open(output_file, "w") as f:
        json.dump(results, f, indent=2)

    print(f"Results saved to: {output_file}")
    print(f"Found {len(speakers)} speakers: {', '.join(sorted(speakers))}")


def save_rttm_format(segments, output_file: str, audio_path: str, max_speakers: int = 4):
    """Save results in RTTM (Rich Transcription Time Marked) format."""
    audio_filename = Path(audio_path).stem
    speakers = set()

    # Handle the case where segments is a list containing a single list of string entries
    if len(segments) == 1 and isinstance(segments[0], list):
        segments = segments[0]

    # First pass: parse all segments into dicts for potential speaker limiting
    parsed_segments = []
    for i, segment in enumerate(segments):
        try:
            if isinstance(segment, str):
                parts = segment.strip().split()
                if len(parts) >= 3:
                    start = float(parts[0])
                    end = float(parts[1])
                    speaker = str(parts[2])
                else:
                    print(f"Warning: Invalid string segment format: {segment}")
                    continue
            elif hasattr(segment, 'start') and hasattr(segment, 'end') and hasattr(segment, 'label'):
                start = float(segment.start)
                end = float(segment.end)
                speaker = str(segment.label)
            elif isinstance(segment, (list, tuple)) and len(segment) >= 3:
                start = float(segment[0])
                end = float(segment[1])
                speaker = str(segment[2])
            elif isinstance(segment, dict):
                start = float(segment.get('start', 0))
                end = float(segment.get('end', 0))
                speaker = str(segment.get('speaker', segment.get('label', f'speaker_{i}')))
            else:
                start = float(getattr(segment, 'start', 0))
                end = float(getattr(segment, 'end', 0))
                speaker = str(getattr(segment, 'label', getattr(segment, 'speaker', f'speaker_{i}')))

            parsed_segments.append({
                "start": start,
                "end": end,
                "speaker": speaker,
                "duration": end - start,
                "confidence": 1.0,
            })
            speakers.add(speaker)

        except Exception as e:
            print(f"Warning: Could not process segment {i} for RTTM: {e}")
            print(f"Segment: {segment}")

    # Filter ghost speakers (always-on, runs before max_speakers limit)
    if parsed_segments:
        parsed_segments = filter_ghost_speakers(parsed_segments)
        speakers = {seg["speaker"] for seg in parsed_segments}

    # Enforce max_speakers limit
    if len(speakers) > max_speakers and parsed_segments:
        parsed_segments = limit_speakers(parsed_segments, max_speakers)
        speakers = {seg["speaker"] for seg in parsed_segments}

    # Write RTTM output
    with open(output_file, "w") as f:
        for seg in parsed_segments:
            line = f"SPEAKER {audio_filename} 1 {seg['start']:.3f} {seg['duration']:.3f} <NA> <NA> {seg['speaker']} <NA> <NA>\n"
            f.write(line)

    print(f"RTTM results saved to: {output_file}")
    print(f"Found {len(speakers)} speakers: {', '.join(sorted(speakers))}")


def main():
    parser = argparse.ArgumentParser(
        description="Speaker diarization using NVIDIA Sortformer model (local model only)",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
    # Basic diarization with JSON output
    python sortformer_diarize.py samples/sample.wav output.json

    # Generate RTTM format output
    python sortformer_diarize.py samples/sample.wav output.rttm

    # Specify device and batch size
    python sortformer_diarize.py --device cuda --batch-size 2 samples/sample.wav output.json

Note: This script requires diar_streaming_sortformer_4spk-v2.1.nemo to be in the same directory.
        """,
    )

    parser.add_argument("audio_file", help="Path to input audio file (WAV, FLAC, etc.)")
    parser.add_argument("output_file", help="Path to output file (.json for JSON format, .rttm for RTTM format)")
    parser.add_argument("--batch-size", type=int, default=1, help="Batch size for processing (default: 1)")
    parser.add_argument("--device", choices=["cuda", "cpu", "auto"], default="auto", help="Device to use for inference (default: auto-detect)")
    parser.add_argument("--max-speakers", type=int, default=4, help="Maximum number of speakers (default: 4, optimized for this model)")
    parser.add_argument("--output-format", choices=["json", "rttm"], help="Output format (auto-detected from file extension if not specified)")
    parser.add_argument("--streaming", action="store_true", help="Enable streaming mode")
    parser.add_argument("--chunk-length-s", type=float, default=30.0, help="Chunk length in seconds for streaming mode (default: 30.0)")

    args = parser.parse_args()

    # Validate inputs
    if not os.path.exists(args.audio_file):
        print(f"Error: Audio file not found: {args.audio_file}")
        sys.exit(1)

    # Auto-detect output format from file extension if not specified
    if args.output_format is None:
        if args.output_file.lower().endswith('.rttm'):
            output_format = "rttm"
        else:
            output_format = "json"
    else:
        output_format = args.output_format

    # Create output directory if it doesn't exist
    output_dir = Path(args.output_file).parent
    output_dir.mkdir(parents=True, exist_ok=True)

    device = None if args.device == "auto" else args.device

    # Run diarization
    diarize_audio(
        audio_path=args.audio_file,
        output_file=args.output_file,
        batch_size=args.batch_size,
        device=device,
        max_speakers=args.max_speakers,
        output_format=output_format,
        streaming_mode=args.streaming,
        chunk_length_s=args.chunk_length_s,
    )


if __name__ == "__main__":
    main()
