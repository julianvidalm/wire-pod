package cortex

import (
	"fmt"
	"math"
	"time"
)

// EndReason says why a capture ended.
type EndReason string

const (
	EndSilence  EndReason = "end of speech"
	EndMaxLen   EndReason = "max length"
	EndNoSpeech EndReason = "no speech"
	EndStream   EndReason = "stream ended"
)

// Utterance is the mic audio captured after one wake.
type Utterance struct {
	ESN     string
	Samples []int16
	Rate    int    // true samples per second
	Start   uint64 // robot timestamp (ms) of the first captured frame
	End     uint64 // robot timestamp (ms) of the last captured frame
	Reason  EndReason
	Voiced  time.Duration // speech the VAD heard after the wake (the margin before it is not counted)
	Level   float64       // RMS of that speech in dBFS; -inf without any
	Onset   time.Duration // when the user's speech began, after the wake; 0 when they never spoke
}

// Duration is how much audio the utterance holds.
func (u Utterance) Duration() time.Duration {
	if u.Rate <= 0 {
		return 0
	}
	return time.Duration(len(u.Samples)) * time.Second / time.Duration(u.Rate)
}

// CaptureConfig sets how an utterance is cut out of the mic stream. Every duration is audio time, counted in
// samples at MicRate, so a capture behaves the same live and in a faster-than-real-time replay.
type CaptureConfig struct {
	MicRate int // true mic rate (Vector: about 15662, although MicTap announces 16000)
	// The capture starts WakeMargin before the wake's position. Mic audio is always kept for WakeMargin plus
	// PreRoll, so a wake the detector reports up to PreRoll late still finds its start.
	PreRoll    time.Duration
	WakeMargin time.Duration
	// MinSpeech is the consecutive speech after the wake needed before the user counts as speaking. Audio up to
	// the wake (the margin, which holds the end of the wake word) is sent to the transcriber but never counts.
	MinSpeech time.Duration
	// GatedMinSpeech is the consecutive speech needed instead when the run holds a frame heard through the wake
	// grace, that is while the robot itself was sounding (L110): its earcon echo and the gesture's noise make
	// speech-like runs of up to ~0.6 s that are not the user, while a question said in the same breath as the
	// wake word runs on past them (L101). Shorter runs are kept for the transcriber but never start the
	// utterance. Below MinSpeech, MinSpeech applies.
	GatedMinSpeech time.Duration
	// WindowSpeech and SpeechWindow, when both are set, also start the utterance on speech heard through the wake
	// grace that is not one consecutive run: WindowSpeech of speech within any SpeechWindow of those frames (the
	// robot firmware's sustained-speech rule, minSpeech_s 0.6 in speechWindow_s 1.0). The VAD fragments a question
	// said in the same breath as the wake word, so no run of it may reach GatedMinSpeech (robot-wake, 16:55:49).
	// The onset is the first speech frame of that window. Zero leaves GatedMinSpeech alone.
	WindowSpeech time.Duration
	SpeechWindow time.Duration
	EndSilence   time.Duration // silence after speech that ends the utterance
	MaxLength    time.Duration // longest utterance, counted from the user's speech onset (T3g)
	NoSpeech     time.Duration // the capture gives up when nobody speaks this long after the wake
	// PromptAfter is when, without speech since the wake, the robot is asked to show it is still listening
	// (PromptDue); 0 never asks. The robot turns to the voice first, so the user speaks late (S6, L71).
	PromptAfter time.Duration
	// WakeGrace: from WakeMargin before the wake until WakeGrace after it, the capture hears the real mic of
	// frames the half-duplex gate silenced (Frame.Real), so the robot's own wake reaction (the earcon, its
	// get-in sound) does not zero what the user says in the same breath as the wake word (L101). Later frames,
	// and the wake detector always, hear the gate's silence. 0 turns the window off.
	WakeGrace time.Duration
	// An utterance whose speech is quieter than MinLevel (dBFS) is not transcribed: nothing was heard (speech
	// shorter than MinSpeech never starts the utterance). A transcript that is one of whisper's silence phrases ("Thank you.") is answered only when its
	// speech reached ClearLevel.
	MinLevel   float64
	ClearLevel float64
	// Trace, when set, gets one line per frame listened to after the wake (diagnosis, cortex-replay -trace).
	Trace func(line string)
}

// DefaultCaptureConfig is the capture the live cortex uses.
func DefaultCaptureConfig() CaptureConfig {
	return CaptureConfig{
		MicRate:    15662,
		PreRoll:    time.Second,
		WakeMargin: time.Second, // L61 (PC wake, retired); a voice request starts at the wake and uses none
		MinSpeech:  200 * time.Millisecond,
		// L110: earcon echo runs 0.2-0.36 s and gesture noise 0.57-0.59 s (12:08 session, 5 wakes); the
		// one-breath question of L101 W2 runs 0.98 s
		GatedMinSpeech: 750 * time.Millisecond,
		EndSilence:     700 * time.Millisecond,
		MaxLength:      8 * time.Second,
		NoSpeech:       9 * time.Second, // S6: the prompt at 6 s, then 3 s more
		PromptAfter:    6 * time.Second,
		WakeGrace:      1500 * time.Millisecond, // the earcon ends ~0.7 s after the wake, the gate's hangover 0.6 s later
		MinLevel:       -48,
		ClearLevel:     -38,
	}
}

