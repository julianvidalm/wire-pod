package mictapgate

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
)

type capture struct {
	mu     sync.Mutex
	frames []mictap.Frame
	ended  bool
	start  bool
}

func (c *capture) Start(string, mictap.Hello) { c.start = true }
func (c *capture) Frame(_ string, f mictap.Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames = append(c.frames, f)
}
func (c *capture) End(string, error) { c.ended = true }

func (c *capture) mic() []mictap.Frame {
	var out []mictap.Frame
	for _, f := range c.frames {
		if f.Type == mictap.FrameMic {
			out = append(out, f)
		}
	}
	return out
}

func payload(n int, v int16) []byte {
	p := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(p[2*i:], uint16(v))
	}
	return p
}

func zero(p []byte) bool {
	for _, b := range p {
		if b != 0 {
			return false
		}
	}
	return true
}

func TestSinkSilencesMicFramesWhileTheRobotTalks(t *testing.T) {
	inner := &capture{}
	var lines []string
	s := New(inner, cortex.DefaultHalfDuplexConfig(), 16000, func(a ...any) { lines = append(lines, fmt.Sprint(a...)) })
	s.Start("0dd1b652", mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	spk := uint64(0)
	for ts := uint64(0); ts < 3000; ts += 10 {
		for spk <= ts {
			level := int16(0)
			if spk >= 512 && spk < 1024 {
				level = 8000
			}
			s.Frame("0dd1b652", mictap.Frame{Type: mictap.FrameSpeaker, Flags: 1, Timestamp: spk, Payload: payload(1024, level)})
			spk += 32
		}
		s.Frame("0dd1b652", mictap.Frame{Type: mictap.FrameMic, Flags: 1, Seq: uint32(ts / 10), Timestamp: ts, Payload: payload(160, 300)})
	}
	s.End("0dd1b652", nil)
	mic := inner.mic()
	if len(mic) != 300 || !inner.start || !inner.ended {
		t.Fatalf("%d mic frames, start %v, end %v", len(mic), inner.start, inner.ended)
	}
	for i, f := range mic {
		want := f.Timestamp >= 512 && f.Timestamp <= 1024+600
		if f.Seq != uint32(i) || f.Flags != 1 || len(f.Payload) != 320 || zero(f.Payload) != want {
			t.Fatalf("mic %d at %d ms: seq %d flags %d %d bytes zero %v, want zero %v", i, f.Timestamp, f.Seq, f.Flags,
				len(f.Payload), zero(f.Payload), want)
		}
	}
	if len(inner.frames)-len(mic) != 94 {
		t.Fatalf("%d speaker frames passed on, want 94", len(inner.frames)-len(mic))
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "0dd1b652 silenced 1 spans, 1.1 s of 3.0 s of mic audio") {
		t.Fatalf("log %q", lines)
	}
}

func TestSinkDoesNotChangeTheCallersPayload(t *testing.T) {
	inner := &capture{}
	s := New(inner, cortex.DefaultHalfDuplexConfig(), 16000, func(...any) {})
	s.Start("a", mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	s.Frame("a", mictap.Frame{Type: mictap.FrameSpeaker, Timestamp: 0, Payload: payload(1024, 8000)})
	s.Frame("a", mictap.Frame{Type: mictap.FrameSpeaker, Timestamp: 32, Payload: payload(1024, 8000)})
	p := payload(160, 300)
	s.Frame("a", mictap.Frame{Type: mictap.FrameMic, Timestamp: 10, Payload: p})
	if zero(p) {
		t.Fatal("the gate zeroed the caller's payload (the recorder shares it)")
	}
	if m := inner.mic(); len(m) != 1 || !zero(m[0].Payload) {
		t.Fatalf("inner got %v", m)
	}
}

// A robot that streams no speaker audio (SpeakerRate 0 in its hello) is not gated: mic frames pass at once.
func TestSinkPassesTheMicThroughWithoutASpeakerStream(t *testing.T) {
	inner := &capture{}
	s := New(inner, cortex.DefaultHalfDuplexConfig(), 16000, func(...any) {})
	s.Start("a", mictap.Hello{Version: 1, MicRate: 16000})
	for ts := uint64(0); ts < 100; ts += 10 {
		s.Frame("a", mictap.Frame{Type: mictap.FrameMic, Timestamp: ts, Payload: payload(160, 300)})
		if m := inner.mic(); len(m) != int(ts/10)+1 || zero(m[len(m)-1].Payload) {
			t.Fatalf("after mic %d ms the inner sink has %d frames", ts, len(m))
		}
	}
	s.End("a", nil)
}
