package initwirepod

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/julianvidalm/wire-pod/chipper/pkg/logger"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/oww"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword/voskrec"
)

// wakeDetectorEnv picks the wake word detector: "vosk" (the default) or "oww" (openWakeWord).
const wakeDetectorEnv = "WAKE_DETECTOR"

// wakeWordModelEnv names a Vosk model directory (the Spanish small model works best on Vector's microphone).
// With the Vosk detector selected and this set, every robot's MicTap stream also runs through the detector.
const wakeWordModelEnv = "WAKEWORD_VOSK_MODEL"

// openWakeWord settings, used when WAKE_DETECTOR=oww.
const (
	owwModelEnv     = "OWW_MODEL"     // .onnx model (required)
	owwThresholdEnv = "OWW_THRESHOLD" // lowest score that wakes, default 0.7
	owwPythonEnv    = "OWW_PYTHON"    // python with openwakeword, onnxruntime, soxr and numpy, default python3
	owwSidecarEnv   = "OWW_SIDECAR"   // sidecar script, default oww-sidecar/oww_sidecar.py (wire-pod runs in chipper)
	owwMicRateEnv   = "OWW_MIC_RATE"  // true mic rate the sidecar resamples from, default 15625 (MicTap says 16000)

	owwDefaultThreshold = 0.7
	owwDefaultMicRate   = 15625
	owwRefractoryMs     = 2000
)

var (
	micTapSinkOnce sync.Once
	micTapSinkVal  mictap.Sink
)

// micTapSink receives every robot's MicTap stream; both TLS listeners share it.
func micTapSink() mictap.Sink {
	micTapSinkOnce.Do(func() { micTapSinkVal = newMicTapSink() })
	return micTapSinkVal
}

// newMicTapSink always logs each stream. With a wake word detector configured it also runs it, which for now only
// logs detections: starting a conversation from one is still to be designed.
func newMicTapSink() mictap.Sink {
	var logSink mictap.Sink = mictap.NewLogSink(logger.Println)
	// MICTAP_RECORD_DIR saves every session's mic and speaker audio, to replay real sessions offline.
	if rec := os.Getenv("MICTAP_RECORD_DIR"); rec != "" {
		logSink = mictap.Tee(logSink, mictap.NewRecordSink(rec, logger.Println))
	}
	detector, desc, err := wakeDetector()
	if err != nil {
		logger.Println("Wake word detector off:", err)
		return logSink
	}
	if detector == nil {
		return logSink
	}
	logger.Println("Wake word detector on,", desc)
	return mictap.Tee(logSink, detector)
}

// wakeDetector builds the detector WAKE_DETECTOR selects, or nil when the Vosk default has no model configured.
func wakeDetector() (mictap.Sink, string, error) {
	switch kind := strings.ToLower(strings.TrimSpace(os.Getenv(wakeDetectorEnv))); kind {
	case "", "vosk":
		return voskDetector()
	case "oww":
		return owwDetector()
	default:
		return nil, "", fmt.Errorf("unknown %s %q (want vosk or oww)", wakeDetectorEnv, kind)
	}
}

func voskDetector() (mictap.Sink, string, error) {
	dir := os.Getenv(wakeWordModelEnv)
	if dir == "" {
		return nil, "", nil
	}
	model, err := voskrec.LoadModel(dir)
	if err != nil {
		return nil, "", fmt.Errorf("vosk: %w", err)
	}
	detector := wakeword.New(wakeword.Config{
		NewRecognizer: model.NewRecognizer,
		Matcher:       wakeword.NewMatcher(wakeword.DefaultWords, wakeword.DefaultMinConfidence),
		Handler:       logWake{},
		HoldoffMs:     300,
		CooldownMs:    1500,
		Logf:          logger.Println,
	})
	return detector, "model " + dir, nil
}

func owwDetector() (mictap.Sink, string, error) {
	model := os.Getenv(owwModelEnv)
	if model == "" {
		return nil, "", fmt.Errorf("%s=oww needs %s", wakeDetectorEnv, owwModelEnv)
	}
	if _, err := os.Stat(model); err != nil {
		return nil, "", fmt.Errorf("oww: %w", err)
	}
	threshold, err := envFloat(owwThresholdEnv, owwDefaultThreshold)
	if err != nil {
		return nil, "", err
	}
	if threshold <= 0 || threshold > 1 {
		return nil, "", fmt.Errorf("%s %v is outside (0, 1]", owwThresholdEnv, threshold)
	}
	rate, err := envFloat(owwMicRateEnv, owwDefaultMicRate)
	if err != nil {
		return nil, "", err
	}
	python := envOr(owwPythonEnv, "python3")
	sidecar := envOr(owwSidecarEnv, "oww-sidecar/oww_sidecar.py")
	detector := oww.New(oww.Config{
		Command:      []string{python, sidecar, "--model", model, "--in-rate", strconv.Itoa(int(rate))},
		Word:         "hey jarvis",
		Threshold:    threshold,
		RefractoryMs: owwRefractoryMs,
		Handler:      logWake{},
		Logf:         logger.Println,
	})
	return detector, fmt.Sprintf("openWakeWord model %s, threshold %.2f, mic %d Hz, sidecar %s %s",
		model, threshold, int(rate), python, sidecar), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) (float64, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return f, nil
}

type logWake struct{}

func (logWake) OnWake(d wakeword.Detection) {
	logger.Println(fmt.Sprintf("Wake word: %s heard %q (confidence %.2f) in %q", d.ESN, d.Word.Text, d.Word.Conf, d.Text))
}
