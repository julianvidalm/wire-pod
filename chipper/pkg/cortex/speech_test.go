package cortex

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestSplitSentences(t *testing.T) {
	cases := []struct {
		lang, text string
		want       []string
	}{
		{"en", "Sure. The math book was sad! Why? Because it had problems.",
			[]string{"Sure.", "The math book was sad!", "Why?", "Because it had problems."}},
		{"es", "¿Te refieres a chistes? Yo sí. ¡Qué bien! ¿Quieres otro?",
			[]string{"¿Te refieres a chistes?", "Yo sí.", "¡Qué bien!", "¿Quieres otro?"}},
		{"es", "El Sr. Pérez y la Dra. Ruiz llegaron a las 3.5 horas, p. ej. hoy. Luego se fueron.",
			[]string{"El Sr. Pérez y la Dra. Ruiz llegaron a las 3.5 horas, p. ej. hoy.", "Luego se fueron."}},
		{"en", "Mr. Smith met Dr. Jones, e.g. at 5 p.m. on St. Mark's day. It rained.",
			[]string{"Mr. Smith met Dr. Jones, e.g. at 5 p.m. on St. Mark's day.", "It rained."}},
		{"en", "Wait... what?! \"Really.\" Yes", []string{"Wait... what?!", "\"Really.\"", "Yes"}},
		{"en", "First line\nSecond line.\n\n  Third  ", []string{"First line", "Second line.", "Third"}},
		{"es", "  ", nil},
	}
	for _, c := range cases {
		if got := SplitSentences(c.text, c.lang); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitSentences(%q, %s) = %q, want %q", c.text, c.lang, got, c.want)
		}
	}
}

func TestResampleS16KeepsLengthAndRate(t *testing.T) {
	const from, to, hz = 22050, 16000, 500.0
	in := make([]int16, from/2) // half a second
	for i := range in {
		in[i] = int16(20000 * math.Sin(2*math.Pi*hz*float64(i)/from))
	}
	out := ResampleS16(in, from, to)
	if len(out) != to/2 {
		t.Fatalf("got %d samples, want %d", len(out), to/2)
	}
	crossings := 0
	for i := 1; i < len(out); i++ {
		if (out[i-1] < 0) != (out[i] < 0) {
			crossings++
		}
	}
	if want := int(2 * hz / 2); crossings < want-2 || crossings > want+2 { // 2 per period, half a second
		t.Fatalf("%d zero crossings, want about %d (pitch changed)", crossings, want)
	}
}

func dbfs(s []int16) float64 {
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return 20 * math.Log10(math.Sqrt(sum/float64(len(s)))/32768)
}

func tone(n int, amp float64) []int16 {
	s := make([]int16, n)
	for i := range s {
		s[i] = int16(amp * math.Sin(2*math.Pi*300*float64(i)/16000))
	}
	return s
}

func TestNormalizeLevelReachesTargetOnSpeech(t *testing.T) {
	quiet := tone(16000, 32768*math.Pow(10, -30.0/20)*math.Sqrt2) // -30 dBFS RMS
	in := append(make([]int16, 16000), quiet...)                  // a second of silence first
	out := NormalizeLevel(in, -20)
	if len(out) != len(in) {
		t.Fatalf("length changed: %d -> %d", len(in), len(out))
	}
	if got := dbfs(out[16000:]); math.Abs(got+20) > 0.5 {
		t.Fatalf("speech at %.1f dBFS, want -20 (silence must not count)", got)
	}
	for _, v := range out[:16000] {
		if v != 0 {
			t.Fatalf("silence became %d", v)
		}
	}
}

func TestNormalizeLevelNeverClips(t *testing.T) {
	// A short loud click in quiet speech: reaching -10 dBFS would clip the click, so the gain stops below it.
	in := tone(16000, 1000)
	in[8000] = 20000
	out := NormalizeLevel(in, -10)
	for i, v := range out {
		if v == math.MaxInt16 || v == math.MinInt16 {
			t.Fatalf("sample %d clipped", i)
		}
	}
	if out[8000] < 28000 || out[8000] > 29300 {
		t.Fatalf("peak %d, want the limit near -1 dBFS (29204)", out[8000])
	}
	if silent := NormalizeLevel(make([]int16, 100), -20); !reflect.DeepEqual(silent, make([]int16, 100)) {
		t.Fatalf("silence changed: %v", silent)
	}
}

