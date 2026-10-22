package wirepodbrain

import (
	"context"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
)

// realBrain is the brain over wire-pod's own intent lists (chipper/intent-data) and a recording LLM.
func realBrain(t *testing.T) (*Brain, *fakeLLM) {
	t.Helper()
	intents, err := LoadIntents("../../../intent-data", map[string]string{"en": "en-US", "es": "es-ES"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &fakeLLM{answer: "Sure."}
	return New(Config{Intents: intents, DefaultLanguage: "en", LLM: llm.answerText}), llm
}

// Live 2026-10-20: a keyphrase that is only a small part of a real question ("first" in "the first conditional",
// "happy to" in a long sentence) took the question away from the LLM. Such weak partial matches are ignored.
func TestAWeakPartialMatchIsAnsweredByTheLLM(t *testing.T) {
	for _, text := range []string{
		"What do you think about the first conditional?",
		"Can you explain me the first conditional please?",
		"I'm Vector. Happy to chat with you. What can I do for you today?",
	} {
		b, llm := realBrain(t)
		got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: text, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if got != (cortex.Answer{Text: "Sure."}) || len(llm.calls) != 1 || llm.calls[0] != text {
			t.Fatalf("%q: got %+v, LLM asked %q; want the LLM's answer", text, got, llm.calls)
		}
	}
}

// A partial match that is most of the question still is the intent, also when whisper repeats the sentence.
func TestAStrongPartialMatchIsStillTheIntent(t *testing.T) {
	for _, text := range []string{"What time is it?", "What time is it? What time is it?"} {
		b, llm := realBrain(t)
		got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: text, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Intent != "intent_clock_time" || len(llm.calls) != 0 {
			t.Fatalf("%q: got %+v, LLM asked %q; want intent_clock_time", text, got, llm.calls)
		}
	}
}

// "Cuéntame un chiste" keeps the answer it gets today.
func TestCuentameUnChisteKeepsItsAnswer(t *testing.T) {
	b, llm := realBrain(t)
	got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: "Cuéntame un chiste.", Language: "es"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cuéntame un chiste: %+v, LLM %q", got, llm.calls)
	if got.Intent != cuentameWant || (cuentameWant == "" && len(llm.calls) != 1) {
		t.Fatalf("got %+v, LLM %q; want intent %q", got, llm.calls, cuentameWant)
	}
}

// cuentameWant is what "Cuéntame un chiste." got before the weak-match rule ("" means the LLM answered).
const cuentameWant = ""

func TestCoverageCountsARepeatedSentenceOnce(t *testing.T) {
	for text, want := range map[string]float64{
		"What time is it?":                  0.75,
		"What time is it? What time is it?": 0.75,
		"what time is it what time is it":   0.375, // no sentence marks: nothing to collapse
		"":                                  0,
	} {
		if got := coverage("time is it", text); got != want {
			t.Fatalf("%q: coverage %v, want %v", text, got, want)
		}
	}
}

// An exact match is always the intent, however short its keyphrase: "first" said alone is still the fist bump.
func TestAnExactMatchIsAlwaysTheIntent(t *testing.T) {
	b, llm := realBrain(t)
	got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: "First", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Intent != "intent_play_fistbump" || len(llm.calls) != 0 {
		t.Fatalf("got %+v, LLM %q; want intent_play_fistbump", got, llm.calls)
	}
}

// The live rule (user's choice "Larga 0,3"): a partial match's coverage is that of the matched intent's longest
// keyphrase in the question, at least 0.3 of its words. Short natural commands answer again.
func TestShortNaturalCommandsAreIntents(t *testing.T) {
	for text, want := range map[cortex.Transcript]string{
		{Text: "Go to sleep.", Language: "en"}:         "intent_system_sleep",      // "go to sleep" 3/3, not "sleep" 1/3
		{Text: "Take a picture.", Language: "en"}:      "intent_photo_take_extend", // "picture" 1/3: answers at 0.3
		{Text: "Gira a la izquierda.", Language: "es"}: "intent_imperative_turnleft",
	} {
		b, llm := realBrain(t)
		got, err := b.Answer(context.Background(), "replay", text)
		if err != nil {
			t.Fatal(err)
		}
		if got.Intent != want || len(llm.calls) != 0 {
			t.Fatalf("%q: got %+v, LLM %q; want %s", text.Text, got, llm.calls, want)
		}
	}
}

// A keyphrase must begin at the start of a word: "no" is not in "Buenos", "correcto" not in "Incorrecto".
func TestKeyphrasesBeginAtAWord(t *testing.T) {
	for text, notWant := range map[cortex.Transcript]string{
		{Text: "Buenos días.", Language: "es"}: "intent_imperative_negative",
		{Text: "Incorrecto.", Language: "es"}:  "intent_imperative_affirmative",
	} {
		b, _ := realBrain(t)
		got, err := b.Answer(context.Background(), "replay", text)
		if err != nil {
			t.Fatal(err)
		}
		if got.Intent == notWant {
			t.Fatalf("%q: got %+v", text.Text, got)
		}
	}
}

// Whisper writes punctuation: an exact keyphrase still matches the words of the question ("No." is "no", an intent
// that requires an exact match).
func TestAnExactMatchIgnoresPunctuation(t *testing.T) {
	for _, text := range []string{"No.", "No thanks.", "no"} {
		b, llm := realBrain(t)
		got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: text, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Intent != "intent_imperative_negative" || len(llm.calls) != 0 {
			t.Fatalf("%q: got %+v, LLM %q; want intent_imperative_negative", text, got, llm.calls)
		}
	}
}

// An apostrophe stays inside its word when counting coverage: "what's" is one word.
func TestCoverageCountsWhatsAsOneWord(t *testing.T) {
	if got := coverage("weather forecast", "What's the weather forecast for tomorrow?"); got != 2.0/6 {
		t.Fatalf("coverage %v, want 2/6", got)
	}
}

// The live rule (user's choice "0,3 sin contar cortesía") leaves politeness and filler words out of the question's
// word count: "Go forward a bit." is "forward" in "go forward" (1 of 2), "Ven aquí, por favor." "ven aquí" (2 of 2).
func TestPolitenessWordsDoNotDiluteACommand(t *testing.T) {
	for text, want := range map[cortex.Transcript]string{
		{Text: "Go forward a bit.", Language: "en"}:    "intent_imperative_forward",
		{Text: "Ven aquí, por favor.", Language: "es"}: "intent_imperative_come",
	} {
		b, llm := realBrain(t)
		got, err := b.Answer(context.Background(), "replay", text)
		if err != nil {
			t.Fatal(err)
		}
		if got.Intent != want || len(llm.calls) != 0 {
			t.Fatalf("%q: got %+v, LLM %q; want %s", text.Text, got, llm.calls, want)
		}
	}
}
