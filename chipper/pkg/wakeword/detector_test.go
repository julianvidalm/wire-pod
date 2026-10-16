package wakeword

import (
	"errors"
	"sync"
	"testing"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap"
)

// scriptedRecognizer returns the next scripted result once it has received `every` samples since the previous
// one, and records every sample it was given.
type scriptedRecognizer struct {
	mu       sync.Mutex
	results  []result
	every    int
	pending  int
	received []int16
	closed   bool
}

type result struct {
	words []Word
	final bool
}

func final(ws ...string) result   { return result{words: words(ws...), final: true} }
func partial(ws ...string) result { return result{words: words(ws...)} }

func (r *scriptedRecognizer) Accept(pcm []int16) ([]Word, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.received = append(r.received, pcm...)
	r.pending += len(pcm)
	if r.pending < r.every || len(r.results) == 0 {
		return nil, false, nil
	}
	r.pending = 0
	next := r.results[0]
	r.results = r.results[1:]
	return next.words, next.final, nil
}

func (r *scriptedRecognizer) Close() { r.mu.Lock(); r.closed = true; r.mu.Unlock() }

type recordingHandler struct {
	mu   sync.Mutex
	wake []Detection
}

func (h *recordingHandler) OnWake(d Detection) {
	h.mu.Lock()
	h.wake = append(h.wake, d)
	h.mu.Unlock()
}

// early returns the detections made while the user was still talking (not the final, whole-utterance ones).
func early(h *recordingHandler) []Detection {
	var out []Detection
	for _, d := range h.wake {
		if !d.Final {
			out = append(out, d)
		}
	}
	return out
}

func micFrame(seq uint32, flags uint8, value int16) mictap.Frame {
	payload := make([]byte, 2*samplesPerFrame)
	for i := 0; i < samplesPerFrame; i++ {
		payload[2*i] = byte(uint16(value))
		payload[2*i+1] = byte(uint16(value) >> 8)
	}
	return mictap.Frame{Type: mictap.FrameMic, Seq: seq, Timestamp: uint64(seq) * 10, Flags: flags, Payload: payload}
}

func newDetector(t *testing.T, rec *scriptedRecognizer, h Handler, cfg Config) *Detector {
	t.Helper()
	cfg.NewRecognizer = func() (Recognizer, error) { return rec, nil }
	cfg.Handler = h
	if cfg.Matcher.words == nil {
		cfg.Matcher = NewMatcher(DefaultWords, 0)
	}
	cfg.Logf = t.Log
	return New(cfg)
}

var hello = mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000}

func TestDetectorReportsWakeWordWithItsUtterance(t *testing.T) {
	rec := &scriptedRecognizer{every: samplesPerFrame, results: []result{final("hola", "cómo", "estás", "vector")}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{})
	d.Start("esn1", hello)
	d.Frame("esn1", micFrame(0, 0, 100))
	d.End("esn1", nil)
	if len(early(h)) != 1 {
		t.Fatalf("detections %+v", h.wake)
	}
	got := early(h)[0]
	if got.ESN != "esn1" || got.Word.Text != "vector" || got.Text != "hola cómo estás vector" || got.FrameTimestamp != 0 {
		t.Fatalf("detection %+v", got)
	}
	if !rec.closed {
		t.Fatal("recognizer not closed at End")
	}
}

func TestDetectorIgnoresUtterancesWithoutTheName(t *testing.T) {
	rec := &scriptedRecognizer{every: samplesPerFrame, results: []result{final("director", "de", "orquesta")}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{})
	d.Start("esn1", hello)
	d.Frame("esn1", micFrame(0, 0, 100))
	if len(h.wake) != 0 {
		t.Fatalf("unexpected detections %+v", h.wake)
	}
}

func TestDetectorFeedsSilenceWhileVectorSpeaksOrMovesAndForTheHoldoff(t *testing.T) {
	rec := &scriptedRecognizer{every: 1 << 30}
	d := newDetector(t, rec, &recordingHandler{}, Config{HoldoffMs: 20, GateOnMotion: true})
	d.Start("esn1", hello)
	d.Frame("esn1", micFrame(0, 0, 7))                           // heard
	d.Frame("esn1", micFrame(1, mictap.MicFlagSpeakerActive, 7)) // Vector speaking: silence
	d.Frame("esn1", micFrame(2, mictap.MicFlagRobotMoving, 7))   // Vector moving: silence
	d.Frame("esn1", micFrame(3, 0, 7))                           // 10 ms after the gate: still in the holdoff
	d.Frame("esn1", micFrame(4, 0, 7))                           // 20 ms after: heard again
	want := []int16{7, 0, 0, 0, 7}
	if len(rec.received) != len(want)*samplesPerFrame {
		t.Fatalf("received %d samples", len(rec.received))
	}
	for i, v := range want {
		if got := rec.received[i*samplesPerFrame]; got != v {
			t.Errorf("frame %d: sample %d, want %d", i, got, v)
		}
	}
}

