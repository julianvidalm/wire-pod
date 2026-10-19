# chipper

## Wake word detector (MicTap)

When a robot streams MicTap (what it hears and plays), wire-pod can run a wake word detector on the mic audio.
Each detection is logged and, unless attend is off, the robot is sent an attend intent over its MicTap session
(as if its cloud had matched it, with no trigger word):

```
Wake word: <esn> heard "hey jarvis" (confidence 0.93) in "hey jarvis"
attend sent <esn> intent_greeting_hello
```

A failed send logs `attend failed <esn> <intent>: <error>` (for example `robot has no open MicTap session`), and a
wake that arrives while the previous send is still in flight logs `attend dropped <esn>: …`. The send never runs on
the detector's goroutine.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CORTEX_ATTEND_INTENT` | `intent_greeting_hello` | intent sent on each wake; set but empty (`export CORTEX_ATTEND_INTENT=`) turns attend off and only logs |
| `CORTEX_ATTEND_REFRACTORY` | `3s` | Go duration; wakes from the same robot this soon after an attend are ignored, so the animation is not retriggered |

The detector is picked in `source.sh`:

| Variable | Default | Meaning |
| --- | --- | --- |
| `WAKE_DETECTOR` | `vosk` | `vosk` (Vosk word matcher) or `oww` (openWakeWord) |
| `WAKEWORD_VOSK_MODEL` | unset | Vosk model directory; with `vosk` selected and this unset, no detector runs |
| `OWW_MODEL` | required for `oww` | openWakeWord `.onnx` model |
| `OWW_THRESHOLD` | `0.7` | lowest score that wakes, in (0, 1] |
| `OWW_PYTHON` | `python3` | Python with `openwakeword`, `onnxruntime`, `soxr` and `numpy` |
| `OWW_SIDECAR` | `oww-sidecar/oww_sidecar.py` | sidecar script (relative to `chipper`, where wire-pod runs) |
| `OWW_MIC_RATE` | `15625` | true rate of Vector's mic; MicTap labels it 16000 |

Example (the v3 model trained on the user's voice):

```sh
export WAKE_DETECTOR=oww
export OWW_MODEL=/home/julianvidal/coding/v3ctor-build/oww-train/model/v3/hey_jarvis_v3ctor.onnx
export OWW_THRESHOLD=0.7
export OWW_PYTHON=/home/julianvidal/.cache/v3ctor/corpus-venv/bin/python
```

With `oww`, each robot stream gets its own sidecar process (`oww-sidecar/oww_sidecar.py`): it reads the raw
mic samples on stdin, resamples them from `OWW_MIC_RATE` to 16 kHz with soxr, scores 80 ms frames with the model,
and reports scores as JSON lines. wire-pod applies the threshold and a 2 s refractory period (one wake per
utterance), restarts a crashed sidecar with backoff, logs its stderr, and never blocks the mic stream: when the
sidecar falls behind, frames are dropped and the count is logged.

Logs to look for at startup and when a robot connects:

```
Wake word detector on, openWakeWord model …, threshold 0.70, mic 15625 Hz, sidecar …
oww: <esn>: oww-sidecar: ready, model hey_jarvis_v3ctor, input 15625 Hz, report >= 0.7
```

Offline check without a robot, replaying a MicTap recording through the same detector and sidecar:

```sh
go run ./cmd/oww-replay -python "$OWW_PYTHON" -model "$OWW_MODEL" -speed 20 ~/.cache/v3ctor/sessions/<session>_mic.wav
```

Sidecar test on the user's held-out clips:

```sh
OWW_MODEL=… OWW_CLIPS=~/coding/v3ctor-build/oww-train/model/hey_jarvis_v3ctor \
  "$OWW_PYTHON" -m unittest oww-sidecar/test_oww_sidecar.py
```
