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
| `CORTEX_ATTEND_INTENT` | `intent_greeting_hello` | intent sent on each wake, with parameters `{"wake_ts_ms":N}` (robot timestamp of the mic frame where the wake word was detected, 0 if unknown; the robot turns toward the voice over the wake phrase ending there); set but empty (`export CORTEX_ATTEND_INTENT=`) turns attend off and only logs |
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
| `OWW_MIC_RATE` | `15662` | measured rate of Vector's mic; MicTap labels it 16000 |

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
| brain | wire-pod's intent matching in the transcript's language (`intent-data/en-US.json`, `es-ES.json`), then its LLM (`pkg/cortex/wirepodbrain`), told to answer in the question's language: "Responde siempre en español." when whisper heard Spanish or the question reads Spanish, else "Answer in English." (added to the system prompt of the cortex's requests only; wire-pod's own LLM requests are unchanged) |
| robot | MicTap session commands: an intent result, or knowledge (an answer to say), with `CORTEX_TTS=piper` spoken by the cortex as an audio stream |

The capture starts `CORTEX_WAKE_MARGIN` (1 s) before the wake's end, so whisper hears the whole "hey jarvis": on
a short question that is what tells it Spanish from English (L61: "¿qué hora es?" alone came out as "Do you
guys?"). It keeps that margin plus 1 s of every robot's mic audio, so a wake that arrives late still finds its
start. It ends after 700 ms of silence that follows speech and stops `CORTEX_CAPTURE_MAX` (8 s) after the speech
started. The robot first turns to the voice (S6), so the user speaks late: with no speech
`CORTEX_LISTEN_PROMPT_AFTER` (6 s) after the wake the robot is sent `CORTEX_LISTEN_PROMPT_INTENT`
(`intent_cortex_listening_prompt`, its question mark) and keeps listening; with still none at `CORTEX_NO_SPEECH`
(9 s) the capture gives up and sends the end intent. The prompt goes through the same ordered queue as the attend
and the end, and is skipped when a newer wake is already listening. Only speech after the wake's end counts, and only once it lasts 200 ms:
the margin holds "hey jarvis" and is sent to whisper, but it never starts the utterance, so a pause between the
name and the question does not end it. A leading wake phrase in the transcript ("Hey Jarvis,", "Oye Jarvis",
"Hola Jarvis", whisper's "Hey, Jorby.", "Ay, Jarvis.", "Hey Jorbit") is removed before the brain; a transcript that is only the wake phrase is nothing heard. These times count audio samples at `OWW_MIC_RATE`,
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
| `CORTEX_WAKE_MARGIN` | `1s` | how far before the wake's end the capture starts |
| `CORTEX_LISTEN_PROMPT_AFTER` | `6s` | no speech this long after the wake sends the prompt intent; `0s` never |
| `CORTEX_LISTEN_PROMPT_INTENT` | `intent_cortex_listening_prompt` | the prompt's intent; empty sends nothing |
| `CORTEX_NO_SPEECH` | `9s` | no speech this long after the wake ends the capture (and sends the end intent) |
| `CORTEX_CAPTURE_MAX` | `8s` | longest question, from the start of the speech |
| `CORTEX_VAD_MODE` | `3` | webrtc VAD aggressiveness, 0-3 |
| `CORTEX_ATTEND_END_INTENT` | `intent_cortex_listening_end` | intent sent when a capture ends without an answer (no speech, nothing heard, only the wake phrase, a transcription or brain error, a dropped utterance), so the robot stops listening; sent in order with the attends, skipped when a newer wake is already listening, and for a dropped utterance only if nothing ahead of it was answered; empty sends nothing |
| `CORTEX_THINKING_INTENT` | `intent_cortex_thinking` | intent sent when a capture ends with speech, before whisper, so the robot shows its thinking face until the answer; in order with the attends, skipped when a newer wake is already listening, and the answer waits (at most 2 s) for it; empty sends nothing |
| `CORTEX_PERSONA` | `Jarvis` | the robot's name in the LLM prompt (replaces "Vector"); `-` keeps the prompt as is |

### Half duplex

While the robot talks, wire-pod does not listen: the wake word detector and the conversation capture get
silence instead of the mic (`pkg/cortex` `HalfDuplex`, wired by `pkg/cortex/mictapgate`). The robot counts as
talking while its MicTap speaker frames are louder than `CORTEX_HALF_DUPLEX_DB`, never from the robot's
speaker-active flag (it stays on whenever the sound sink is open). Mic and speaker frames carry timestamps on
the robot's clock, so a mic frame is silenced when its timestamp falls between a sound's start and
`CORTEX_HALF_DUPLEX_HANGOVER` after its end. The echo reaches the mic about 250 ms after the speaker frame's
timestamp and fades into the room noise within about 300 ms more; 600 ms covers both with margin. Speaker
frames may arrive after the mic frames they overlap, so each mic frame waits for the speaker audio covering
its time, at most 300 ms (normally one 32 ms speaker frame: the robot sends speaker frames all the time). A
silenced frame is zeros of the same length, so the pre-roll and every timestamp stay intact. The recorder
(`MICTAP_RECORD_DIR`) and the stream log still get the real mic audio.

The wake gesture is the exception for the capture (L101): the robot plays its wake reaction (the earcon at about
+0.2..+0.7 s, -12 dBFS on its speaker) while the user often goes straight on with the question ("Hola Jarvis,
cuéntame un chiste" in one breath), so the gate would zero the question. The gate hands the conversation the real
mic beside every frame it silenced, and the capture listens to it from its start (`CORTEX_WAKE_MARGIN` before the
wake) until `CORTEX_HALF_DUPLEX_WAKE_GRACE` after the wake, or until the capture ends if sooner. The wake detector
always hears the gate, and so does the capture after the window (robot speech, answers). The earcon's echo on the
mic (-31..-41 dBFS) is not speech to the VAD: a wake with nobody speaking after it still prompts at 6 s and gives
up at 9 s (`TestTheEarconEchoAloneIsNotSpeech`, a recorded wake gesture).

| Variable | Default | Meaning |
| --- | --- | --- |
| `CORTEX_HALF_DUPLEX` | `on` | `off` lets the detector and the conversation hear the robot talk (the behavior before half duplex) |
| `CORTEX_HALF_DUPLEX_HANGOVER` | `600ms` | Go duration the mic stays silenced after the robot's last sound |
| `CORTEX_HALF_DUPLEX_WAKE_GRACE` | `1.5s` | Go duration after the wake until which the capture hears the mic the gate silenced; `0s` never |
| `CORTEX_HALF_DUPLEX_DB` | `-40` | speaker level (dBFS, RMS of one 32 ms frame) that counts as talking; the idle speaker is digital zero or dither at -115 to -75 dBFS and can hold at -58/-59 for seconds after an answer, the explorer's animation sounds sit at -48 to -53 (at -50 they flickered the gate over a wake), speech -45 to -10 |

A bad `CORTEX_HALF_DUPLEX_HANGOVER` (not a duration, or negative) or `CORTEX_HALF_DUPLEX_DB` (not a number, or
outside -120..0) is logged (`Cortex half duplex: … using the default …`) and its default is used; the gate stays
on. A robot whose hello has no speaker rate is not gated, and when no speaker frame has come for 2 s of robot
time the mic passes straight through (no 300 ms wait) until speaker frames resume.

### PC voice (piper)

The cortex's `Voice` port (`pkg/cortex` `SentenceVoice` over a `Synthesizer`) turns an answer into 16 kHz mono
s16 chunks, one per sentence, so the first sentence can play while the next is synthesized. Sentences end at
`.` `!` `?` `…` followed by a space and at line breaks; a period after an abbreviation (Sr., Dra., p. ej., Mr.,
e.g.) or an initial, or before a lowercase word or a digit, does not end one. Each sentence is resampled to
16 kHz and scaled so its speech (20 ms blocks above -50 dBFS) has an RMS of `CORTEX_TTS_LEVEL_DB`, never past a
-1 dBFS peak; the robot's own TTS reaches the MicTap tap at about -15 to -25 dBFS. Cancelling the context
stops after the sentence in flight.

The `piper` adapter (`pkg/cortex/piper`) runs `tts-sidecar/tts_sidecar.py` once and keeps it: a fresh process
spends about 1-1.6 s loading onnxruntime and the voices, while a loaded voice speaks a sentence in 130-210 ms
(RTF about 0.04 on the CPU). It starts with wire-pod (with `CORTEX_CONVERSATION=on`), restarts itself on the
next sentence after it dies, exits at the end of its stdin and, on Linux, when wire-pod dies.

With `CORTEX_TTS=piper` the conversation speaks knowledge answers itself (`cortex.Talker`, T8d); intent answers
keep their robot behaviors. Once the first sentence is synthesized it sends, in order on the robot's MicTap
session: the knowledge answer with `external_voice` and `expected_duration_ms` (the robot shows its talking face
instead of speaking), an audio `prepare` (16000 Hz, `CORTEX_TTS_VOLUME`), chunks of at most 1024 bytes, and
`complete`. The first 0.2 s go at once (the robot starts playing after 0.2 s buffered), then the stream stays at
most 3 s ahead of playback (vic-anim overflows past about 6.2 s); sentences follow each other in the same stream.
The expected duration is exact for one sentence; for more it is the first sentence's duration plus the rest's
letters at its rate (at least 80 ms each) and 300 ms per sentence, plus a quarter: the robot keeps its face until
the stream ends, so a longer guess is the safer one. The voice is the answer's language when its text clearly
reads as Spanish or English, else the question's (whisper's `es` is Spanish, anything else English).

Fallbacks: synthesis failing before the first sentence, or the robot reporting `FAILED`/`OVERFLOW` before it
reported `STARTED` (also up to 1.5 s after the last chunk), sends the plain knowledge answer, which the robot says
with its own voice. A failure after that cancels the stream and is logged; the answer is never spoken twice. A
robot without the T8b/T8c firmware ignores the stream and `external_voice` and speaks the answer itself. Each
answer logs `Cortex voice: <esn> stream <id>: <n> sentences, <s> (expected <s>, <lang>); synthesis <ms>, first
audio <ms> after the answer, max lead <s>, <n> gaps`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CORTEX_TTS` | `off` | `piper` starts the sidecar, logs `cortex: TTS piper, es …, en …, level … dBFS` and speaks knowledge answers with it; `off` lets the robot say them (the commands are the same as before T8d) |
| `CORTEX_TTS_SENTENCE_TIMEOUT` | `3s` | longest synthesis of one sentence (warm piper takes 130-210 ms); past it the sidecar is killed and restarted on the next sentence, and the answer falls back as when synthesis fails: before the first sentence the robot says it, later the stream is cancelled; not a positive duration is an error (voice off) |
| `CORTEX_TTS_VOLUME` | `100` | SDK playback volume 1-100, under the robot's master volume; anything else logs and uses 100 |
| `CORTEX_TTS_VOICE_ES` | `~/.cache/v3ctor/piper-voices/es_MX-claude-high.onnx` | Spanish piper voice (its `.onnx.json` next to it) |
| `CORTEX_TTS_VOICE_EN` | `~/.cache/v3ctor/piper-voices/en_US-lessac-medium.onnx` | English piper voice |
| `CORTEX_TTS_LEVEL_DB` | `-20` | speech RMS in dBFS, in [-60, 0) |
| `CORTEX_TTS_PYTHON` | `python3` | Python with `onnxruntime`, `piper_phonemize` and `numpy` |
| `CORTEX_TTS_SIDECAR` | `tts-sidecar/tts_sidecar.py` | sidecar script (relative to `chipper`) |

A bad `CORTEX_TTS` or `CORTEX_TTS_LEVEL_DB`, or a sidecar that does not start, logs `Cortex voice off (the robot
speaks)` and the robot says the answers. To hear a voice and measure it:

```sh
go run -tags nolibopusfile ./cmd/cortex-say -lang es -repeat 3 -o /tmp/es.wav "Hola. ¿Cómo estás?"
```

It prints the sidecar start time and, per run, the time to the first sentence and the RTF (the first run is
cold); `-voice` tries another `.onnx` for `-lang`, `-level` another level. `pkg/cortex/piper`'s integration
test runs when both voices and the Python (`CORTEX_TTS_PYTHON`, else `~/.cache/v3ctor/oww-train-venv`) exist.

It applies whenever a wake word detector is on. Logs, at startup and when a robot's stream ends:

```
Cortex half duplex on: the detector and the conversation hear silence while the robot's speaker is above -40 dBFS and for 600ms after, the mic waits at most 300ms for the speaker audio
Cortex half duplex: <esn> silenced 12 spans, 34.5 s of 600.2 s of mic audio (0 mic frames released before their speaker audio arrived, 0 speaker sounds arrived too late)
```

Logs, at startup and for one question:

```
Cortex conversation on: whisper large-v3-turbo-q5_0 (en,es), full audio context, wire-pod intents (en-US, es-ES) then its LLM as Jarvis, webrtc VAD mode 3; capture from 1s before the wake, …
Wake word: <esn> heard "hey jarvis" (confidence 0.93) in "hey jarvis"
Cortex: <esn> listening from 1s before the wake
attend sent <esn> intent_greeting_hello
Cortex: <esn> heard 2.7 s (end of speech, speech 1.2 s at -31 dBFS)
Cortex: <esn> transcribed (es, 0.82 s): ¿Qué hora es? [raw: Hey Jarvis, ¿qué hora es?]
Cortex: <esn> answer intent intent_clock_time  (0.00 s), sent
```

Nothing is transcribed when the speech after the wake is quieter than -48 dBFS (`nothing heard in 1.2 s (…),
nothing sent`). On quiet or noisy audio whisper writes the closing lines of the
videos it learned from ("Thank you.", "Thanks for watching!", "Gracias."); such a transcript is answered only
when its speech reached -38 dBFS, so a clearly spoken "gracias" still is (`nothing heard: "Thank you." is
whisper's word for quiet audio (…), nothing sent`).

Other outcomes are logged too: `listening prompt intent_cortex_listening_prompt, sent` (or `signal sent …` with attend on),
`nothing heard: no speech after 10.0 s, nothing sent`, `transcribed no words "."`,
`nothing heard after the wake phrase "Hey Jarvis."`,
`answer "…" (1.10 s), sent` for an LLM answer, `still answering, utterance dropped (n dropped)`,
`already listening, wake ignored`, `listening end intent_cortex_listening_end, sent` after any of them that sends no answer, and `transcription failed` or `brain failed` with the error.

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
`-llm=false` skips the LLM. Without `-clock` and `-labels` it replays the whole recording. `-answer <text>` answers
every question with that text; `-tts piper` speaks knowledge answers with the cortex's voice into a fake robot (it
reports each stream started) and prints one `VOICE` line per answer: answer ready to first chunk, chunks and audio
sent, the expected duration, the largest lead over real-time playback and the longest wait between chunks.

Both replays (`cmd/cortex-replay`, `cmd/oww-replay`) run the half-duplex gate when `<session>_speaker.wav` sits
next to the mic recording: speaker frames are interleaved by robot time, taken from `<session>_mic_frames.jsonl`
and `<session>_speaker_frames.jsonl` when the recorder wrote them, else by counting samples from the session
start (recordings made before the frame logs did not fill speaker gaps and drift by seconds, so the gate
silences the wrong audio there; the replays warn). `-half-duplex=false` turns
the gate off and replays exactly as before it (same timing and WAKE lines); `-hangover`, `-half-duplex-db` and (cortex-replay) `-wake-grace` match `CORTEX_HALF_DUPLEX_HANGOVER`,
`CORTEX_HALF_DUPLEX_DB` and `CORTEX_HALF_DUPLEX_WAKE_GRACE`.
With the gate on and frame logs, wakes and labels are on the robot's clock and oww-replay adds each wake's robot
time.
