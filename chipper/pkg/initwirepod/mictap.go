package initwirepod

import (
	"fmt"
	"os"
	"sync"

	"github.com/kercre123/wire-pod/chipper/pkg/logger"
	"github.com/kercre123/wire-pod/chipper/pkg/mictap"
	"github.com/kercre123/wire-pod/chipper/pkg/wakeword"
	"github.com/kercre123/wire-pod/chipper/pkg/wakeword/voskrec"
)

// wakeWordModelEnv names a Vosk model directory (the Spanish small model works best on Vector's microphone).
// When it is set, every robot's MicTap stream also runs through the wake word detector.
const wakeWordModelEnv = "WAKEWORD_VOSK_MODEL"

var (
	micTapSinkOnce sync.Once
	micTapSinkVal  mictap.Sink
)

// micTapSink receives every robot's MicTap stream; both TLS listeners share it.
func micTapSink() mictap.Sink {
	micTapSinkOnce.Do(func() { micTapSinkVal = newMicTapSink() })
	return micTapSinkVal
}

// newMicTapSink always logs each stream. With a wake word model it also runs the detector, which for now only
// logs detections: starting a conversation from one is still to be designed.
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
		Handler:       logWake{},
		HoldoffMs:     300,
		CooldownMs:    1500,
		Logf:          logger.Println,
	})
	logger.Println("Wake word detector on, model", dir)
	return mictap.Tee(logSink, detector)
}

type logWake struct{}

func (logWake) OnWake(d wakeword.Detection) {
	logger.Println(fmt.Sprintf("Wake word: %s heard %q (confidence %.2f) in %q", d.ESN, d.Word.Text, d.Word.Conf, d.Text))
}
