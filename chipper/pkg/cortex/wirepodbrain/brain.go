// Package wirepodbrain is the cortex Brain adapter over wire-pod's own voice logic: the intent matching (with
// its parameter parsing, custom intents and plugins) that answers a robot's voice request, and the LLM it falls
// back to when no intent matches.
//
// wire-pod's matcher answers through the robot's chipper stream; here it answers into a stream that only
// records the intent, which becomes the cortex's Answer. The LLM answer becomes spoken text.
package wirepodbrain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	pb "github.com/digital-dream-labs/api/go/chipperpb"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vtt"
	ttr "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/ttr"
	"google.golang.org/grpc"
)

// ErrNoLLM means the LLM is off; the brain then answers with intent_system_unmatched, as wire-pod does.
var ErrNoLLM = errors.New("the LLM is off")

const unmatchedIntent = "intent_system_unmatched"

type Config struct {
	// Intents holds wire-pod's intent lists by transcript language (whisper codes: "en", "es").
	Intents map[string][]vars.JsonIntent
	// DefaultLanguage picks the list for a transcript in a language without one.
	DefaultLanguage string
	// LLM answers what no intent matched, in lang (ReplyLanguage); nil or ErrNoLLM: intent_system_unmatched.
	LLM func(ctx context.Context, esn, text, lang string) (string, error)
}

type Brain struct{ cfg Config }

func New(cfg Config) *Brain { return &Brain{cfg: cfg} }

// Answer matches the transcript against the intents of its language and falls back to the LLM.
func (b *Brain) Answer(ctx context.Context, esn string, t cortex.Transcript) (cortex.Answer, error) {
	intents, ok := b.cfg.Intents[t.Language]
	if !ok {
		intents = b.cfg.Intents[b.cfg.DefaultLanguage]
	}
	rec := &recordingStream{ctx: ctx}
	req := &vtt.IntentRequest{Device: esn, Session: "cortex", Stream: rec, FirstReq: &pb.StreamingIntentRequest{}}
	// isOpus selects wire-pod's current parameter parser, the one every robot that streams Opus gets.
	if ttr.ProcessTextAll(req, strings.ToLower(t.Text), intents, true) {
		got, ok := rec.result()
		if !ok {
			return cortex.Answer{}, nil // a plugin or custom intent answered by itself
		}
		params := ""
		if len(got.Parameters) > 0 {
			js, err := json.Marshal(got.Parameters)
			if err != nil {
				return cortex.Answer{}, err
			}
			params = string(js)
		}
		return cortex.Answer{Intent: got.Action, ParamsJSON: params}, nil
	}
	if b.cfg.LLM == nil {
		return cortex.Answer{Intent: unmatchedIntent}, nil
	}
	text, err := b.cfg.LLM(ctx, esn, t.Text, ReplyLanguage(t))
	if errors.Is(err, ErrNoLLM) {
		return cortex.Answer{Intent: unmatchedIntent}, nil
	}
	if err != nil {
		return cortex.Answer{}, err
	}
	return cortex.Answer{Text: SpokenText(text)}, nil
}

// ReplyLanguage is the language the answer must be in (L101): Spanish ("es") when whisper heard Spanish or the
// question reads Spanish, English ("en") otherwise, as the cortex's voices are.
func ReplyLanguage(t cortex.Transcript) string {
	if strings.HasPrefix(strings.ToLower(t.Language), "es") || cortex.ReadsSpanish(t.Text) {
		return "es"
	}
	return "en"
}

// SpokenText drops the {{command||parameter}} actions wire-pod's LLM prompt asks for: the robot says the answer
// with its knowledge-graph animations instead.
func SpokenText(answer string) string {
	var said []string
	for _, a := range ttr.GetActionsFromString(answer) {
		if a.Action == ttr.ActionSayText && strings.TrimSpace(a.Parameter) != "" {
			said = append(said, strings.TrimSpace(a.Parameter))
		}
	}
	return strings.Join(said, " ")
}

// recordingStream stands in for the robot's chipper stream and keeps the intent wire-pod sends.
type recordingStream struct {
	grpc.ServerStream
	ctx context.Context
	mu  sync.Mutex
	got *pb.IntentResult
}

func (s *recordingStream) Send(r *pb.IntentResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.got == nil && r.GetIntentResult() != nil {
		s.got = r.GetIntentResult()
	}
	return nil
}

func (s *recordingStream) Recv() (*pb.StreamingIntentRequest, error) {
	return nil, errors.New("the cortex brain has no audio stream")
}

func (s *recordingStream) Context() context.Context { return s.ctx }

func (s *recordingStream) result() (*pb.IntentResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got, s.got != nil
}

// LoadIntents reads wire-pod's intent lists from dir (chipper/intent-data), one per language:
// locales maps a whisper code to a file name without .json, for example {"en": "en-US", "es": "es-ES"}.
func LoadIntents(dir string, locales map[string]string) (map[string][]vars.JsonIntent, error) {
	out := map[string][]vars.JsonIntent{}
	for lang, locale := range locales {
		b, err := os.ReadFile(filepath.Join(dir, locale+".json"))
		if err != nil {
			return nil, err
		}
		var intents []vars.JsonIntent
		if err := json.Unmarshal(b, &intents); err != nil {
			return nil, fmt.Errorf("%s: %w", locale, err)
		}
		out[lang] = intents
	}
	return out, nil
}
