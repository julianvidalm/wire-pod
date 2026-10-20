package initwirepod

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
	mictapserver "github.com/julianvidalm/wire-pod/chipper/pkg/servers/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
)

var testDetection = wakeword.Detection{
	ESN:  "0dd1b652",
	Word: wakeword.Word{Text: "hey jarvis", Conf: 0.93},
	Text: "hey jarvis",
}

const testWakeLine = `Wake word: 0dd1b652 heard "hey jarvis" (confidence 0.93) in "hey jarvis"`

type lineLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *lineLog) logf(a ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprint(a...))
	l.mu.Unlock()
}

func (l *lineLog) has(want string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

func (l *lineLog) waitFor(t *testing.T, want string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if l.has(want) {
			return
		}
	}
	t.Fatalf("no log line with %q in %q", want, l.lines)
}

// chanSender records commands on a channel and can hold Send until release is closed.
type chanSender struct {
	got     chan *mictappb.Command
	esns    chan string
	release chan struct{}
}

func newChanSender() *chanSender {
	return &chanSender{got: make(chan *mictappb.Command, 8), esns: make(chan string, 8)}
}

func (c *chanSender) Send(esn string, cmd *mictappb.Command) error {
	c.esns <- esn
	c.got <- cmd
	if c.release != nil {
		<-c.release
	}
	return nil
}

func TestWakeLineIsUnchanged(t *testing.T) {
	if got := wakeLine(testDetection); got != testWakeLine {
		t.Fatalf("got %q, want %q", got, testWakeLine)
	}
}

func TestWakeLogsAndSendsTheDefaultAttendIntent(t *testing.T) {
	t.Setenv(attendIntentEnv, "")
	unsetEnv(t, attendIntentEnv)
	t.Setenv(attendRefractoryEnv, "")
	s, log := newChanSender(), &lineLog{}
	h, err := newWakeHandler(log.logf, s)
	if err != nil {
		t.Fatal(err)
	}
	h.OnWake(testDetection)
	if !log.has(testWakeLine) {
		t.Fatalf("wake line missing from %q", log.lines)
	}
	select {
	case cmd := <-s.got:
		if esn := <-s.esns; esn != "0dd1b652" || cmd.GetIntentResult().GetIntent() != "intent_greeting_hello" {
			t.Fatalf("sent %q %v", esn, cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no attend command sent")
	}
	log.waitFor(t, "attend sent 0dd1b652 intent_greeting_hello")
	h.OnWake(testDetection) // inside the default 3 s refractory period
	select {
	case cmd := <-s.got:
		t.Fatalf("second wake sent %v", cmd)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTheAttendCarriesTheWakeFrameTimestamp(t *testing.T) {
	t.Setenv(attendIntentEnv, "intent_cortex_listening")
	t.Setenv(attendRefractoryEnv, "10ms")
	s := newChanSender()
	h, err := newWakeHandler((&lineLog{}).logf, s)
	if err != nil {
		t.Fatal(err)
	}
	d := testDetection
	d.FrameTimestamp = 987654
	h.OnWake(d)
	cmd := <-s.got
	if got := cmd.GetIntentResult().GetIntent(); got != "intent_cortex_listening" {
		t.Fatalf("sent %v", cmd)
	}
	if got, want := cmd.GetIntentResult().GetParametersJson(), `{"wake_ts_ms":987654}`; got != want {
		t.Fatalf("parameters %s, want %s", got, want)
	}
}

func TestWakeSendsTheConfiguredIntent(t *testing.T) {
	t.Setenv(attendIntentEnv, "intent_imperative_lookatme")
	t.Setenv(attendRefractoryEnv, "10ms")
	s := newChanSender()
	h, err := newWakeHandler((&lineLog{}).logf, s)
	if err != nil {
		t.Fatal(err)
	}
	h.OnWake(testDetection)
	if cmd := <-s.got; cmd.GetIntentResult().GetIntent() != "intent_imperative_lookatme" {
		t.Fatalf("sent %v", cmd)
	}
}

func TestEmptyAttendIntentOnlyLogs(t *testing.T) {
	t.Setenv(attendIntentEnv, "")
	s, log := newChanSender(), &lineLog{}
	h, err := newWakeHandler(log.logf, s)
	if err != nil {
		t.Fatal(err)
	}
	h.OnWake(testDetection)
	if !log.has(testWakeLine) {
		t.Fatalf("wake line missing from %q", log.lines)
	}
	select {
	case cmd := <-s.got:
		t.Fatalf("attend is off but sent %v", cmd)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWakeDoesNotBlockOnABlockedSend(t *testing.T) {
	t.Setenv(attendIntentEnv, "intent_greeting_hello")
	t.Setenv(attendRefractoryEnv, "1ns")
	s := newChanSender()
	s.release = make(chan struct{})
	defer close(s.release)
	log := &lineLog{}
	h, err := newWakeHandler(log.logf, s)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < 5; i++ {
		h.OnWake(testDetection)
		time.Sleep(time.Millisecond) // past the 1 ns refractory period
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("OnWake took %v with a blocked send", took)
	}
	log.waitFor(t, "attend dropped 0dd1b652")
}

func TestWakeLogsWhenTheRobotHasNoSession(t *testing.T) {
	t.Setenv(attendIntentEnv, "intent_greeting_hello")
	t.Setenv(attendRefractoryEnv, "")
	sessions := mictapserver.New(mictap.NewLogSink(func(...any) {})).Sessions()
	log := &lineLog{}
	h, err := newWakeHandler(log.logf, sessions)
	if err != nil {
		t.Fatal(err)
	}
	h.OnWake(testDetection)
	log.waitFor(t, "attend failed 0dd1b652 intent_greeting_hello: "+mictapserver.ErrNoSession.Error())
}

func TestInvalidAttendRefractoryIsAnError(t *testing.T) {
	t.Setenv(attendIntentEnv, "intent_greeting_hello")
	t.Setenv(attendRefractoryEnv, "soon")
	log := &lineLog{}
	h, err := newWakeHandler(log.logf, newChanSender())
	if err == nil || !strings.Contains(err.Error(), attendRefractoryEnv) {
		t.Fatalf("got %v, want a %s error", err, attendRefractoryEnv)
	}
	h.OnWake(testDetection) // still logs the wake
	if !log.has(testWakeLine) {
		t.Fatalf("wake line missing from %q", log.lines)
	}
}

// unsetEnv removes key for this test; t.Setenv restores its old value afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}