// Capture keeps one robot's recent mic audio and, after a wake, cuts the utterance that follows it. It is not
// safe for concurrent use; Conversation serializes it.
type Capture struct {
	cfg CaptureConfig
	vad VAD

	ring        []Frame // the pre-roll, oldest first
	ringSamples int

	capturing bool
	wakeTs    uint64 // frames up to this robot time are context, not speech
	cur       Utterance
	afterWake int     // samples captured after the wake
	voiced    int     // speech samples after the wake
	voicedSq  float64 // sum of their squares
	voicedRun int     // consecutive speech samples after the wake
	runGated  bool    // ... and that run holds a frame heard through the wake grace (the robot was sounding)
	silentRun int     // consecutive non-speech samples since the user started speaking
	spoke     bool
	window    []windowFrame // the frames heard through the wake grace in the last SpeechWindow
	onset     int           // samples after the wake when the user's speech began
	prompted  bool          // the prompt came due in this capture
	promptDue bool          // ... and PromptDue has not reported it yet
}

func NewCapture(cfg CaptureConfig, vad VAD) *Capture {
	return &Capture{cfg: cfg, vad: vad}
}

// samples converts audio time to a sample count at the mic rate.
func (c *Capture) samples(d time.Duration) int {
	return int(d * time.Duration(c.cfg.MicRate) / time.Second)
}

// Feed adds one mic frame. While capturing it returns the utterance once it ends.
func (c *Capture) Feed(f Frame) (Utterance, bool) {
	c.remember(f)
	if !c.capturing {
		return Utterance{}, false
	}
	return c.listen(f)
}

// remember keeps f in the pre-roll and forgets the frames that fall out of it.
func (c *Capture) remember(f Frame) {
	c.ring = append(c.ring, f)
	c.ringSamples += len(f.Samples)
	limit := c.samples(c.cfg.PreRoll + c.cfg.WakeMargin)
	drop := 0
	for c.ringSamples-len(c.ring[drop].Samples) >= limit && drop < len(c.ring)-1 {
		c.ringSamples -= len(c.ring[drop].Samples)
		drop++
	}
	if drop > 0 {
		c.ring = append(c.ring[:0], c.ring[drop:]...)
	}
}

// Start begins a capture at the wake whose end was heard at robot time wakeTs, from the pre-roll frame at
// wakeTs minus WakeMargin. The pre-roll frames from there on are listened to at once, so the utterance may
// already be complete.
func (c *Capture) Start(wakeTs uint64) (Utterance, bool) {
	from := uint64(0)
	if margin := uint64(c.cfg.WakeMargin / time.Millisecond); wakeTs > margin {
		from = wakeTs - margin
	}
	c.capturing, c.wakeTs, c.spoke, c.prompted, c.promptDue = true, wakeTs, false, false, false
	c.afterWake, c.voiced, c.voicedSq, c.voicedRun, c.silentRun, c.runGated = 0, 0, 0, 0, 0, false
	c.window = c.window[:0]
	c.cur = Utterance{Rate: c.cfg.MicRate}
	for _, f := range c.ring {
		if f.Timestamp < from {
			continue
		}
		if u, done := c.listen(f); done {
			return u, true
		}
	}
	return Utterance{}, false
}

// listen adds one frame to the capture in progress and ends it when its rules say so.
func (c *Capture) listen(f Frame) (Utterance, bool) {
	if len(c.cur.Samples) == 0 {
		c.cur.Start = f.Timestamp
	}
	c.cur.End = f.Timestamp
	samples, gated := f.Samples, f.Real != nil && c.inWakeGrace(f.Timestamp)
	if gated {
		samples = f.Real
	}
	c.cur.Samples = append(c.cur.Samples, samples...)
	if f.Timestamp <= c.wakeTs {
		if c.cfg.Trace != nil {
			c.trace(f, samples, false)
		}
		return Utterance{}, false // the margin: kept for the transcriber, never the start of speech
	}
	n := len(samples)
	c.afterWake += n
	speech := c.vad.IsSpeech(samples)
	if speech {
		c.voiced += n
		for _, s := range samples {
			c.voicedSq += float64(s) * float64(s)
		}
		c.voicedRun += n
		c.runGated = c.runGated || gated
		c.silentRun = 0
		if !c.spoke && c.voicedRun >= c.minSpeech() {
			c.spoke = true
			c.onset = c.afterWake - c.voicedRun
		}
		if gated && !c.spoke {
			if first, ok := c.heardInWindow(c.afterWake-n, n, true); ok {
				c.spoke, c.onset = true, first
			}
		}
	} else {
		if gated && !c.spoke {
			c.heardInWindow(c.afterWake-n, n, false)
		}
		c.voicedRun, c.runGated = 0, false
		if c.spoke {
			c.silentRun += n
		}
	}
	if c.cfg.Trace != nil {
		c.trace(f, samples, speech)
	}
	switch {
	case c.spoke && c.silentRun >= c.samples(c.cfg.EndSilence):
		return c.finish(EndSilence), true
	case c.spoke && c.afterWake-c.onset >= c.samples(c.cfg.MaxLength):
		return c.finish(EndMaxLen), true
	case !c.spoke && c.afterWake >= c.samples(c.cfg.NoSpeech):
		return c.finish(EndNoSpeech), true
	case !c.spoke && !c.prompted && c.voicedRun == 0 && c.cfg.PromptAfter > 0 &&
		c.afterWake >= c.samples(c.cfg.PromptAfter):
		c.prompted, c.promptDue = true, true
	}
	return Utterance{}, false
}

