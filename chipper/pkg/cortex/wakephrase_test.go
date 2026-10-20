package cortex

import (
	"testing"
	"time"
)

// askWith runs one clear question whose transcript is text and returns what the brain got (nil: not called).
func askWith(t *testing.T, text string, level int16) (*fakeBrain, *fakeRobot, *logLines) {
	t.Helper()
	b := &fakeBrain{answer: Answer{Intent: "intent_clock_time"}}
	r := newFakeRobot()
	logs := &logLines{}
	tr := &fakeTranscriber{text: text}
	c := newTestConversation(tr, b, r, logs)
	wakeThenAsk(c, "esn1", 500*time.Millisecond, time.Second, level)
	for deadline := time.Now().Add(2 * time.Second); tr.count() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	return b, r, logs
}

// S1: the robot wakes on its own "hey vector" and whisper may write the wake phrase too; the brain gets only the
// question, never the robot's name.
func TestALeadingWakePhraseIsNotSentToTheBrain(t *testing.T) {
	for raw, want := range map[string]string{
		"Hey Vector, ¿qué hora es?":                "¿Qué hora es?",
		"hey, vector what time is it":              "What time is it",
		"Hey, Víctor. What time is it?":            "What time is it?",
		"Hey Béctor. - Que horas?":                 "Que horas?",
		"Hi Vector. Tell me a joke.":               "Tell me a joke.",
		"Ey Vektor, cuéntame un chiste.":           "Cuéntame un chiste.",
		"Oye, Vector, cuéntame un chiste.":         "Cuéntame un chiste.",
		"Vector, stop.":                            "Stop.",
		"Hey Vector. Hey Vector, what time is it?": "What time is it?", // said twice
		"HEY VECTOR! ¿Cómo te llamas?":             "¿Cómo te llamas?",
		"Hey Victor, set a timer.":                 "Set a timer.",
		"Hey Bector, set a timer.":                 "Set a timer.",
		"What do you think of Vector?":             "What do you think of Vector?",
		"Hey, what's up?":                          "Hey, what's up?",
		"¿qué hora es?":                            "¿qué hora es?",
		"Vectorcito, ¿qué hora es?":                "Vectorcito, ¿qué hora es?",
	} {
		b, r, _ := askWith(t, raw, clearAmplitude)
		r.next(t)
		if len(b.calls) != 1 || b.calls[0].Text != want {
			t.Fatalf("%q: brain got %+v, want %q", raw, b.calls, want)
		}
	}
}

// S1: "jarvis" is no longer the robot's name: it reaches the brain as said.
func TestJarvisIsNoLongerStripped(t *testing.T) {
	for _, raw := range []string{"Hey Jarvis, ¿qué hora es?", "Jarvis, stop."} {
		if got := stripWakePhrase(raw); got != raw {
			t.Fatalf("%q: got %q, want it unchanged", raw, got)
		}
	}
}

// A transcript that is only the wake phrase holds no question: nothing is sent, as for no speech.
func TestOnlyTheWakePhraseIsNothingHeard(t *testing.T) {
	for _, raw := range []string{"Hey Vector.", "Hey, Víctor!", "vector", "Oye Vector..."} {
		b, r, logs := askWith(t, raw, clearAmplitude)
		r.none(t)
		if b.count() != 0 || !logs.has("nothing heard") {
			t.Fatalf("%q: brain called %d times, logs %q; want 0 and a nothing-heard line", raw, b.count(), logs.lines)
		}
	}
}

// The silence-phrase guard still applies to what follows the wake phrase.
func TestAQuietSilencePhraseAfterTheWakePhraseIsNothingHeard(t *testing.T) {
	b, r, logs := askWith(t, "Hey Vector. Thank you.", 260)
	r.none(t)
	if b.count() != 0 || !logs.has("nothing heard") {
		t.Fatalf("brain called %d times, logs %q; want 0 and a nothing-heard line", b.count(), logs.lines)
	}
}

// The log keeps its format and adds the raw transcript only when the wake phrase was stripped.
func TestTheTranscriptLogShowsTheRawTextOnlyWhenStripped(t *testing.T) {
	_, r, logs := askWith(t, "Hey Vector, ¿qué hora es?", clearAmplitude)
	r.next(t)
	if !logs.has("): ¿Qué hora es? [raw: Hey Vector, ¿qué hora es?]") {
		t.Fatalf("no stripped line with the raw text in %q", logs.lines)
	}
	_, r, logs = askWith(t, "¿Qué hora es?", clearAmplitude)
	r.next(t)
	if !logs.has("): ¿Qué hora es?") || logs.has("[raw:") {
		t.Fatalf("want the plain line without a raw part in %q", logs.lines)
	}
}

// The greetings whisper writes before the name (L101: "Hola", "Ay", "Ah") are stripped with it.
func TestGreetingsHolaAyAhAreStripped(t *testing.T) {
	for raw, want := range map[string]string{
		"Hola Vector, cuéntame un chiste.": "Cuéntame un chiste.",
		"Hola, Víctor. ¿Qué hora es?":      "¿Qué hora es?",
		"Ay, Vector, cuéntame un chiste.":  "Cuéntame un chiste.",
		"Ah Vector, what time is it?":      "What time is it?",
		"Viktor, ¿qué hora es?":            "¿Qué hora es?",
	} {
		if got := stripWakePhrase(raw); got != want {
			t.Fatalf("%q: got %q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{"Ay, Vector.", "Hola Víctor.", "Hey Béctor!"} {
		if got := stripWakePhrase(raw); hasWord(got) {
			t.Fatalf("%q: got %q, want nothing left", raw, got)
		}
	}
}

// Words that only sound like the name, and greetings alone, are kept.
func TestNearNamesAndLoneGreetingsAreKept(t *testing.T) {
	for _, raw := range []string{"Victoria, ¿qué hora es?", "Vectorial math.", "Doctor, please.",
		"Hector is here.", "Vecto.", "Hey George, hi.", "Hola, ¿qué tal?", "Ay, qué susto.", "Ah, I see.",
		"Javier Vista."} {
		if got := stripWakePhrase(raw); got != raw {
			t.Fatalf("%q: got %q, want it unchanged", raw, got)
		}
	}
}
