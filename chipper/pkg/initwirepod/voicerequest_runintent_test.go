package initwirepod

import (
	"context"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/wirepodbrain"
)

// robot-wake T7: a command the matcher misses, which wire-pod's brain's LLM answers with
// {{runIntent||intent_system_charger}}, is the request's one chipper response with that intent: the LLM's words are
// not spoken (no session audio), even with the cortex's voice on.
func TestAVoiceRequestTheLLMRunsAsAnIntentIsOneIntentResponse(t *testing.T) {
	intents, err := wirepodbrain.LoadIntents("../../"+intentDataDir, map[string]string{"en": "en-US", "es": "es-ES"})
	if err != nil {
		t.Fatal(err)
	}
	llm := func(context.Context, string, string, string) (string, error) {
		return "{{runIntent||intent_system_charger}} Heading home now!", nil
	}
	b := &recBrain{inner: wirepodbrain.New(wirepodbrain.Config{Intents: intents, DefaultLanguage: "en", LLM: llm})}
	vt := newVoiceRequestTest(t, "could you head back to your dock?", b, toneVoice, speechPCM(true))
	vt.ask()
	r := vt.only(t)
	wantIntent(t, r, "intent_system_charger")
	if q := r.GetIntentResult().GetQueryText(); q != "could you head back to your dock?" {
		t.Fatalf("query %q", q)
	}
	noSessionCommand(t, vt)
	if !vt.log.has("answer intent intent_system_charger") {
		t.Fatalf("log %q", vt.log.lines)
	}
}
