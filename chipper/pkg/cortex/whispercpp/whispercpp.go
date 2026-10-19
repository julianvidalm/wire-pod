// Package whispercpp is the cortex Transcriber adapter over whisper.cpp, the engine wire-pod's own
// speech-to-text uses (pkg/wirepod/stt/whisper.cpp). It loads its own model context, so the cortex never
// shares whisper's state with a voice request the robot sends the classic way.
package whispercpp

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/stt/whisperlang"
	whisper "github.com/kercre123/whisper.cpp/bindings/go"
)

const (
	whisperRate = 16000
	// minSamples pads short utterances: whisper needs a little over one second (as wire-pod's STT does).
	minSamples = whisperRate * 1020 / 1000
)

type Config struct {
	ModelPath string   // ggml model file
	Languages []string // whisper codes the user speaks (en, es); empty: whisper's own detection
	Threads   int      // defaults to the number of CPUs
	// FullAudioCtx encodes whisper's whole 30 s window instead of one sized to the utterance: slower on the CPU,
	// but large models (large-v3-turbo) repeat and invent text with the short window (WHISPER_AUDIO_CTX=full).
	FullAudioCtx bool
}

// Transcriber transcribes one utterance at a time; whisper's context is not safe for concurrent use.
type Transcriber struct {
	mu      sync.Mutex
	ctx     *whisper.Context
	params  whisper.Params
	langIDs []int
	threads int
	fullCtx bool
}

// New loads the model.
func New(cfg Config) (*Transcriber, error) {
	ctx := whisper.Whisper_init(cfg.ModelPath)
	if ctx == nil {
		return nil, fmt.Errorf("whisper: cannot load %s", cfg.ModelPath)
	}
	t := &Transcriber{ctx: ctx, threads: cfg.Threads, fullCtx: cfg.FullAudioCtx}
	if t.threads <= 0 {
		t.threads = runtime.NumCPU()
	}
	for _, code := range cfg.Languages {
		id := ctx.Whisper_lang_id(code)
		if id < 0 {
			ctx.Whisper_free()
			return nil, fmt.Errorf("whisper: unknown language %q", code)
		}
		t.langIDs = append(t.langIDs, id)
	}
	t.params = ctx.Whisper_full_default_params(whisper.SamplingStrategy(whisper.SAMPLING_GREEDY))
	t.params.SetTranslate(false)
	t.params.SetPrintSpecial(false)
	t.params.SetPrintProgress(false)
	t.params.SetPrintRealtime(false)
	t.params.SetPrintTimestamps(false)
	t.params.SetThreads(t.threads)
	t.params.SetNoContext(true)
	t.params.SetSingleSegment(true)
	return t, nil
}

// Transcribe resamples the utterance from the robot's true mic rate to 16 kHz, picks its language among the
// configured ones and returns whisper's text without its sound notes ("(beeping)").
func (t *Transcriber) Transcribe(ctx context.Context, u cortex.Utterance) (cortex.Transcript, error) {
	if len(u.Samples) == 0 {
		return cortex.Transcript{}, errors.New("whisper: empty utterance")
	}
	data := cortex.Resample(u.Samples, u.Rate, whisperRate)
	if len(data) < minSamples {
		data = append(data, make([]float32, minSamples-len(data))...)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return cortex.Transcript{}, err
	}
	params := t.params
	if !t.fullCtx {
		params.SetAudioCtx(whisperlang.AudioCtx(len(data)))
	}
	lang := ""
	if id := t.language(data); id >= 0 {
		params.SetLanguage(id)
		lang = whisper.Whisper_lang_str(id)
	}
	if err := t.ctx.Whisper_full(params, data, nil, nil, nil); err != nil {
		return cortex.Transcript{}, fmt.Errorf("whisper: %w", err)
	}
	var text []string
	for i := 0; i < t.ctx.Whisper_full_n_segments(); i++ {
		text = append(text, t.ctx.Whisper_full_get_segment_text(i))
	}
	if lang == "" {
		lang = whisper.Whisper_lang_str(t.ctx.Whisper_full_lang_id())
	}
	return cortex.Transcript{Text: whisperlang.CleanText(strings.Join(text, " ")), Language: lang}, nil
}

// language returns the most likely configured language of data, or -1 to let whisper decide.
func (t *Transcriber) language(data []float32) int {
	if len(t.langIDs) == 0 {
		return -1
	}
	if err := t.ctx.Whisper_pcm_to_mel(data, t.threads); err != nil {
		return t.langIDs[0]
	}
	probs, err := t.ctx.Whisper_lang_auto_detect(0, t.threads)
	if err != nil {
		return t.langIDs[0]
	}
	return whisperlang.Pick(probs, t.langIDs)
}
