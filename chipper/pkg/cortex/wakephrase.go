package cortex

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The capture starts a second before the wake (L61), so whisper writes the wake phrase too: "Hey Jarvis, ¿qué
// hora es?". The brain must get only the question.

// wakeGreetings may come before the name; alone they are kept ("Hey, what's up?").
var wakeGreetings = map[string]bool{"hey": true, "hi": true, "ey": true, "ei": true, "hei": true, "oye": true,
	"hola": true, "ay": true, "ah": true} // L101: "Hola Jarvis" comes out as "Ay, Jarvis."

// wakeSeparators are what whisper writes between the wake phrase and the question.
const wakeSeparators = " \t\n,.!?;:-–—…"

// stripWakePhrase removes the leading "hey jarvis" (or "jarvis", "oye jarvis", whisper's "Hey, Jorby." ...) from
// a transcript, as often as the user said it, without case or punctuation, and capitalizes what follows. Text
// without one is returned as is.
func stripWakePhrase(text string) string {
	for {
		rest, ok := cutWakePhrase(text)
		if !ok {
			return text
		}
		text = capitalize(rest)
	}
}

// cutWakePhrase returns what follows one leading wake phrase, and whether there was one.
func cutWakePhrase(text string) (string, bool) {
	words := wordSpans(text, 2)
	switch {
	case len(words) >= 1 && isWakeName(text[words[0][0]:words[0][1]]):
		words = words[:1]
	case len(words) == 2 && wakeGreetings[strings.ToLower(text[words[0][0]:words[0][1]])] &&
		isWakeName(text[words[1][0]:words[1][1]]):
		words = words[1:]
	default:
		return text, false
	}
	return strings.TrimLeft(text[words[0][1]:], wakeSeparators), true
}

// wordSpans returns the byte spans of the first n runs of letters and digits in s.
func wordSpans(s string, n int) [][2]int {
	var spans [][2]int
	start := -1
	for i, r := range s {
		word := unicode.IsLetter(r) || unicode.IsDigit(r)
		switch {
		case word && start < 0:
			start = i
		case !word && start >= 0:
			spans = append(spans, [2]int{start, i})
			start = -1
			if len(spans) == n {
				return spans
			}
		}
	}
	if start >= 0 {
		spans = append(spans, [2]int{start, len(s)})
	}
	return spans
}

// isWakeName says whether word is how whisper writes "jarvis": Jarvis, Jervis, Jarby, Jorby, Yarvis, Harvis.
// Its sounds are folded (b→v, y→i, o/e→a, a first y or h→j, a final s or t dropped: "Jorbit", L101) and must
// give "jarvi".
func isWakeName(word string) bool {
	w := []rune(strings.ToLower(word))
	if len(w) < 4 || len(w) > 7 {
		return false
	}
	for i, r := range w {
		switch {
		case i == 0 && (r == 'y' || r == 'h'):
			w[i] = 'j'
		case r == 'b':
			w[i] = 'v'
		case r == 'y':
			w[i] = 'i'
		case r == 'o' || r == 'e':
			w[i] = 'a'
		case r == 'z':
			w[i] = 's'
		}
	}
	folded := string(w)
	return strings.TrimSuffix(folded, "s") == "jarvi" || strings.TrimSuffix(folded, "t") == "jarvi"
}

// capitalize upper-cases the first letter of s when only opening marks ("¿", "¡", quotes) come before it.
func capitalize(s string) string {
	for i, r := range s {
		if unicode.IsLetter(r) {
			return s[:i] + string(unicode.ToUpper(r)) + s[i+utf8.RuneLen(r):]
		}
		if !strings.ContainsRune("¿¡\"'«“ ", r) {
			return s
		}
	}
	return s
}
