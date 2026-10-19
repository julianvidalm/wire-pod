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

// L61: the capture now holds "hey jarvis", so whisper writes it; the brain gets only the question.
func TestALeadingWakePhraseIsNotSentToTheBrain(t *testing.T) {
	for raw, want := range map[string]string{
		"Hey Jarvis, ¿qué hora es?":        "¿Qué hora es?",
		"hey, jarvis what time is it":      "What time is it",
		"Hey, Jorby. What time is it?":     "What time is it?",
		"Hey, Jarby. - Que horas?":         "Que horas?",
		"Hi Jarvis. Tell me a joke.":       "Tell me a joke.",
		"Ey Jarvis, cuéntame un chiste.":   "Cuéntame un chiste.",
		"Oye, Jarvis, cuéntame un chiste.": "Cuéntame un chiste.",
		"Jarvis, stop.":                    "Stop.",
		"HEY JARVIS! ¿Cómo te llamas?":     "¿Cómo te llamas?",
		"Hey Jervis, set a timer.":         "Set a timer.",
		"What do you think of Jarvis?":     "What do you think of Jarvis?",
		"Hey, what's up?":                  "Hey, what's up?",
		"¿qué hora es?":                    "¿qué hora es?",
		"Jarvisito, ¿qué hora es?":         "Jarvisito, ¿qué hora es?",
	} {
		b, r, _ := askWith(t, raw, clearAmplitude)
		r.next(t)
		if len(b.calls) != 1 || b.calls[0].Text != want {
			t.Fatalf("%q: brain got %+v, want %q", raw, b.calls, want)
		}
	}
}

// A transcript that is only the wake phrase holds no question: nothing is sent, as for no speech.
func TestOnlyTheWakePhraseIsNothingHeard(t *testing.T) {
	for _, raw := range []string{"Hey Jarvis.", "Hey, Jorby!", "jarvis", "Oye Jarvis..."} {
		b, r, logs := askWith(t, raw, clearAmplitude)
		r.none(t)
		if b.count() != 0 || !logs.has("nothing heard") {
			t.Fatalf("%q: brain called %d times, logs %q; want 0 and a nothing-heard line", raw, b.count(), logs.lines)
		}
	}
}

// The silence-phrase guard still applies to what follows the wake phrase.
func TestAQuietSilencePhraseAfterTheWakePhraseIsNothingHeard(t *testing.T) {
	b, r, logs := askWith(t, "Hey Jarvis. Thank you.", 260)
	r.none(t)
	if b.count() != 0 || !logs.has("nothing heard") {
		t.Fatalf("brain called %d times, logs %q; want 0 and a nothing-heard line", b.count(), logs.lines)
	}
}

// The log keeps its format and adds the raw transcript only when the wake phrase was stripped.
func TestTheTranscriptLogShowsTheRawTextOnlyWhenStripped(t *testing.T) {
	_, r, logs := askWith(t, "Hey Jarvis, ¿qué hora es?", clearAmplitude)
	r.next(t)
	if !logs.has("): ¿Qué hora es? [raw: Hey Jarvis, ¿qué hora es?]") {
		t.Fatalf("no stripped line with the raw text in %q", logs.lines)
	}
	_, r, logs = askWith(t, "¿Qué hora es?", clearAmplitude)
	r.next(t)
	if !logs.has("): ¿Qué hora es?") || logs.has("[raw:") {
		t.Fatalf("want the plain line without a raw part in %q", logs.lines)
	}
}
