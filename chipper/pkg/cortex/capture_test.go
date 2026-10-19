package cortex

import (
	"testing"
	"time"
)

// Test audio runs at 16000 Hz in 160-sample frames, so each frame is exactly 10 ms and frame timestamps are
// multiples of 10. The audio itself is the clock: durations are counted in samples.
// The capture rules are the live defaults except the rate and the margin: these tests keep the 100 ms margin and
// its 1 s ring (L42) so their numbers stay; the pre-roll tests below use the live defaults.
func testCaptureConfig() CaptureConfig {
	c := DefaultCaptureConfig()
	c.MicRate = 16000
	c.WakeMargin, c.PreRoll, c.MaxLength = 100*time.Millisecond, 900*time.Millisecond, 8*time.Second
	return c
}

// liveCaptureConfig is the live defaults at the test rate.
func liveCaptureConfig() CaptureConfig {
	c := DefaultCaptureConfig()
	c.MicRate = 16000
	return c
}

// markVAD is the fake VAD: a frame is speech when its first sample is not zero.
type markVAD struct{}

func (markVAD) IsSpeech(s []int16) bool { return len(s) > 0 && s[0] != 0 }

// clearAmplitude is a clearly spoken frame (about -30 dBFS); quiet frames are all zero.
const clearAmplitude = 1000

func testFrame(ts uint64, speech bool) Frame {
	if speech {
		return levelFrame(ts, clearAmplitude)
	}
	return levelFrame(ts, 0)
}

// levelFrame is a frame whose every sample is amplitude: the fake VAD calls it speech unless it is zero, and its
// level is 20·log10(amplitude/32768) dBFS.
func levelFrame(ts uint64, amplitude int16) Frame {
	s := make([]int16, 160)
	for i := range s {
		s[i] = amplitude
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
	// A 150 ms blip is shorter than the 200 ms onset: it is not the user speaking.
	if _, done := feedRange(t, c, 1000, 1140, true); done {
		t.Fatal("ended on the blip")
	}
	u, done := feedRange(t, c, 1150, 20000, false)
	if !done || u.Reason != EndNoSpeech {
		t.Fatalf("got done %v reason %q, want %q", done, u.Reason, EndNoSpeech)
	}
	if u.End != 4000 {
		t.Fatalf("gave up at %d, want 4000 (4 s of audio after the wake at 0)", u.End)
	}
}

func TestCaptureListensToThePreRollAtOnce(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	// The wake arrives late: the user already spoke after it and went quiet for 700 ms inside the pre-roll.
	feedRange(t, c, 0, 290, true)
	feedRange(t, c, 300, 1000, false)
	u, done := c.Start(0)
	if !done || u.Reason != EndSilence || u.End != 990 {
		t.Fatalf("got done %v reason %q end %d, want %q at 990", done, u.Reason, u.End, EndSilence)
	}
}

// L35: the margin before the wake holds the tail of "jarvis". It is sent to whisper, but it is not the user
// starting to speak, so the pause before the question does not end the capture.
func TestCaptureCountsSpeechOnlyAfterTheWake(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 899, false)
	feedRange(t, c, 900, 990, true) // the tail of "jarvis", up to the wake's frame
	if _, done := c.Start(990); done {
		t.Fatal("ended at the start")
	}
	if _, done := feedRange(t, c, 1000, 1990, false); done { // the user pauses for 1 s
		t.Fatal("the pause ended the capture")
	}
	if _, done := feedRange(t, c, 2000, 3490, true); done { // the question, 1.5 s
		t.Fatal("ended during the question")
	}
	u, done := feedRange(t, c, 3500, 6000, false)
	if !done || u.Reason != EndSilence {
		t.Fatalf("got done %v reason %q, want %q", done, u.Reason, EndSilence)
	}
	if u.Start != 890 || u.End != 4190 {
		t.Fatalf("got %d to %d, want 890 (the margin is kept) to 4190", u.Start, u.End)
	}
	if u.Voiced != 1500*time.Millisecond {
		t.Fatalf("voiced %v, want 1.5s (the question only)", u.Voiced)
	}
}

