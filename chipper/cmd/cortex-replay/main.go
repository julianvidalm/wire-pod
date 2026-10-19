// Command cortex-replay replays a recorded MicTap session through the whole PC conversation, offline: the
// openWakeWord detector, the pre-roll capture with VAD endpointing, whisper and wire-pod's brain, wired as
// wire-pod wires them for a live robot (pkg/initwirepod). For each labeled line it prints what was heard and
// what the brain answered. Nothing reaches a robot: the Robot port only records, and the brain answers for a
// robot called "replay", which matches no known robot.
//
// Run it from chipper (it reads apiConfig.json, without writing it, and intent-data/):
//
//	cortex-replay -python ~/.cache/v3ctor/corpus-venv/bin/python -oww-model hey_jarvis_v3ctor.onnx \
//	    [-clock session_clock.jsonl -labels labels.jsonl [-ids s]] [-whisper-model ../whisper.cpp/models/ggml-small.bin] \
//	    [-llm=false] [-speed 1] [-half-duplex=false] [-hangover 600ms] [-half-duplex-db -60] session_mic.wav
//
// The WAV's samples are sent as 10 ms MicTap mic frames (160 samples) timestamped at the true mic rate, at
// -speed times real time, over the span of the selected labels (without labels: the whole recording). When
// session_speaker.wav sits next to it, its speaker frames are interleaved by robot time (from the
// _mic_frames.jsonl and _speaker_frames.jsonl logs when present, else counting samples from the session start)
// and the half-duplex gate silences the mic for the detector and the capture while the robot talks, as
// wire-pod does with CORTEX_HALF_DUPLEX=on.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/mictapgate"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/replay"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/whispercpp"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/wirepodbrain"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/oww"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vad"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
)

const esn = "replay"

func main() {
	python := flag.String("python", "python3", "python with openwakeword, onnxruntime, soxr and numpy")
	sidecar := flag.String("sidecar", "oww-sidecar/oww_sidecar.py", "openWakeWord sidecar script")
	owwModel := flag.String("oww-model", "", "openWakeWord .onnx model")
	threshold := flag.Float64("threshold", 0.7, "lowest wake score")
	micRate := flag.Int("mic-rate", 15662, "true rate of the recording (MicTap labels it 16000)")
	clockPath := flag.String("clock", "", "the session's _clock.jsonl")
	labelsPath := flag.String("labels", "", "labels .jsonl")
	ids := flag.String("ids", "s", "replay the labels whose id starts with this")
	whisperModel := flag.String("whisper-model", "../whisper.cpp/models/ggml-small.bin", "whisper.cpp model")
	fullCtx := flag.Bool("full-audio-ctx", false, "encode whisper's whole 30 s window (large models need it)")
	languages := flag.String("languages", "en,es", "languages whisper chooses among")
	vadMode := flag.Int("vad-mode", 3, "webrtc VAD aggressiveness, 0-3")
	margin := flag.Duration("margin", cortex.DefaultCaptureConfig().WakeMargin, "capture starts this long before the wake")
	useLLM := flag.Bool("llm", true, "ask wire-pod's configured LLM when no intent matches")
	persona := flag.String("persona", "Jarvis", "the robot's name in the LLM prompt")
	speed := flag.Float64("speed", 1, "replay speed, times real time")
	halfDuplex := flag.Bool("half-duplex", true, "silence the mic while the recorded speaker sounds (needs _speaker.wav)")
	hd := cortex.DefaultHalfDuplexConfig()
	flag.DurationVar(&hd.Hangover, "hangover", hd.Hangover, "half duplex: mic stays silenced this long after the sound")
	flag.Float64Var(&hd.Threshold, "half-duplex-db", hd.Threshold, "half duplex: speaker level (dBFS) that counts as talking")
	flag.Parse()
	if *owwModel == "" || (*labelsPath != "") != (*clockPath != "") || flag.NArg() != 1 || *speed <= 0 {
		fmt.Fprintln(os.Stderr, "usage: cortex-replay -oww-model m.onnx [-clock c.jsonl -labels l.jsonl] [flags] <mic.wav>")
		os.Exit(2)
	}
	logf := func(a ...any) { fmt.Fprintln(os.Stderr, a...) }
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, "cortex-replay:", err)
		os.Exit(1)
	}
	recording, err := replay.Load(flag.Arg(0), *micRate)
	if err != nil {
		fail(err)
	}
	samples := recording.Mic
	var clock clockFile
	var labels []label
	if *labelsPath != "" {
		if clock, err = readClock(*clockPath); err != nil {
			fail(err)
		}
		if labels, err = readLabels(*labelsPath, *ids); err != nil {
			fail(err)
		}
		if len(labels) == 0 {
			fail(fmt.Errorf("no labels with an id starting with %q", *ids))
		}
	}
	if err := readAPIConfig("apiConfig.json"); err != nil {
		fail(err)
	}

	tr, err := whispercpp.New(whispercpp.Config{ModelPath: *whisperModel, Languages: strings.Split(*languages, ","),
		FullAudioCtx: *fullCtx})
	if err != nil {
		fail(err)
	}
	intents, err := wirepodbrain.LoadIntents("intent-data", map[string]string{"en": "en-US", "es": "es-ES"})
	if err != nil {
		fail(err)
	}
	brainCfg := wirepodbrain.Config{Intents: intents, DefaultLanguage: "en"}
	if *useLLM {
		brainCfg.LLM = (&wirepodbrain.LLM{Persona: *persona}).Answer
	}
	rec := &recorder{}
	capture := cortex.DefaultCaptureConfig()
	capture.MicRate, capture.WakeMargin = *micRate, *margin
	conv := cortex.NewConversation(cortex.Config{
		Capture: capture,
		NewVAD: func() cortex.VAD {
			v, err := vad.NewWebRTC(*vadMode)
			if err != nil {
				fail(err)
			}
			return v
		},
		Transcriber: recordingTranscriber{tr, rec},
		Brain:       recordingBrain{wirepodbrain.New(brainCfg), rec},
		Robot:       rec,
		Logf:        logf,
	})
	det := oww.New(oww.Config{
		Command: []string{*python, *sidecar, "--model", *owwModel, "--in-rate", strconv.Itoa(*micRate),
			"--report", strconv.FormatFloat(*threshold, 'f', -1, 64)},
		Word:         "hey jarvis",
		Threshold:    *threshold,
		RefractoryMs: 2000,
		StopGrace:    time.Minute,
		Handler: wakeFunc(func(d wakeword.Detection) {
			rec.wake(d.FrameTimestamp)
			conv.Wake(d.ESN, d.FrameTimestamp)
		}),
		Logf: logf,
	})

	rate := *micRate
	from, to := 0, len(samples) // without labels, the whole recording
	if len(labels) > 0 {
		from = clock.position(labels[0].StartMs-1500, rate)
		to = clock.position(labels[len(labels)-1].EndMs+3000, rate)
		from, to = max(0, from-from%160), min(len(samples), to)
	}
	fmt.Fprintf(os.Stderr, "replaying %d labels, %.0f s of audio from %.0f s, at %gx\n", len(labels),
		float64(to-from)/float64(rate), float64(from)/float64(rate), *speed)

	var sink mictap.Sink = replaySink{conv, det}
	switch {
	case recording.Speaker == nil:
		fmt.Fprintln(os.Stderr, "no speaker recording: half duplex off")
	case *halfDuplex:
		fmt.Fprintf(os.Stderr, "half duplex on (speaker aligned by %s): above %g dBFS, hangover %v\n",
			recording.Alignment, hd.Threshold, hd.Hangover)
		sink = mictapgate.New(sink, hd, logf)
	default:
		fmt.Fprintln(os.Stderr, "half duplex off")
	}
	recording.Play(sink, esn, from, to, 0, *speed)
	rec.settle(15 * time.Second)

	for i := range labels {
		labels[i].startTs = recording.MicTime(clock.position(labels[i].StartMs, rate))
		labels[i].endTs = recording.MicTime(clock.position(labels[i].EndMs, rate))
	}
	rec.print(labels)
}