// windowFrame is one frame heard through the wake grace: where it starts (samples after the wake), its length and
// whether the VAD heard speech in it.
type windowFrame struct {
	at, n  int
	speech bool
}

// heardInWindow applies the WindowSpeech rule to one frame heard through the wake grace, which started at at
// (samples after the wake): it says whether the speech in the last SpeechWindow reaches WindowSpeech, and where
// that window's speech began.
func (c *Capture) heardInWindow(at, n int, speech bool) (int, bool) {
	if c.cfg.WindowSpeech <= 0 || c.cfg.SpeechWindow <= 0 {
		return 0, false
	}
	c.window = append(c.window, windowFrame{at, n, speech})
	from := at + n - c.samples(c.cfg.SpeechWindow)
	drop := 0
	for drop < len(c.window) && c.window[drop].at < from {
		drop++
	}
	c.window = c.window[drop:]
	voiced, first := 0, -1
	for _, f := range c.window {
		if f.speech {
			voiced += f.n
			if first < 0 {
				first = f.at
			}
		}
	}
	return first, voiced >= c.samples(c.cfg.WindowSpeech)
}

// minSpeech is the run, in samples, that makes the speech in progress the user's onset.
func (c *Capture) minSpeech() int {
	if c.runGated && c.cfg.GatedMinSpeech > c.cfg.MinSpeech {
		return c.samples(c.cfg.GatedMinSpeech)
	}
	return c.samples(c.cfg.MinSpeech)
}

// trace reports one frame: its time after the wake, level, VAD verdict and the onset state.
func (c *Capture) trace(f Frame, samples []int16, speech bool) {
	sq := 0.0
	for _, s := range samples {
		sq += float64(s) * float64(s)
	}
	db := math.Inf(-1)
	if len(samples) > 0 && sq > 0 {
		db = 20 * math.Log10(math.Sqrt(sq/float64(len(samples)))/32768)
	}
	c.cfg.Trace(fmt.Sprintf("capture %+6d ms %6.1f dBFS gated=%-5v speech=%-5v run=%4d ms spoke=%v onset=%d ms silent=%d ms",
		int64(f.Timestamp)-int64(c.wakeTs), db, f.Real != nil, speech, c.ms(c.voicedRun), c.spoke, c.ms(c.onset), c.ms(c.silentRun)))
}

// ms converts a sample count at the mic rate to milliseconds.
func (c *Capture) ms(n int) int { return n * 1000 / c.cfg.MicRate }

// inWakeGrace says whether the capture hears the real mic of a gated frame at robot time ts: from the start of
// the capture (the margin before the wake) until WakeGrace after the wake. The window belongs to the capture in
// progress, so it ends early when the capture does.
func (c *Capture) inWakeGrace(ts uint64) bool {
	return c.capturing && c.cfg.WakeGrace > 0 && ts <= c.wakeTs+uint64(c.cfg.WakeGrace/time.Millisecond)
}

func (c *Capture) finish(r EndReason) Utterance {
	u := c.cur
	u.Reason = r
	u.Voiced = time.Duration(c.voiced) * time.Second / time.Duration(c.cfg.MicRate)
	if c.spoke {
		u.Onset = time.Duration(c.onset) * time.Second / time.Duration(c.cfg.MicRate)
	}
	u.Level = math.Inf(-1)
	if c.voiced > 0 {
		u.Level = 20 * math.Log10(math.Sqrt(c.voicedSq/float64(c.voiced))/32768)
	}
	c.capturing = false
	c.cur = Utterance{}
	return u
}

// PromptDue says, once per capture, that nobody has spoken PromptAfter after the wake: the robot should show it
// is still listening.
func (c *Capture) PromptDue() bool {
	due := c.promptDue
	c.promptDue = false
	return due
}

// Capturing says whether a capture is in progress.
func (c *Capture) Capturing() bool { return c.capturing }

// Abort ends a capture in progress, for example when the robot's stream stops.
func (c *Capture) Abort() (Utterance, bool) {
	if !c.capturing {
		return Utterance{}, false
	}
	return c.finish(EndStream), true
}