func TestDetectorCooldownSuppressesRepeats(t *testing.T) {
	rec := &scriptedRecognizer{every: samplesPerFrame, results: []result{final("vector"), final("vector"), final("vector")}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{CooldownMs: 15})
	d.Start("esn1", hello)
	d.Frame("esn1", micFrame(0, 0, 1)) // t=0: wake
	d.Frame("esn1", micFrame(1, 0, 1)) // t=10 ms: within the cooldown
	d.Frame("esn1", micFrame(2, 0, 1)) // t=20 ms: wake again
	if w := early(h); len(w) != 2 || w[1].FrameTimestamp != 20 {
		t.Fatalf("detections %+v", h.wake)
	}
}

func TestDetectorSkipsStreamsWithAnotherMicRate(t *testing.T) {
	created := 0
	d := New(Config{
		NewRecognizer: func() (Recognizer, error) { created++; return &scriptedRecognizer{}, nil },
		Matcher:       NewMatcher(DefaultWords, 0),
		Handler:       &recordingHandler{},
		Logf:          t.Log,
	})
	d.Start("esn1", mictap.Hello{Version: 1, MicRate: 8000})
	d.Frame("esn1", micFrame(0, 0, 1))
	d.End("esn1", nil)
	if created != 0 {
		t.Fatalf("created %d recognizers for an 8 kHz stream", created)
	}
}

func TestDetectorSurvivesRecognizerCreationFailure(t *testing.T) {
	d := New(Config{
		NewRecognizer: func() (Recognizer, error) { return nil, errors.New("no model") },
		Matcher:       NewMatcher(DefaultWords, 0),
		Handler:       &recordingHandler{},
		Logf:          t.Log,
	})
	d.Start("esn1", hello)
	d.Frame("esn1", micFrame(0, 0, 1))
	d.End("esn1", nil)
}

func TestDetectorKeepsRobotsApart(t *testing.T) {
	recs := map[string]*scriptedRecognizer{}
	var mu sync.Mutex
	h := &recordingHandler{}
	order := []string{"a", "b"}
	i := 0
	d := New(Config{
		NewRecognizer: func() (Recognizer, error) {
			mu.Lock()
			defer mu.Unlock()
			r := &scriptedRecognizer{every: samplesPerFrame}
			if order[i] == "b" {
				r.results = []result{final("vector")}
			}
			recs[order[i]] = r
			i++
			return r, nil
		},
		Matcher: NewMatcher(DefaultWords, 0),
		Handler: h,
		Logf:    t.Log,
	})
	d.Start("a", hello)
	d.Start("b", hello)
	d.Frame("a", micFrame(0, 0, 1))
	d.Frame("b", micFrame(0, 0, 1))
	if w := early(h); len(w) != 1 || w[0].ESN != "b" {
		t.Fatalf("detections %+v", h.wake)
	}
}

func TestDetectorWakesOnPartialResultsOnceperWord(t *testing.T) {
	// "vector" shows up in a partial result while the user keeps talking; the final result of the same
	// utterance repeats it and must not wake the robot again.
	rec := &scriptedRecognizer{every: samplesPerFrame, results: []result{
		partial("hola"),
		partial("hola", "cómo", "estás", "vector"),
		partial("hola", "cómo", "estás", "vector", "qué"),
		final("hola", "cómo", "estás", "vector", "qué", "hora", "es"),
	}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{})
	d.Start("esn1", hello)
	for seq := uint32(0); seq < 4; seq++ {
		d.Frame("esn1", micFrame(seq, 0, 1))
	}
	if w := early(h); len(w) != 1 || w[0].FrameTimestamp != 10 || w[0].Text != "hola cómo estás vector" {
		t.Fatalf("detections %+v", h.wake)
	}
}

func TestDetectorWakesAgainForASecondNameInTheSameUtterance(t *testing.T) {
	second := words("vector", "y", "víctor")
	second[2].Start = 5 // a later, distinct word
	rec := &scriptedRecognizer{every: samplesPerFrame, results: []result{
		partial("vector"),
		{words: second},
	}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{})
	d.Start("esn1", hello)
	d.Frame("esn1", micFrame(0, 0, 1))
	d.Frame("esn1", micFrame(1, 0, 1))
	if w := early(h); len(w) != 2 || w[1].Word.Text != "víctor" {
		t.Fatalf("detections %+v", h.wake)
	}
}

