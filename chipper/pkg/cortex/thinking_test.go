package cortex

import (
	"context"
	"sync"
	"testing"
)

// L80 (T8c): the robot shows its thinking face from the end of the user's speech until the answer, so the cortex
// sends the thinking intent when a capture ends with speech, before transcribing it.
const thinkingIntent = "intent_cortex_thinking"

// signalsAtTranscribe is a Transcriber that records which signals were already queued when it was called.
type signalsAtTranscribe struct {
	sig  *recordingSignaler
	mu   sync.Mutex
	seen [][]string
}

func (s *signalsAtTranscribe) Transcribe(context.Context, Utterance) (Transcript, error) {
	s.sig.mu.Lock()
	seen := append([]string(nil), s.sig.intents...)
	s.sig.mu.Unlock()
	s.mu.Lock()
	s.seen = append(s.seen, seen)
	s.mu.Unlock()
	return Transcript{Text: "¿qué hora es?", Language: "es"}, nil
}

func newThinkingConversation(tr Transcriber, r Robot, thinking string) *Conversation {
	return NewConversation(Config{
		Capture:        testCaptureConfig(),
		NewVAD:         func() VAD { return markVAD{} },
		Transcriber:    tr,
		Brain:          &fakeBrain{answer: Answer{Intent: "intent_clock_time"}},
		Robot:          r,
		EndIntent:      endIntent,
		ThinkingIntent: thinking,
	})
}

func TestThinkingIsSignalledWhenSpeechEndsBeforeTranscribing(t *testing.T) {
	sig := &recordingSignaler{}
	tr := &signalsAtTranscribe{sig: sig}
	r := newFakeRobot()
	c := newThinkingConversation(tr, r, thinkingIntent)
	c.SignalThrough(sig)
	speak(c, "esn1", 0)
	if got := r.next(t); got != "intent esn1 intent_clock_time " {
		t.Fatalf("answer %q", got)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.seen) != 1 || len(tr.seen[0]) != 1 || tr.seen[0][0] != thinkingIntent {
		t.Fatalf("signals queued when transcribing %q, want just %s", tr.seen, thinkingIntent)
	}
}

func TestThinkingIsStaleOnceANewerWakeListens(t *testing.T) {
	sig := &recordingSignaler{}
	tr := &signalsAtTranscribe{sig: sig}
	r := newFakeRobot()
	c := newThinkingConversation(tr, r, thinkingIntent)
	c.SignalThrough(sig)
	ts := speak(c, "esn1", 0)
	r.next(t)
	sig.mu.Lock()
	if len(sig.stale) == 0 {
		sig.mu.Unlock()
		t.Fatal("no thinking signal")
	}
	stale := sig.stale[0]
	sig.mu.Unlock()
	if stale() {
		t.Fatal("thinking is stale before any newer wake")
	}
	c.Wake("esn1", ts)
	if !stale() {
		t.Fatal("thinking is not stale after a newer wake")
	}
}

func TestNoThinkingWithoutSpeech(t *testing.T) {
	sig := &recordingSignaler{}
	c := newPromptConversation(&fakeTranscriber{text: "x"}, &fakeBrain{answer: Answer{Text: "x"}}, newFakeRobot(), "")
	c.cfg.ThinkingIntent = thinkingIntent
	c.SignalThrough(sig)
	listenFor(c, 0, 0)
	sig.mu.Lock()
	defer sig.mu.Unlock()
	for _, in := range sig.intents {
		if in == thinkingIntent {
			t.Fatalf("signals %q: thinking without speech", sig.intents)
		}
	}
}

func TestAnEmptyThinkingIntentSendsNothing(t *testing.T) {
	sig := &recordingSignaler{}
	tr := &signalsAtTranscribe{sig: sig}
	r := newFakeRobot()
	c := newThinkingConversation(tr, r, "")
	c.SignalThrough(sig)
	speak(c, "esn1", 0)
	r.next(t)
	sig.mu.Lock()
	defer sig.mu.Unlock()
	if len(sig.intents) != 0 {
		t.Fatalf("signals %q, want none", sig.intents)
	}
}
