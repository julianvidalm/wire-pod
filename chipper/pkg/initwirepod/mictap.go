package initwirepod

import (
	"fmt"
	"os"
	"sync"

	"github.com/kercre123/wire-pod/chipper/pkg/logger"
	"github.com/kercre123/wire-pod/chipper/pkg/mictap"
	"github.com/kercre123/wire-pod/chipper/pkg/pcled"
	"github.com/kercre123/wire-pod/chipper/pkg/wakeword"
	"github.com/kercre123/wire-pod/chipper/pkg/wakeword/voskrec"
)

// wakeWordModelEnv names a Vosk model directory (the Spanish small model works best on Vector's microphone).
// When it is set, every robot's MicTap stream also runs through the wake word detector.
const wakeWordModelEnv = "WAKEWORD_VOSK_MODEL"

var (
	micTapSinkOnce sync.Once
	micTapSinkVal  mictap.Sink

	// pcPending holds what the PC heard each robot say, until the robot's voice request picks it up.
	pcPending = pcled.NewPending(pcled.DefaultPendingTTL)

	// utteranceTranscriber is set by binaries with a better recognizer for whole sentences (whisper.cpp).
	utteranceTranscriber pcled.Transcriber
)

// SetUtteranceTranscriber lets the PC-led conversation transcribe whole sentences; call it before
// StartFromProgramInit. Without it the PC only wakes the robot, which transcribes its own request.
func SetUtteranceTranscriber(t pcled.Transcriber) {
	utteranceTranscriber = t
}

// micTapSink receives every robot's MicTap stream; both TLS listeners share it.
func micTapSink() mictap.Sink {
	micTapSinkOnce.Do(func() { micTapSinkVal = newMicTapSink() })
	return micTapSinkVal
}

// newMicTapSink always logs each stream. With a wake word model it also runs the detector: a sentence with
// Vector's name wakes the robot and is answered as its voice request (PC-led conversation, pkg/pcled).
func newMicTapSink() mictap.Sink {
	logSink := mictap.NewLogSink(logger.Println)
	dir := os.Getenv(wakeWordModelEnv)
	if dir == "" {
		return logSink
	}
	model, err := voskrec.LoadModel(dir)
	if err != nil {
		logger.Println("Wake word detector off:", err)
		return logSink
	}
	detector := wakeword.New(wakeword.Config{
		NewRecognizer: model.NewRecognizer,
		Matcher:       wakeword.NewMatcher(wakeword.DefaultWords, wakeword.DefaultMinConfidence),
		Handler: handlers{logWake{}, &pcled.Responder{
			Transcribe: utteranceTranscriber, Pending: pcPending, Sender: sessionsSender{}, Logf: logger.Println}},
		HoldoffMs:  300,
		CooldownMs: 1500,
		Logf:       logger.Println,
	})
	logger.Println("Wake word detector on, model", dir)
	return mictap.Tee(logSink, detector)
}

type logWake struct{}

func (logWake) OnWake(d wakeword.Detection) {
	if d.Final {
		logger.Println(fmt.Sprintf("Wake word: %s finished a sentence with the name (%.1f s): %q",
			d.ESN, float64(len(d.Utterance))/16000, d.Text))
		return
	}
	logger.Println(fmt.Sprintf("Wake word: %s heard %q (confidence %.2f) in %q", d.ESN, d.Word.Text, d.Word.Conf, d.Text))
}

// handlers passes each detection to every handler in order.
type handlers []wakeword.Handler

func (hs handlers) OnWake(d wakeword.Detection) {
	for _, h := range hs {
		h.OnWake(d)
	}
}
