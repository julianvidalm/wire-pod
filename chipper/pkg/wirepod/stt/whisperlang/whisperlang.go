// Package whisperlang picks the transcription language for whisper among the languages the user speaks, for
// people who talk to Vector in more than one language. Whisper's own auto-detection considers about 100
// languages and can mistake a short, accented phrase for a related one (Spanish for Portuguese or Galician);
// restricting the choice to the user's languages avoids that.
package whisperlang

import (
	"regexp"
	"strings"
)

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

const (
	// MinAudioCtx keeps enough encoder context for whisper to recognize short commands reliably.
	MinAudioCtx = 256
	// fullAudioCtx is whisper's whole 30 s window (50 positions per second of 16 kHz audio).
	fullAudioCtx     = 1500
	ctxPerSecond     = 50
	audioCtxMargin   = 64
	samplesPerSecond = 16000
)

// AudioCtx returns the encoder context for an utterance of n 16 kHz samples. Whisper encodes a full 30 s
// window by default, which made a 2 s question take about 5 s on the PC; sizing the window to the audio
// (plus a margin) makes it several times faster without losing the words.
func AudioCtx(samples int) int {
	n := (samples*ctxPerSecond+samplesPerSecond-1)/samplesPerSecond + audioCtxMargin
	return max(MinAudioCtx, min(n, fullAudioCtx))
}

// annotations are the non-speech notes whisper writes for sounds, such as Vector's own beep ("*pitido*",
// "(phone beeps)", "[música]", "♪"); left in, they confuse intent matching.
var annotations = regexp.MustCompile(`\*[^*]*\*|\([^)]*\)|\[[^\]]*\]|♪`)

// CleanText removes sound annotations and leading dashes from a transcription and normalizes spaces.
func CleanText(s string) string {
	s = annotations.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(strings.TrimLeft(s, "- "))
}
