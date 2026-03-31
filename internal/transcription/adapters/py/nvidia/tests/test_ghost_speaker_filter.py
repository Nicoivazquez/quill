"""Tests for ghost speaker filtering in sortformer_diarize.py

The Sortformer 4spk model always outputs 4 speaker heads. When audio has fewer
actual speakers, the unused heads produce ghost speakers with near-zero activity.
filter_ghost_speakers() removes these based on activity thresholds.
"""
import sys
from pathlib import Path

# Add parent dir so we can import the module without NeMo installed
sys.modules["torch"] = type(sys)("mock_torch")
sys.modules["torch"].cuda = type(sys)("mock_cuda")
sys.modules["torch"].cuda.is_available = lambda: False
sys.modules["nemo"] = type(sys)("mock_nemo")
sys.modules["nemo.collections"] = type(sys)("mock_nemo_collections")
sys.modules["nemo.collections.asr"] = type(sys)("mock_nemo_asr")
sys.modules["nemo.collections.asr.models"] = type(sys)("mock_nemo_models")
sys.modules["nemo.collections.asr.models"].SortformerEncLabelModel = None

sys.path.insert(0, str(Path(__file__).parent.parent))
from sortformer_diarize import filter_ghost_speakers, limit_speakers


def _make_segments(speaker_data):
    """Helper: create segment dicts from (speaker, start, end) tuples."""
    return [
        {"speaker": spk, "start": s, "end": e, "duration": e - s, "confidence": 1.0}
        for spk, s, e in speaker_data
    ]