func TestDetectorIgnoresLowConfidencePartials(t *testing.T) {
	low := words("sin", "vector")
	low[1].Conf = 0.54 // "sin nombre tuyo" heard as "sin vector" in the T1 recordings
	rec := &scriptedRecognizer{every: samplesPerFrame, results: []result{{words: low}}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{Matcher: NewMatcher(DefaultWords, DefaultMinConfidence)})
	d.Start("esn1", hello)
	d.Frame("esn1", micFrame(0, 0, 1))
	if len(h.wake) != 0 {
		t.Fatalf("detections %+v", h.wake)
	}
}

// frameValue makes each mic frame's samples equal to its seq, so an extracted utterance shows which frames it
// covers.
func frameValue(seq uint32) mictap.Frame { return micFrame(seq, 0, int16(seq)) }

func timed(text string, start, end float64) Word {
	return Word{Text: text, Conf: 1, Start: start, End: end}
}

func TestFinalResultCarriesTheWholeUtterance(t *testing.T) {
	// 100 frames = 1 s of audio; the final result arrives with the last one. The words span 0.40-0.70 s, so
	// with the 0.3 s margins the utterance covers 0.10-1.00 s: frames 10..99.
	final := result{final: true, words: []Word{timed("hola", 0.40, 0.50), timed("vector", 0.55, 0.70)}}
	rec := &scriptedRecognizer{every: 100 * samplesPerFrame, results: []result{final}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{})
	d.Start("esn1", hello)
	for seq := uint32(0); seq < 100; seq++ {
		d.Frame("esn1", frameValue(seq))
	}
	var finals []Detection
	for _, w := range h.wake {
		if w.Final {
			finals = append(finals, w)
		}
	}
	if len(finals) != 1 {
		t.Fatalf("final detections %+v", h.wake)
	}
	u := finals[0].Utterance
	if len(u) != 90*samplesPerFrame || u[0] != 10 || u[len(u)-1] != 99 {
		t.Fatalf("utterance %d samples, first %d, last %d", len(u), u[0], u[len(u)-1])
	}
	if finals[0].Text != "hola vector" {
		t.Fatalf("text %q", finals[0].Text)
	}
}

func TestEarlyAndFinalDetectionsForOneUtterance(t *testing.T) {
	rec := &scriptedRecognizer{every: samplesPerFrame, results: []result{
		{words: []Word{timed("vector", 0, 0.01)}},
		{final: true, words: []Word{timed("vector", 0, 0.01), timed("hola", 0.01, 0.02)}},
	}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{})
	d.Start("esn1", hello)
	d.Frame("esn1", frameValue(0))
	d.Frame("esn1", frameValue(1))
	if len(h.wake) != 2 || h.wake[0].Final || !h.wake[1].Final || h.wake[1].Utterance == nil {
		t.Fatalf("detections %+v", h.wake)
	}
}

func TestFinalWithoutTheNameIsNotADetection(t *testing.T) {
	rec := &scriptedRecognizer{every: samplesPerFrame, results: []result{
		{final: true, words: []Word{timed("hola", 0, 0.01)}},
	}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{})
	d.Start("esn1", hello)
	d.Frame("esn1", frameValue(0))
	if len(h.wake) != 0 {
		t.Fatalf("detections %+v", h.wake)
	}
}

func TestUtteranceOlderThanTheBufferIsClampedToWhatIsKept(t *testing.T) {
	// The final claims the utterance started at 0 s, but only the last maxUtteranceSeconds are kept.
	frames := uint32(maxUtteranceSeconds*100 + 50)
	end := float64(frames) / 100
	final := result{final: true, words: []Word{timed("vector", 0, end)}}
	rec := &scriptedRecognizer{every: int(frames) * samplesPerFrame, results: []result{final}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{})
	d.Start("esn1", hello)
	for seq := uint32(0); seq < frames; seq++ {
		d.Frame("esn1", frameValue(seq))
	}
	u := h.wake[len(h.wake)-1].Utterance
	if len(u) != maxUtteranceSeconds*micRate || u[0] != int16(frames-maxUtteranceSeconds*100) {
		t.Fatalf("utterance %d samples starting at frame %d", len(u), u[0])
	}
}

