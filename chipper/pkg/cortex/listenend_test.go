package cortex

import (
	"context"
	"errors"
	"testing"
	"time"
)

// L63: the robot holds its listening eyes until the answer; a capture that ends without one sends EndIntent.
const endIntent = "intent_cortex_listening_end"

const endCommand = "intent esn1 " + endIntent + " "

func newEndConversation(tr Transcriber, b Brain, r Robot, end string) *Conversation {
	return NewConversation(Config{
		Capture:     testCaptureConfig(),
		NewVAD:      func() VAD { return markVAD{} },
		Transcriber: tr,
		Brain:       b,
		Robot:       r,
		EndIntent:   end,
	})
}

type failingTranscriber struct{}

func (failingTranscriber) Transcribe(context.Context, Utterance) (Transcript, error) {
	return Transcript{}, errors.New("no whisper")
}

func TestACaptureWithoutAnswerEndsTheListening(t *testing.T) {
	for name, tc := range map[string]struct {
		tr        Transcriber
		brain     Brain
		amplitude int16
	}{
		"nothing heard":          {&fakeTranscriber{text: "hola"}, &fakeBrain{answer: Answer{Text: "x"}}, 50},
		"no words":               {&fakeTranscriber{text: "."}, &fakeBrain{answer: Answer{Text: "x"}}, clearAmplitude},
		"silence phrase":         {&fakeTranscriber{text: "Thank you."}, &fakeBrain{answer: Answer{Text: "x"}}, 260},
		"only the wake phrase":   {&fakeTranscriber{text: "Hey Jarvis."}, &fakeBrain{answer: Answer{Text: "x"}}, clearAmplitude},
		"transcription error":    {failingTranscriber{}, &fakeBrain{answer: Answer{Text: "x"}}, clearAmplitude},
		"brain error":            {&fakeTranscriber{text: "hola"}, failingBrain{}, clearAmplitude},
		"brain answered nothing": {&fakeTranscriber{text: "hola"}, &fakeBrain{}, clearAmplitude},
	} {
		r := newFakeRobot()
		c := newEndConversation(tc.tr, tc.brain, r, endIntent)
		wakeThenAsk(c, "esn1", 500*time.Millisecond, time.Second, tc.amplitude)
		if got := r.next(t); got != endCommand {
			t.Fatalf("%s: robot got %q, want %q", name, got, endCommand)
		}
		r.none(t)
	}
}

func TestNoSpeechEndsTheListening(t *testing.T) {
	r := newFakeRobot()
	c := newEndConversation(&fakeTranscriber{text: "x"}, &fakeBrain{answer: Answer{Text: "x"}}, r, endIntent)
	c.Wake("esn1", 0)
	for ts := uint64(0); ts < 5000; ts += 10 {
		c.Feed("esn1", testFrame(ts, false))
	}
	if got := r.next(t); got != endCommand {
		t.Fatalf("robot got %q, want %q", got, endCommand)
	}
}

func TestAStreamThatEndsMidCaptureEndsTheListening(t *testing.T) {
	r := newFakeRobot()
	c := newEndConversation(&fakeTranscriber{text: "x"}, &fakeBrain{answer: Answer{Text: "x"}}, r, endIntent)
	c.Open("esn1")
	c.Wake("esn1", 0)
	for ts := uint64(0); ts < 300; ts += 10 {
		c.Feed("esn1", testFrame(ts, true))
	}
	c.Close("esn1")
	if got := r.next(t); got != endCommand {
		t.Fatalf("robot got %q, want %q", got, endCommand)
	}
}

// A capture dropped while the robot is still being answered gets no answer of its own.
func TestADroppedCaptureEndsTheListening(t *testing.T) {
	hold := make(chan struct{})
	b := &fakeBrain{answer: Answer{Intent: "intent_clock_time"}, hold: hold}
	r := newFakeRobot()
	c := newEndConversation(&fakeTranscriber{text: "qué hora es"}, b, r, endIntent)
	ts := speak(c, "esn1", 0)
	for b.count() == 0 {
		time.Sleep(time.Millisecond)
	}
	ts = speak(c, "esn1", ts) // waits in the queue
	speak(c, "esn1", ts)      // dropped
	if got := r.next(t); got != endCommand {
		t.Fatalf("robot got %q, want %q for the dropped capture", got, endCommand)
	}
	close(hold)
	r.next(t)
	r.next(t)
	r.none(t)
}

func TestAnAnswerDoesNotEndTheListening(t *testing.T) {
	for _, a := range []Answer{{Intent: "intent_clock_time"}, {Text: "Son las tres."}} {
		r := newFakeRobot()
		c := newEndConversation(&fakeTranscriber{text: "¿qué hora es?"}, &fakeBrain{answer: a}, r, endIntent)
		wakeThenAsk(c, "esn1", 500*time.Millisecond, time.Second, clearAmplitude)
		if got := r.next(t); got == endCommand {
			t.Fatalf("answer %+v: robot got the end intent", a)
		}
		r.none(t)
	}
}

func TestAnEmptyEndIntentSendsNothing(t *testing.T) {
	r := newFakeRobot()
	c := newEndConversation(&fakeTranscriber{text: "Hey Jarvis."}, &fakeBrain{answer: Answer{Text: "x"}}, r, "")
	wakeThenAsk(c, "esn1", 500*time.Millisecond, time.Second, clearAmplitude)
	c.Wake("esn1", 10000)
	for ts := uint64(10000); ts < 15000; ts += 10 {
		c.Feed("esn1", testFrame(ts, false))
	}
	r.none(t)
}
