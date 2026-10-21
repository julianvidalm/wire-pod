package cortex

import "context"

// The cortex's ports. Every component of the conversation sits behind one of them, so an adapter can be
// swapped by configuration and compared on the same corpus:
//
//   - the audio: initwirepod hands each voice request's audio to a RequestCapture, and its utterance to
//     Conversation.Respond with the request's Reply.
//   - VAD, Transcriber and Brain: the interfaces below; the cortex's voice: TargetSpeaker (the Talker).

// Frame is 10 ms of a voice request's audio: samples at the request's rate and the time (ms from the request's
// start) they were heard at.
type Frame struct {
	Samples   []int16
	Timestamp uint64
	// Real is the audio of a frame heard through the wake grace window (Samples are zeros under the gate), nil
	// otherwise. Only the capture's wake grace window listens to it (CaptureConfig.WakeGrace).
	Real []int16
}

// VAD says whether one frame holds speech. Each request gets its own instance (see Config.NewVAD).
type VAD interface {
	IsSpeech(samples []int16) bool
}

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
