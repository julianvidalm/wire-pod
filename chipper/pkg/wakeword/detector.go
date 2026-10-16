package wakeword

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap"
)

const (
	micRate         = 16000
	samplesPerFrame = 160 // one MicTap mic frame: 10 ms at 16 kHz

	// maxUtteranceSeconds of audio are kept per robot, enough for a long sentence around the name.
	maxUtteranceSeconds = 15
	// utteranceMarginSeconds is added before the first and after the last recognized word.
	utteranceMarginSeconds = 0.3
)

// Recognizer turns 16 kHz mono speech into words. Accept returns the words recognized so far in the current
// utterance (a partial result, which may still change) or, with final true, the words of an utterance the
// recognizer has just closed (Vosk: after a pause). It may return nil when it has nothing new.
type Recognizer interface {
	Accept(pcm []int16) (words []Word, final bool, err error)
	Close()
}

// sameWordSeconds: a partial and the final result place the same spoken word within this many seconds.
const sameWordSeconds = 0.3

type RecognizerFactory func() (Recognizer, error)

// Detection is one wake: the robot, the matched word, the recognized text, and the robot timestamp (ms) of the
// mic frame that completed it. Early detections come from partial results while the user is still talking;
// a Final detection comes once the recognizer closes the utterance and carries its audio (16 kHz mono), so
// the whole sentence can be transcribed again with a better recognizer.
type Detection struct {
	ESN            string
	Word           Word
	Text           string
	FrameTimestamp uint64
	Final          bool
	Utterance      []int16
}

// Handler acts on a detection (for now: log it; later: start a conversation).
type Handler interface {
	OnWake(d Detection)
}

type Config struct {
	NewRecognizer RecognizerFactory
	Matcher       Matcher
	Handler       Handler
	// HoldoffMs keeps feeding silence this long after Vector stopped speaking or moving, so the tail of its own
	// voice (echo) and motor noise cannot wake it (echo handling level 1).
	HoldoffMs uint64
	// CooldownMs ignores further detections for this long after one, so one call does not wake the robot twice.
	CooldownMs uint64
	Logf       func(a ...any)
	// Debug logs every utterance the recognizer closes, with how many frames it got and how many were gated,
	// to diagnose missed names.
	Debug bool
}

// Detector implements mictap.Sink: one recognizer per robot stream.
type Detector struct {
	cfg      Config
	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	mu         sync.Mutex
	rec        Recognizer
	pcm        []int16
	silence    []int16
	gatedUntil uint64 // robot ms
	lastWake   uint64
	woke       bool
	seen       []float64 // start times of names already handled in the current utterance
	ring       []int16   // the last maxUtteranceSeconds of audio given to the recognizer
	frames     int       // mic frames since the last final result (Debug)
	gated      int       // of those, replaced by silence for the recognizer (Debug)
	speaking   int       // of those, flagged as Vector speaking (Debug)
	moving     int       // of those, flagged as Vector moving (Debug)
	fed        int       // samples given to the recognizer since it started (the clock of its word times)
}

// record keeps audio for utterance extraction.
func (s *session) record(audio []int16) {
	for _, v := range audio {
		s.ring[s.fed%len(s.ring)] = v
		s.fed++
	}
}

// utterance returns the recorded audio between two recognizer times (seconds), clamped to what is kept.
func (s *session) utterance(start, end float64) []int16 {
	from := max(int(start*micRate), s.fed-len(s.ring), 0)
	to := min(int(end*micRate), s.fed)
	if from >= to {
		return nil
	}
	out := make([]int16, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, s.ring[i%len(s.ring)])
	}
	return out
}

func New(cfg Config) *Detector {
	if cfg.Logf == nil {
		cfg.Logf = func(...any) {}
	}
	return &Detector{cfg: cfg, sessions: map[string]*session{}}
}

