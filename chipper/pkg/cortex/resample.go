package cortex

import "math"

// resampleTaps is the windowed-sinc half width, in input samples.
const resampleTaps = 16

// Resample converts s16 samples at rate from into float samples in [-1, 1) at rate to, with a Lanczos-windowed
// sinc interpolator (low-passed when downsampling). Vector's mic really runs at 15625 Hz although MicTap
// announces 16000; whisper needs 16000.
func Resample(in []int16, from, to int) []float32 {
	if from == to || from <= 0 || to <= 0 {
		out := make([]float32, len(in))
		for i, s := range in {
			out[i] = float32(s) / 32768
		}
		return out
	}
	n := int(int64(len(in)) * int64(to) / int64(from))
	out := make([]float32, n)
	step := float64(from) / float64(to) // input samples per output sample
	cutoff := math.Min(1, float64(to)/float64(from))
	for i := range out {
		x := float64(i) * step
		c := int(math.Floor(x))
		var sum, weight float64
		for k := c - resampleTaps + 1; k <= c+resampleTaps; k++ {
			if k < 0 || k >= len(in) {
				continue
			}
			d := x - float64(k)
			w := cutoff * sinc(cutoff*d) * sinc(d/resampleTaps)
			sum += w * float64(in[k])
			weight += w
		}
		if weight != 0 {
			out[i] = float32(sum / weight / 32768)
		}
	}
	return out
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}