// fakeSynth returns one 22.05 kHz tone per sentence; gate, when set, holds each sentence after the first until
// the test releases it.
type fakeSynth struct {
	mu    sync.Mutex
	calls []string
	langs []string
	gate  chan struct{}
}

func (f *fakeSynth) Synthesize(ctx context.Context, lang, sentence string) ([]int16, int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, sentence)
	f.langs = append(f.langs, lang)
	n := len(f.calls)
	f.mu.Unlock()
	if f.gate != nil && n > 1 {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	return tone(22050/10*len(sentence), 8000), 22050, nil
}

func (f *fakeSynth) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestSpeakStreamsSentencesInOrderFirstOneEarly(t *testing.T) {
	synth := &fakeSynth{gate: make(chan struct{})}
	speech := NewSentenceVoice(synth, -20).Speak(context.Background(), "Uno. Dos dos. Tres tres tres.", "es")
	select {
	case c := <-speech.Chunks():
		if c.Index != 0 || c.Text != "Uno." {
			t.Fatalf("first chunk %d %q", c.Index, c.Text)
		}
		if want := 16000 / 10 * len("Uno."); len(c.Samples) != want {
			t.Fatalf("first chunk has %d samples at 16 kHz, want %d", len(c.Samples), want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first sentence did not arrive while the second was still being synthesized")
	}
	close(synth.gate)
	var texts []string
	total := 16000 / 10 * len("Uno.")
	for c := range speech.Chunks() {
		texts = append(texts, c.Text)
		total += len(c.Samples)
	}
	if !reflect.DeepEqual(texts, []string{"Dos dos.", "Tres tres tres."}) {
		t.Fatalf("later chunks %q", texts)
	}
	res, err := speech.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.Sentences != 3 || res.Duration != time.Duration(total)*time.Second/SpeechRate {
		t.Fatalf("result %+v, want 3 sentences and %v", res, time.Duration(total)*time.Second/SpeechRate)
	}
	if synth.langs[0] != "es" {
		t.Fatalf("synth language %q", synth.langs[0])
	}
}

func TestSpeakCancelStopsSynthesis(t *testing.T) {
	synth := &fakeSynth{gate: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	speech := NewSentenceVoice(synth, -20).Speak(ctx, "One. Two. Three. Four.", "en")
	<-speech.Chunks()
	cancel()
	for range speech.Chunks() {
	}
	if _, err := speech.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error %v, want context.Canceled", err)
	}
	if n := synth.callCount(); n > 2 {
		t.Fatalf("%d sentences synthesized after cancel, want at most the one in flight", n)
	}
}

type failSynth struct{}

func (failSynth) Synthesize(context.Context, string, string) ([]int16, int, error) {
	return nil, 0, errors.New("boom")
}

func TestSpeakReportsSynthErrorsAndEmptyText(t *testing.T) {
	speech := NewSentenceVoice(failSynth{}, -20).Speak(context.Background(), "Hi.", "en")
	for range speech.Chunks() {
		t.Fatal("chunk from a failing synth")
	}
	if _, err := speech.Wait(); err == nil {
		t.Fatal("no error from a failing synth")
	}
	empty := NewSentenceVoice(failSynth{}, -20).Speak(context.Background(), " ", "en")
	if res, err := empty.Wait(); err != nil || res.Sentences != 0 || res.Duration != 0 {
		t.Fatalf("empty text: %+v %v", res, err)
	}
}

// The per-phase kernels give what the generic resampler gives, edges included.
func TestResampleS16MatchesResample(t *testing.T) {
	in := make([]int16, 22050/3)
	for i := range in {
		in[i] = int16(12000*math.Sin(float64(i)*0.07) + 6000*math.Sin(float64(i)*0.9))
	}
	ref := Resample(in, 22050, 16000)
	got := ResampleS16(in, 22050, 16000)
	for i := range ref {
		if d := float64(got[i]) - float64(ref[i])*32768; d > 1 || d < -1 {
			t.Fatalf("sample %d: %d, generic %.1f", i, got[i], float64(ref[i])*32768)
		}
	}
}

func BenchmarkResampleS16FiveSeconds(b *testing.B) {
	in := make([]int16, 22050*5)
	for i := range in {
		in[i] = int16(i % 3000)
	}
	for b.Loop() {
		ResampleS16(in, 22050, 16000)
	}
}
