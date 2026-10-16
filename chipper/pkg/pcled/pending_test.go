package pcled

import (
	"errors"
	"testing"
	"time"

	sr "github.com/kercre123/wire-pod/chipper/pkg/wirepod/speechrequest"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestPending(ttl time.Duration) (*Pending, *clock) {
	c := &clock{t: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	p := NewPending(ttl)
	p.now = c.now
	return p, c
}

func TestPendingUtteranceIsTakenOnce(t *testing.T) {
	p, _ := newTestPending(10 * time.Second)
	p.Set("esn1", "what time is it")
	if text, ok := p.Take("esn1"); !ok || text != "what time is it" {
		t.Fatalf("took %q %v", text, ok)
	}
	if _, ok := p.Take("esn1"); ok {
		t.Fatal("taken twice")
	}
	if _, ok := p.Take("other"); ok {
		t.Fatal("taken for another robot")
	}
}

func TestPendingUtteranceExpires(t *testing.T) {
	p, c := newTestPending(10 * time.Second)
	p.Set("esn1", "hola")
	c.t = c.t.Add(11 * time.Second)
	if _, ok := p.Take("esn1"); ok {
		t.Fatal("an expired utterance answered a later voice request")
	}
}

func TestWrappedSTTUsesThePendingUtterance(t *testing.T) {
	p, _ := newTestPending(10 * time.Second)
	calls := 0
	stt := WrapSTT(func(sr.SpeechRequest) (string, error) { calls++; return "from the robot audio", nil }, p, nil)

	p.Set("esn1", "qué hora es")
	if text, err := stt(sr.SpeechRequest{Device: "esn1"}); err != nil || text != "qué hora es" || calls != 0 {
		t.Fatalf("got %q %v, robot STT called %d times", text, err, calls)
	}
	// No pending utterance: the robot's own audio is transcribed as before.
	if text, err := stt(sr.SpeechRequest{Device: "esn1"}); err != nil || text != "from the robot audio" || calls != 1 {
		t.Fatalf("got %q %v, robot STT called %d times", text, err, calls)
	}
}

func TestWrappedSTTKeepsErrors(t *testing.T) {
	p, _ := newTestPending(10 * time.Second)
	boom := errors.New("boom")
	stt := WrapSTT(func(sr.SpeechRequest) (string, error) { return "", boom }, p, nil)
	if _, err := stt(sr.SpeechRequest{Device: "esn1"}); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}
