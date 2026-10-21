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

// fakeRobot records each answer a voice request got as one line on a channel.
type fakeRobot struct{ sent chan string }

func newFakeRobot() *fakeRobot { return &fakeRobot{sent: make(chan string, 16)} }

// to is the reply of one of the robot's voice requests.
func (r *fakeRobot) to(esn string) Reply { return robotReply{r, esn} }

type robotReply struct {
	r   *fakeRobot
	esn string
}

func (x robotReply) Intent(intent, params, _ string) error {
	x.r.sent <- fmt.Sprintf("intent %s %s %s", x.esn, intent, params)
	return nil
}

func (x robotReply) Knowledge(answer, query string) error {
	x.r.sent <- fmt.Sprintf("knowledge %s %q for %q", x.esn, answer, query)
	return nil
}

func (x robotReply) KnowledgeVoice(answer, query string, _ time.Duration) error {
	x.r.sent <- fmt.Sprintf("knowledge voice %s %q for %q", x.esn, answer, query)
	return nil
}

func (r *fakeRobot) next(t *testing.T) string {
	t.Helper()
	select {
	case s := <-r.sent:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("the robot got no answer")
		return ""
	}
}

func (r *fakeRobot) none(t *testing.T) {
	t.Helper()
	select {
	case s := <-r.sent:
		t.Fatalf("unexpected answer %s", s)
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

// testConversation is a conversation whose voice requests are answered into robot.
type testConversation struct {
	*Conversation
	robot *fakeRobot
}

func newTestConversation(tr Transcriber, b Brain, r *fakeRobot, logs *logLines) *testConversation {
	return &testConversation{NewConversation(Config{
		Capture:     testCaptureConfig(),
		NewVAD:      func() VAD { return markVAD{} },
		Transcriber: tr,
		Brain:       b,
		Logf:        logs.logf,
	}), r}
}

// ask is one voice request from robot esn: its audio starts at the wake with pause of silence, then question at
// amplitude (none when question is 0) and two seconds of silence. The capture cuts it with the conversation's
// rules and the utterance is answered into the robot.
func ask(c *testConversation, esn string, pause, question time.Duration, amplitude int16) Result {
	rc := c.NewRequestCapture(16000)
	var pcm []int16
	add := func(d time.Duration, amp int16) {
		for i := 0; i < int(d*16000/time.Second); i++ {
			pcm = append(pcm, amp)
		}
	}
	add(pause, 0)
	add(question, amplitude)
	add(2*time.Second, 0)
	u, done := rc.Feed(pcm)
	if !done {
		u = rc.End(EndStream)
	}
	u.ESN = esn
	return c.Respond(context.Background(), u, c.robot.to(esn))
}

// speak is a voice request with a clear half-second question two seconds after the wake, past the wake grace.
func speak(c *testConversation, esn string) Result {
	return ask(c, esn, 2*time.Second, 500*time.Millisecond, clearAmplitude)
}

func TestConversationTranscribesAndAnswersEachRequestOnce(t *testing.T) {
	tr := &fakeTranscriber{text: "qué hora es"}
	b := &fakeBrain{answer: Answer{Intent: "intent_clock_time"}}
	r := newFakeRobot()
	c := newTestConversation(tr, b, r, &logLines{})
	if res := speak(c, "esn1"); res.Outcome != Answered || res.Answer != "intent intent_clock_time" {
		t.Fatalf("result %+v", res)
	}
	r.next(t)
	speak(c, "esn1")
	r.next(t)
	r.none(t)
	if tr.count() != 2 || b.count() != 2 {
		t.Fatalf("transcriber called %d times, brain %d times; want 2 and 2", tr.count(), b.count())
	}
	if got := tr.calls[0]; got.ESN != "esn1" || got.Reason != EndSilence {
		t.Fatalf("first utterance %s %q, want esn1, %q", got.ESN, got.Reason, EndSilence)
	}
	if got := b.calls[0]; got.Text != "qué hora es" || got.Language != "es" {
		t.Fatalf("brain got %+v", got)
	}
}

func TestAnIntentAnswerIsTheRequestsIntent(t *testing.T) {
	r := newFakeRobot()
	b := &fakeBrain{answer: Answer{Intent: "intent_clock_settimer", ParamsJSON: `{"timer_duration":"300"}`}}
	c := newTestConversation(&fakeTranscriber{text: "set a timer for five minutes"}, b, r, &logLines{})
	speak(c, "esn1")
	if got, want := r.next(t), `intent esn1 intent_clock_settimer {"timer_duration":"300"}`; got != want {
		t.Fatalf("robot got %s, want %s", got, want)
	}
	r.none(t)
}

func TestATextAnswerWithoutVoiceIsKnowledge(t *testing.T) {
	r := newFakeRobot()
	b := &fakeBrain{answer: Answer{Text: "Estoy muy bien, gracias."}}
	c := newTestConversation(&fakeTranscriber{text: "¿cómo estás?"}, b, r, &logLines{})
	speak(c, "esn1")
	if got, want := r.next(t), `knowledge esn1 "Estoy muy bien, gracias." for "¿cómo estás?"`; got != want {
		t.Fatalf("robot got %s, want %s", got, want)
	}
	r.none(t)
}

func TestNoSpeechAfterAWakeIsNothingHeard(t *testing.T) {
	tr := &fakeTranscriber{text: "Thank you."}
	r := newFakeRobot()
	logs := &logLines{}
	c := newTestConversation(tr, &fakeBrain{answer: Answer{Text: "You're welcome!"}}, r, logs)
	if res := ask(c, "esn1", 4*time.Second, 0, 0); res.Outcome != NothingHeard {
		t.Fatalf("outcome %v, want nothing heard", res.Outcome)
	}
	r.none(t)
	if tr.count() != 0 {
		t.Fatalf("transcriber called %d times for silence", tr.count())
	}
	if !logs.has("nothing heard: no speech in") {
		t.Fatalf("no log line about the missing speech in %q", logs.lines)
	}
}

// Whisper turns the robot's own sounds into "." or nothing; there is no question to answer.
func TestATranscriptWithoutWordsIsNotAnswered(t *testing.T) {
	for _, text := range []string{"  ", ".", "¿?", "..."} {
		b := &fakeBrain{answer: Answer{Text: "x"}}
		r := newFakeRobot()
		c := newTestConversation(&fakeTranscriber{text: text}, b, r, &logLines{})
		if res := speak(c, "esn1"); res.Outcome != NothingHeard {
			t.Fatalf("%q: outcome %v, want nothing heard", text, res.Outcome)
		}
		r.none(t)
		if b.count() != 0 {
			t.Fatalf("brain called %d times for the transcript %q", b.count(), text)
		}
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
	if res := speak(c, "esn1"); res.Outcome != Failed {
		t.Fatalf("outcome %v, want failed", res.Outcome)
	}
	if !logs.has("no LLM") {
		t.Fatalf("brain error not logged in %q", logs.lines)
	}
	r.none(t)
}

// L35: the wake, a pause, then the question: the pause must not end the capture before it.
func TestAPauseAfterTheWakeStillCapturesTheQuestion(t *testing.T) {
	tr := &fakeTranscriber{text: "¿qué hora es?"}
	r := newFakeRobot()
	c := newTestConversation(tr, &fakeBrain{answer: Answer{Intent: "intent_clock_time"}}, r, &logLines{})
	ask(c, "esn1", time.Second, 1500*time.Millisecond, clearAmplitude)
	if got, want := r.next(t), "intent esn1 intent_clock_time "; got != want {
		t.Fatalf("robot got %q, want %q", got, want)
	}
	r.none(t)
	if tr.count() != 1 || tr.calls[0].Reason != EndSilence {
		t.Fatalf("transcribed %+v, want one utterance ended by silence", tr.calls)
	}
}

// Whisper turns near-silence into "Thank you."; the VAD may still call some of it speech.
func TestNearSilenceAfterTheWakeSendsNothing(t *testing.T) {
	for _, amplitude := range []int16{60, 260} { // about -55 and -42 dBFS
		tr := &fakeTranscriber{text: "Thank you."}
		b := &fakeBrain{answer: Answer{Text: "You're welcome!"}}
		r := newFakeRobot()
		logs := &logLines{}
		c := newTestConversation(tr, b, r, logs)
		ask(c, "esn1", 2*time.Second, 600*time.Millisecond, amplitude)
		r.none(t)
		if b.count() != 0 {
			t.Fatalf("amplitude %d: brain called %d times", amplitude, b.count())
		}
		if !logs.has("nothing heard") {
			t.Fatalf("amplitude %d: no nothing-heard line in %q", amplitude, logs.lines)
		}
	}
}

func TestAShortSoundAfterTheWakeSendsNothing(t *testing.T) {
	tr := &fakeTranscriber{text: "you"}
	r := newFakeRobot()
	logs := &logLines{}
	c := newTestConversation(tr, &fakeBrain{answer: Answer{Text: "x"}}, r, logs)
	ask(c, "esn1", 2*time.Second, 150*time.Millisecond, clearAmplitude)
	r.none(t)
	if tr.count() != 0 || !logs.has("nothing heard") {
		t.Fatalf("transcriber called %d times, logs %q; want 0 and a nothing-heard line", tr.count(), logs.lines)
	}
}

// A hallucination's text is also a real answer: "gracias" said clearly is answered.
func TestAClearlySpokenGraciasIsAnswered(t *testing.T) {
	tr := &fakeTranscriber{text: "Gracias."}
	r := newFakeRobot()
	c := newTestConversation(tr, &fakeBrain{answer: Answer{Text: "De nada."}}, r, &logLines{})
	ask(c, "esn1", 2*time.Second, 600*time.Millisecond, clearAmplitude)
	if got, want := r.next(t), `knowledge esn1 "De nada." for "Gracias."`; got != want {
		t.Fatalf("robot got %s, want %s", got, want)
	}
}

// Only whisper's silence phrases are checked against the level: a quieter real question is still answered.
func TestAQuietRealQuestionIsAnswered(t *testing.T) {
	tr := &fakeTranscriber{text: "¿qué hora es?"}
	r := newFakeRobot()
	c := newTestConversation(tr, &fakeBrain{answer: Answer{Intent: "intent_clock_time"}}, r, &logLines{})
	ask(c, "esn1", 2*time.Second, time.Second, 260)
	if got, want := r.next(t), "intent esn1 intent_clock_time "; got != want {
		t.Fatalf("robot got %q, want %q", got, want)
	}
}

// The replay of session 150003 said "Stop." with 0.2 s of speech: a short command is answered.
func TestAShortClearCommandIsAnswered(t *testing.T) {
	tr := &fakeTranscriber{text: "Stop."}
	r := newFakeRobot()
	c := newTestConversation(tr, &fakeBrain{answer: Answer{Intent: "intent_imperative_quiet"}}, r, &logLines{})
	ask(c, "esn1", 2*time.Second, 200*time.Millisecond, clearAmplitude)
	if got, want := r.next(t), "intent esn1 intent_imperative_quiet "; got != want {
		t.Fatalf("robot got %q, want %q", got, want)
	}
}
