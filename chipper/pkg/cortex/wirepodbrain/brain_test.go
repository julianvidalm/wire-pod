package wirepodbrain

import (
	"context"
	"errors"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
	"github.com/sashabaranov/go-openai"
)

var testIntents = map[string][]vars.JsonIntent{
	"en": {{Name: "intent_clock_time", Keyphrases: []string{"what time is it", "the time"}}},
	"es": {{Name: "intent_clock_time", Keyphrases: []string{"qué hora es"}}},
}

type fakeLLM struct {
	calls  []string
	answer string
	err    error
}

func (f *fakeLLM) answerText(_ context.Context, _, text string) (string, error) {
	f.calls = append(f.calls, text)
	return f.answer, f.err
}

func TestAMatchedPhraseInEitherLanguageIsAnIntent(t *testing.T) {
	llm := &fakeLLM{answer: "x"}
	b := New(Config{Intents: testIntents, DefaultLanguage: "en", LLM: llm.answerText})
	for _, tr := range []cortex.Transcript{
		{Text: "¿Qué hora es?", Language: "es"},
		{Text: "What time is it?", Language: "en"},
	} {
		got, err := b.Answer(context.Background(), "replay", tr)
		if err != nil {
			t.Fatal(err)
		}
		if got != (cortex.Answer{Intent: "intent_clock_time"}) {
			t.Fatalf("%q: got %+v, want intent_clock_time without parameters", tr.Text, got)
		}
	}
	if len(llm.calls) != 0 {
		t.Fatalf("LLM asked %q for matched intents", llm.calls)
	}
}

func TestAnUnmatchedPhraseIsAnsweredByTheLLMWithoutItsCommands(t *testing.T) {
	llm := &fakeLLM{answer: "{{playAnimationWI||happy}} Estoy muy bien. {{playAnimationWI||love}}"}
	b := New(Config{Intents: testIntents, DefaultLanguage: "en", LLM: llm.answerText})
	got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: "¿Cómo estás?", Language: "es"})
	if err != nil {
		t.Fatal(err)
	}
	if got != (cortex.Answer{Text: "Estoy muy bien."}) {
		t.Fatalf("got %+v", got)
	}
	if len(llm.calls) != 1 || llm.calls[0] != "¿Cómo estás?" {
		t.Fatalf("LLM calls %q", llm.calls)
	}
}

func TestWithTheLLMOffAnUnmatchedPhraseIsUnmatched(t *testing.T) {
	b := New(Config{Intents: testIntents, DefaultLanguage: "en",
		LLM: (&fakeLLM{err: ErrNoLLM}).answerText})
	got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: "tell me a joke", Language: "en"})
	if err != nil || got != (cortex.Answer{Intent: "intent_system_unmatched"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestAnLLMErrorIsReturned(t *testing.T) {
	b := New(Config{Intents: testIntents, LLM: (&fakeLLM{err: errors.New("down")}).answerText})
	if _, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: "hola", Language: "es"}); err == nil {
		t.Fatal("no error")
	}
}

func TestPersonaRenamesTheRobotInTheSystemPrompt(t *testing.T) {
	req := openai.ChatCompletionRequest{Messages: []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are a helpful, animated robot called Vector."},
		{Role: openai.ChatMessageRoleUser, Content: "Vector, hi"},
	}}
	(&LLM{Persona: "Jarvis"}).rename(&req)
	if req.Messages[0].Content != "You are a helpful, animated robot called Jarvis." || req.Messages[1].Content != "Vector, hi" {
		t.Fatalf("got %q / %q", req.Messages[0].Content, req.Messages[1].Content)
	}
}

func TestLoadIntentsReadsOneListPerLanguage(t *testing.T) {
	got, err := LoadIntents("../../../intent-data", map[string]string{"en": "en-US", "es": "es-ES"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got["en"]) == 0 || len(got["es"]) == 0 {
		t.Fatalf("got %d en and %d es intents", len(got["en"]), len(got["es"]))
	}
}