// replaySink hands the stream to the conversation first (so the pre-roll holds each frame before the detector
// can report a wake in it) and to the detector, and ends the detector first, so its last wakes still find the
// conversation open.
type replaySink struct {
	conv *cortex.Conversation
	det  mictap.Sink
}

func (s replaySink) Start(esn string, h mictap.Hello) {
	s.conv.Open(esn)
	s.det.Start(esn, h)
}

func (s replaySink) Frame(esn string, f mictap.Frame) {
	if f.Type == mictap.FrameMic {
		s.conv.Feed(esn, cortex.Frame{Samples: f.Samples(), Timestamp: f.Timestamp})
	}
	s.det.Frame(esn, f)
}

func (s replaySink) End(esn string, err error) {
	s.det.End(esn, err)
	s.conv.Close(esn)
}

type wakeFunc func(wakeword.Detection)

func (f wakeFunc) OnWake(d wakeword.Detection) { f(d) }

// readAPIConfig loads wire-pod's settings (LLM provider, key, prompt) without writing them back, as
// vars.ReadConfig would.
func readAPIConfig(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &vars.APIConfig)
}

// clockPoint ties a PC time to a mic sample position in the recording (MicTap's _clock.jsonl).
type clockPoint struct {
	UnixMs     int64 `json:"unix_ms"`
	MicSamples int64 `json:"mic_samples"`
}

type clockFile []clockPoint

func readClock(path string) (clockFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c clockFile
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		var p clockPoint
		if err := json.Unmarshal(lines.Bytes(), &p); err == nil {
			c = append(c, p)
		}
	}
	if len(c) == 0 {
		return nil, fmt.Errorf("%s: no clock points", path)
	}
	return c, lines.Err()
}

// position returns the mic sample position heard at PC time unixMs.
func (c clockFile) position(unixMs int64, rate int) int {
	i := sort.Search(len(c), func(i int) bool { return c[i].UnixMs > unixMs }) - 1
	if i < 0 {
		i = 0
	}
	return int(c[i].MicSamples + (unixMs-c[i].UnixMs)*int64(rate)/1000)
}

