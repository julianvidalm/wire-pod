// Package whisperlang picks the transcription language for whisper among the languages the user speaks, for
// people who talk to Vector in more than one language. Whisper's own auto-detection considers about 100
// languages and can mistake a short, accented phrase for a related one (Spanish for Portuguese or Galician);
// restricting the choice to the user's languages avoids that.
package whisperlang

import "strings"

// ParseAllowed reads a list like "en,es" (WHISPER_LANGUAGES) into whisper language codes: lowercased, region
// dropped ("es-ES" -> "es"), duplicates and empty entries removed.
func ParseAllowed(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		code := strings.ToLower(strings.TrimSpace(part))
		code, _, _ = strings.Cut(code, "-")
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	return out
}

// Pick returns the language id among allowed with the highest probability in probs (whisper's language
// detection output, indexed by language id), or -1 when no allowed id is valid, which leaves the choice to
// whisper's own detection.
func Pick(probs []float32, allowed []int) int {
	best := -1
	for _, id := range allowed {
		if id < 0 || id >= len(probs) {
			continue
		}
		if best == -1 || probs[id] > probs[best] {
			best = id
		}
	}
	return best
}
