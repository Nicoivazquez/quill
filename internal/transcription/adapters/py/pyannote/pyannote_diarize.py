#!/usr/bin/env python3
"""
PyAnnote speaker diarization script.
Processes audio files to identify and separate different speakers.
"""

import argparse
import json
import sys
import os
import functools
from pathlib import Path

# ---------------------------------------------------------------------------
# Compatibility shim: older pyannote.audio internally passes the deprecated
# `use_auth_token` kwarg to huggingface_hub functions.  Newer huggingface_hub
# (>=0.24) removed it entirely, causing a TypeError.  We monkey-patch BEFORE
# importing pyannote so its `from huggingface_hub import ...` picks up the
# wrapped versions.  We also patch submodules (e.g. huggingface_hub.file_download)
# to catch imports that bypass the top-level namespace.
# ---------------------------------------------------------------------------
import huggingface_hub as _hfh

def _strip_use_auth_token(fn):
    """Wrap a huggingface_hub function to silently drop `use_auth_token`."""
    @functools.wraps(fn)
    def _wrapper(*args, **kwargs):
        kwargs.pop("use_auth_token", None)
        return fn(*args, **kwargs)
    return _wrapper

_FUNCS_TO_PATCH = ("hf_hub_download", "snapshot_download", "cached_download",
                   "model_info", "repo_info", "list_repo_files")

# Patch top-level huggingface_hub namespace
for _name in _FUNCS_TO_PATCH:
    if hasattr(_hfh, _name):
        setattr(_hfh, _name, _strip_use_auth_token(getattr(_hfh, _name)))

# Also patch submodules that pyannote may import directly (e.g.
# `from huggingface_hub.file_download import hf_hub_download`).
import importlib as _imp
for _submod_name in ("file_download", "_hf_api", "hub_mixin", "utils"):
    try:
        _submod = _imp.import_module(f"huggingface_hub.{_submod_name}")
        for _name in _FUNCS_TO_PATCH:
            if hasattr(_submod, _name):
                setattr(_submod, _name, _strip_use_auth_token(getattr(_submod, _name)))
    except (ImportError, ModuleNotFoundError):
        pass
# ---------------------------------------------------------------------------

from pyannote.audio import Pipeline
import torch

# Fix for PyTorch 2.6+ which defaults weights_only=True
# We need to allowlist PyAnnote's custom classes
try:
    from pyannote.audio.core.task import Specifications, Problem, Resolution
    if hasattr(torch.serialization, "add_safe_globals"):
        torch.serialization.add_safe_globals([Specifications, Problem, Resolution])
except ImportError:
    pass
except Exception as e:
    print(f"Warning: Could not add safe globals: {e}")


def diarize_audio(
    audio_path: str,
    output_file: str,
    hf_token: str,
    model: str = "pyannote/speaker-diarization-community-1",
    min_speakers: int = None,
    max_speakers: int = None,
    output_format: str = "rttm",
    device: str = "auto",
):
    """
    Perform speaker diarization on audio file using PyAnnote.
    """
    print(f"Loading PyAnnote speaker diarization pipeline: {model}")

    try:
        # Set the token in the environment so huggingface_hub picks it up
        # automatically — avoids version-specific kwarg issues with
        # Pipeline.from_pretrained().
        os.environ["HF_TOKEN"] = hf_token

        pipeline = Pipeline.from_pretrained(model)

        # Move to specified device
        # if device == "auto" or device == "cuda":
        try:
            if torch.cuda.is_available():
                pipeline = pipeline.to(torch.device("cuda"))
                print("Using CUDA for diarization")
            elif device == "cuda":
                print("CUDA requested but not available, falling back to CPU")
            else:
                print("CUDA not available, using CPU")
        except ImportError:
            print("PyTorch not available for CUDA, using CPU")
        except Exception as e:
            print(f"Error moving to device: {e}, using CPU")

        print("Pipeline loaded successfully")
    except Exception as e:
        print(f"Error loading pipeline: {e}")
        print("Make sure you have a valid Hugging Face token and have accepted the model's license")
        sys.exit(1)

    print(f"Processing audio file: {audio_path}")

    try:
        # Run diarization
        diarization_params = {}
        if min_speakers is not None:
            diarization_params["min_speakers"] = min_speakers
        if max_speakers is not None:
            diarization_params["max_speakers"] = max_speakers

        if diarization_params:
            print(f"Using speaker constraints: {diarization_params}")
            diarization = pipeline(audio_path, **diarization_params)
        else:
            print("Using automatic speaker detection")
            diarization = pipeline(audio_path)

        print(f"Diarization completed. Saving results to: {output_file}")

        if output_format == "rttm":
            # Save the diarization output to RTTM format
            with open(output_file, "w") as rttm:
                diarization.write_rttm(rttm)
        else:
            # Save as JSON format
            save_json_format(diarization, output_file, audio_path)

        # Print summary
        speakers = set()
        total_speech_time = 0.0

        # Iterate over speaker diarization
        # PyAnnote 4.x returns a DiarizeOutput object with a speaker_diarization attribute
        if hasattr(diarization, "speaker_diarization"):
            for turn, speaker in diarization.speaker_diarization:
                speakers.add(speaker)
                total_speech_time += turn.duration
        elif hasattr(diarization, "itertracks"):
            # Fallback for older versions
            for segment, track, speaker in diarization.itertracks(yield_label=True):
                speakers.add(speaker)
                total_speech_time += segment.duration
        else:
            # Try iterating directly (some versions return Annotation directly)
            for segment, track, speaker in diarization.itertracks(yield_label=True):
                speakers.add(speaker)
                total_speech_time += segment.duration

        print(f"\nDiarization Summary:")
        print(f"  Speakers detected: {len(speakers)}")
        print(f"  Speaker labels: {sorted(speakers)}")
        print(f"  Total speech time: {total_speech_time:.2f} seconds")
        print(f"  Output file saved: {output_file}")

    except Exception as e:
        print(f"Error during diarization: {e}")
        sys.exit(1)


