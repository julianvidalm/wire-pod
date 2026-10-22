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

// L110: a speech run heard under the gate (the robot sounding) starts the utterance only once it lasts
// GatedMinSpeech; the onset is then the start of the run.
func TestASpeechRunUnderTheGateNeedsGatedMinSpeech(t *testing.T) {
	for _, tc := range []struct {
		name   string
		until  uint64 // the gated speech runs 2100..until
		reason EndReason
	}{
		{"0.6 s: the robot's own sound", 2690, EndNoSpeech},
		{"0.8 s: the user", 2890, EndSilence},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCapture(liveCaptureConfig(), markVAD{})
			feedGated(c, 0, 2000, 0, 0, 0, 0)
			c.Start(2000)
			u, done := feedGated(c, 2010, 2090, 0, 0, 0, 0)
			if !done {
				u, done = feedGated(c, 2100, tc.until, 2100, tc.until, clearAmplitude, 0)
			}
			if !done {
				u, done = feedGated(c, tc.until+10, 11500, 0, 0, 0, 0)
			}
			if !done || u.Reason != tc.reason {
				t.Fatalf("got %v %q, want %q", done, u.Reason, tc.reason)
			}
		})
	}
}

// L42 holds for plain frames: 0.2 s of speech right after the wake, with the robot quiet, is the user's onset.
func TestPlainSpeechRightAfterTheWakeStillNeedsOnlyMinSpeech(t *testing.T) {
	c := NewCapture(liveCaptureConfig(), markVAD{})
	feedGated(c, 0, 2000, 0, 0, 0, 0)
	c.Start(2000)
	u, done := feedGated(c, 2010, 2250, 0, 0, 0, clearAmplitude)
	if !done {
		u, done = feedGated(c, 2260, 11500, 0, 0, 0, 0)
	}
	if !done || u.Reason != EndSilence {
		t.Fatalf("got %v %q, want an utterance that ends with silence", done, u.Reason)
	}
}

// A short run under the gate does not count, but the user's speech after the grace still starts the utterance at
// MinSpeech, and MaxLength counts from that onset.
func TestTheUserSpeaksAfterTheEarconEcho(t *testing.T) {
	cfg := liveCaptureConfig()
	cfg.MaxLength = time.Second
	c := NewCapture(cfg, markVAD{})
	feedGated(c, 0, 2000, 0, 0, 0, 0)
	c.Start(2000)
	u, done := feedGated(c, 2010, 2600, 2300, 2600, clearAmplitude, 0) // earcon echo 0.3 s
	if !done {
		u, done = feedGated(c, 2610, 4990, 0, 0, 0, 0)
	}
	if !done {
		u, done = feedGated(c, 5000, 9000, 0, 0, 0, clearAmplitude) // the user, from 5000
	}
	if !done || u.Reason != EndMaxLen || u.End < 5990 {
		t.Fatalf("got %v %q ending at %d, want max length 1 s after the onset at 5000", done, u.Reason, u.End)
	}
}

func TestTheLiveGatedMinSpeechIs750ms(t *testing.T) {
	if g := DefaultCaptureConfig().GatedMinSpeech; g != 750*time.Millisecond {
		t.Fatalf("GatedMinSpeech %v, want 750ms", g)
	}
}

func TestTheLiveWakeGraceIsOneAndAHalfSeconds(t *testing.T) {
	if g := DefaultCaptureConfig().WakeGrace; g != 1500*time.Millisecond {
		t.Fatalf("WakeGrace %v, want 1.5s", g)
	}
}

// gateSpan is when, relative to the wake (ms), the half-duplex gate silenced a recorded clip: those frames reach
// the capture as zeros with the real mic under them, the others as the plain mic.
type gateSpan struct{ from, to int }

// everyFrame: the whole clip was gated.
var everyFrame = gateSpan{-100000, 100000}

