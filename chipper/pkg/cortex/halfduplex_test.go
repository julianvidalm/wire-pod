package cortex

import (
	"testing"
	"time"
)

const testSpeakerRate = 32000

// speakerFrame returns one 32 ms speaker buffer (1024 samples at 32 kHz) of a constant level.
func speakerFrame(level int16) []int16 {
	s := make([]int16, 1024)
	for i := range s {
		s[i] = level
	}
	return s
}

func mic(ts uint64) Frame {
	s := make([]int16, 160)
	for i := range s {
		s[i] = 500
	}
	return Frame{Samples: s, Timestamp: ts}
}

func silent(f Frame) bool {
	for _, s := range f.Samples {
		if s != 0 {
			return false
		}
	}
	return true
}

// feedSession gives the gate speaker frames from fromMs to toMs: loud ones (-10 dBFS) when loud, else digital
// silence, interleaving mic frames every 10 ms the way the robot sends them (speaker frames first).
func feedSession(g *HalfDuplex[Frame], toMs uint64, loud func(ts uint64) bool) []Frame {
	var out []Frame
	next := uint64(0)
	for ts := uint64(0); ts < toMs; ts += 10 {
		for next <= ts {
			level := int16(0)
			if loud(next) {
				level = 10000
			}
			out = append(out, g.Speaker(next, speakerFrame(level), testSpeakerRate)...)
			next += 32
		}
		out = append(out, g.Mic(mic(ts))...)
	}
	return append(out, g.Flush()...)
}

func gatedAt(t *testing.T, out []Frame) map[uint64]bool {
	t.Helper()
	got := map[uint64]bool{}
	for i, f := range out {
		if i > 0 && f.Timestamp <= out[i-1].Timestamp {
			t.Fatalf("frame %d at %d ms after %d ms: out of order", i, f.Timestamp, out[i-1].Timestamp)
		}
		if len(f.Samples) != 160 {
			t.Fatalf("frame at %d ms has %d samples, want 160", f.Timestamp, len(f.Samples))
		}
		got[f.Timestamp] = silent(f)
	}
	return got
}

func TestHalfDuplexSilencesTheMicWhileTheSpeakerSoundsByRobotTime(t *testing.T) {
	cfg := DefaultHalfDuplexConfig()
	g := NewHalfDuplex[Frame](cfg)
	// The robot talks from 1024 ms to 2048 ms (speaker frames 32..63).
	out := feedSession(g, 5000, func(ts uint64) bool { return ts >= 1024 && ts < 2048 })
	got := gatedAt(t, out)
	if len(got) != 500 {
		t.Fatalf("%d mic frames out, want all 500", len(got))
	}
	for ts := uint64(0); ts < 5000; ts += 10 {
		want := ts >= 1024 && ts <= 2048+uint64(cfg.Hangover/time.Millisecond)
		if got[ts] != want {
			t.Fatalf("mic at %d ms silenced %v, want %v", ts, got[ts], want)
		}
	}
	st := g.Stats()
	if st.Spans != 1 || st.Frames != 500 || st.GatedFrames != 162 /* 1030..2640 ms */ {
		t.Fatalf("stats %+v", st)
	}
}

func TestHalfDuplexKeepsTheMicOpenOnSilenceAndNoiseBelowTheThreshold(t *testing.T) {
	cfg := DefaultHalfDuplexConfig() // -60 dBFS
	g := NewHalfDuplex[Frame](cfg)
	var out []Frame
	for i, level := range []int16{0, 1, 10, 20} { // digital silence and dither up to -64 dBFS
		for ts := uint64(i * 1000); ts < uint64(i*1000+1000); ts += 10 {
			if ts%32 < 10 {
				out = append(out, g.Speaker(ts, speakerFrame(level), testSpeakerRate)...)
			}
			out = append(out, g.Mic(mic(ts))...)
		}
	}
	out = append(out, g.Flush()...)
	for ts, gated := range gatedAt(t, out) {
		if gated {
			t.Fatalf("mic at %d ms silenced by speaker noise under %v dBFS", ts, cfg.Threshold)
		}
	}
	if st := g.Stats(); st.GatedFrames != 0 || st.Spans != 0 || st.Frames != 400 {
		t.Fatalf("stats %+v", st)
	}
}

