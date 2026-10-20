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

// A capture dropped while an older one is still being answered gets no answer of its own; the older answer ends
// the robot's listening, so the dropped capture sends no end.
func TestADroppedCaptureLeavesTheEndToThePendingAnswer(t *testing.T) {
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
	r.none(t)
	close(hold)
	for i := 0; i < 2; i++ {
		if got := r.next(t); got != "intent esn1 intent_clock_time " {
			t.Fatalf("robot got %q, want the answers only", got)
		}
	}
	r.none(t)
}

// When the pending utterances end without answers, the dropped (newest) capture's end is sent once they are done;
// the older ends are stale and skipped.
func TestADroppedCaptureEndsTheListeningWhenNothingIsAnswered(t *testing.T) {
	hold := make(chan struct{})
	b := &fakeBrain{hold: hold}
	r := newFakeRobot()
	c := newEndConversation(&fakeTranscriber{text: "qué hora es"}, b, r, endIntent)
	ts := speak(c, "esn1", 0)
	for b.count() == 0 {
		time.Sleep(time.Millisecond)
	}
	ts = speak(c, "esn1", ts)
	speak(c, "esn1", ts)
	r.none(t)
	close(hold)
	if got := r.next(t); got != endCommand {
		t.Fatalf("robot got %q, want %q", got, endCommand)
	}
	r.none(t)
}

// L67 B1: capture N ends without an answer only after wake N+1 started capture N+1 (and sent its attend). Its end
// would stop the robot listening for N+1, so it is skipped.
func TestALateEndDoesNotStopTheNextCapture(t *testing.T) {
	r := newFakeRobot()
	logs := &logLines{}
	hold := make(chan struct{})
	b := &fakeBrain{hold: hold} // a slow brain that answers nothing
	c := NewConversation(Config{Capture: testCaptureConfig(), NewVAD: func() VAD { return markVAD{} },
		Transcriber: &fakeTranscriber{text: "hola"}, Brain: b, Robot: r, Logf: logs.logf, EndIntent: endIntent})
	wakeThenAsk(c, "esn1", 300*time.Millisecond, time.Second, clearAmplitude) // capture N ends, worker busy
	for deadline := time.Now().Add(time.Second); b.count() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	ts := uint64(10000)
	for end := ts + 1000; ts < end; ts += 10 {
		c.Feed("esn1", levelFrame(ts, clearAmplitude))
	}
	c.Wake("esn1", ts-10) // wake N+1: capture N+1 is listening
	close(hold)
	for deadline := time.Now().Add(2 * time.Second); !logs.has("skipped") && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	r.none(t)
	if !logs.has("listening end skipped") {
		t.Fatalf("no skipped line in %q", logs.lines)
	}
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
