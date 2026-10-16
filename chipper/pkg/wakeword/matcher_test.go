package wakeword

import "testing"

func words(texts ...string) []Word {
	out := make([]Word, len(texts))
	for i, t := range texts {
		out[i] = Word{Text: t, Conf: 1}
	}
	return out
}

func TestMatcherFindsTheNameAnywhereInTheUtterance(t *testing.T) {
	m := NewMatcher(DefaultWords, 0)
	for _, utterance := range [][]Word{
		words("vector"),
		words("hola", "cómo", "estás", "vector"),
		words("hello", "vector", "what", "time", "is", "it"),
		words("víctor", "cuéntame", "un", "chiste"),
		words("VECTOR!"),
		words("Víctor,"),
	} {
		if _, ok := m.Match(utterance); !ok {
			t.Errorf("no match in %v", utterance)
		}
	}
}

func TestMatcherIgnoresSimilarWords(t *testing.T) {
	m := NewMatcher(DefaultWords, 0)
	// From the T1 robot recordings: "director de orquesta" and "lector cuéntame un chiste" must not wake Vector.
	for _, utterance := range [][]Word{
		words("director", "de", "orquesta"),
		words("lector", "cuéntame", "un", "chiste"),
		words("hoy", "hace", "frío", "afuera"),
		words("vectores"),
		nil,
	} {
		if w, ok := m.Match(utterance); ok {
			t.Errorf("matched %q in %v", w.Text, utterance)
		}
	}
}

func TestMatcherHonorsMinimumConfidence(t *testing.T) {
	m := NewMatcher(DefaultWords, 0.6)
	if _, ok := m.Match([]Word{{Text: "vector", Conf: 0.4}}); ok {
		t.Fatal("matched below the confidence threshold")
	}
	w, ok := m.Match([]Word{{Text: "hola", Conf: 0.9}, {Text: "vector", Conf: 0.8}})
	if !ok || w.Text != "vector" {
		t.Fatalf("got %+v %v", w, ok)
	}
}

func TestMatchAllReturnsEveryNameInOrder(t *testing.T) {
	m := NewMatcher(DefaultWords, 0)
	got := m.MatchAll(words("vector", "cuéntame", "víctor"))
	if len(got) != 2 || got[0].Text != "vector" || got[1].Text != "víctor" {
		t.Fatalf("got %+v", got)
	}
}
