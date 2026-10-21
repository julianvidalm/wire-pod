# chipper

## MicTap command link

Each robot holds one MicTap `Session` with wire-pod (the robot is the client, `pkg/servers/mictap`). The robot sends
no mic audio on it (robot-wake T1): the session carries commands down (`Sessions.Send`: intent results, knowledge
answers, the cortex's audio stream) and the robot's audio stream reports up (`STARTED`, `COMPLETED`, …). A robot
that still streams MicTap audio is only logged (`mictap.LogSink`). To try the robot side by hand:

```
/api-mictap/command?serial=<esn>&intent=intent_clock_time[&params=<json>]
/api-mictap/command?serial=<esn>&say=<text>
```

## Conversation (cortex)

The robot wakes itself on "hey vector" (its own Porcupine) and streams the question to wire-pod as a stock voice
request. With `CORTEX_CONVERSATION=on` and `CORTEX_VOICE_REQUESTS=on` the cortex answers it instead of wire-pod's
STT and intent matching: it cuts the question out of the request's audio, transcribes it, decides the answer and
sends it as the request's one response, with its own voice over the MicTap session. `CORTEX_CONVERSATION` only
builds the conversation the voice requests use; nothing else uses it.

Every step is a port in `pkg/cortex` with its adapter:

| Port | Adapter |
| --- | --- |
| audio | the voice request's Opus audio decoded to 16 kHz (`pkg/initwirepod`), cut by `cortex.RequestCapture` |
| VAD | webrtc VAD (`pkg/vad`) |
| transcriber | whisper.cpp with its own model context (`pkg/cortex/whispercpp`), en or es per utterance |
| brain | wire-pod's intent matching in the transcript's language (`intent-data/en-US.json`, `es-ES.json`), then its LLM (`pkg/cortex/wirepodbrain`), told to answer in the question's language: "Responde siempre en español." when whisper heard Spanish or the question reads Spanish, else "Answer in English." (added to the system prompt of the cortex's requests only; wire-pod's own LLM requests are unchanged) |
| reply | the voice request's chipper response (`chipperReply`): an intent, or knowledge |
| voice | `cortex.Talker`: with `CORTEX_TTS=piper` the answer's audio over the robot's MicTap session |

The request's audio starts at the robot's wake. The user speaks once the VAD hears 200 ms of speech in a row; in the
first 1.5 s, which hold the robot's own earcon and get-in sound, it takes 750 ms in a row, or 600 ms within any 1 s
as the robot firmware counts it, so a question said in the same breath as the wake is heard whole. A pause shorter
than 700 ms does not end the question; `CORTEX_CAPTURE_MAX` (8 s) from the speech onset, the robot ending the
stream or 14.5 s end it too, and `CORTEX_NO_SPEECH` (9 s) without speech gives up. A request where the user never
spoke (a short noise burst) or whose speech is quieter than -48 dBFS is nothing heard and never reaches whisper. A
leading wake phrase in the transcript ("Hey Vector,", "Oye Vector", "Hola Vector", whisper's "Hey, Víctor.", "Hey
Béctor", "Vektor") is removed before the brain; a transcript that is only the wake phrase is nothing heard.
Everything is answered within 19 s of the request (vic-cloud gives up at 20 s). The request gets exactly one
response on its own stream, never a MicTap intent:

| Answer | Response |
| --- | --- |
| brain intent | that intent, its JSON parameters as strings |
| text, with `CORTEX_TTS=piper` | `intent_knowledge_response_extend_bypass` with `answer`, `answer_type`, `query_text`, `external_voice` `true` and `expected_duration_ms`, then the piper audio over the MicTap session (needs the firmware that types these two parameters); when the voice cannot start, the plain knowledge answer the robot says |
| text, robot voice | the knowledge graph response |
| no speech (never 200 ms in a row), speech under -48 dBFS, no words, only "hey vector", a quiet silence phrase | `intent_system_noaudio` |
| transcription or brain error | `intent_system_unmatched` |

When no intent matches, the LLM configured in the web interface answers (provider, key, prompt and remembered
chat); its `{{command}}` actions are dropped, except one: with LLM commands on in the web interface, the cortex adds
wire-pod's command `{{runIntent||<intent>}}` to the prompt, listing the robot's action intents
(`wirepodbrain.ActionIntents`: charger, sleep, turns, come, explore, dance, tricks, cube games, eye color, volume,
photo, timers, messages, goodnight, goodbye …, each with what it does and an English and a Spanish example). When
the LLM answers a request with a whitelisted intent, that intent is the request's one chipper response, its
parameters parsed from the user's words by wire-pod's parameter code as if the matcher had picked it, and the LLM's
words are dropped (the intent has its own animations and speech). An intent outside the list is ignored and the
LLM's words are answered. wire-pod's own LLM path does not get runIntent.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CORTEX_CONVERSATION` | `off` | `on` builds the conversation (needs `STT_SERVICE=whisper.cpp`); the voice requests need it |
| `CORTEX_VOICE_REQUESTS` | `off` | `on` (with `CORTEX_CONVERSATION=on`) answers the robot's own voice requests with the conversation; unset or `off`, wire-pod answers them exactly as before |
| `CORTEX_WHISPER_MODEL` | `WHISPER_MODEL` | the conversation's whisper model, for example `large-v3-turbo-q5_0` (`ggml-<name>.bin` in the whisper models directory) |
| `WHISPER_AUDIO_CTX` | unset | `full` encodes the whole 30 s window; large-v3-turbo needs it (with the short window it repeats and invents text) |
| `WHISPER_LANGUAGES` | `en,es` | languages whisper chooses among for each utterance |
| `CORTEX_NO_SPEECH` | `9s` | no speech this long after the wake ends the request as nothing heard |
| `CORTEX_CAPTURE_MAX` | `8s` | longest question, from the start of the speech |
| `CORTEX_VAD_MODE` | `3` | webrtc VAD aggressiveness, 0-3 |
| `CORTEX_PERSONA` | `-` | the robot's name in the LLM prompt (replaces "Vector"); unset or `-` keeps the prompt as is, so the robot is Vector |
| `CORTEX_VOICE_DUMP_DIR` | unset | saves each voice request as `<esn>_<YYYYMMDD-HHMMSS.mmm>_request.wav` (the whole request audio received, 16 kHz mono, before endpointing) and `_request.json` (end reason, utterance and speech onset sample offsets in the WAV, voiced time, level, whisper's raw and stripped text, language, outcome, answer, the chipper response, capture/transcribe/think/total ms), to tell audio, endpointing and whisper problems apart; a failure is logged once and never disturbs the answer |
| `CORTEX_VOICE_DUMP_MAX_MB` | `200` | caps the dump directory: after each dump the oldest request pairs are deleted until it fits; `0` keeps everything |

On quiet or noisy audio whisper writes the closing lines of the videos it learned from ("Thank you.", "Thanks for
watching!", "Gracias."); such a transcript is answered only when its speech reached -38 dBFS, so a clearly spoken
"gracias" still is.

Logs, at startup and for one question:

```
Cortex conversation on: whisper large-v3-turbo-q5_0 (en,es), full audio context, wire-pod intents (en-US, es-ES) then its LLM, webrtc VAD mode 3; a question ends after 700ms of silence, …
Cortex voice requests on: the robot's wakes are answered by the conversation; listening at most 14.5s, answering within 19s of the request
Cortex: <esn> voice request: 2.1 s of audio, end of speech after 2.13 s
Cortex: <esn> heard 2.1 s (end of speech, speech 1.2 s at -31 dBFS)
Cortex: <esn> transcribed (es, 0.82 s): ¿Qué hora es? [raw: Hey Vector, ¿qué hora es?]
Cortex: <esn> answer intent intent_clock_time  (0.00 s), sent
Cortex: <esn> voice request response intent_clock_time 3.01 s after the request, sent
```

### Intent audit

The cortex brain answers with wire-pod's intents only when the match is good enough; otherwise the LLM answers
(`pkg/cortex/wirepodbrain`). An exact keyphrase match (comparing words, so whisper's punctuation does not hide it)
always answers. Otherwise the intent with the longest keyphrase in the question answers when that keyphrase begins at
a word and covers at least 30% of the question's words (`minPartialCoverage`), not counting politeness and filler
words (`FillerWords`: please, a bit, por favor, un poco …): "Go to sleep." is `go to sleep`, "Ven aquí, por favor."
is `ven aquí`, "What do you think about the first conditional?" goes to the LLM. wire-pod's own voice path keeps its matcher.

`go run ./cmd/intent-audit -generate -out <report.md> [-before <dir of older intent lists>]` runs the corpora in
`pkg/cortex/wirepodbrain/testdata/intent-audit` (template, hand-written, real and held-out sentences, in English and
Spanish) through that same step without calling the LLM, and reports per keyphrase the commands it answers, the
questions it takes from the LLM and the commands it loses, for several rules and thresholds. `-previous <dir>`
compares with the lists before the latest additions. `-llm apiConfig.json` also sends the lost commands and the
held-out non-commands through the brain with the configured LLM (paid calls, remembered chat off) and keeps the
runIntent results in `testdata/llm-probe/results.md`, which the report includes.

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

With `CORTEX_TTS=piper` the conversation speaks text answers itself (`cortex.Talker`, T8d); intent answers
keep their robot behaviors. Once the first sentence is synthesized it sends the voice request's one response, the
knowledge bypass with `external_voice` and `expected_duration_ms` (the robot shows its talking face instead of
speaking), then on the robot's MicTap session an audio `prepare` (16000 Hz, `CORTEX_TTS_VOLUME`), chunks of at most
1024 bytes, and `complete`. The first 0.2 s go at once (the robot starts playing after 0.2 s buffered), then the stream stays at
most 3 s ahead of playback (vic-anim overflows past about 6.2 s); sentences follow each other in the same stream.
The expected duration is exact for one sentence; for more it is the first sentence's duration plus the rest's
letters at its rate (at least 80 ms each) and 300 ms per sentence, plus a quarter: the robot keeps its face until
the stream ends, so a longer guess is the safer one. The voice is the answer's language when its text clearly
reads as Spanish or English, else the question's (whisper's `es` is Spanish, anything else English).

Fallbacks: synthesis failing before the first sentence sends the plain knowledge answer as the request's response,
which the robot says with its own voice. Once that response went out the request has its one answer: the robot
reporting `FAILED`/`OVERFLOW` is logged (the robot's own voice would be a second response, which is refused), a
later synthesis failure cancels the stream, and the answer is never spoken twice. A
robot without the T8b/T8c firmware ignores the stream and `external_voice` and speaks the answer itself. Each
answer logs `Cortex voice: <esn> stream <id>: <n> sentences, <s> (expected <s>, <lang>); synthesis <ms>, first
audio <ms> after the answer, max lead <s>, <n> gaps`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CORTEX_TTS` | `off` | `piper` starts the sidecar, logs `cortex: TTS piper, es …, en …, level … dBFS` and speaks text answers with it; `off` lets the robot say them |
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
