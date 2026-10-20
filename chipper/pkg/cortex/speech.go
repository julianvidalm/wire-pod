package cortex

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// SpeechRate is the rate of every SpeechChunk: 16 kHz mono s16, what the robot's SDK audio stream plays.
const SpeechRate = 16000

// Synthesizer is the port a text-to-speech engine adapter implements (pkg/cortex/piper): one sentence in
// language lang ("es", "en") to mono s16 samples at the engine's own rate.
type Synthesizer interface {
	Synthesize(ctx context.Context, lang, sentence string) (samples []int16, rate int, err error)
}

// Voice is the port the conversation speaks answers through. Speak returns at once; the speech arrives
// sentence by sentence on Speech.Chunks, the first one as soon as it is synthesized.
type Voice interface {
	Speak(ctx context.Context, text, lang string) *Speech
}

// SpeechChunk is one spoken sentence: Samples at SpeechRate, level-normalized.
type SpeechChunk struct {
	Index   int
	Text    string
	Samples []int16
}

// SpeechResult is known once a Speech is done: how many sentences were sent and how long they play.
type SpeechResult struct {
	Sentences int
	Duration  time.Duration
}

// Speech is one answer being synthesized. Chunks is closed when synthesis ends (done, failed or cancelled);
// Wait then returns the result and the error that stopped it, if any.
type Speech struct {
	chunks chan SpeechChunk
	done   chan struct{}
	res    SpeechResult
	err    error
}

func (s *Speech) Chunks() <-chan SpeechChunk { return s.chunks }

// Wait blocks until synthesis ends. The caller should drain Chunks (or cancel) so it can.
func (s *Speech) Wait() (SpeechResult, error) {
	<-s.done
	return s.res, s.err
}

// SentenceVoice is the Voice over a Synthesizer: it splits the text into sentences, synthesizes them in order,
// resamples each one to SpeechRate and normalizes its level to levelDB (dBFS RMS over the speech).
type SentenceVoice struct {
	synth   Synthesizer
	levelDB float64
	timeout time.Duration // per sentence; 0: none
}

// WithSentenceTimeout bounds synthesizing one sentence (0: no bound): a sentence that takes longer stops the
// speech with context.DeadlineExceeded, so a hung engine never holds an answer.
func (v *SentenceVoice) WithSentenceTimeout(d time.Duration) *SentenceVoice {
	v.timeout = d
	return v
}

// synthesize runs one sentence under the sentence timeout.
func (v *SentenceVoice) synthesize(ctx context.Context, lang, sentence string) ([]int16, int, error) {
	if v.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, v.timeout)
		defer cancel()
	}
	pcm, rate, err := v.synth.Synthesize(ctx, lang, sentence)
	if err == nil {
		err = ctx.Err()
	}
	return pcm, rate, err
}

func NewSentenceVoice(s Synthesizer, levelDB float64) *SentenceVoice {
	return &SentenceVoice{synth: s, levelDB: levelDB}
}

// Speak synthesizes text in lang. Cancelling ctx stops it after the sentence in flight; no later sentence is
// synthesized and Wait returns ctx's error.
func (v *SentenceVoice) Speak(ctx context.Context, text, lang string) *Speech {
	sentences := SplitSentences(text, lang)
	s := &Speech{chunks: make(chan SpeechChunk, len(sentences)), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		defer close(s.chunks)
		var samples int
		for i, sentence := range sentences {
			if err := ctx.Err(); err != nil {
				s.err = err
				break
			}
			pcm, rate, err := v.synthesize(ctx, lang, sentence)
			if err != nil {
				s.err = fmt.Errorf("sentence %d: %w", i+1, err)
				break
			}
			pcm = NormalizeLevel(ResampleS16(pcm, rate, SpeechRate), v.levelDB)
			select {
			case s.chunks <- SpeechChunk{Index: i, Text: sentence, Samples: pcm}:
			case <-ctx.Done():
				s.err = ctx.Err()
			}
			if s.err != nil {
				break
			}
			samples += len(pcm)
			s.res.Sentences++
		}
		s.res.Duration = time.Duration(samples) * time.Second / SpeechRate
	}()
	return s
}

// abbreviations end with a period that does not end a sentence (lowercase, without the final period).
var abbreviations = map[string]bool{
	// en
	"mr": true, "mrs": true, "ms": true, "dr": true, "st": true, "jr": true, "sr": true, "prof": true,
	"vs": true, "e.g": true, "i.e": true, "a.m": true, "p.m": true, "approx": true,
	// es
	"sra": true, "srta": true, "dra": true, "ud": true, "uds": true, "vd": true, "lic": true, "ing": true,
	"ej": true, "pág": true, "núm": true, "av": true, "avda": true, "aprox": true, "tel": true,
}

const closers = "\"'”’»)]"

