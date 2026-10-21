package wirepodbrain

import (
	"context"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
)

// Live 2026-10-21 (robot-wake L43): "Go to the charger." went to the LLM ("charger" covers 1 of 4 words), which
// answered it had no command for it. The natural phrasings the intent audit lost are keyphrases now (robot-wake T7 A).
func TestNaturalPhrasingsOfLostCommandsAreIntents(t *testing.T) {
	for text, want := range map[cortex.Transcript]string{
		{Text: "Go to the charger.", Language: "en"}:       "intent_system_charger",
		{Text: "Go to your charger.", Language: "en"}:      "intent_system_charger",
		{Text: "Go charge.", Language: "en"}:               "intent_system_charger",
		{Text: "Take a nap.", Language: "en"}:              "intent_system_sleep",
		{Text: "Bye bye.", Language: "en"}:                 "intent_greeting_goodbye",
		{Text: "Turn to the left.", Language: "en"}:        "intent_imperative_turnleft",
		{Text: "Turn to the right.", Language: "en"}:       "intent_imperative_turnright",
		{Text: "Give me a fist bump.", Language: "en"}:     "intent_play_fistbump",
		{Text: "Ve al cargador.", Language: "es"}:          "intent_system_charger",
		{Text: "Ve a cargarte.", Language: "es"}:           "intent_system_charger",
		{Text: "Vete al cargador.", Language: "es"}:        "intent_system_charger",
		{Text: "Gira hacia la izquierda.", Language: "es"}: "intent_imperative_turnleft",
		{Text: "Gira hacia la derecha.", Language: "es"}:   "intent_imperative_turnright",
		{Text: "Date una vuelta.", Language: "es"}:         "intent_imperative_turnaround",
		{Text: "Hora de dormir.", Language: "es"}:          "intent_system_sleep",
	} {
		b, llm := realBrain(t)
		got, err := b.Answer(context.Background(), "replay", text)
		if err != nil {
			t.Fatal(err)
		}
		if got.Intent != want || len(llm.calls) != 0 {
			t.Errorf("%q: got %+v, LLM %q; want %s", text.Text, got, llm.calls, want)
		}
	}
}
