package cortex

import (
	"math"
	"time"
)

// Half duplex: while the robot talks the cortex does not listen. The gate decides "the robot is talking" from
// the speaker audio the robot streams (its level, never the robot's speaker-active flag, which stays on
// whenever the sound sink is open) and, by robot timestamp, silences every mic frame heard from the start of
// a sound until Hangover after its end. Silenced frames become zeros of the same length, so nothing
// downstream loses time or pre-roll.

// HalfDuplexConfig tunes the gate.
type HalfDuplexConfig struct {
	// Hangover keeps the mic silenced after the speaker's last sound: the echo reaches the mic ~250 ms after the
	// speaker frame's timestamp (L53) and fades into the room noise within ~300 ms more.
	Hangover time.Duration
	// Threshold is the speaker level (dBFS, RMS of one speaker frame) above which the robot counts as
	// talking. Recordings: the idle speaker is digital zero or dither at -115 to -75 dBFS, speech -45 to -10.
	Threshold float64
	// MaxDelay is the longest a mic frame waits for the speaker frames covering its time. The robot sends
	// speaker frames all the time (silence included), so frames normally wait one speaker frame (32 ms).
	MaxDelay time.Duration
	// StallTimeout: when no speaker frame has come for this long of robot (mic) time, the speaker stream counts
	// as absent and mic frames pass straight through, without waiting MaxDelay, until speaker frames resume.
	StallTimeout time.Duration
}

func DefaultHalfDuplexConfig() HalfDuplexConfig {
	return HalfDuplexConfig{Hangover: 600 * time.Millisecond, Threshold: -50, MaxDelay: 300 * time.Millisecond,
		StallTimeout: 2 * time.Second}
}

// MicFrame is what the gate holds: a mic frame with its robot timestamp (ms) that can return a copy of itself
// with its audio replaced by zeros of the same length.
type MicFrame[T any] interface {
	RobotTime() uint64
	Silenced() T
}

func (f Frame) RobotTime() uint64 { return f.Timestamp }

func (f Frame) Silenced() Frame {
	f.Samples = make([]int16, len(f.Samples))
	return f
}

// HalfDuplexStats counts what the gate did on one stream.
type HalfDuplexStats struct {
	Frames      int // mic frames released
	GatedFrames int // of them, silenced
	Spans       int // runs of silenced frames
	// Unconfirmed mic frames were released after MaxDelay before any speaker frame covered their time, and
	// Late speaker sounds arrived after mic frames they should have silenced had been released.
	Unconfirmed int
	Late        int
}

// HalfDuplex gates one robot's mic stream. It is not safe for concurrent use: one stream feeds it in order.
type HalfDuplex[T MicFrame[T]] struct {
	cfg        HalfDuplexConfig
	pending    []T
	sounds     []span // speaker sound, start to end (ms), in arrival order, merged when they overlap
	speakerTop uint64 // latest speaker frame start seen
	hasSpeaker bool
	newestMic  uint64
	firstMic   uint64
	anyMic     bool
	released   uint64 // timestamp of the last released mic frame
	anyOut     bool
	wasGated   bool
	stats      HalfDuplexStats
}

type span struct{ start, end uint64 }

// maxPending bounds the held mic frames whatever their timestamps say.
const maxPending = 1000

func NewHalfDuplex[T MicFrame[T]](cfg HalfDuplexConfig) *HalfDuplex[T] {
	return &HalfDuplex[T]{cfg: cfg}
}

// Speaker takes one speaker frame (robot timestamp, samples at rate Hz) and returns the mic frames it releases.
func (h *HalfDuplex[T]) Speaker(ts uint64, samples []int16, rate int) []T {
	if !h.hasSpeaker || ts > h.speakerTop {
		h.speakerTop = ts
	}
	h.hasSpeaker = true
	if rate > 0 && len(samples) > 0 && level(samples) > h.cfg.Threshold {
		end := ts + uint64(len(samples))*1000/uint64(rate)
		if h.anyOut && ts <= h.released {
			h.stats.Late++
		}
		if n := len(h.sounds); n > 0 && ts <= h.sounds[n-1].end && end >= h.sounds[n-1].start {
			last := &h.sounds[n-1]
			last.start, last.end = min(last.start, ts), max(last.end, end)
		} else {
			h.sounds = append(h.sounds, span{ts, end})
		}
	}
	return h.release(false)
}

// Mic takes one mic frame and returns the frames now released, in order, silenced where the robot talked.
func (h *HalfDuplex[T]) Mic(f T) []T {
	h.pending = append(h.pending, f)
	ts := f.RobotTime()
	if !h.anyMic {
		h.firstMic, h.newestMic, h.anyMic = ts, ts, true
	}
	if ts > h.newestMic {
		h.newestMic = ts
	}
	return h.release(false)
}

// Flush releases every held frame (the stream ended).
func (h *HalfDuplex[T]) Flush() []T { return h.release(true) }

func (h *HalfDuplex[T]) Stats() HalfDuplexStats { return h.stats }

func (h *HalfDuplex[T]) release(all bool) []T {
	maxDelay := uint64(h.cfg.MaxDelay / time.Millisecond)
	stall := uint64(h.cfg.StallTimeout / time.Millisecond)
	stalled := h.anyMic && stall > 0 && ((!h.hasSpeaker && h.newestMic >= h.firstMic+stall) ||
		(h.hasSpeaker && h.newestMic > h.speakerTop+stall))
	var out []T
	n := 0
	for ; n < len(h.pending); n++ {
		f := h.pending[n]
		ts := f.RobotTime()
		covered := h.hasSpeaker && h.speakerTop >= ts
		switch {
		case covered, all:
		case stalled || h.newestMic >= ts+maxDelay || len(h.pending)-n > maxPending:
			h.stats.Unconfirmed++
		default:
			h.pending = append(h.pending[:0], h.pending[n:]...)
			return out
		}
		out = append(out, h.decide(f))
	}
	h.pending = h.pending[:0]
	return out
}

func (h *HalfDuplex[T]) decide(f T) T {
	ts := f.RobotTime()
	hang := uint64(h.cfg.Hangover / time.Millisecond)
	gated := false
	kept := h.sounds[:0]
	for _, s := range h.sounds {
		if s.end+hang < ts {
			continue // over for good: mic frames come out in time order
		}
		kept = append(kept, s)
		if s.start <= ts {
			gated = true
		}
	}
	h.sounds = kept
	h.released, h.anyOut = ts, true
	h.stats.Frames++
	if gated {
		h.stats.GatedFrames++
		if !h.wasGated {
			h.stats.Spans++
		}
		f = f.Silenced()
	}
	h.wasGated = gated
	return f
}

// level is the RMS of samples in dBFS.
func level(samples []int16) float64 {
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	if rms == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(rms/32768)
}
