package vars

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Intent phrases per spoken language, for speech-to-text engines that detect the language of each utterance
// (whisper.cpp with WHISPER_LANGUAGES). Each utterance is matched only against its own language: mixing the
// lists would let short Spanish phrases such as "no" match inside English words ("know", "now").
var (
	intentLangMu       sync.Mutex
	intentsByLanguage  = map[string][]JsonIntent{}
	utteranceLanguages = map[string]string{}
)

// LoadIntentLanguages loads dir/<code>-*.json for each language code (for example "es" loads es-ES.json).
func LoadIntentLanguages(dir string, codes []string) error {
	loaded := map[string][]JsonIntent{}
	for _, code := range codes {
		files, _ := filepath.Glob(filepath.Join(dir, code+"-*.json"))
		if len(files) == 0 {
			return fmt.Errorf("no intent data for language %q in %s", code, dir)
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			return err
		}
		var intents []JsonIntent
		if err := json.Unmarshal(data, &intents); err != nil {
			return fmt.Errorf("%s: %w", files[0], err)
		}
		loaded[code] = intents
	}
	intentLangMu.Lock()
	intentsByLanguage = loaded
	intentLangMu.Unlock()
	return nil
}

// IntentsFor returns the intents of a language, or the configured IntentList when the language is unknown.
func IntentsFor(code string) []JsonIntent {
	intentLangMu.Lock()
	defer intentLangMu.Unlock()
	if intents, ok := intentsByLanguage[code]; ok {
		return intents
	}
	return IntentList
}

// SetUtteranceLanguage records the language of a robot's latest utterance.
func SetUtteranceLanguage(device, code string) {
	intentLangMu.Lock()
	utteranceLanguages[device] = code
	intentLangMu.Unlock()
}

// UtteranceLanguage returns the language of a robot's latest utterance, or "" when none was detected.
func UtteranceLanguage(device string) string {
	intentLangMu.Lock()
	defer intentLangMu.Unlock()
	return utteranceLanguages[device]
}

func resetIntentLanguages() {
	intentLangMu.Lock()
	intentsByLanguage = map[string][]JsonIntent{}
	utteranceLanguages = map[string]string{}
	intentLangMu.Unlock()
}
