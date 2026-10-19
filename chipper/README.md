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

## Conversation (cortex)

With `CORTEX_CONVERSATION=on`, the PC owns the conversation: after each wake it captures what the user says from
the same MicTap mic stream, transcribes it, decides the answer and sends it to the robot over its MicTap
session. The robot only streams audio and executes the command; there is no trigger word and no second
listening pass on the robot. Off (the default) keeps the attend-only wake above.

Every step is a port in `pkg/cortex` with its adapter:

| Port | Adapter |
| --- | --- |
| audio source | the MicTap stream (`pkg/initwirepod`), or a recording in `cmd/cortex-replay` |
| wake detector | `pkg/oww` or `pkg/wakeword`, through the wake handler |
| VAD | webrtc VAD (`pkg/vad`) |
| echo canceller | pass-through (the robot's processed mic already removes most of its sound) |
| transcriber | whisper.cpp with its own model context (`pkg/cortex/whispercpp`), en or es per utterance |
| brain | wire-pod's intent matching in the transcript's language (`intent-data/en-US.json`, `es-ES.json`), then its LLM (`pkg/cortex/wirepodbrain`) |
| robot | MicTap session commands: an intent result, or knowledge (an answer to say) |

The capture keeps 1 s of every robot's mic audio, so a wake that arrives late still finds its start. It starts
`CORTEX_WAKE_MARGIN` before the wake's end (just after "hey jarvis"), ends after 700 ms of silence that follows
speech, stops at 8 s, and gives up after 4 s without speech. Only speech after the wake's end counts, and only
once it lasts 200 ms: the margin holds the end of "jarvis" and is sent to whisper, but it never starts the
utterance, so a pause between the name and the question does not end it. These times count audio samples at `OWW_MIC_RATE`,
and the utterance is resampled from that rate to whisper's 16 kHz. Each robot has one worker that transcribes and
answers in order. A second utterance waits; more are dropped and logged, so the mic stream and the wake detector
never wait for whisper or the LLM.

An intent answer (for example `intent_clock_time`, or `intent_clock_settimer_extend` with
`{"timer_duration":"300"}`) is sent as an intent result. When no intent matches, the LLM configured in the web
interface answers (provider, key, prompt and remembered chat). Its `{{command}}` actions are dropped and the
answer is sent as knowledge, which the robot says with its knowledge-graph animations. With the LLM off, the
answer is `intent_system_unmatched`, as in wire-pod's own voice flow.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CORTEX_CONVERSATION` | `off` | `on` captures, transcribes and answers after each wake (needs `STT_SERVICE=whisper.cpp`) |
| `CORTEX_WHISPER_MODEL` | `WHISPER_MODEL` | the conversation's whisper model, for example `large-v3-turbo-q5_0` (`ggml-<name>.bin` in the whisper models directory) |
| `WHISPER_AUDIO_CTX` | unset | `full` encodes the whole 30 s window; large-v3-turbo needs it (with the short window it repeats and invents text) |
| `WHISPER_LANGUAGES` | `en,es` | languages whisper chooses among for each utterance |
| `CORTEX_WAKE_MARGIN` | `100ms` | how far before the wake's end the capture starts |
| `CORTEX_VAD_MODE` | `3` | webrtc VAD aggressiveness, 0-3 |
| `CORTEX_PERSONA` | `Jarvis` | the robot's name in the LLM prompt (replaces "Vector"); `-` keeps the prompt as is |

Logs, at startup and for one question:

```
Cortex conversation on: whisper large-v3-turbo-q5_0 (en,es), full audio context, wire-pod intents (en-US, es-ES) then its LLM as Jarvis, webrtc VAD mode 3; capture from 100ms before the wake, …
Wake word: <esn> heard "hey jarvis" (confidence 0.93) in "hey jarvis"
Cortex: <esn> listening from 100ms before the wake
attend sent <esn> intent_greeting_hello
Cortex: <esn> heard 1.8 s (end of speech, speech 1.2 s at -31 dBFS)
Cortex: <esn> transcribed (es, 0.82 s): ¿Qué hora es?
Cortex: <esn> answer intent intent_clock_time  (0.00 s), sent
```

Nothing is transcribed when the speech after the wake is quieter than -48 dBFS (`nothing heard in 1.2 s (…),
nothing sent`). On quiet or noisy audio whisper writes the closing lines of the
videos it learned from ("Thank you.", "Thanks for watching!", "Gracias."); such a transcript is answered only
when its speech reached -38 dBFS, so a clearly spoken "gracias" still is (`nothing heard: "Thank you." is
whisper's word for quiet audio (…), nothing sent`).

Other outcomes are logged too: `nothing heard: no speech after 4.1 s, nothing sent`, `transcribed no words "."`,
`answer "…" (1.10 s), sent` for an LLM answer, `still answering, utterance dropped (n dropped)`,
`already listening, wake ignored`, and `transcription failed` or `brain failed` with the error.

The attend greeting still plays on each wake, and its sounds and motion reach the mic: they keep the capture open
until they stop (about 1.5 to 4.5 s after the wake). After a bare "hey jarvis", whisper may turn them into
"Thank you.", which is then answered. `CORTEX_ATTEND_INTENT=` turns the greeting off.

Offline check without a robot: replay a recording with its labels through the wake detector, capture, whisper
and brain. Nothing is sent to a robot. Run it from `chipper`, with the whisper build's CGO flags from
`run-chipper.sh`:

```sh
go run -tags nolibopusfile ./cmd/cortex-replay -python "$OWW_PYTHON" -oww-model "$OWW_MODEL" \
  -clock ~/.cache/v3ctor/sessions/<session>_clock.jsonl -labels ~/.cache/v3ctor/corpus/<labels>.jsonl -ids s \
  -whisper-model ../whisper.cpp/models/ggml-large-v3-turbo-q5_0.bin -full-audio-ctx \
  ~/.cache/v3ctor/sessions/<session>_mic.wav
```

It prints one row per labeled line: the wake, the captured utterance, its transcription and the brain's answer.
`-llm=false` skips the LLM. Without `-clock` and `-labels` it replays the whole recording.
