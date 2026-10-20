package wirepodbrain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
	ttr "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/ttr"
	"github.com/sashabaranov/go-openai"
)

var testIntents = map[string][]vars.JsonIntent{
	"en": {{Name: "intent_clock_time", Keyphrases: []string{"what time is it", "the time"}}},
	"es": {{Name: "intent_clock_time", Keyphrases: []string{"qué hora es"}}},
}

type fakeLLM struct {
	calls  []string
	langs  []string
	answer string
	err    error
}

func (f *fakeLLM) answerText(_ context.Context, _, text, lang string) (string, error) {
	f.calls = append(f.calls, text)
	f.langs = append(f.langs, lang)
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

// L101: the LLM answers in the language of the question: Spanish when whisper heard Spanish or the question
// reads Spanish, English otherwise.
func TestTheLLMGetsTheQuestionsLanguage(t *testing.T) {
	for _, tc := range []struct {
		tr   cortex.Transcript
		want string
	}{
		{cortex.Transcript{Text: "Contame un chiste.", Language: "es"}, "es"},
		{cortex.Transcript{Text: "Cuéntame un chiste, por favor.", Language: "en"}, "es"},
		{cortex.Transcript{Text: "Tell me a joke.", Language: "en"}, "en"},
		{cortex.Transcript{Text: "Tell me a joke.", Language: ""}, "en"},
	} {
		llm := &fakeLLM{answer: "x"}
		b := New(Config{Intents: testIntents, DefaultLanguage: "en", LLM: llm.answerText})
		if _, err := b.Answer(context.Background(), "replay", tc.tr); err != nil {
			t.Fatal(err)
		}
		if len(llm.langs) != 1 || llm.langs[0] != tc.want {
			t.Fatalf("%+v: LLM got languages %q, want %q", tc.tr, llm.langs, tc.want)
		}
	}
}

func systemOf(req openai.ChatCompletionRequest) string {
	for _, m := range req.Messages {
		if m.Role == openai.ChatMessageRoleSystem {
			return m.Content
		}
	}
	return ""
}

// The cortex's request tells the LLM which language to answer in; the question itself is unchanged.
func TestTheCortexRequestAsksForTheAnswersLanguage(t *testing.T) {
	for lang, want := range map[string]string{"es": "Responde siempre en español.", "en": "Answer in English."} {
		req := (&LLM{Persona: "Jarvis"}).request("replay", "cuéntame un chiste", lang)
		if sys := systemOf(req); !strings.HasSuffix(sys, want) || !strings.Contains(sys, "Jarvis") {
			t.Fatalf("%s: system prompt %q, want it to end with %q", lang, sys, want)
		}
		if last := req.Messages[len(req.Messages)-1]; last.Role != openai.ChatMessageRoleUser ||
			last.Content != "cuéntame un chiste" {
			t.Fatalf("%s: last message %+v", lang, last)
		}
	}
}

// wire-pod's own LLM requests (the robot's knowledge graph, not the cortex) get no language instruction.
func TestWirePodsOwnRequestIsUnchanged(t *testing.T) {
	sys := systemOf(ttr.CreateAIReq("cuéntame un chiste", "replay", false, false))
	if strings.Contains(sys, "Responde siempre") || strings.Contains(sys, "Answer in English") {
		t.Fatalf("wire-pod's system prompt %q got the cortex's language instruction", sys)
	}
	if req := (&LLM{}).request("replay", "hi", ""); systemOf(req) != sys {
		t.Fatalf("without a language the cortex prompt %q differs from wire-pod's %q", systemOf(req), sys)
	}
}
