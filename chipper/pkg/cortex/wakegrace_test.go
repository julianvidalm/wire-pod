package cortex

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/vad"
)

// gatedFrame is a frame the half-duplex gate silenced: zeros, with the real mic audio (amplitude) under them.
func gatedFrame(ts uint64, amplitude int16) Frame {
	f := levelFrame(ts, 0)
	f.Real = levelFrame(ts, amplitude).Samples
	return f
}

// feedGated feeds frames from..to (inclusive): gated ones (the robot talked) between gateFrom and gateTo,
// holding real, the others plain with plain. It returns the first utterance that ends.
func feedGated(c *Capture, from, to, gateFrom, gateTo uint64, real, plain int16) (Utterance, bool) {
	for ts := from; ts <= to; ts += 10 {
		f := levelFrame(ts, plain)
		if ts >= gateFrom && ts <= gateTo {
			f = gatedFrame(ts, real)
		}
		if u, done := c.Feed(f); done {
			return u, true
		}
	}
	return Utterance{}, false
}

// L101: the user says the question in the same breath as the wake word while the robot plays its wake reaction;
// the gate silences that, but the capture hears the real mic until WakeGrace after the wake.
func TestTheCaptureHearsTheRealMicDuringTheWakeGesture(t *testing.T) {
	c := NewCapture(liveCaptureConfig(), markVAD{})
	feedGated(c, 0, 2000, 1650, 2000, clearAmplitude, 0) // the robot's sound before the wake
	if _, done := c.Start(2000); done {
		t.Fatal("capture ended at its start")
	}
	// The earcon and the hangover gate 2000..3300 while the user asks from 2100 to 3200.
	u, done := feedGated(c, 2010, 3300, 2010, 3300, clearAmplitude, 0)
	if !done {
		u, done = feedGated(c, 3310, 5000, 0, 0, 0, 0)
	}
	if !done || u.Reason != EndSilence {
		t.Fatalf("got %v %q, want an utterance that ends with silence", done, u.Reason)
	}
	if u.Voiced < 1200*time.Millisecond {
		t.Fatalf("voiced %v, want the 1.3 s the user said under the gate", u.Voiced)
	}
	// The pre-roll the gate silenced (1650..2000, inside the margin) is heard too.
	if i := (1700 - int(u.Start)) / 10 * 160; u.Samples[i] != clearAmplitude {
		t.Fatalf("sample at 1700 ms is %d, want the real mic", u.Samples[i])
	}
}

// The window ends WakeGrace after the wake: later gated frames are the gate's silence again.
func TestTheWakeGraceWindowEndsAtTheGrace(t *testing.T) {
	c := NewCapture(liveCaptureConfig(), markVAD{})
	feedGated(c, 0, 2000, 0, 0, 0, 0)
	c.Start(2000)
	// Robot speech heard from 3400 ms on, gated: only 3400..3500 (110 ms, under MinSpeech) is in the window.
	u, done := feedGated(c, 2010, 11500, 3400, 11500, clearAmplitude, 0)
	if !done || u.Reason != EndNoSpeech {
		t.Fatalf("got %v %q, want no speech", done, u.Reason)
	}
	at := func(ms uint64) int16 { return u.Samples[int(ms-u.Start)/10*160] }
	if at(3500) != clearAmplitude || at(3510) != 0 {
		t.Fatalf("at 3500 ms %d, at 3510 ms %d; want the real mic, then silence", at(3500), at(3510))
	}
}

// WakeGrace 0 turns the window off: the capture hears only what the gate let through.
func TestWithoutGraceTheCaptureHearsTheGate(t *testing.T) {
	cfg := liveCaptureConfig()
	cfg.WakeGrace = 0
	c := NewCapture(cfg, markVAD{})
	feedGated(c, 0, 2000, 0, 0, 0, 0)
	c.Start(2000)
	u, done := feedGated(c, 2010, 11500, 2010, 3300, clearAmplitude, 0)
	if !done || u.Reason != EndNoSpeech {
		t.Fatalf("got %v %q, want no speech: the gate's silence", done, u.Reason)
	}
}

func TestTheLiveWakeGraceIsOneAndAHalfSeconds(t *testing.T) {
	if g := DefaultCaptureConfig().WakeGrace; g != 1500*time.Millisecond {
		t.Fatalf("WakeGrace %v, want 1.5s", g)
	}
}

// fixtureCapture replays a recorded wake gesture (session 0dd1b652_20261020-085500, 1 s before to 1.5 s after
// the wake, true rate 15682 Hz) through a capture with the webrtc VAD, as the gate delivers it during the
// gesture: every frame silenced, the real mic under it; then the room's silence.
func fixtureCapture(t *testing.T, name string, wakeSample int) (Utterance, int) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	const rate = 15682
	cfg := DefaultCaptureConfig()
	cfg.MicRate = rate
	v, err := vad.NewWebRTC(3)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCapture(cfg, v)
	ts := func(sample int) uint64 { return uint64(sample * 1000 / rate) }
	n := len(b) / 2
	i := 0
	for ; i+160 <= n; i += 160 {
		real := make([]int16, 160)
		for k := range real {
			real[k] = int16(binary.LittleEndian.Uint16(b[2*(i+k):]))
		}
		c.Feed(Frame{Samples: make([]int16, 160), Real: real, Timestamp: ts(i)})
	}
	if _, done := c.Start(ts(wakeSample)); done {
		t.Fatal("ended at the start")
	}
	prompts := 0
	for ; i < wakeSample+10*rate; i += 160 {
		if u, done := c.Feed(Frame{Samples: make([]int16, 160), Timestamp: ts(i)}); done {
			return u, prompts
		}
		if c.PromptDue() {
			prompts++
		}
	}
	t.Fatal("the capture never ended")
	return Utterance{}, 0
}

// W1 09:02:33: nobody spoke after the wake; the robot's earcon echo (-31..-41 dBFS) alone is not speech, so the
// timeline still prompts at 6 s and gives up at 9 s.
func TestTheEarconEchoAloneIsNotSpeech(t *testing.T) {
	u, prompts := fixtureCapture(t, "wake_gesture_w1.s16", 15822)
	if u.Reason != EndNoSpeech || prompts != 1 {
		t.Fatalf("got %q with %d prompts (voiced %v), want no speech after one prompt", u.Reason, prompts, u.Voiced)
	}
}

// W2 09:05:33: "Hola Jarvis, cuéntame un chiste" in one breath, under the earcon: the capture hears it.
func TestTheQuestionUnderTheEarconIsSpeech(t *testing.T) {
	u, _ := fixtureCapture(t, "wake_gesture_w2.s16", 15740)
	if u.Reason != EndSilence || u.Voiced < 500*time.Millisecond {
		t.Fatalf("got %q, voiced %v; want the question, ended by silence", u.Reason, u.Voiced)
	}
}