def save_json_format(diarization, output_file: str, audio_path: str):
    """Save diarization results in JSON format."""
    segments = []
    speakers = set()

    # PyAnnote 4.x
    if hasattr(diarization, "speaker_diarization"):
        for turn, speaker in diarization.speaker_diarization:
            segments.append({
                "start": turn.start,
                "end": turn.end,
                "speaker": speaker,
                "confidence": 1.0,
                "duration": turn.duration
            })
            speakers.add(speaker)
    # Older versions
    elif hasattr(diarization, "itertracks"):
        for segment, track, speaker in diarization.itertracks(yield_label=True):
            segments.append({
                "start": segment.start,
                "end": segment.end,
                "speaker": speaker,
                "confidence": 1.0,
                "duration": segment.duration
            })
            speakers.add(speaker)

    # Sort segments by start time
    segments.sort(key=lambda x: x["start"])

    results = {
        "audio_file": audio_path,
        "model": "pyannote/speaker-diarization-community-1",
        "segments": segments,
        "speakers": sorted(speakers),
        "speaker_count": len(speakers),
        "total_duration": max(seg["end"] for seg in segments) if segments else 0,
        "processing_info": {
            "total_segments": len(segments),
            "total_speech_time": sum(seg["duration"] for seg in segments)
        }
    }

    with open(output_file, "w") as f:
        json.dump(results, f, indent=2)


def main():
    parser = argparse.ArgumentParser(
        description="Perform speaker diarization using PyAnnote.audio"
    )
    parser.add_argument(
        "audio_file",
        help="Path to audio file"
    )
    parser.add_argument(
        "--output", "-o",
        required=True,
        help="Output file path"
    )
    parser.add_argument(
        "--hf-token",
        default=os.environ.get("HF_TOKEN", ""),
        help="Hugging Face access token (defaults to HF_TOKEN env var)"
    )
    parser.add_argument(
        "--model",
        default="pyannote/speaker-diarization-community-1",
        help="PyAnnote model to use"
    )
    parser.add_argument(
        "--min-speakers",
        type=int,
        help="Minimum number of speakers"
    )
    parser.add_argument(
        "--max-speakers",
        type=int,
        help="Maximum number of speakers"
    )
    parser.add_argument(
        "--output-format",
        choices=["rttm", "json"],
        default="rttm",
        help="Output format"
    )
    parser.add_argument(
        "--device",
        choices=["cpu", "cuda", "auto"],
        default="auto",
        help="Device to use for computation"
    )
    args = parser.parse_args()

    # Validate input file
    if not os.path.exists(args.audio_file):
        print(f"Error: Audio file not found: {args.audio_file}")
        sys.exit(1)

    # Validate speaker constraints
    if args.min_speakers is not None and args.min_speakers < 1:
        print("Error: min_speakers must be at least 1")
        sys.exit(1)

    if args.max_speakers is not None and args.max_speakers < 1:
        print("Error: max_speakers must be at least 1")
        sys.exit(1)

    if (args.min_speakers is not None and args.max_speakers is not None and
        args.min_speakers > args.max_speakers):
        print("Error: min_speakers cannot be greater than max_speakers")
        sys.exit(1)

    # Create output directory if it doesn't exist
    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)

    try:
        diarize_audio(
            audio_path=args.audio_file,
            output_file=args.output,
            hf_token=args.hf_token,
            model=args.model,
            min_speakers=args.min_speakers,
            max_speakers=args.max_speakers,
            output_format=args.output_format,
            device=args.device,
        )
    except Exception as e:
        print(f"Error during diarization: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