func TestHalfDuplexHangoverIsConfigurable(t *testing.T) {
	cfg := DefaultHalfDuplexConfig()
	cfg.Hangover = 200 * time.Millisecond
	g := NewHalfDuplex[Frame](cfg)
	out := feedSession(g, 3000, func(ts uint64) bool { return ts >= 1024 && ts < 1056 }) // one loud frame
	got := gatedAt(t, out)
	for ts := uint64(1000); ts < 1600; ts += 10 {
		want := ts >= 1024 && ts <= 1056+200
		if got[ts] != want {
			t.Fatalf("mic at %d ms silenced %v, want %v (hangover 200 ms)", ts, got[ts], want)
		}
	}
}

// Speaker frames travel on another path in the robot and may reach the cortex after the mic frames they
// overlap: the gate holds the mic until the speaker audio covering it has arrived.
func TestHalfDuplexWaitsForLateSpeakerFrames(t *testing.T) {
	g := NewHalfDuplex[Frame](DefaultHalfDuplexConfig())
	var out []Frame
	for ts := uint64(0); ts < 1000; ts += 10 {
		out = append(out, g.Mic(mic(ts))...)
		if ts >= 150 { // speaker frames arrive 150 ms late
			spk := ts - 150
			if spk%32 < 10 {
				spk -= spk % 32
				level := int16(0)
				if spk >= 320 && spk < 480 {
					level = 10000
				}
				out = append(out, g.Speaker(spk, speakerFrame(level), testSpeakerRate)...)
			}
		}
	}
	out = append(out, g.Flush()...)
	got := gatedAt(t, out)
	if len(got) != 100 {
		t.Fatalf("%d mic frames out, want 100", len(got))
	}
	for ts := uint64(300); ts < 330; ts += 10 {
		if got[ts] != (ts >= 320) {
			t.Fatalf("mic at %d ms silenced %v", ts, got[ts])
		}
	}
	for ts := uint64(320); ts < 1000; ts += 10 {
		if !got[ts] {
			t.Fatalf("mic at %d ms not silenced, though the late speaker frames cover it", ts)
		}
	}
	if st := g.Stats(); st.Unconfirmed != 0 {
		t.Fatalf("stats %+v: frames released before their speaker audio", st)
	}
}

// Without speaker frames (or with them later than MaxDelay) the mic is never held longer than MaxDelay.
func TestHalfDuplexHoldsTheMicAtMostMaxDelay(t *testing.T) {
	cfg := DefaultHalfDuplexConfig()
	g := NewHalfDuplex[Frame](cfg)
	maxDelay := uint64(cfg.MaxDelay / time.Millisecond)
	for ts := uint64(0); ts < 2000; ts += 10 {
		for _, f := range g.Mic(mic(ts)) {
			if ts-f.Timestamp > maxDelay {
				t.Fatalf("mic at %d ms released at %d ms, held more than %d ms", f.Timestamp, ts, maxDelay)
			}
		}
	}
	rest := g.Flush()
	if n := len(rest); n == 0 || uint64(n) > maxDelay/10+1 {
		t.Fatalf("flush gave %d frames", n)
	}
	if st := g.Stats(); st.Frames != 200 || st.Unconfirmed == 0 {
		t.Fatalf("stats %+v", st)
	}
}

func TestHalfDuplexSilencedFramesKeepTheirLengthAndTime(t *testing.T) {
	g := NewHalfDuplex[Frame](DefaultHalfDuplexConfig())
	g.Speaker(0, speakerFrame(10000), testSpeakerRate)
	g.Speaker(32, speakerFrame(10000), testSpeakerRate)
	in := Frame{Samples: make([]int16, 173), Timestamp: 10, SpeakerActive: true}
	for i := range in.Samples {
		in.Samples[i] = 77
	}
	out := g.Mic(in)
	if len(out) != 1 {
		t.Fatalf("released %d frames, want 1", len(out))
	}
	f := out[0]
	if len(f.Samples) != 173 || !silent(f) || f.Timestamp != 10 || !f.SpeakerActive {
		t.Fatalf("got %+v", f)
	}
	if in.Samples[0] != 77 {
		t.Fatal("the gate changed the caller's samples")
	}
}

func TestHalfDuplexDefaults(t *testing.T) {
	cfg := DefaultHalfDuplexConfig()
	if cfg.Hangover != 600*time.Millisecond || cfg.Threshold != -60 || cfg.MaxDelay != 300*time.Millisecond {
		t.Fatalf("defaults %+v", cfg)
	}
}
