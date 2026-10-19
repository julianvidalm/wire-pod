// Package mictapgate is the MicTap adapter of the cortex half-duplex gate (cortex.HalfDuplex): a mictap.Sink
// that hands its inner sink every robot's stream with the mic frames heard while the robot talked replaced by
// zeros of the same length. Speaker and other frames pass through unchanged; mic frames may reach the inner
// sink up to the gate's MaxDelay later than they arrived, always in order. Anything that must keep the real
// audio (the recorder) sits beside this sink, not behind it.
package mictapgate

import (
	"fmt"
	"sync"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
)

// Sink gates the mic frames of every stream it receives before passing them to its inner sink.
type Sink struct {
	inner   mictap.Sink
	cfg     cortex.HalfDuplexConfig
	micRate int
	logf    func(a ...any)

	mu      sync.Mutex
	streams map[string]*stream
}

type stream struct {
	mu          sync.Mutex
	gate        *cortex.HalfDuplex[micFrame]
	speakerRate int
}

// New returns the gate in front of inner; logf gets one summary line per stream when it ends, with times at
// micRate, the mic's true rate.
func New(inner mictap.Sink, cfg cortex.HalfDuplexConfig, micRate int, logf func(a ...any)) *Sink {
	return &Sink{inner: inner, cfg: cfg, micRate: micRate, logf: logf, streams: map[string]*stream{}}
}

func (s *Sink) Start(esn string, h mictap.Hello) {
	st := &stream{gate: cortex.NewHalfDuplex[micFrame](s.cfg), speakerRate: int(h.SpeakerRate)}
	s.mu.Lock()
	s.streams[esn] = st
	s.mu.Unlock()
	s.inner.Start(esn, h)
}

func (s *Sink) stream(esn string) *stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[esn]
}

func (s *Sink) Frame(esn string, f mictap.Frame) {
	st := s.stream(esn)
	if st == nil {
		s.inner.Frame(esn, f) // no Start: nothing to gate against
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []micFrame
	switch f.Type {
	case mictap.FrameMic:
		out = st.gate.Mic(micFrame(f))
	case mictap.FrameSpeaker:
		s.inner.Frame(esn, f)
		out = st.gate.Speaker(f.Timestamp, f.Samples(), st.speakerRate)
	default:
		s.inner.Frame(esn, f)
	}
	for _, m := range out {
		s.inner.Frame(esn, mictap.Frame(m))
	}
}

func (s *Sink) End(esn string, err error) {
	s.mu.Lock()
	st := s.streams[esn]
	delete(s.streams, esn)
	s.mu.Unlock()
	if st != nil {
		st.mu.Lock()
		for _, m := range st.gate.Flush() {
			s.inner.Frame(esn, mictap.Frame(m))
		}
		s.logf(Summary(esn, st.gate.Stats(), s.micRate))
		st.mu.Unlock()
	}
	s.inner.End(esn, err)
}

// Summary is the one line logged per stream (mic frames hold 160 samples at micRate).
func Summary(esn string, st cortex.HalfDuplexStats, micRate int) string {
	sec := func(frames int) float64 { return float64(frames*160) / float64(max(micRate, 1)) }
	return fmt.Sprintf("Cortex half duplex: %s silenced %d spans, %.1f s of %.1f s of mic audio "+
		"(%d mic frames released before their speaker audio arrived, %d speaker sounds arrived too late)",
		esn, st.Spans, sec(st.GatedFrames), sec(st.Frames), st.Unconfirmed, st.Late)
}

// micFrame is a MicTap mic frame as the gate holds it.
type micFrame mictap.Frame

func (m micFrame) RobotTime() uint64 { return m.Timestamp }

func (m micFrame) Silenced() micFrame {
	m.Payload = make([]byte, len(m.Payload))
	return m
}
