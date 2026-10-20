package cortex

import "context"

// The cortex's ports. Every component of the conversation sits behind one of them, so an adapter can be
// swapped by configuration and compared on the same corpus:
//
//   - AudioSource: an adapter pushes each robot's mic frames into Conversation.Open, Feed and Close (live:
//     initwirepod's MicTap sink; offline: cmd/cortex-replay reading a recorded session).
//   - WakeDetector: an adapter calls Conversation.Wake with the robot timestamp of the wake word's end (pkg/oww
//     and pkg/wakeword, through initwirepod's wake handler).
//   - VAD, EchoCanceller, Transcriber, Brain and Robot: the interfaces below.

// Frame is one mic frame of one robot: samples at the true mic rate and the robot timestamp (ms) they were
// heard at, on the same clock wake detections use.
type Frame struct {
	Samples       []int16
	Timestamp     uint64
	SpeakerActive bool // the robot was playing sound
	// Real is the mic audio under Samples when the half-duplex gate silenced the frame (Samples are zeros), nil
	// otherwise. Only the capture's wake grace window listens to it (CaptureConfig.WakeGrace).
	Real []int16
}

// VAD says whether one mic frame holds speech. Each robot stream gets its own instance (see Config.NewVAD).
type VAD interface {
	IsSpeech(samples []int16) bool
}

// EchoCanceller removes the robot's own sound from its mic frames before anything listens to them.
type EchoCanceller interface {
	Cancel(esn string, f Frame) Frame
}

// PassThrough is the EchoCanceller that changes nothing: the robot's processed mic already removes most of
// its own sound.
type PassThrough struct{}

func (PassThrough) Cancel(_ string, f Frame) Frame { return f }

// Transcript is what the user said, in the language the transcriber chose (a whisper code such as "en").
type Transcript struct {
	Text     string
	Language string
}

// Transcriber turns one captured utterance into text.
type Transcriber interface {
	Transcribe(ctx context.Context, u Utterance) (Transcript, error)
}

// Answer is what the robot should do about an utterance: run Intent (with its parameters as JSON), or say
// Text. An Answer with neither means the brain already handled the request itself.
type Answer struct {
	Intent     string
	ParamsJSON string
	Text       string
}

// Brain decides the answer to what the user said.
type Brain interface {
	Answer(ctx context.Context, esn string, t Transcript) (Answer, error)
}

// Robot executes the cortex's answers on one robot.
type Robot interface {
	// SendIntentResult delivers an intent as if the robot's cloud had matched it (for example intent_clock_time).
	SendIntentResult(esn, intent, paramsJSON string) error
	// SendKnowledge makes the robot say answer, the reply to queryText.
	SendKnowledge(esn, answer, queryText string) error
}