func TestUtteranceKeepsTheRealAudioOfGatedFrames(t *testing.T) {
	// Gating hides Vector's own voice and motors from the name detector, but the user's sentence often
	// overlaps them; the better recognizer gets the real audio, not the silence the detector heard.
	final := result{final: true, words: []Word{timed("vector", 0.30, 0.70)}}
	rec := &scriptedRecognizer{every: 100 * samplesPerFrame, results: []result{final}}
	h := &recordingHandler{}
	d := newDetector(t, rec, h, Config{HoldoffMs: 0, GateOnMotion: true})
	d.Start("esn1", hello)
	for seq := uint32(0); seq < 100; seq++ {
		var flags uint8
		if seq >= 40 && seq < 60 {
			flags = mictap.MicFlagRobotMoving
		}
		d.Frame("esn1", micFrame(seq, flags, int16(seq+1)))
	}
	u := h.wake[len(h.wake)-1].Utterance
	for i, v := range u {
		if v == 0 {
			t.Fatalf("sample %d of the utterance is silence: gated audio was not kept", i)
		}
	}
}

// speakerFrame is one speaker buffer (1024 samples at 32 kHz, 32 ms) of constant value, at robot time ms.
func speakerFrame(seq uint32, ms uint64, value int16) mictap.Frame {
	payload := make([]byte, 2*1024)
	for i := 0; i < 1024; i++ {
		payload[2*i] = byte(uint16(value))
		payload[2*i+1] = byte(uint16(value) >> 8)
	}
	return mictap.Frame{Type: mictap.FrameSpeaker, Seq: seq, Timestamp: ms, Flags: 1, Payload: payload}
}

// heardFrames returns, per mic frame given to the recognizer, whether it was the real audio (true) or silence.
func heardFrames(rec *scriptedRecognizer) []bool {
	var out []bool
	for i := 0; i+samplesPerFrame <= len(rec.received); i += samplesPerFrame {
		out = append(out, rec.received[i] != 0)
	}
	return out
}

func TestSilentSpeakerOutputDoesNotGateDespiteTheFlag(t *testing.T) {
	// vic-anim flags "speaker active" whenever its audio sink is open, which in idle is most of the time,
	// even when it plays silence. The speaker audio itself tells whether Vector is making any sound.
	rec := &scriptedRecognizer{every: 1 << 30}
	d := newDetector(t, rec, &recordingHandler{}, Config{HoldoffMs: 300})
	d.Start("esn1", hello)
	for seq := uint32(0); seq < 20; seq++ {
		if seq%3 == 0 {
			d.Frame("esn1", speakerFrame(seq/3, uint64(seq)*10, 0))
		}
		d.Frame("esn1", micFrame(seq, mictap.MicFlagSpeakerActive, 7))
	}
	for i, real := range heardFrames(rec) {
		if !real {
			t.Fatalf("mic frame %d was gated while the speaker played silence", i)
		}
	}
}

func TestAudibleSpeakerOutputGatesUntilTheHoldoffAfterIt(t *testing.T) {
	rec := &scriptedRecognizer{every: 1 << 30}
	d := newDetector(t, rec, &recordingHandler{}, Config{HoldoffMs: 300})
	d.Start("esn1", hello)
	d.Frame("esn1", speakerFrame(0, 0, 0))
	// Vector says something at 100 ms (one 32 ms buffer), then is silent again.
	for seq := uint32(0); seq < 60; seq++ {
		if seq == 10 {
			d.Frame("esn1", speakerFrame(1, 100, 3000))
		}
		if seq == 14 {
			d.Frame("esn1", speakerFrame(2, 132, 0))
		}
		d.Frame("esn1", micFrame(seq, mictap.MicFlagSpeakerActive, 7))
	}
	heard := heardFrames(rec)
	// Gated from 100 ms to the end of the loud buffer (132 ms) plus the 300 ms holdoff: frames 10..43.
	for i, real := range heard {
		if want := i < 10 || i >= 44; real != want {
			t.Fatalf("mic frame %d heard=%v, want %v (all: %v)", i, real, want, heard)
		}
	}
}

func TestMotionDoesNotGateByDefault(t *testing.T) {
	// Vector turns its head toward whoever talks; gating on motion hid half of the user's sentence, and its
	// motors do not sound like its name.
	rec := &scriptedRecognizer{every: 1 << 30}
	d := newDetector(t, rec, &recordingHandler{}, Config{HoldoffMs: 300})
	d.Start("esn1", hello)
	for seq := uint32(0); seq < 10; seq++ {
		d.Frame("esn1", micFrame(seq, mictap.MicFlagRobotMoving, 7))
	}
	for i, real := range heardFrames(rec) {
		if !real {
			t.Fatalf("mic frame %d was gated for motion", i)
		}
	}
}