func TestCaptureBeforeAnyQuestionWaitsFourSecondsAfterTheWake(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 899, false)
	feedRange(t, c, 900, 990, true)
	c.Start(990)
	u, done := feedRange(t, c, 1000, 20000, false)
	if !done || u.Reason != EndNoSpeech || u.End != 4990 {
		t.Fatalf("got done %v reason %q end %d, want %q at 4990", done, u.Reason, u.End, EndNoSpeech)
	}
}

func TestCaptureMeasuresTheLevelOfTheSpeechAfterTheWake(t *testing.T) {
	c := NewCapture(testCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 990, true) // loud, but before the wake
	c.Start(990)
	for ts := uint64(1000); ts < 1500; ts += 10 {
		c.Feed(levelFrame(ts, 328)) // -40 dBFS
	}
	u, done := feedRange(t, c, 1500, 3000, false)
	if !done || u.Reason != EndSilence {
		t.Fatalf("got done %v reason %q, want %q", done, u.Reason, EndSilence)
	}
	if u.Voiced != 500*time.Millisecond || u.Level < -40.1 || u.Level > -39.9 {
		t.Fatalf("voiced %v at %.2f dBFS, want 500ms at -40 dBFS", u.Voiced, u.Level)
	}
}

// L61: whisper needs "hey jarvis" to tell a short Spanish question from English, so the live capture starts 1 s
// before the wake, even when the detector reports the wake half a second late.
func TestLiveCaptureStartsOneSecondBeforeTheWake(t *testing.T) {
	c := NewCapture(liveCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 2990, false)
	c.Start(2500)
	u, _ := c.Abort()
	if u.Start != 1500 || u.End != 2990 || len(u.Samples) != 150*160 {
		t.Fatalf("got %d to %d with %d samples, want 1500 to 2990 with %d", u.Start, u.End, len(u.Samples), 150*160)
	}
}

// The whole wake phrase is in the capture but is never the user speaking: the pause after it and the question
// decide the end, and only the question counts as speech.
func TestLiveCaptureNeverCountsTheWakePhraseAsSpeech(t *testing.T) {
	c := NewCapture(liveCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 1499, false)
	feedRange(t, c, 1500, 2500, true) // "hey jarvis", up to the wake's frame
	if _, done := c.Start(2500); done {
		t.Fatal("ended at the start")
	}
	if _, done := feedRange(t, c, 2510, 3500, false); done { // the user pauses for 1 s
		t.Fatal("the pause ended the capture")
	}
	if _, done := feedRange(t, c, 3510, 5000, true); done { // the question, 1.5 s
		t.Fatal("ended during the question")
	}
	u, done := feedRange(t, c, 5010, 8000, false)
	if !done || u.Reason != EndSilence || u.Start != 1500 || u.End != 5700 {
		t.Fatalf("got done %v %q from %d to %d, want %q from 1500 to 5700", done, u.Reason, u.Start, u.End, EndSilence)
	}
	if u.Voiced != 1500*time.Millisecond {
		t.Fatalf("voiced %v, want 1.5s (the question only)", u.Voiced)
	}
}

// The timeouts still count from the wake: 4 s without a question, and as before about 8 s of question at most.
func TestLiveCaptureTimesOutFromTheWake(t *testing.T) {
	c := NewCapture(liveCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 1499, false)
	feedRange(t, c, 1500, 2500, true)
	c.Start(2500)
	u, done := feedRange(t, c, 2510, 20000, false)
	if !done || u.Reason != EndNoSpeech || u.End != 6500 {
		t.Fatalf("got done %v %q at %d, want %q at 6500", done, u.Reason, u.End, EndNoSpeech)
	}

	c = NewCapture(liveCaptureConfig(), markVAD{})
	feedRange(t, c, 0, 2500, true)
	c.Start(2500)
	u, done = feedRange(t, c, 2510, 20000, true)
	if !done || u.Reason != EndMaxLen || u.End != 10490 {
		t.Fatalf("got done %v %q at %d, want %q at 10490 (7.99 s after the wake)", done, u.Reason, u.End, EndMaxLen)
	}
}
