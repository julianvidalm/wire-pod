"""Checks the sidecar on the user's own held-out clips (recorded on the robot, written at 16 kHz).

    OWW_MODEL=~/coding/v3ctor-build/oww-train/model/v3/hey_jarvis_v3ctor.onnx \
    OWW_CLIPS=~/coding/v3ctor-build/oww-train/model/hey_jarvis_v3ctor \
    ~/.cache/v3ctor/corpus-venv/bin/python -m unittest oww-sidecar/test_oww_sidecar.py

Skipped when OWW_MODEL or OWW_CLIPS is not set.
"""

import json
import os
import subprocess
import sys
import unittest
import wave

HERE = os.path.dirname(os.path.abspath(__file__))
SIDECAR = os.path.join(HERE, "oww_sidecar.py")
MODEL = os.path.expanduser(os.environ.get("OWW_MODEL", ""))
CLIPS = os.path.expanduser(os.environ.get("OWW_CLIPS", ""))
THRESHOLD = 0.7
RATE = 16000


def clip_pcm(name):
    with wave.open(os.path.join(CLIPS, name)) as w:
        assert w.getframerate() == RATE and w.getnchannels() == 1 and w.getsampwidth() == 2
        return w.readframes(w.getnframes())


def run_sidecar(pcm):
    """Feeds 1 s of silence, the clip and 1 s of silence at 16 kHz (resampling off) and returns the reports."""
    silence = bytes(2 * RATE)
    out = subprocess.run(
        [sys.executable, SIDECAR, "--model", MODEL, "--in-rate", str(RATE), "--report", "0.1"],
        input=silence + pcm + silence, capture_output=True, check=True, timeout=120,
    )
    return [json.loads(line) for line in out.stdout.decode().splitlines() if line.strip()]


@unittest.skipUnless(MODEL and CLIPS, "OWW_MODEL and OWW_CLIPS not set")
class SidecarTest(unittest.TestCase):
    def test_reports_a_detection_on_a_user_wake_line(self):
        reports = run_sidecar(clip_pcm("positive_test/user_20261018-152118_j30_r1.wav"))
        self.assertTrue(any(r["score"] >= THRESHOLD for r in reports), reports)
        # positions count input samples and fall inside the fed audio
        self.assertTrue(all(0 < r["sample"] <= 4 * RATE for r in reports), reports)

    def test_reports_no_detection_on_a_user_negative_line(self):
        reports = run_sidecar(clip_pcm("negative_test/user_20261018-152118_n10_r1.wav"))
        self.assertFalse(any(r["score"] >= THRESHOLD for r in reports), reports)

    def test_resamples_the_robot_rate_and_maps_positions_back_to_input_samples(self):
        pcm = bytes(2 * 15662 * 3)  # 3 s of silence at the robot's true rate: no reports, clean exit
        out = subprocess.run([sys.executable, SIDECAR, "--model", MODEL, "--in-rate", "15662", "--report", "0"],
                             input=pcm, capture_output=True, check=True, timeout=120)
        reports = [json.loads(l) for l in out.stdout.decode().splitlines() if l.strip()]
        self.assertEqual(len(reports), 3 * RATE // 1280)
        self.assertAlmostEqual(reports[-1]["sample"], len(reports) * 1280 * 15662 / RATE, delta=1)


if __name__ == "__main__":
    unittest.main()
