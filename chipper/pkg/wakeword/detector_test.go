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
	if len(h.wake) != 1 {
		t.Fatalf("detections %+v", h.wake)
	}
	got := h.wake[0]
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
	d := newDetector(t, rec, &recordingHandler{}, Config{HoldoffMs: 20})
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
	if len(h.wake) != 2 || h.wake[1].FrameTimestamp != 20 {
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
	if len(h.wake) != 1 || h.wake[0].ESN != "b" {
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
	if len(h.wake) != 1 || h.wake[0].FrameTimestamp != 10 || h.wake[0].Text != "hola cómo estás vector" {
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
	if len(h.wake) != 2 || h.wake[1].Word.Text != "víctor" {
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
