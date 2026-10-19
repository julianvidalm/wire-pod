package main

import (
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/replay"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
)

// With the gate off a wake line is exactly the baseline's: seconds and mic sample from the timestamp counted
// at the mic rate from the file's start, no robot time.
func TestWakeLineWithTheGateOffIsUnchanged(t *testing.T) {
	d := wakeword.Detection{FrameTimestamp: 129680, Word: wakeword.Word{Conf: 0.894}}
	got := wakeLine(d, &replay.Recording{MicRate: 15662}, false)
	if want := "WAKE at 129.68 s (mic sample 2031048) score 0.894"; got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestWakeLineWithTheGateOnCarriesTheRobotTime(t *testing.T) {
	d := wakeword.Detection{FrameTimestamp: 129680, Word: wakeword.Word{Conf: 0.894}}
	got := wakeLine(d, &replay.Recording{MicRate: 15662}, true)
	if want := "WAKE at 129.68 s (mic sample 2031048, robot time 129680 ms) score 0.894"; got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
