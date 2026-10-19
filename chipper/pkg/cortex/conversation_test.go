package cortex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTranscriber struct {
	mu    sync.Mutex
	calls []Utterance
	text  string
}

func (f *fakeTranscriber) Transcribe(_ context.Context, u Utterance) (Transcript, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, u)
	return Transcript{Text: f.text, Language: "es"}, nil
}

func (f *fakeTranscriber) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeBrain answers every transcript with answer; with hold set, each call waits until hold is closed.
type fakeBrain struct {
	mu     sync.Mutex
	calls  []Transcript
	answer Answer
	hold   chan struct{}
}

func (f *fakeBrain) Answer(_ context.Context, _ string, t Transcript) (Answer, error) {
	f.mu.Lock()
	f.calls = append(f.calls, t)
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		<-hold
	}
	return f.answer, nil
}

func (f *fakeBrain) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeRobot records each command as one line on a channel.
type fakeRobot struct{ sent chan string }

func newFakeRobot() *fakeRobot { return &fakeRobot{sent: make(chan string, 16)} }

func (r *fakeRobot) SendIntentResult(esn, intent, params string) error {
	r.sent <- fmt.Sprintf("intent %s %s %s", esn, intent, params)
	return nil
}

func (r *fakeRobot) SendKnowledge(esn, answer, query string) error {
	r.sent <- fmt.Sprintf("knowledge %s %q for %q", esn, answer, query)
	return nil
}

func (r *fakeRobot) next(t *testing.T) string {
	t.Helper()
	select {
	case s := <-r.sent:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("the robot got no command")
		return ""
	}
}

func (r *fakeRobot) none(t *testing.T) {
	t.Helper()
	select {
	case s := <-r.sent:
		t.Fatalf("unexpected command %s", s)
	case <-time.After(50 * time.Millisecond):
	}
}

type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(a ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprint(a...))
	l.mu.Unlock()
}

