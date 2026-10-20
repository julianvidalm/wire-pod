package cortex

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Whisper may write the robot's wake phrase too: "Hey Vector, ¿qué hora es?" (S1: only "hey vector", detected on
// the robot). The brain must get only the question.

// wakeGreetings may come before the name; alone they are kept ("Hey, what's up?").
var wakeGreetings = map[string]bool{"hey": true, "hi": true, "ey": true, "ei": true, "hei": true, "oye": true,
	"hola": true, "ay": true, "ah": true} // L101: whisper writes "Hola" as "Ay"

// wakeSeparators are what whisper writes between the wake phrase and the question.
const wakeSeparators = " \t\n,.!?;:-–—…"

// stripWakePhrase removes the leading "hey vector" (or "vector", "oye vector", whisper's "Hey, Víctor." ...) from
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

// isWakeName says whether word is how whisper writes "vector": Vector, Víctor, Victor, Béctor, Vektor, Viktor.
// Its sounds are folded (accents dropped, a first b→v, k→c, a second-letter i→e) and must give "vector".
func isWakeName(word string) bool {
	w := []rune(strings.ToLower(word))
	if len(w) != 6 {
		return false
	}
	for i, r := range w {
		switch {
		case strings.ContainsRune("áàä", r):
			r = 'a'
		case strings.ContainsRune("éèë", r):
			r = 'e'
		case strings.ContainsRune("íìï", r):
			r = 'i'
		case strings.ContainsRune("óòö", r):
			r = 'o'
		}
		switch {
		case i == 0 && r == 'b':
			r = 'v'
		case r == 'k':
			r = 'c'
		case i == 1 && r == 'i':
			r = 'e'
		}
		w[i] = r
	}
	return string(w) == "vector"
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