// fixtureCapture replays a recorded wake (raw s16le, true rate rate, the wake at wakeSample) through a capture
// with the webrtc VAD, as the gate delivered it: frames inside gate silenced with the real mic under them, the
// others plain; after the clip, the room's silence until the capture ends. It returns the utterance and how
// many prompts came due.
func fixtureCapture(t *testing.T, name string, rate, wakeSample int, gate gateSpan) (Utterance, int) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultCaptureConfig()
	cfg.MicRate = rate
	v, err := vad.NewWebRTC(3)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCapture(cfg, v)
	ts := func(sample int) uint64 { return uint64(sample * 1000 / rate) }
	wakeTs := ts(wakeSample)
	frame := func(i int) Frame {
		f := Frame{Samples: make([]int16, 160), Timestamp: ts(i)}
		if i+160 > len(b)/2 {
			return f // after the clip: silence
		}
		real := make([]int16, 160)
		for k := range real {
			real[k] = int16(binary.LittleEndian.Uint16(b[2*(i+k):]))
		}
		if rel := int(f.Timestamp) - int(wakeTs); rel >= gate.from && rel <= gate.to {
			f.Real = real
		} else {
			f.Samples = real
		}
		return f
	}
	i := 0
	for ; i < wakeSample; i += 160 {
		c.Feed(frame(i))
	}
	if _, done := c.Start(wakeTs); done {
		t.Fatal("ended at the start")
	}
	prompts := 0
	for ; i < wakeSample+10*rate; i += 160 {
		if u, done := c.Feed(frame(i)); done {
			return u, prompts
		}
		if c.PromptDue() {
			prompts++
		}
	}
	t.Fatal("the capture never ended")
	return Utterance{}, 0
}

// W1 09:02:33 (session 0dd1b652_20261020-085500, 15682 Hz): nobody spoke after the wake; the robot's earcon echo
// (-31..-41 dBFS) alone is not speech, so the timeline still prompts at 6 s and gives up at 9 s.
func TestTheEarconEchoAloneIsNotSpeech(t *testing.T) {
	u, prompts := fixtureCapture(t, "wake_gesture_w1.s16", 15682, 15822, everyFrame)
	if u.Reason != EndNoSpeech || prompts != 1 {
		t.Fatalf("got %q with %d prompts (voiced %v), want no speech after one prompt", u.Reason, prompts, u.Voiced)
	}
}

// W2 09:05:33: "Hola Jarvis, cuéntame un chiste" in one breath, under the earcon (gated -234..+1377 ms) and on
// past it: the capture hears it.
func TestTheQuestionUnderTheEarconIsSpeech(t *testing.T) {
	u, _ := fixtureCapture(t, "wake_gesture_w2.s16", 15682, 15740, gateSpan{-234, 1377})
	if u.Reason != EndSilence || u.Voiced < 500*time.Millisecond {
		t.Fatalf("got %q, voiced %v; want the question, ended by silence", u.Reason, u.Voiced)
	}
}

// L110, 13:49:36 (session 0dd1b652_20261020-120832, 15674 Hz): the user said "hey jarvis" and waited for the
// gesture. The earcon echo under the gate (+224..+1266 ms) is a 0.3 s speech-like run: it is not the user's
// onset, so the capture keeps waiting, prompts at 6 s and gives up at 9 s.
func TestTheEarconEchoIsNotTheOnsetWhileTheUserWaits(t *testing.T) {
	u, prompts := fixtureCapture(t, "wake_wait_earcon.s16", 15674, 15755, gateSpan{224, 1266})
	if u.Reason != EndNoSpeech || prompts != 1 {
		t.Fatalf("got %q with %d prompts (voiced %v, %.1f s), want no speech after one prompt", u.Reason, prompts,
			u.Voiced, u.Duration().Seconds())
	}
}

// L110, 13:56:14: the wake gesture's sound and motor noise make a 0.6 s speech-like run that starts under the
// gate (+194..+1195 ms) and ends after it: not the user's onset either.
func TestTheGestureNoiseIsNotTheOnsetWhileTheUserWaits(t *testing.T) {
	u, prompts := fixtureCapture(t, "wake_wait_gesture.s16", 15674, 15708, gateSpan{194, 1195})
	if u.Reason != EndNoSpeech || prompts != 1 {
		t.Fatalf("got %q with %d prompts (voiced %v, %.1f s), want no speech after one prompt", u.Reason, prompts,
			u.Voiced, u.Duration().Seconds())
	}
}
