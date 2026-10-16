package pcled

import (
	"time"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap/mictappb"
	"github.com/kercre123/wire-pod/chipper/pkg/wakeword"
)

// recentRequestWindow: a robot that made its own voice request this recently (it heard "Hey Vector" itself)
// is not woken again for the same sentence.
const recentRequestWindow = 8 * time.Second

// Transcriber turns an utterance (16 kHz mono) into text and its language ("" when not detected); wire-pod's
// whisper provides it.
type Transcriber func(pcm []int16) (text, lang string, err error)

// CommandSender reaches a robot over its MicTap session.
type CommandSender interface {
	Send(esn string, cmd *mictappb.Command) error
}

// Responder is the wake word Handler of the PC-led conversation. When the user finishes a sentence that
// contains Vector's name, it transcribes the whole sentence (when it has a Transcriber), keeps it as the
// robot's pending utterance, and wakes the robot, whose voice request is then answered with that utterance.
type Responder struct {
	Transcribe Transcriber // optional; without it the robot transcribes its own request
	Pending    *Pending
	Sender     CommandSender
	Logf       func(a ...any)
}

var wakeCommand = &mictappb.Command{Command: &mictappb.Command_Wake{Wake: &mictappb.Wake{}}}

func (r *Responder) OnWake(d wakeword.Detection) {
	if !d.Final {
		return // wait for the whole sentence
	}
	// Transcribing takes up to a second or two: do it off the robot's audio stream.
	go r.respond(d)
}

func (r *Responder) respond(d wakeword.Detection) {
	if r.Pending.requestedWithin(d.ESN, recentRequestWindow) {
		r.logf("PC-led: ", d.ESN, " already woke itself; not waking it again")
		return
	}
	if r.Transcribe != nil && len(d.Utterance) > 0 {
		text, lang, err := r.Transcribe(d.Utterance)
		switch {
		case err != nil:
			r.logf("PC-led: transcription failed, the robot will transcribe its own request: ", err)
		case text != "":
			r.Pending.Set(d.ESN, text, lang)
			r.logf("PC-led: ", d.ESN, " said: ", text)
		}
	}
	if err := r.Sender.Send(d.ESN, wakeCommand); err != nil {
		r.logf("PC-led: could not wake ", d.ESN, ": ", err)
	}
}

func (r *Responder) logf(a ...any) {
	if r.Logf != nil {
		r.Logf(a...)
	}
}