func (d *Detector) Start(esn string, h mictap.Hello) {
	if h.MicRate != micRate {
		d.cfg.Logf("wakeword: ", esn, " sends ", h.MicRate, " Hz mic audio; only 16000 Hz is supported")
		return
	}
	rec, err := d.cfg.NewRecognizer()
	if err != nil {
		d.cfg.Logf("wakeword: no recognizer for ", esn, ": ", err)
		return
	}
	d.mu.Lock()
	if old := d.sessions[esn]; old != nil {
		old.rec.Close()
	}
	d.sessions[esn] = &session{rec: rec, pcm: make([]int16, samplesPerFrame), silence: make([]int16, samplesPerFrame),
		ring: make([]int16, maxUtteranceSeconds*micRate)}
	d.mu.Unlock()
}

func (d *Detector) Frame(esn string, f mictap.Frame) {
	if f.Type != mictap.FrameMic || len(f.Payload) != 2*samplesPerFrame {
		return
	}
	d.mu.Lock()
	s := d.sessions[esn]
	d.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.pcm {
		s.pcm[i] = int16(binary.LittleEndian.Uint16(f.Payload[2*i:]))
	}
	// The utterance keeps the real audio: the user's sentence often overlaps Vector's voice or motors, and the
	// better recognizer that transcribes it copes with them. Only the name detector hears silence there.
	s.record(s.pcm)
	audio := s.pcm
	s.frames++
	if f.Flags&mictap.MicFlagSpeakerActive != 0 {
		s.speaking++
	}
	if f.Flags&mictap.MicFlagRobotMoving != 0 {
		s.moving++
	}
	if f.Flags&(mictap.MicFlagSpeakerActive|mictap.MicFlagRobotMoving) != 0 {
		s.gatedUntil = f.Timestamp + d.cfg.HoldoffMs
		audio = s.silence
		s.gated++
	} else if f.Timestamp < s.gatedUntil {
		audio = s.silence
		s.gated++
	}
	utterance, final, err := s.rec.Accept(audio)
	if err != nil {
		d.cfg.Logf("wakeword: ", esn, ": ", err)
		return
	}
	// Partial results catch the name while the user is still talking; each spoken name is handled once,
	// even though later partials and the final result of the same utterance repeat it.
	for _, w := range d.cfg.Matcher.MatchAll(utterance) {
		if s.handled(w) {
			continue
		}
		s.seen = append(s.seen, w.Start)
		if s.woke && f.Timestamp < s.lastWake+d.cfg.CooldownMs {
			continue
		}
		s.woke, s.lastWake = true, f.Timestamp
		d.cfg.Handler.OnWake(Detection{ESN: esn, Word: w, Text: text(utterance), FrameTimestamp: f.Timestamp})
	}
	if final {
		if d.cfg.Debug {
			d.cfg.Logf(fmt.Sprintf("wakeword: %s heard %q (%d frames, %d gated: %d speaking, %d moving)",
				esn, text(utterance), s.frames, s.gated, s.speaking, s.moving))
		}
		s.frames, s.gated, s.speaking, s.moving = 0, 0, 0, 0
		s.seen = s.seen[:0]
		if names := d.cfg.Matcher.MatchAll(utterance); len(names) > 0 {
			first, last := utterance[0], utterance[len(utterance)-1]
			d.cfg.Handler.OnWake(Detection{ESN: esn, Word: names[0], Text: text(utterance), FrameTimestamp: f.Timestamp,
				Final: true, Utterance: s.utterance(first.Start-utteranceMarginSeconds, last.End+utteranceMarginSeconds)})
		}
	}
}

func (s *session) handled(w Word) bool {
	for _, start := range s.seen {
		if w.Start-start < sameWordSeconds && start-w.Start < sameWordSeconds {
			return true
		}
	}
	return false
}

func (d *Detector) End(esn string, _ error) {
	d.mu.Lock()
	s := d.sessions[esn]
	delete(d.sessions, esn)
	d.mu.Unlock()
	if s != nil {
		s.mu.Lock()
		s.rec.Close()
		s.mu.Unlock()
	}
}

func text(utterance []Word) string {
	parts := make([]string, len(utterance))
	for i, w := range utterance {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}
