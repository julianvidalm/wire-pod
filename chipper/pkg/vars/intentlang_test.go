package vars

import (
	"os"
	"path/filepath"
	"testing"
)

func writeIntents(t *testing.T, dir, file, json string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(json), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIntentsFollowTheLanguageOfTheUtterance(t *testing.T) {
	dir := t.TempDir()
	writeIntents(t, dir, "en-US.json", `[{"name":"intent_clock_time","keyphrases":["what time is it"]}]`)
	writeIntents(t, dir, "es-ES.json", `[{"name":"intent_clock_time","keyphrases":["qué hora es"]},{"name":"intent_imperative_negative","keyphrases":["no"]}]`)
	defer func(old []JsonIntent) { IntentList = old; resetIntentLanguages() }(IntentList)
	IntentList = []JsonIntent{{Name: "configured"}}

	if err := LoadIntentLanguages(dir, []string{"en", "es"}); err != nil {
		t.Fatal(err)
	}
	if got := IntentsFor("es"); len(got) != 2 || got[0].Keyphrases[0] != "qué hora es" {
		t.Fatalf("es: %+v", got)
	}
	// Spanish keyphrases such as "no" never meet English sentences ("know", "now").
	if got := IntentsFor("en"); len(got) != 1 || got[0].Keyphrases[0] != "what time is it" {
		t.Fatalf("en: %+v", got)
	}
	// Unknown or undetected languages keep the configured list.
	for _, lang := range []string{"", "fr"} {
		if got := IntentsFor(lang); len(got) != 1 || got[0].Name != "configured" {
			t.Fatalf("%q: %+v", lang, got)
		}
	}
}

func TestLoadIntentLanguagesReportsAMissingLanguage(t *testing.T) {
	defer resetIntentLanguages()
	if err := LoadIntentLanguages(t.TempDir(), []string{"es"}); err == nil {
		t.Fatal("no error for a language without intent data")
	}
}

func TestUtteranceLanguageIsRememberedPerRobot(t *testing.T) {
	defer resetIntentLanguages()
	SetUtteranceLanguage("esn1", "es")
	SetUtteranceLanguage("esn2", "en")
	if UtteranceLanguage("esn1") != "es" || UtteranceLanguage("esn2") != "en" || UtteranceLanguage("esn3") != "" {
		t.Fatal("languages mixed up between robots")
	}
}
