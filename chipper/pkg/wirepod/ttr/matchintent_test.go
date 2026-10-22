package wirepod_ttr

import (
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
)

// MatchIntent keeps ProcessTextAll's order: a perfect match anywhere wins over an earlier partial one, and a
// partial match skips intents that require an exact match.
func TestMatchIntentPrefersAPerfectMatchAndSkipsExactOnlyIntents(t *testing.T) {
	intents := []vars.JsonIntent{
		{Name: "intent_exact_only", Keyphrases: []string{"time"}, RequireExactMatch: true},
		{Name: "intent_partial", Keyphrases: []string{"time is"}},
		{Name: "intent_clock_time", Keyphrases: []string{"what time is it"}},
	}
	for text, want := range map[string]IntentMatch{
		"What time is it":    {Intent: "intent_clock_time", Phrase: "what time is it", Exact: true},
		"so what time is it": {Intent: "intent_partial", Phrase: "time is"},
		"time":               {Intent: "intent_exact_only", Phrase: "time", Exact: true},
	} {
		if got, ok := MatchIntent(text, intents); !ok || got != want {
			t.Fatalf("%q: got %+v, %v; want %+v", text, got, ok, want)
		}
	}
	if _, ok := MatchIntent("hello there", intents); ok {
		t.Fatal("matched nothing-related text")
	}
}
