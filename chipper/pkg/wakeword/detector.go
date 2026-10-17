package wakeword

import (
	"encoding/binary"
	"strings"
	"sync"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
)

const (
	micRate         = 16000
	samplesPerFrame = 160 // one MicTap mic frame: 10 ms at 16 kHz
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

// Detection is one wake: the robot, the matched word, the whole utterance, and the robot timestamp (ms) of the
// mic frame that completed it.
type Detection struct {
	ESN            string
	Word           Word
	Text           string
	FrameTimestamp uint64
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
	d.sessions[esn] = &session{rec: rec, pcm: make([]int16, samplesPerFrame), silence: make([]int16, samplesPerFrame)}
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
	audio := s.pcm
	if f.Flags&(mictap.MicFlagSpeakerActive|mictap.MicFlagRobotMoving) != 0 {
		s.gatedUntil = f.Timestamp + d.cfg.HoldoffMs
		audio = s.silence
	} else if f.Timestamp < s.gatedUntil {
		audio = s.silence
	} else {
		for i := range s.pcm {
			s.pcm[i] = int16(binary.LittleEndian.Uint16(f.Payload[2*i:]))
		}
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
		s.seen = s.seen[:0]
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
