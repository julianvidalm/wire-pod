// Package wakeword detects Vector's name in what the robot hears (MicTap) so the cortex can wake it: "vector"
// on its own, anywhere in an utterance ("hola cómo estás Vector", "hello vector what time is it"), in Spanish or
// English. It is independent of the robot's own "Hey Vector" detector.
//
// Detector implements mictap.Sink; speech recognition is behind the Recognizer port (see voskrec for Vosk) and
// what happens on a detection is behind the Handler port.
package wakeword

import (
	"strings"
	"unicode"
)

// DefaultWords are the forms of the name the recognizers produce for "Vector" in Spanish and English (accents
// are ignored when matching, so "víctor" matches "victor").
var DefaultWords = []string{"vector", "victor"}

// DefaultMinConfidence drops low-confidence matches: in the T1 recordings "sin nombre tuyo" was heard as
// "sin vector" at 0.54, while the real calls scored 0.72 to 0.97.
const DefaultMinConfidence = 0.6

// Word is one recognized word; Start and End are seconds from the start of the recognizer's audio.
type Word struct {
	Text       string
	Conf       float64
	Start, End float64
}

// Matcher compares whole words only: near-misses such as "director" or "lector" never match, because a broad
// "-ector" rule woke the robot on unrelated speech in the T1 recordings.
type Matcher struct {
	words   map[string]bool
	minConf float64
}

func NewMatcher(names []string, minConf float64) Matcher {
	m := Matcher{words: map[string]bool{}, minConf: minConf}
	for _, n := range names {
		m.words[normalize(n)] = true
	}
	return m
}

// Match returns the first word of an utterance that is the name, with at least the minimum confidence.
func (m Matcher) Match(utterance []Word) (Word, bool) {
	if all := m.MatchAll(utterance); len(all) > 0 {
		return all[0], true
	}
	return Word{}, false
}

// MatchAll returns every word of an utterance that is the name, in order.
func (m Matcher) MatchAll(utterance []Word) []Word {
	var out []Word
	for _, w := range utterance {
		if w.Conf >= m.minConf && m.words[normalize(w.Text)] {
			out = append(out, w)
		}
	}
	return out
}

var accents = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u")

func normalize(s string) string {
	s = accents.Replace(strings.ToLower(s))
	return strings.TrimFunc(s, func(r rune) bool { return !unicode.IsLetter(r) })
}
