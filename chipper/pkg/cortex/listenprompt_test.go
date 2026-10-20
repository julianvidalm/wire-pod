package cortex

import (
	"sync"
	"testing"
	"time"
)

// T3g (S6): with no question 6 s after the wake the robot shows its prompt and keeps listening; at 9 s it gives up.
const promptIntent = "intent_cortex_listening_prompt"

const promptCommand = "intent esn1 " + promptIntent + " "

func newPromptConversation(tr Transcriber, b Brain, r Robot, prompt string) *Conversation {
	return NewConversation(Config{
		Capture:      liveCaptureConfig(),
		NewVAD:       func() VAD { return markVAD{} },
		Transcriber:  tr,
		Brain:        b,
		Robot:        r,
		EndIntent:    endIntent,
		PromptIntent: prompt,
	})
}

// listenFor feeds a wake at 990 ms, silence until speechAt after it, speech for speechFor (none when 0) and silence
// up to 12 s after the wake.
func listenFor(c *Conversation, speechAt, speechFor time.Duration) {
	for ts := uint64(0); ts < 1000; ts += 10 {
		c.Feed("esn1", testFrame(ts, false))
	}
	c.Wake("esn1", 990)
	from, to := 990+uint64(speechAt/time.Millisecond), 990+uint64((speechAt+speechFor)/time.Millisecond)
	for ts := uint64(1000); ts < 990+12000; ts += 10 {
		c.Feed("esn1", testFrame(ts, speechFor > 0 && ts > from && ts <= to))
	}
}

func TestThePromptIsSentWhenNobodySpeaksThenTheListeningEnds(t *testing.T) {
	r := newFakeRobot()
	c := newPromptConversation(&fakeTranscriber{text: "x"}, &fakeBrain{answer: Answer{Text: "x"}}, r, promptIntent)
	listenFor(c, 0, 0)
	for _, want := range []string{promptCommand, endCommand} {
		if got := r.next(t); got != want {
			t.Fatalf("robot got %q, want %q", got, want)
		}
	}
	r.none(t)
}

func TestAnEarlyQuestionGetsNoPrompt(t *testing.T) {
	r := newFakeRobot()
	c := newPromptConversation(&fakeTranscriber{text: "¿qué hora es?"},
		&fakeBrain{answer: Answer{Intent: "intent_clock_time"}}, r, promptIntent)
	listenFor(c, 2*time.Second, time.Second)
	if got := r.next(t); got != "intent esn1 intent_clock_time " {
		t.Fatalf("robot got %q, want only the answer", got)
	}
	r.none(t)
}

func TestAQuestionAfterThePromptIsAnswered(t *testing.T) {
	r := newFakeRobot()
	c := newPromptConversation(&fakeTranscriber{text: "¿qué hora es?"},
		&fakeBrain{answer: Answer{Intent: "intent_clock_time"}}, r, promptIntent)
	listenFor(c, 7*time.Second, time.Second)
	for _, want := range []string{promptCommand, "intent esn1 intent_clock_time "} {
		if got := r.next(t); got != want {
			t.Fatalf("robot got %q, want %q", got, want)
		}
	}
	r.none(t)
}

func TestAnEmptyPromptIntentSendsNoPrompt(t *testing.T) {
	r := newFakeRobot()
	c := newPromptConversation(&fakeTranscriber{text: "x"}, &fakeBrain{answer: Answer{Text: "x"}}, r, "")
	listenFor(c, 0, 0)
	if got := r.next(t); got != endCommand {
		t.Fatalf("robot got %q, want only the end", got)
	}
	r.none(t)
}

// recordingSignaler keeps every signal so a test can ask, later, whether it went stale.
type recordingSignaler struct {
	mu      sync.Mutex
	intents []string
	stale   []func() bool
}

func (s *recordingSignaler) Signal(_, intent string, stale func() bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.intents = append(s.intents, intent)
	s.stale = append(s.stale, stale)
}

// A prompt whose turn comes only after a newer wake started a capture is skipped, like the end (L67).
func TestAPromptIsStaleOnceANewerWakeListens(t *testing.T) {
	sig := &recordingSignaler{}
	c := newPromptConversation(&fakeTranscriber{text: "x"}, &fakeBrain{answer: Answer{Text: "x"}}, newFakeRobot(),
		promptIntent)
	c.SignalThrough(sig)
	listenFor(c, 0, 0)
	sig.mu.Lock()
	intents, stale := append([]string(nil), sig.intents...), append([]func() bool(nil), sig.stale...)
	sig.mu.Unlock()
	if len(intents) != 2 || intents[0] != promptIntent || intents[1] != endIntent {
		t.Fatalf("signals %q, want the prompt then the end", intents)
	}
	if stale[0]() {
		t.Fatal("the prompt is stale before any newer wake")
	}
	c.Wake("esn1", 20000)
	if !stale[0]() {
		t.Fatal("the prompt is not stale after a newer wake")
	}
}
