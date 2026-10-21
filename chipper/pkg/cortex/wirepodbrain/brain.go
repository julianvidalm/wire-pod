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
	// MinPartialCoverage replaces minPartialCoverage when set (the intent audit compares thresholds).
	MinPartialCoverage float64
	// FirstPhrase and Substring are for the intent audit only, to compare with the live rule: FirstPhrase keeps
	// the intent and keyphrase wire-pod's matcher returns (the first in list order) instead of the longest keyphrase
	// in the question; Substring lets a keyphrase match inside a word, as wire-pod's stock matcher does.
	FirstPhrase, Substring bool
	// CountFiller, for the intent audit only (the live brain leaves it off), counts the politeness and filler words
	// of FillerWords in the question's word count, as the rule did before.
	CountFiller bool
}

type Brain struct{ cfg Config }

func New(cfg Config) *Brain { return &Brain{cfg: cfg} }

// Answer matches the transcript against the intents of its language and falls back to the LLM.
func (b *Brain) Answer(ctx context.Context, esn string, t cortex.Transcript) (cortex.Answer, error) {
	// Only the routed intent reaches wire-pod's matcher (which then parses its parameters): no route or a weak one
	// leaves none, so plugins and custom intents still run and then the LLM answers.
	var intents []vars.JsonIntent
	var exact bool
	var phrase string
	if r, ok := b.Route(t); ok && !r.Weak {
		exact, phrase = r.Exact, r.Phrase
		for _, in := range b.intents(t) {
			if in.Name == r.Intent {
				intents = append(intents, in)
			}
		}
	}
	rec := &recordingStream{ctx: ctx}
	req := &vtt.IntentRequest{Device: esn, Session: "cortex", Stream: rec, FirstReq: &pb.StreamingIntentRequest{}}
	// isOpus selects wire-pod's current parameter parser, the one every robot that streams Opus gets.
	text := strings.ToLower(t.Text)
	if exact {
		text = phrase // the keyphrase whisper's punctuation hid from wire-pod's exact comparison
	}
	if ttr.ProcessTextAll(req, text, intents, true) {
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

// intents is the intent list for the transcript's language.
func (b *Brain) intents(t cortex.Transcript) []vars.JsonIntent {
	intents, ok := b.cfg.Intents[t.Language]
	if !ok {
		intents = b.cfg.Intents[b.cfg.DefaultLanguage]
	}
	return intents
}

// Route is the brain's intent step for one transcript: the keyphrase wire-pod's matcher picks, how much of the
// question it covers, and whether that match is too weak to answer (Weak: the LLM answers instead).
type Route struct {
	ttr.IntentMatch
	Coverage float64 // the phrase's share of the question's words (1 for an exact match)
	Weak     bool
}

// Route says which intent the brain's matcher picks for t, before plugins, custom intents and parameters; false
// when no keyphrase matches. An exact match is wire-pod's; a partial one is the intent with the longest keyphrase in
// the question ("go to sleep" in "Go to sleep.", not "sleep"; "gira a la izquierda", not "gira"), which must begin
// at the start of a word, and its coverage is that keyphrase's share of the question's words without FillerWords.
// Answer follows it: a weak route goes to the LLM.
func (b *Brain) Route(t cortex.Transcript) (Route, bool) {
	intents, wordStart := b.intents(t), !b.cfg.Substring
	m, ok := ttr.MatchIntentWith(t.Text, intents, wordStart)
	if !ok {
		return Route{}, false
	}
	r := Route{IntentMatch: m, Coverage: 1}
	if m.Exact {
		return r, true
	}
	phrase := m.Phrase
	if !b.cfg.FirstPhrase {
		r.IntentMatch, phrase = longestMatch(t.Text, intents, wordStart)
	}
	min := b.cfg.MinPartialCoverage
	if min <= 0 {
		min = minPartialCoverage
	}
	text := t.Text
	if !b.cfg.CountFiller {
		text = withoutFiller(text, FillerWords[t.Language])
	}
	r.Coverage = min1(coverage(phrase, text))
	r.Weak = r.Coverage < min
	return r, true
}

// longestMatch is the partial match with the most words among every intent's keyphrases that text holds (the
// first in list order on a tie): "gira a la izquierda" (turn left), not "gira" (turn around). Its Phrase is that
// keyphrase. Intents that require an exact match are skipped, as in wire-pod's matcher.
func longestMatch(text string, intents []vars.JsonIntent, wordStart bool) (ttr.IntentMatch, string) {
	lower := strings.ToLower(text)
	var best ttr.IntentMatch
	n := 0
	for _, in := range intents {
		if in.RequireExactMatch {
			continue
		}
		for _, p := range in.Keyphrases {
			p = strings.ToLower(p)
			if w := len(words(p)); w > n && ttr.ContainsPhrase(lower, p, wordStart) {
				best, n = ttr.IntentMatch{Intent: in.Name, Phrase: p}, w
			}
		}
	}
	return best, best.Phrase
}

// FillerWords are the politeness and filler phrases left out of the question's word count when measuring a partial
// match's coverage, so they do not make a command longer: "Go forward a bit." is "forward" in "go forward" (1 of 2),
// "Ven aquí, por favor." is "ven aquí". The user chose this list with the intent audit ("0,3 sin contar cortesía",
// robot-wake 2026-10-20, odd/reports/intent-audit.md); keep it short: a word here never counts against a command
// but never helps a non-command either.
var FillerWords = map[string][]string{
	"en": {"please", "a bit", "a little", "now", "to you", "for me", "can you", "could you", "would you"},
	"es": {"por favor", "un poco", "ahora", "ya", "puedes", "podrías", "para ti"},
}

// withoutFiller removes the filler phrases (whole words) from text.
func withoutFiller(text string, filler []string) string {
	w := words(collapseRepeats(text))
	for _, f := range filler {
		fw := words(f)
		for i := 0; i+len(fw) <= len(w); {
			if strings.Join(w[i:i+len(fw)], " ") == strings.Join(fw, " ") {
				w = append(w[:i:i], w[i+len(fw):]...)
				continue
			}
			i++
		}
	}
	return strings.Join(w, " ")
}

func min1(v float64) float64 {
	if v > 1 {
		return 1
	}
	return v
}

// minPartialCoverage is the share of the question's words the matched intent's longest keyphrase in it must make
// up for a partial match to answer: "go to sleep" in "Go to sleep." (3 of 3) and "picture" in "Take a picture." (1
// of 3) answer; "first" in "What do you think about the first conditional?" (1 of 8) or "happy to" in a long
// sentence does not, and the LLM answers instead. Exact matches always answer. FillerWords are not counted. 0.3 was
// chosen with the intent audit (cmd/intent-audit, odd/reports/intent-audit.md): on natural sentences 0.4 lost a
// third of the commands.
const minPartialCoverage = 0.3

// coverage is the share of text's words that phrase's words make up, counting a sentence whisper repeated
// ("What time is it? What time is it?") once.
func coverage(phrase, text string) float64 {
	n := len(words(collapseRepeats(text)))
	if n == 0 {
		return 0
	}
	return float64(len(words(phrase))) / float64(n)
}

// collapseRepeats drops each sentence of text that repeats the one before it, without case or punctuation.
func collapseRepeats(text string) string {
	var kept []string
	prev := ""
	for _, s := range strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(".?!¿¡;\n", r) }) {
		key := strings.Join(words(s), " ")
		if key == "" || key == prev {
			continue
		}
		kept, prev = append(kept, s), key
	}
	return strings.Join(kept, " ")
}

// words are text's words in lower case: runs of letters, digits and apostrophes ("what's" is one word).
func words(text string) []string {
	return strings.Fields(ttr.PhraseWords(text))
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
