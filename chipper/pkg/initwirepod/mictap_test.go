package initwirepod

import (
	"os"
	"testing"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap"
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