// SplitSentences splits text into the sentences to synthesize one by one: after . ! ? … (and any closing quote
// or bracket) followed by a space, and at line breaks. A period does not end a sentence after a known
// abbreviation (Sr., Dra., p. ej., Mr., e.g.) or an initial, nor when the next word starts in lowercase or
// with a digit; Spanish ¿ ¡ open the sentence they belong to.
func SplitSentences(text, lang string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		start := 0
		for i := 0; i < len(line); {
			r, w := utf8.DecodeRuneInString(line[i:])
			if !isTerminator(r) {
				i += w
				continue
			}
			end := i
			periodsOnly := true
			for end < len(line) {
				r, w := utf8.DecodeRuneInString(line[end:])
				if !isTerminator(r) {
					break
				}
				if r != '.' && r != '…' {
					periodsOnly = false
				}
				end += w
			}
			for end < len(line) {
				r, w := utf8.DecodeRuneInString(line[end:])
				if !strings.ContainsRune(closers, r) {
					break
				}
				end += w
			}
			next := strings.TrimLeftFunc(line[end:], unicode.IsSpace)
			boundary := end == len(line) || (len(next) < len(line[end:]))
			if boundary && periodsOnly && next != "" {
				if continues(next) || (line[i] == '.' && end-i == 1 && isAbbreviation(line[start:i])) {
					boundary = false
				}
			}
			if boundary {
				out = appendSentence(out, line[start:end])
				start = end
			}
			i = end
		}
		out = appendSentence(out, line[start:])
	}
	return out
}

func isTerminator(r rune) bool { return r == '.' || r == '!' || r == '?' || r == '…' }

func continues(next string) bool {
	r, _ := utf8.DecodeRuneInString(next)
	return unicode.IsLower(r) || unicode.IsDigit(r)
}

func isAbbreviation(before string) bool {
	fields := strings.Fields(before)
	if len(fields) == 0 {
		return false
	}
	word := strings.ToLower(strings.TrimLeft(fields[len(fields)-1], "\"'“‘«(¿¡"))
	return utf8.RuneCountInString(word) == 1 || abbreviations[word]
}

func appendSentence(out []string, s string) []string {
	if s = strings.TrimSpace(s); s != "" {
		out = append(out, s)
	}
	return out
}

// ResampleS16 is Resample with s16 output (rounded and clamped). When from/to reduces to a ratio with a small
// denominator (22050 -> 16000 is 441/320) it uses one precomputed kernel per phase, about 10x faster.
func ResampleS16(in []int16, from, to int) []int16 {
	g := gcd(from, to)
	if from == to || from <= 0 || to <= 0 || to/g > 4096 || len(in) < 2*resampleTaps {
		f := Resample(in, from, to)
		out := make([]int16, len(f))
		for i, v := range f {
			out[i] = clampS16(float64(v) * 32768)
		}
		return out
	}
	p, q := from/g, to/g // output i sits at input i*p/q
	cutoff := math.Min(1, float64(to)/float64(from))
	kernels := make([][2 * resampleTaps]float64, q)
	for ph := range kernels {
		d0 := float64(ph) / float64(q) // fractional position past the centre sample
		var weight float64
		for j := range kernels[ph] {
			d := d0 + float64(resampleTaps-1-j)
			kernels[ph][j] = cutoff * sinc(cutoff*d) * sinc(d/resampleTaps)
			weight += kernels[ph][j]
		}
		for j := range kernels[ph] {
			kernels[ph][j] /= weight
		}
	}
	n := int(int64(len(in)) * int64(to) / int64(from))
	out := make([]int16, n)
	step := float64(from) / float64(to)
	for i := range out {
		c := int(int64(i) * int64(p) / int64(q))
		lo := c - resampleTaps + 1
		if lo < 0 || c+resampleTaps >= len(in) {
			out[i] = clampS16(resampleAt(in, float64(i)*step, cutoff)) // the kernel passes an end
			continue
		}
		k := &kernels[int(int64(i)*int64(p)%int64(q))]
		var sum float64
		for j, w := range k {
			sum += w * float64(in[lo+j])
		}
		out[i] = clampS16(sum)
	}
	return out
}

// resampleAt is Resample's interpolation at input position x, renormalized over the samples that exist.
func resampleAt(in []int16, x, cutoff float64) float64 {
	c := int(math.Floor(x))
	var sum, weight float64
	for k := max(0, c-resampleTaps+1); k <= c+resampleTaps && k < len(in); k++ {
		d := x - float64(k)
		w := cutoff * sinc(cutoff*d) * sinc(d/resampleTaps)
		sum += w * float64(in[k])
		weight += w
	}
	if weight == 0 {
		return 0
	}
	return sum / weight
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

const (
	levelBlock    = SpeechRate / 50               // 20 ms
	levelFloorRMS = 32768 * 0.0031622776601683794 // -50 dBFS: quieter blocks are pauses, not speech
	levelPeakMax  = 32768 * 0.8912509381337456    // -1 dBFS
)

// NormalizeLevel scales samples so the speech (20 ms blocks above -50 dBFS) has an RMS of targetDB dBFS,
// with the gain lowered if the loudest sample would pass -1 dBFS. Samples with no speech come back unchanged.
func NormalizeLevel(in []int16, targetDB float64) []int16 {
	out := make([]int16, len(in))
	copy(out, in)
	var sum float64
	var n, peak int
	for b := 0; b < len(in); b += levelBlock {
		blk := in[b:min(b+levelBlock, len(in))]
		var bs float64
		for _, v := range blk {
			bs += float64(v) * float64(v)
			if a := abs(int(v)); a > peak {
				peak = a
			}
		}
		if math.Sqrt(bs/float64(len(blk))) >= levelFloorRMS {
			sum += bs
			n += len(blk)
		}
	}
	if n == 0 {
		return out
	}
	gain := 32768 * math.Pow(10, targetDB/20) / math.Sqrt(sum/float64(n))
	if float64(peak)*gain > levelPeakMax {
		gain = levelPeakMax / float64(peak)
	}
	for i, v := range in {
		out[i] = clampS16(float64(v) * gain)
	}
	return out
}

func clampS16(v float64) int16 {
	v = math.Round(v)
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
