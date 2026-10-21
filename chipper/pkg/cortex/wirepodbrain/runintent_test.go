package wirepodbrain

import (
	"context"
	"strings"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
	ttr "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/ttr"
)

// robot-wake T7 (B): a command the keyphrase matcher misses goes to the LLM, which can run one of the robot's
// action intents with wire-pod's {{command||parameter}} mechanism: {{runIntent||<intent>}}.

func llmBrain(t *testing.T, answer string) (*Brain, *fakeLLM) {
	t.Helper()
	b, llm := realBrain(t)
	llm.answer = answer
	return b, llm
}

func TestTheLLMRunsAWhitelistedIntentAsTheOneAnswer(t *testing.T) {
	b, llm := llmBrain(t, "{{runIntent||intent_system_charger}} Heading to my charger now!")
	got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: "Could you head back to your dock?", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got != (cortex.Answer{Intent: "intent_system_charger"}) {
		t.Fatalf("got %+v, want only intent_system_charger (no spoken text)", got)
	}
	if len(llm.calls) != 1 {
		t.Fatalf("LLM calls %q", llm.calls)
	}
}

func TestAnIntentOutsideTheWhitelistIsIgnoredAndTheTextAnswered(t *testing.T) {
	for _, intent := range []string{"intent_names_ask", "intent_weather_extend", "intent_made_up", ""} {
		b, _ := llmBrain(t, "{{runIntent||"+intent+"}} I'm not sure about that.")
		got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: "Who do you think I am?", Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if got != (cortex.Answer{Text: "I'm not sure about that."}) {
			t.Fatalf("runIntent %q: got %+v, want the text only", intent, got)
		}
	}
}

func TestWithoutRunIntentTheTextIsAnsweredAsBefore(t *testing.T) {
	b, _ := llmBrain(t, "{{playAnimationWI||happy}} Paris is the capital of France.")
	got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: "What's the capital of France?", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got != (cortex.Answer{Text: "Paris is the capital of France."}) {
		t.Fatalf("got %+v", got)
	}
}

// The intent's parameters come from the user's words, by wire-pod's own parameter code, as if the matcher had
// picked that intent.
func TestARunIntentGetsItsParametersFromTheTranscript(t *testing.T) {
	for _, tc := range []struct {
		text, intent, want, params string
	}{
		{"Could your eyes be purple instead?", "intent_imperative_eyecolor",
			"intent_imperative_eyecolor_specific_extend", `{"eye_color":"COLOR_PURPLE"}`},
		{"I can barely hear you, make it loud.", "intent_imperative_volumelevel_extend",
			"intent_imperative_volumelevel_extend", `{"volume_level":"VOLUME_5"}`},
	} {
		b, _ := llmBrain(t, "Sure! {{runIntent||"+tc.intent+"}}")
		got, err := b.Answer(context.Background(), "replay", cortex.Transcript{Text: tc.text, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if got != (cortex.Answer{Intent: tc.want, ParamsJSON: tc.params}) {
			t.Fatalf("%q: got %+v, want %s %s", tc.text, got, tc.want, tc.params)
		}
	}
}

// Every whitelisted intent is one of wire-pod's intents in both languages and has a description for the LLM.
func TestTheWhitelistHoldsKnownActionIntents(t *testing.T) {
	intents, err := LoadIntents("../../../intent-data", map[string]string{"en": "en-US", "es": "es-ES"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range ActionIntents {
		for lang, list := range intents {
			found := false
			for _, in := range list {
				found = found || in.Name == a.Intent
			}
			if !found {
				t.Errorf("%s is not a %s intent", a.Intent, lang)
			}
		}
		if strings.TrimSpace(a.Does) == "" || len(a.Examples) < 2 {
			t.Errorf("%s: needs a description and an example per language", a.Intent)
		}
	}
}

// The cortex's LLM request describes runIntent and the whitelisted intents; wire-pod's own request does not.
func TestOnlyTheCortexRequestOffersRunIntent(t *testing.T) {
	old := vars.APIConfig.Knowledge.CommandsEnable
	vars.APIConfig.Knowledge.CommandsEnable = true
	defer func() { vars.APIConfig.Knowledge.CommandsEnable = old }()
	cortexSys := systemOf((&LLM{}).request("replay", "go to your dock", "en"))
	for _, want := range []string{"Command Name: runIntent", "intent_system_charger", "playAnimationWI"} {
		if !strings.Contains(cortexSys, want) {
			t.Errorf("cortex system prompt lacks %q", want)
		}
	}
	if stock := systemOf(ttr.CreateAIReq("go to your dock", "replay", false, false)); strings.Contains(stock, "runIntent") {
		t.Errorf("wire-pod's own prompt offers runIntent: %q", stock)
	}
}
