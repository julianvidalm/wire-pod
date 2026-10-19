package cortex

import (
	"testing"
	"time"
)

// Test audio runs at 16000 Hz in 160-sample frames, so each frame is exactly 10 ms and frame timestamps are
// multiples of 10. The audio itself is the clock: durations are counted in samples.
func testCaptureConfig() CaptureConfig {
	return CaptureConfig{
		MicRate:    16000,
		PreRoll:    time.Second,
		WakeMargin: 100 * time.Millisecond,
		MinSpeech:  30 * time.Millisecond,
		EndSilence: 700 * time.Millisecond,
		MaxLength:  8 * time.Second,
		NoSpeech:   3 * time.Second,
	}
}

// markVAD is the fake VAD: a frame is speech when its first sample is not zero.
type markVAD struct{}

func (markVAD) IsSpeech(s []int16) bool { return len(s) > 0 && s[0] != 0 }

func testFrame(ts uint64, speech bool) Frame {
	s := make([]int16, 160)
	if speech {
		s[0] = 1000
	}
	return Frame{Samples: s, Timestamp: ts}
}

// feedRange feeds frames from ts from to to (inclusive) and returns the first utterance that ends.
func feedRange(t *testing.T, c *Capture, from, to uint64, speech bool) (Utterance, bool) {
	t.Helper()
	for ts := from; ts <= to; ts += 10 {
		if u, done := c.Feed(testFrame(ts, speech)); done {
			return u, true
		}
	}
	return Utterance{}, false
}

func TestCaptureStartsAtTheWakeMinusTheMargin(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 1990, false)
	if _, done := c.Start(1500); done {
		t.Fatal("capture ended at its start")
	}
	if !c.Capturing() {
		t.Fatal("not capturing after Start")
	}
	u, ok := c.Abort()
	if !ok {
		t.Fatal("Abort returned no utterance")
	}
	// The pre-roll from 1400 ms (wake 1500 minus the 100 ms margin) through the last frame, 1990 ms.
	if u.Start != 1400 || u.End != 1990 || len(u.Samples) != 60*160 || u.Reason != EndStream {
		t.Fatalf("got start %d end %d, %d samples, %q; want 1400, 1990, %d samples, %q",
			u.Start, u.End, len(u.Samples), u.Reason, 60*160, EndStream)
	}
}

func TestCaptureOfALateWakeStartsAtTheOldestPreRollFrame(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 1990, false)
	c.Start(200) // older than the 1 s pre-roll, which holds 1000..1990
	u, _ := c.Abort()
	if u.Start != 1000 || len(u.Samples) != 100*160 {
		t.Fatalf("got start %d with %d samples, want 1000 with %d", u.Start, len(u.Samples), 100*160)
	}
}

func TestCaptureIgnoresAudioUntilAWake(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	if u, done := feedRange(t, c, 0, 20000, true); done {
		t.Fatalf("utterance without a wake: %+v", u.Reason)
	}
	if c.Capturing() {
		t.Fatal("capturing without a wake")
	}
	if _, ok := c.Abort(); ok {
		t.Fatal("Abort returned an utterance without a capture")
	}
}

func TestCaptureEndsAfterSilenceThatFollowsSpeech(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 990, false)
	c.Start(990)
	if _, done := feedRange(t, c, 1000, 1490, true); done {
		t.Fatal("ended during speech")
	}
	u, done := feedRange(t, c, 1500, 5000, false)
	if !done {
		t.Fatal("no end after the silence")
	}
	// 700 ms of silence: frames 1500..2190.
	if u.Reason != EndSilence || u.Start != 890 || u.End != 2190 {
		t.Fatalf("got %q from %d to %d, want %q from 890 to 2190", u.Reason, u.Start, u.End, EndSilence)
	}
	if want := (2190-890)/10 + 1; len(u.Samples) != want*160 {
		t.Fatalf("got %d samples, want %d", len(u.Samples), want*160)
	}
	if c.Capturing() {
		t.Fatal("still capturing after the end")
	}
	if got := u.Duration(); got != 1310*time.Millisecond {
		t.Fatalf("duration %v, want 1.31s", got)
	}
}

func TestCaptureStopsAtTheMaxLength(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	c.Start(0)
	u, done := feedRange(t, c, 0, 20000, true)
	if !done || u.Reason != EndMaxLen {
		t.Fatalf("got done %v reason %q, want %q", done, u.Reason, EndMaxLen)
	}
	if len(u.Samples) != 8*16000 || u.End != 7990 {
		t.Fatalf("got %d samples ending at %d, want %d ending at 7990", len(u.Samples), u.End, 8*16000)
	}
}

func TestCaptureGivesUpWhenNobodySpeaks(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	c.Start(0)
	feedRange(t, c, 0, 990, false)
	// One 10 ms blip is shorter than MinSpeech: it is not the user speaking.
	if _, done := c.Feed(testFrame(1000, true)); done {
		t.Fatal("ended on the blip")
	}
	u, done := feedRange(t, c, 1010, 20000, false)
	if !done || u.Reason != EndNoSpeech {
		t.Fatalf("got done %v reason %q, want %q", done, u.Reason, EndNoSpeech)
	}
	if u.End != 2990 {
		t.Fatalf("gave up at %d, want 2990 (3 s after the capture started)", u.End)
	}
}

func TestCaptureListensToThePreRollAtOnce(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	// The wake arrives late: the user already spoke and went quiet for 700 ms inside the pre-roll.
	feedRange(t, c, 0, 190, true)
	feedRange(t, c, 200, 900, false)
	u, done := c.Start(0)
	if !done || u.Reason != EndSilence || u.End != 890 {
		t.Fatalf("got done %v reason %q end %d, want %q at 890", done, u.Reason, u.End, EndSilence)
	}
}