func (l *logLines) has(want string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

func newTestConversation(tr Transcriber, b Brain, r Robot, logs *logLines) *Conversation {
	return NewConversation(Config{
		Capture:     testCaptureConfig(),
		NewVAD:      func() VAD { return markVAD{} },
		Transcriber: tr,
		Brain:       b,
		Robot:       r,
		Logf:        logs.logf,
	})
}

// speak feeds one second of quiet, a wake at its end, half a second of speech and a second of silence, starting
// at robot time t0; it returns the time after the last frame.
func speak(c *Conversation, esn string, t0 uint64) uint64 {
	ts := t0
	feed := func(n int, speech bool) {
		for i := 0; i < n; i++ {
			c.Feed(esn, testFrame(ts, speech))
			ts += 10
		}
	}
	feed(100, false)
	c.Wake(esn, ts-10)
	feed(50, true)
	feed(100, false)
	return ts
}

func TestConversationTranscribesAndAnswersEachUtteranceOnce(t *testing.T) {
	tr := &fakeTranscriber{text: "qué hora es"}
	b := &fakeBrain{answer: Answer{Intent: "intent_clock_time"}}
	r := newFakeRobot()
	c := newTestConversation(tr, b, r, &logLines{})
	c.Open("esn1")
	ts := speak(c, "esn1", 0)
	r.next(t)
	speak(c, "esn1", ts)
	r.next(t)
	r.none(t)
	if tr.count() != 2 || b.count() != 2 {
		t.Fatalf("transcriber called %d times, brain %d times; want 2 and 2", tr.count(), b.count())
	}
	if got := tr.calls[0]; got.ESN != "esn1" || got.Reason != EndSilence || got.Duration() != 1310*time.Millisecond {
		t.Fatalf("first utterance %s %q %v, want esn1, %q, 1.31s", got.ESN, got.Reason, got.Duration(), EndSilence)
	}
	if got := b.calls[0]; got.Text != "qué hora es" || got.Language != "es" {
		t.Fatalf("brain got %+v", got)
	}
}

func TestAnIntentAnswerSendsAnIntentResult(t *testing.T) {
	r := newFakeRobot()
	b := &fakeBrain{answer: Answer{Intent: "intent_clock_settimer", ParamsJSON: `{"timer_duration":"300"}`}}
	c := newTestConversation(&fakeTranscriber{text: "set a timer for five minutes"}, b, r, &logLines{})
	speak(c, "esn1", 0)
	if got, want := r.next(t), `intent esn1 intent_clock_settimer {"timer_duration":"300"}`; got != want {
		t.Fatalf("robot got %s, want %s", got, want)
	}
	r.none(t)
}

func TestATextAnswerSendsKnowledge(t *testing.T) {
	r := newFakeRobot()
	b := &fakeBrain{answer: Answer{Text: "Estoy muy bien, gracias."}}
	c := newTestConversation(&fakeTranscriber{text: "¿cómo estás?"}, b, r, &logLines{})
	speak(c, "esn1", 0)
	if got, want := r.next(t), `knowledge esn1 "Estoy muy bien, gracias." for "¿cómo estás?"`; got != want {
		t.Fatalf("robot got %s, want %s", got, want)
	}
	r.none(t)
}

func TestNoSpeechAfterAWakeSendsNothing(t *testing.T) {
	tr := &fakeTranscriber{text: "x"}
	r := newFakeRobot()
	logs := &logLines{}
	c := newTestConversation(tr, &fakeBrain{answer: Answer{Text: "x"}}, r, logs)
	c.Wake("esn1", 0)
	for ts := uint64(0); ts < 5000; ts += 10 {
		c.Feed("esn1", testFrame(ts, false))
	}
	r.none(t)
	if tr.count() != 0 {
		t.Fatalf("transcriber called %d times for silence", tr.count())
	}
	if !logs.has("no speech") {
		t.Fatalf("no log line about the missing speech in %q", logs.lines)
	}
}

// Whisper turns the robot's own greeting sounds into "." or nothing; there is no question to answer.
func TestATranscriptWithoutWordsIsNotAnswered(t *testing.T) {
	for _, text := range []string{"  ", ".", "¿?", "..."} {
		b := &fakeBrain{answer: Answer{Text: "x"}}
		r := newFakeRobot()
		tr := &fakeTranscriber{text: text}
		c := newTestConversation(tr, b, r, &logLines{})
		speak(c, "esn1", 0)
		for deadline := time.Now().Add(2 * time.Second); tr.count() == 0 && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		r.none(t)
		if b.count() != 0 {
			t.Fatalf("brain called %d times for the transcript %q", b.count(), text)
		}
	}
}

func TestABlockedBrainDoesNotBlockTheMic(t *testing.T) {
	hold := make(chan struct{})
	b := &fakeBrain{answer: Answer{Intent: "intent_clock_time"}, hold: hold}
	tr := &fakeTranscriber{text: "qué hora es"}
	r := newFakeRobot()
	logs := &logLines{}
	c := newTestConversation(tr, b, r, logs)

	fed := make(chan struct{})
	go func() {
		defer close(fed)
		ts := uint64(0)
		for i := 0; i < 5; i++ { // one being answered, one waiting, three dropped
			ts = speak(c, "esn1", ts)
			if i == 0 {
				for b.count() == 0 {
					time.Sleep(time.Millisecond)
				}
			}
		}
		// The mic keeps flowing long after the brain stopped answering.
		for i := 0; i < 10000; i++ {
			c.Feed("esn1", testFrame(ts, false))
			ts += 10
		}
	}()
	select {
	case <-fed:
	case <-time.After(5 * time.Second):
		t.Fatal("feeding the mic blocked while the brain was blocked")
	}
	if !logs.has("dropped") {
		t.Fatalf("no log line about dropped utterances in %q", logs.lines)
	}
	close(hold)
	r.next(t)
	r.next(t)
	r.none(t)
	if b.count() != 2 {
		t.Fatalf("brain called %d times, want 2 (the one in flight and the one queued)", b.count())
	}
}

type failingBrain struct{}

func (failingBrain) Answer(context.Context, string, Transcript) (Answer, error) {
	return Answer{}, errors.New("no LLM")
}

func TestABrainErrorIsLoggedAndSendsNothing(t *testing.T) {
	r := newFakeRobot()
	logs := &logLines{}
	c := newTestConversation(&fakeTranscriber{text: "hola"}, failingBrain{}, r, logs)
	speak(c, "esn1", 0)
	for deadline := time.Now().Add(2 * time.Second); !logs.has("no LLM") && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if !logs.has("no LLM") {
		t.Fatalf("brain error not logged in %q", logs.lines)
	}
	r.none(t)
}

func TestAStreamThatEndsMidCaptureSendsNothing(t *testing.T) {
	tr := &fakeTranscriber{text: "x"}
	r := newFakeRobot()
	c := newTestConversation(tr, &fakeBrain{answer: Answer{Text: "x"}}, r, &logLines{})
	c.Open("esn1")
	c.Wake("esn1", 0)
	for ts := uint64(0); ts < 300; ts += 10 {
		c.Feed("esn1", testFrame(ts, true))
	}
	c.Close("esn1")
	r.none(t)
	if tr.count() != 0 {
		t.Fatalf("transcriber called %d times for a cut capture", tr.count())
	}
}