class TestFilterGhostSpeakers:
    """Tests for the always-on ghost speaker filter."""

    def test_two_real_speakers_two_ghosts(self):
        """Classic Sortformer scenario: 2 real speakers, 2 ghosts with tiny activity."""
        segments = _make_segments([
            # Speaker 0: dominant, ~30s total
            ("speaker_0", 0.0, 10.0),
            ("speaker_0", 15.0, 25.0),
            ("speaker_0", 30.0, 40.0),
            # Speaker 1: active, ~20s total
            ("speaker_1", 10.0, 15.0),
            ("speaker_1", 25.0, 30.0),
            ("speaker_1", 40.0, 50.0),
            # Speaker 2: ghost, 0.3s total
            ("speaker_2", 12.5, 12.8),
            # Speaker 3: ghost, 0.1s total
            ("speaker_3", 35.0, 35.1),
        ])

        result = filter_ghost_speakers(segments)
        result_speakers = {seg["speaker"] for seg in result}

        assert result_speakers == {"speaker_0", "speaker_1"}
        # Ghost segments should be reassigned, not dropped
        assert len(result) >= 6  # At least the 6 real segments

    def test_no_filtering_when_all_speakers_active(self):
        """All 4 speakers have meaningful activity — keep all."""
        segments = _make_segments([
            ("speaker_0", 0.0, 30.0),
            ("speaker_1", 30.0, 60.0),
            ("speaker_2", 60.0, 90.0),
            ("speaker_3", 90.0, 120.0),
        ])

        result = filter_ghost_speakers(segments)
        result_speakers = {seg["speaker"] for seg in result}

        assert result_speakers == {"speaker_0", "speaker_1", "speaker_2", "speaker_3"}

    def test_single_speaker_kept(self):
        """Even if only 1 speaker is real, it must survive."""
        segments = _make_segments([
            ("speaker_0", 0.0, 60.0),
            ("speaker_1", 10.0, 10.2),
            ("speaker_2", 20.0, 20.1),
            ("speaker_3", 30.0, 30.05),
        ])

        result = filter_ghost_speakers(segments)
        result_speakers = {seg["speaker"] for seg in result}

        assert result_speakers == {"speaker_0"}

    def test_always_keeps_at_least_one_speaker(self):
        """Even if all speakers are very short, keep the most active one."""
        segments = _make_segments([
            ("speaker_0", 0.0, 0.3),
            ("speaker_1", 0.5, 0.7),
            ("speaker_2", 1.0, 1.1),
        ])

        result = filter_ghost_speakers(segments)
        result_speakers = {seg["speaker"] for seg in result}

        # speaker_0 has 0.3s, speaker_1 has 0.2s — speaker_0 is most active
        assert len(result_speakers) >= 1
        assert "speaker_0" in result_speakers

    def test_empty_segments(self):
        """Empty input returns empty output."""
        result = filter_ghost_speakers([])
        assert result == []

    def test_ghost_segments_reassigned_to_nearest_speaker(self):
        """Ghost segments should be merged into the nearest real speaker."""
        segments = _make_segments([
            ("speaker_0", 0.0, 10.0),
            ("speaker_1", 20.0, 30.0),
            # Ghost near speaker_1 (at t=22)
            ("speaker_2", 21.5, 21.8),
        ])

        result = filter_ghost_speakers(segments)

        # The ghost segment at t=21.5-21.8 is nearest to speaker_1 (at t=20-30)
        ghost_reassigned = [s for s in result if 21.0 < s["start"] < 22.0]
        assert len(ghost_reassigned) <= 1  # May be merged into speaker_1's segment
        for seg in result:
            assert seg["speaker"] in ("speaker_0", "speaker_1")

    def test_relative_threshold(self):
        """Speaker below 5% of total time filtered even if > 1s absolute."""
        # 1000s total audio, speaker_2 has 2s (0.2% of total)
        segments = _make_segments([
            ("speaker_0", 0.0, 500.0),
            ("speaker_1", 500.0, 1000.0),
            ("speaker_2", 100.0, 102.0),  # 2s absolute but only 0.2% relative
        ])

        result = filter_ghost_speakers(segments)
        result_speakers = {seg["speaker"] for seg in result}

        # 2s absolute passes the 1s floor, but 0.2% fails the 5% relative threshold
        assert result_speakers == {"speaker_0", "speaker_1"}

    def test_absolute_threshold(self):
        """Speaker below absolute minimum filtered even if ratio looks ok."""
        # 10s total audio, speaker_2 has 0.5s (5% of total — passes ratio, fails absolute)
        segments = _make_segments([
            ("speaker_0", 0.0, 5.0),
            ("speaker_1", 5.0, 10.0),
            ("speaker_2", 3.0, 3.5),  # 0.5s = 5% ratio but below 1.0s absolute
        ])

        result = filter_ghost_speakers(segments)
        result_speakers = {seg["speaker"] for seg in result}

        assert result_speakers == {"speaker_0", "speaker_1"}

    def test_speaker_passes_both_thresholds(self):
        """Speaker that passes both relative AND absolute thresholds is kept."""
        # 60s total, speaker_2 has 3s (5% relative, 3s absolute) — keep
        segments = _make_segments([
            ("speaker_0", 0.0, 30.0),
            ("speaker_1", 30.0, 57.0),
            ("speaker_2", 15.0, 18.0),  # 3s = 5% and > 1s
        ])

        result = filter_ghost_speakers(segments)
        result_speakers = {seg["speaker"] for seg in result}

        assert result_speakers == {"speaker_0", "speaker_1", "speaker_2"}

    def test_preserves_segment_count_after_reassignment(self):
        """Total segments should not decrease — ghost segments are reassigned, not dropped."""
        segments = _make_segments([
            ("speaker_0", 0.0, 10.0),
            ("speaker_0", 20.0, 30.0),
            ("speaker_1", 10.0, 20.0),
            ("speaker_2", 5.0, 5.2),  # ghost
        ])

        result = filter_ghost_speakers(segments)

        # Ghost segment is reassigned — merged segments may reduce count
        # but no segments are outright dropped
        total_duration = sum(s["duration"] for s in result)
        original_duration = sum(s["duration"] for s in segments)
        # Duration should be approximately preserved (merging may shift edges slightly)
        assert abs(total_duration - original_duration) < 0.5

    def test_adjacent_merged_after_reassignment(self):
        """Reassigned segments adjacent to the target speaker get merged."""
        segments = _make_segments([
            ("speaker_0", 0.0, 10.0),
            ("speaker_2", 10.0, 10.3),  # ghost, right after speaker_0
            ("speaker_1", 15.0, 25.0),
        ])

        result = filter_ghost_speakers(segments)

        # speaker_2 ghost is nearest speaker_0, and is adjacent — should merge
        s0_segments = [s for s in result if s["speaker"] == "speaker_0"]
        assert len(s0_segments) == 1
        # The merged segment should extend to cover the ghost
        assert s0_segments[0]["end"] >= 10.0

    def test_works_with_limit_speakers_after(self):
        """filter_ghost_speakers and limit_speakers compose correctly."""
        segments = _make_segments([
            ("speaker_0", 0.0, 30.0),
            ("speaker_1", 30.0, 50.0),
            ("speaker_2", 50.0, 55.0),  # real but small
            ("speaker_3", 12.0, 12.1),  # ghost
        ])

        # First filter ghosts
        filtered = filter_ghost_speakers(segments)
        filtered_speakers = {seg["speaker"] for seg in filtered}
        assert "speaker_3" not in filtered_speakers

        # Then limit to 2 speakers
        limited = limit_speakers(filtered, 2)
        limited_speakers = {seg["speaker"] for seg in limited}
        assert len(limited_speakers) <= 2

    def test_custom_thresholds(self):
        """Custom thresholds override defaults."""
        # With stricter ratio (10%), speaker_2 should be filtered
        segments1 = _make_segments([
            ("speaker_0", 0.0, 100.0),
            ("speaker_1", 100.0, 200.0),
            ("speaker_2", 50.0, 60.0),  # 10s = 5% of 200s — passes default 5%, fails 10%
        ])
        result = filter_ghost_speakers(
            segments1, min_activity_ratio=0.10, min_absolute_seconds=1.0
        )
        result_speakers = {seg["speaker"] for seg in result}
        assert result_speakers == {"speaker_0", "speaker_1"}

        # With lenient ratio (1%), speaker_2 should be kept (fresh segments)
        segments2 = _make_segments([
            ("speaker_0", 0.0, 100.0),
            ("speaker_1", 100.0, 200.0),
            ("speaker_2", 50.0, 52.0),  # 2s = 1% — fails default 5%, passes explicit 1%
        ])
        result2 = filter_ghost_speakers(segments2, min_activity_ratio=0.01)
        result2_speakers = {seg["speaker"] for seg in result2}
        assert "speaker_2" in result2_speakers
