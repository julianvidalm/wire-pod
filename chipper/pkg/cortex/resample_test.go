package cortex

import (
	"math"
	"testing"
)

// A tone recorded at Vector's true mic rate keeps its pitch and length once resampled to whisper's 16 kHz.
func TestResampleKeepsPitchAndLength(t *testing.T) {
	const from, to, hz = 15662, 16000, 440.0
	in := make([]int16, from) // one second
	for i := range in {
		in[i] = int16(10000 * math.Sin(2*math.Pi*hz*float64(i)/from))
	}
	out := Resample(in, from, to)
	if len(out) != to {
		t.Fatalf("got %d samples, want %d (one second at %d Hz)", len(out), to, to)
	}
	var errSum, refSum float64
	for i := 100; i < len(out)-100; i++ { // the edges lack neighbours
		want := 10000.0 / 32768 * math.Sin(2*math.Pi*hz*float64(i)/to)
		errSum += (float64(out[i]) - want) * (float64(out[i]) - want)
		refSum += want * want
	}
	if ratio := math.Sqrt(errSum / refSum); ratio > 0.01 {
		t.Fatalf("resampled tone is off by %.2f%% RMS", 100*ratio)
	}
}

func TestResampleAtTheSameRateOnlyScales(t *testing.T) {
	out := Resample([]int16{-32768, 0, 16384}, 16000, 16000)
	if len(out) != 3 || out[0] != -1 || out[1] != 0 || out[2] != 0.5 {
		t.Fatalf("got %v", out)
	}
}
