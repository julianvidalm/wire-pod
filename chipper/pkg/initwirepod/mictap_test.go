package initwirepod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/oww"
)

func TestMicTapSinkOnlyLogsWithoutAWakeWordModel(t *testing.T) {
	t.Setenv(wakeWordModelEnv, "")
	if _, ok := newMicTapSink().(*mictap.LogSink); !ok {
		t.Fatal("expected the log sink alone when no model is configured")
	}
}

func TestMicTapSinkFallsBackToLoggingWhenTheModelFailsToLoad(t *testing.T) {
	t.Setenv(wakeWordModelEnv, t.TempDir()) // an empty directory is not a Vosk model
	if _, ok := newMicTapSink().(*mictap.LogSink); !ok {
		t.Fatal("expected the log sink alone when the model cannot be loaded")
	}
}

// Needs a real Vosk model: WAKEWORD_TEST_MODEL=<model dir> go test ./pkg/initwirepod/ -run WithAModel
func TestMicTapSinkRunsTheDetectorWithAModel(t *testing.T) {
	dir := os.Getenv("WAKEWORD_TEST_MODEL")
	if dir == "" {
		t.Skip("WAKEWORD_TEST_MODEL not set")
	}
	t.Setenv(wakeWordModelEnv, dir)
	if _, ok := newMicTapSink().(*mictap.LogSink); ok {
		t.Fatal("expected the log sink plus the detector")
	}
}

func TestWakeDetectorStaysVoskWhenWakeDetectorIsUnset(t *testing.T) {
	t.Setenv(wakeDetectorEnv, "")
	t.Setenv(owwModelEnv, writeFile(t, "model.onnx")) // an oww configuration alone must not switch detectors
	t.Setenv(wakeWordModelEnv, "")
	if sink, desc, err := wakeDetector(); sink != nil || err != nil {
		t.Fatalf("got %T %q %v, want no detector: Vosk without a model, as before", sink, desc, err)
	}
	t.Setenv(wakeWordModelEnv, t.TempDir())
	if _, _, err := wakeDetector(); err == nil || !strings.Contains(err.Error(), "vosk") {
		t.Fatalf("got %v, want the Vosk model load error", err)
	}
}

func TestWakeDetectorSelectsOpenWakeWord(t *testing.T) {
	model := writeFile(t, "hey_jarvis.onnx")
	t.Setenv(wakeDetectorEnv, "oww")
	t.Setenv(owwModelEnv, model)
	t.Setenv(owwThresholdEnv, "")
	t.Setenv(owwPythonEnv, "/venv/bin/python")
	t.Setenv(owwSidecarEnv, "/x/oww_sidecar.py")
	t.Setenv(owwMicRateEnv, "")
	sink, desc, err := wakeDetector()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sink.(*oww.Detector); !ok {
		t.Fatalf("got %T, want *oww.Detector", sink)
	}
	for _, want := range []string{"openWakeWord", model, "threshold 0.70", "mic 15662 Hz", "/venv/bin/python"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description %q lacks %q", desc, want)
		}
	}
}

func TestWakeDetectorMicRateOverride(t *testing.T) {
	t.Setenv(wakeDetectorEnv, "oww")
	t.Setenv(owwModelEnv, writeFile(t, "hey_jarvis.onnx"))
	t.Setenv(owwThresholdEnv, "")
	t.Setenv(owwMicRateEnv, "16000")
	_, desc, err := wakeDetector()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(desc, "mic 16000 Hz") {
		t.Errorf("description %q lacks the OWW_MIC_RATE override", desc)
	}
}

func TestWakeDetectorRejectsBadOpenWakeWordSettings(t *testing.T) {
	model := writeFile(t, "m.onnx")
	for name, env := range map[string]map[string]string{
		"unknown detector": {wakeDetectorEnv: "porcupine"},
		"no model":         {wakeDetectorEnv: "oww", owwModelEnv: ""},
		"missing model":    {wakeDetectorEnv: "oww", owwModelEnv: "/nonexistent.onnx"},
		"bad threshold":    {wakeDetectorEnv: "oww", owwModelEnv: model, owwThresholdEnv: "high"},
		"threshold over 1": {wakeDetectorEnv: "oww", owwModelEnv: model, owwThresholdEnv: "1.5"},
		"bad mic rate":     {wakeDetectorEnv: "oww", owwModelEnv: model, owwMicRateEnv: "fast"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, k := range []string{owwModelEnv, owwThresholdEnv, owwMicRateEnv} {
				t.Setenv(k, "")
			}
			for k, v := range env {
				t.Setenv(k, v)
			}
			if sink, _, err := wakeDetector(); err == nil {
				t.Fatalf("got %T and no error", sink)
			}
		})
	}
}

func writeFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
