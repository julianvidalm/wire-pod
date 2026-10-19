package cortex

import (
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
	MicRate    int           // true mic rate (Vector: 15625, although MicTap announces 16000)
	PreRoll    time.Duration // mic audio always kept, so a wake that arrives late still finds its start
	WakeMargin time.Duration // the capture starts this long before the wake's position
	// MinSpeech is the consecutive speech after the wake needed before the user counts as speaking. Audio up to
	// the wake (the margin, which holds the end of the wake word) is sent to the transcriber but never counts.
	MinSpeech  time.Duration
	EndSilence time.Duration // silence after speech that ends the utterance
	MaxLength  time.Duration // longest utterance, margin included
	NoSpeech   time.Duration // the capture gives up when nobody speaks this long after the wake
	// An utterance whose speech is quieter than MinLevel (dBFS) is not transcribed: nothing was heard (speech
	// shorter than MinSpeech never starts the utterance). A transcript that is one of whisper's silence phrases ("Thank you.") is answered only when its
	// speech reached ClearLevel.
	MinLevel   float64
	ClearLevel float64
}

// DefaultCaptureConfig is the capture the live cortex uses.
func DefaultCaptureConfig() CaptureConfig {
	return CaptureConfig{
		MicRate:    15625,
		PreRoll:    time.Second,
		WakeMargin: 100 * time.Millisecond,
		MinSpeech:  200 * time.Millisecond,
		EndSilence: 700 * time.Millisecond,
		MaxLength:  8 * time.Second,
		NoSpeech:   4 * time.Second, // L34 asks the user to pause between the name and the question
		MinLevel:   -48,
		ClearLevel: -38,
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
	silentRun int     // consecutive non-speech samples since the user started speaking
	spoke     bool
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
	limit := c.samples(c.cfg.PreRoll)
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
	c.capturing, c.wakeTs, c.spoke = true, wakeTs, false
	c.afterWake, c.voiced, c.voicedSq, c.voicedRun, c.silentRun = 0, 0, 0, 0, 0
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
	c.cur.Samples = append(c.cur.Samples, f.Samples...)
	if f.Timestamp <= c.wakeTs {
		return Utterance{}, false // the margin: kept for the transcriber, never the start of speech
	}
	n := len(f.Samples)
	c.afterWake += n
	if c.vad.IsSpeech(f.Samples) {
		c.voiced += n
		for _, s := range f.Samples {
			c.voicedSq += float64(s) * float64(s)
		}
		c.voicedRun += n
		c.silentRun = 0
		if c.voicedRun >= c.samples(c.cfg.MinSpeech) {
			c.spoke = true
		}
	} else {
		c.voicedRun = 0
		if c.spoke {
			c.silentRun += n
		}
	}
	switch {
	case c.spoke && c.silentRun >= c.samples(c.cfg.EndSilence):
		return c.finish(EndSilence), true
	case len(c.cur.Samples) >= c.samples(c.cfg.MaxLength):
		if !c.spoke {
			return c.finish(EndNoSpeech), true
		}
		return c.finish(EndMaxLen), true
	case !c.spoke && c.afterWake >= c.samples(c.cfg.NoSpeech):
		return c.finish(EndNoSpeech), true
	}
	return Utterance{}, false
}

func (c *Capture) finish(r EndReason) Utterance {
	u := c.cur
	u.Reason = r
	u.Voiced = time.Duration(c.voiced) * time.Second / time.Duration(c.cfg.MicRate)
	u.Level = math.Inf(-1)
	if c.voiced > 0 {
		u.Level = 20 * math.Log10(math.Sqrt(c.voicedSq/float64(c.voiced))/32768)
	}
	c.capturing = false
	c.cur = Utterance{}
	return u
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
