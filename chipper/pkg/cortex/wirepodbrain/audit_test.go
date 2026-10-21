package wirepodbrain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
)

var auditIntents = map[string][]vars.JsonIntent{
	"en": {
		{Name: "intent_play_fistbump", Keyphrases: []string{"first", "fist bump"}},
		{Name: "intent_clock_time", Keyphrases: []string{"time is it", "what time is it"}},
	},
}

func TestAuditCountsHitsHijacksAndMissesPerPhrase(t *testing.T) {
	b := New(Config{Intents: auditIntents, DefaultLanguage: "en"})
	r := b.Audit([]Case{
		{Lang: "en", Text: "First place.", Expected: NoIntent, Source: "hand"},
		{Lang: "en", Text: "What do you think about the first conditional?", Expected: NoIntent, Source: "hand"},
		{Lang: "en", Text: "What time is it?", Expected: "intent_clock_time", Source: "hand"},
		{Lang: "en", Text: "Give me a fist bump.", Expected: "intent_play_fistbump", Source: "hand"},
		{Lang: "en", Text: "Could you please bump fists with me right now?", Expected: "intent_play_fistbump", Source: "hand"},
	})
	if r.Negatives != 2 || r.Hijacks != 1 || r.Positives != 3 || r.Hits != 2 || r.Misses != 1 {
		t.Fatalf("totals %+v", r.Totals)
	}
	worst := r.Phrases()[0]
	if worst.Lang != "en" || worst.Intent != "intent_play_fistbump" || worst.Phrase != "first" || worst.Hijacks != 1 ||
		len(worst.HijackedBy) != 1 || worst.HijackedBy[0] != "First place." {
		t.Fatalf("worst phrase %+v", worst)
	}
	md := r.Markdown("test")
	for _, want := range []string{"| en | intent_play_fistbump | `first` | 1 | 0 | 0 |", "First place.",
		"Could you please bump fists with me right now?", "llm"} {
		if !strings.Contains(md, want) {
			t.Fatalf("report has no %q:\n%s", want, md)
		}
	}
	if s := r.Summary(); !strings.Contains(s, "1 of 2 negatives hijacked") || !strings.Contains(s, "1 of 3 positives missed") {
		t.Fatalf("summary %q", s)
	}
}

func TestGeneratedCasesUseEveryKeyphrase(t *testing.T) {
	neg, pos := GenerateCases(auditIntents)
	for _, p := range []string{"first", "fist bump", "time is it", "what time is it"} {
		inNeg, inPos := false, false
		for _, c := range neg {
			inNeg = inNeg || (strings.Contains(strings.ToLower(c.Text), p) && c.Expected == NoIntent)
		}
		for _, c := range pos {
			inPos = inPos || strings.Contains(strings.ToLower(c.Text), p)
		}
		if !inNeg || !inPos {
			t.Fatalf("%q: in negatives %v, in positives %v", p, inNeg, inPos)
		}
	}
}

func TestCasesRoundTripAsTSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.tsv")
	in := []Case{{Lang: "es", Text: "¿Qué hora es?", Expected: "intent_clock_time", Source: "real"}}
	if err := WriteCases(path, "# header\n", in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadCases(path)
	if err != nil || len(out) != 1 || out[0] != in[0] {
		t.Fatalf("got %+v, %v", out, err)
	}
}

// The stored corpora (testdata/intent-audit) cover the real intent lists: every keyphrase of every intent appears
// in a negative sentence, and every intent has at least 3 positive sentences per language.
func TestTheIntentAuditCorporaCoverEveryIntent(t *testing.T) {
	intents, err := LoadIntents("../../../intent-data", map[string]string{"en": "en-US", "es": "es-ES"})
	if err != nil {
		t.Fatal(err)
	}
	cases, err := ReadCorpus(filepath.Join("testdata", "intent-audit"))
	if err != nil {
		t.Fatal(err)
	}
	for lang, list := range intents {
		positives := map[string]int{}
		for _, c := range cases {
			if c.Lang == lang && c.Expected != NoIntent {
				positives[c.Expected]++
			}
		}
		for _, in := range list {
			if positives[in.Name] < 3 {
				t.Errorf("%s %s: %d positive sentences, want 3", lang, in.Name, positives[in.Name])
			}
			for _, p := range in.Keyphrases {
				found := false
				for _, c := range cases {
					found = found || (c.Lang == lang && c.Expected == NoIntent &&
						strings.Contains(strings.ToLower(c.Text), strings.ToLower(p)))
				}
				if !found {
					t.Errorf("%s %s: keyphrase %q is in no negative sentence", lang, in.Name, p)
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join("testdata", "intent-audit", "real.tsv")); err != nil {
		t.Error("no real transcripts:", err)
	}
}

// The live rule measures coverage with the longest keyphrase in the question; the audit-only FirstPhrase variant
// with the keyphrase wire-pod's matcher returned.
func TestCoverageUsesTheIntentsLongestKeyphrase(t *testing.T) {
	intents := map[string][]vars.JsonIntent{"en": {{Name: "intent_system_sleep", Keyphrases: []string{"sleep", "go to sleep"}}}}
	for first, weak := range map[bool]bool{false: false, true: true} {
		b := New(Config{Intents: intents, DefaultLanguage: "en", FirstPhrase: first})
		r, ok := b.Route(cortex.Transcript{Text: "Go to sleep in the corner.", Language: "en"})
		want := map[bool]string{false: "go to sleep", true: "sleep"}[first]
		if !ok || r.Weak != weak || r.Phrase != want {
			t.Fatalf("first phrase %v: route %+v, want weak %v with %q", first, r, weak, want)
		}
	}
}

// The live rule leaves politeness words out of the coverage count ("Go forward a bit." is 1 of 2); the audit-only
// CountFiller counts every word, as before.
func TestCoverageLeavesPolitenessOut(t *testing.T) {
	intents := map[string][]vars.JsonIntent{"en": {{Name: "intent_imperative_forward", Keyphrases: []string{"forward"}}}}
	for count, want := range map[bool]float64{false: 0.5, true: 0.25} {
		b := New(Config{Intents: intents, DefaultLanguage: "en", CountFiller: count})
		r, ok := b.Route(cortex.Transcript{Text: "Go forward a bit.", Language: "en"})
		if !ok || r.Coverage != want {
			t.Fatalf("count filler %v: route %+v, want coverage %v", count, r, want)
		}
	}
}
