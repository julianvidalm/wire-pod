package cortex

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type sent struct{ esn, intent string }

// fakeSender records every intent and can hold Send until release is closed.
type fakeSender struct {
	got     chan sent
	release chan struct{}
	err     error
}

func newFakeSender() *fakeSender { return &fakeSender{got: make(chan sent, 16)} }

func (f *fakeSender) SendIntent(esn, intent string) error {
	f.got <- sent{esn, intent}
	if f.release != nil {
		<-f.release
	}
	return f.err
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) Logf(a ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprint(a...))
	l.mu.Unlock()
}

// waitFor polls until a log line contains want.
func (l *logs) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for _, line := range l.lines {
			if strings.Contains(line, want) {
				l.mu.Unlock()
				return
			}
		}
		l.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t.Fatalf("no log line with %q in %q", want, l.lines)
}

func newTestAttender(s Sender) (*Attender, *fakeClock, *logs) {
	clock, log := &fakeClock{now: time.Unix(1000, 0)}, &logs{}
	a := NewAttender(AttenderConfig{
		Intent:     "intent_greeting_hello",
		Refractory: 3 * time.Second,
		Sender:     s,
		Logf:       log.Logf,
		Now:        clock.Now,
	})
	return a, clock, log
}

func expectSend(t *testing.T, s *fakeSender, want sent) {
	t.Helper()
	select {
	case got := <-s.got:
		if got != want {
			t.Fatalf("sent %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no send, want %+v", want)
	}
}

func expectNoSend(t *testing.T, s *fakeSender) {
	t.Helper()
	select {
	case got := <-s.got:
		t.Fatalf("unexpected send %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAttendSendsTheIntentToThatRobotOnce(t *testing.T) {
	s := newFakeSender()
	a, _, log := newTestAttender(s)
	a.Attend("0dd1b652")
	expectSend(t, s, sent{"0dd1b652", "intent_greeting_hello"})
	log.waitFor(t, "attend sent 0dd1b652 intent_greeting_hello")
	expectNoSend(t, s)
}

func TestAttendIgnoresWakesInsideTheRefractoryPeriod(t *testing.T) {
	s := newFakeSender()
	a, clock, _ := newTestAttender(s)
	a.Attend("0dd1b652")
	expectSend(t, s, sent{"0dd1b652", "intent_greeting_hello"})
	clock.advance(2 * time.Second)
	a.Attend("0dd1b652")
	expectNoSend(t, s)
	clock.advance(1500 * time.Millisecond) // 3.5 s after the first wake
	a.Attend("0dd1b652")
	expectSend(t, s, sent{"0dd1b652", "intent_greeting_hello"})
}

func TestAttendKeepsARefractoryPeriodPerRobot(t *testing.T) {
	s := newFakeSender()
	a, _, _ := newTestAttender(s)
	a.Attend("robot-a")
	expectSend(t, s, sent{"robot-a", "intent_greeting_hello"})
	a.Attend("robot-b")
	expectSend(t, s, sent{"robot-b", "intent_greeting_hello"})
}

func TestAttendNeverBlocksOnASlowSend(t *testing.T) {
	s := newFakeSender()
	s.release = make(chan struct{})
	defer close(s.release)
	a, clock, log := newTestAttender(s)
	start := time.Now()
	a.Attend("0dd1b652")
	expectSend(t, s, sent{"0dd1b652", "intent_greeting_hello"}) // now held inside Send
	for i := 0; i < 5; i++ {
		clock.advance(4 * time.Second)
		a.Attend("0dd1b652")
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("Attend took %v with a blocked send", took)
	}
	log.waitFor(t, "attend dropped 0dd1b652")
}

func TestAttendLogsSendErrorsAndKeepsWorking(t *testing.T) {
	s := newFakeSender()
	s.err = errors.New("robot has no open MicTap session")
	a, clock, log := newTestAttender(s)
	a.Attend("0dd1b652")
	expectSend(t, s, sent{"0dd1b652", "intent_greeting_hello"})
	log.waitFor(t, "attend failed 0dd1b652 intent_greeting_hello: robot has no open MicTap session")
	s.err = nil // read by the worker only after the next send is queued
	clock.advance(4 * time.Second)
	a.Attend("0dd1b652")
	expectSend(t, s, sent{"0dd1b652", "intent_greeting_hello"})
	log.waitFor(t, "attend sent 0dd1b652 intent_greeting_hello")
}

// L67: the end of a listening goes through the same per-robot queue as the attend, so the two never reorder.
func TestASignalIsSentInOrderWithTheAttend(t *testing.T) {
	s := newFakeSender()
	s.release = make(chan struct{})
	a, _, _ := newTestAttender(s)
	a.Signal("0dd1b652", "intent_cortex_listening_end", func() bool { return false })
	a.Attend("0dd1b652")
	close(s.release)
	expectSend(t, s, sent{"0dd1b652", "intent_cortex_listening_end"})
	expectSend(t, s, sent{"0dd1b652", "intent_greeting_hello"})
}

// Whether a signal is stale is decided when its turn comes, after the sends queued before it.
func TestAStaleSignalIsSkippedWhenItsTurnComes(t *testing.T) {
	s := newFakeSender()
	s.release = make(chan struct{})
	a, _, log := newTestAttender(s)
	a.Attend("0dd1b652")
	expectSend(t, s, sent{"0dd1b652", "intent_greeting_hello"}) // held inside Send
	var mu sync.Mutex
	stale := false
	a.Signal("0dd1b652", "intent_cortex_listening_end", func() bool { mu.Lock(); defer mu.Unlock(); return stale })
	mu.Lock()
	stale = true
	mu.Unlock()
	close(s.release)
	log.waitFor(t, "skipped 0dd1b652 intent_cortex_listening_end")
	expectNoSend(t, s)
}