type label struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`

	startTs, endTs uint64 // robot time in the replay
}

func readLabels(path, prefix string) ([]label, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []label
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		var l label
		if err := json.Unmarshal(lines.Bytes(), &l); err == nil && strings.HasPrefix(l.ID, prefix) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out, lines.Err()
}

// heard is one utterance that reached the transcriber.
type heard struct {
	start, end  uint64
	reason      cortex.EndReason
	dur         time.Duration
	text, lang  string
	sttTook     time.Duration
	answer      string
	brainTook   time.Duration
	transcribed bool
}

// recorder is the replay's Robot port and watches the transcriber and brain.
type recorder struct {
	mu      sync.Mutex
	wakes   []uint64
	heard   []*heard
	changed time.Time
}

func (r *recorder) touch() { r.changed = time.Now() }

func (r *recorder) wake(ts uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wakes = append(r.wakes, ts)
	r.touch()
}

func (r *recorder) answered(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := len(r.heard); n > 0 && r.heard[n-1].answer == "" {
		r.heard[n-1].answer = s
	}
	r.touch()
}

func (r *recorder) SendIntentResult(_, intent, params string) error {
	r.answered("intent " + intent + " " + params)
	return nil
}

func (r *recorder) SendKnowledge(_, answer, _ string) error {
	r.answered(fmt.Sprintf("say %q", answer))
	return nil
}

// settle waits until nothing has happened for a while, so the last answers are in.
func (r *recorder) settle(quiet time.Duration) {
	for {
		r.mu.Lock()
		idle := time.Since(r.changed)
		r.mu.Unlock()
		if idle >= quiet {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type recordingTranscriber struct {
	t   cortex.Transcriber
	rec *recorder
}

func (rt recordingTranscriber) Transcribe(ctx context.Context, u cortex.Utterance) (cortex.Transcript, error) {
	began := time.Now()
	t, err := rt.t.Transcribe(ctx, u)
	h := &heard{start: u.Start, end: u.End, reason: u.Reason, dur: u.Duration(), text: t.Text, lang: t.Language,
		sttTook: time.Since(began), transcribed: err == nil}
	if err != nil {
		h.text = "error: " + err.Error()
	}
	rt.rec.mu.Lock()
	rt.rec.heard = append(rt.rec.heard, h)
	rt.rec.touch()
	rt.rec.mu.Unlock()
	return t, err
}

type recordingBrain struct {
	b   cortex.Brain
	rec *recorder
}

func (rb recordingBrain) Answer(ctx context.Context, esn string, t cortex.Transcript) (cortex.Answer, error) {
	began := time.Now()
	a, err := rb.b.Answer(ctx, esn, t)
	rb.rec.mu.Lock()
	if n := len(rb.rec.heard); n > 0 {
		rb.rec.heard[n-1].brainTook = time.Since(began)
		if err != nil {
			rb.rec.heard[n-1].answer = "error: " + err.Error()
		} else if a == (cortex.Answer{}) {
			rb.rec.heard[n-1].answer = "(handled by wire-pod)"
		}
	}
	rb.rec.touch()
	rb.rec.mu.Unlock()
	return a, err
}

// print writes one table row per label: the wake, what was transcribed and what the brain answered.
func (r *recorder) print(labels []label) {
	r.mu.Lock()
	defer r.mu.Unlock()
	within := func(ts uint64, l label) bool { return ts+500 >= l.startTs && ts <= l.endTs+500 }
	fmt.Println("| id | spoken | wake | heard | lang | transcription | answer | stt s | brain s |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|")
	used := map[*heard]bool{}
	for _, l := range labels {
		wake := "-"
		for _, w := range r.wakes {
			if within(w, l) {
				wake = fmt.Sprintf("%.1f", (float64(w)-float64(l.startTs))/1000)
				break
			}
		}
		row := fmt.Sprintf("| %s | %s | %s | - | - | - | - | - | - |", l.ID, l.Text, wake)
		for _, h := range r.heard {
			if !used[h] && within(h.start, l) {
				used[h] = true
				row = fmt.Sprintf("| %s | %s | %s | %.1f s %s | %s | %s | %s | %.2f | %.2f |", l.ID, l.Text, wake,
					h.dur.Seconds(), h.reason, h.lang, h.text, h.answer, h.sttTook.Seconds(), h.brainTook.Seconds())
				break
			}
		}
		fmt.Println(row)
	}
	for _, h := range r.heard {
		if !used[h] {
			fmt.Printf("| ? | (outside the labels, %.1f s) | | %.1f s %s | %s | %s | %s | %.2f | %.2f |\n",
				float64(h.start)/1000, h.dur.Seconds(), h.reason, h.lang, h.text, h.answer, h.sttTook.Seconds(),
				h.brainTook.Seconds())
		}
	}
	fmt.Printf("%d labels, %d wakes, %d utterances transcribed\n", len(labels), len(r.wakes), len(r.heard))
}
