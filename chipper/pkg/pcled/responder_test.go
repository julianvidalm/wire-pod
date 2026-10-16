package pcled

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap/mictappb"
	"github.com/kercre123/wire-pod/chipper/pkg/wakeword"
	sr "github.com/kercre123/wire-pod/chipper/pkg/wirepod/speechrequest"
)

type recordingSender struct {
	mu    sync.Mutex
	sent  []string
	err   error
	ready chan struct{}
}

func newRecordingSender() *recordingSender { return &recordingSender{ready: make(chan struct{}, 8)} }

func (s *recordingSender) Send(esn string, cmd *mictappb.Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cmd.GetWake() != nil {
		s.sent = append(s.sent, esn+":wake")
	}
	s.ready <- struct{}{}
	return s.err
}

func (s *recordingSender) wait(t *testing.T) {
	t.Helper()
	select {
	case <-s.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("no command sent")
	}
}

func final(text string) wakeword.Detection {
	return wakeword.Detection{ESN: "esn1", Text: text, Final: true, Utterance: make([]int16, 16000)}
}

func TestFinalDetectionIsTranscribedStoredAndWakesTheRobot(t *testing.T) {
	pending, _ := newTestPending(10 * time.Second)
	sender := newRecordingSender()
	var heard []int
	r := &Responder{
		Transcribe: func(pcm []int16) (string, string, error) {
			heard = append(heard, len(pcm))
			return "hola vector qué hora es", "es", nil
		},
		Pending: pending, Sender: sender,
	}
	r.OnWake(final("hola víctor que hora es"))
	sender.wait(t)
	if len(heard) != 1 || heard[0] != 16000 {
		t.Fatalf("transcribed %v", heard)
	}
	if u, ok := pending.Take("esn1"); !ok || u.Text != "hola vector qué hora es" || u.Lang != "es" {
		t.Fatalf("pending %+v %v", u, ok)
	}
	if len(sender.sent) != 1 || sender.sent[0] != "esn1:wake" {
		t.Fatalf("sent %v", sender.sent)
	}
}

func TestEarlyDetectionsDoNotWakeTheRobot(t *testing.T) {
	pending, _ := newTestPending(10 * time.Second)
	sender := newRecordingSender()
	r := &Responder{Pending: pending, Sender: sender}
	r.OnWake(wakeword.Detection{ESN: "esn1", Text: "vector"})
	time.Sleep(50 * time.Millisecond)
	if len(sender.sent) != 0 {
		t.Fatalf("sent %v", sender.sent)
	}
}

func TestWithoutATranscriberTheRobotStillWakes(t *testing.T) {
	// A vosk-only wire-pod has no better recognizer: the PC only takes care of the wake, and the robot's own
	// voice request is transcribed as before.
	pending, _ := newTestPending(10 * time.Second)
	sender := newRecordingSender()
	r := &Responder{Pending: pending, Sender: sender}
	r.OnWake(final("hola víctor"))
	sender.wait(t)
	if _, ok := pending.Take("esn1"); ok {
		t.Fatal("stored an utterance without a transcriber")
	}
}

func TestFailedTranscriptionStillWakesWithoutAPendingUtterance(t *testing.T) {
	pending, _ := newTestPending(10 * time.Second)
	sender := newRecordingSender()
	r := &Responder{Transcribe: func([]int16) (string, string, error) { return "", "", errors.New("boom") }, Pending: pending, Sender: sender}
	r.OnWake(final("hola víctor"))
	sender.wait(t)
	if _, ok := pending.Take("esn1"); ok {
		t.Fatal("stored an utterance after a failed transcription")
	}
}

func TestNoSecondWakeWhenTheRobotAlreadyWokeItself(t *testing.T) {
	// The user said "Hey Vector ...": the robot woke on its own and made a voice request, and the PC heard the
	// name in the same sentence. Waking it again would start a second conversation.
	pending, _ := newTestPending(10 * time.Second)
	stt := WrapSTT(func(sr.SpeechRequest) (string, error) { return "what time is it", nil }, pending, nil, nil)
	stt(sr.SpeechRequest{Device: "esn1"})
	sender := newRecordingSender()
	r := &Responder{Transcribe: func([]int16) (string, string, error) { return "hey vector what time is it", "en", nil }, Pending: pending, Sender: sender}
	r.OnWake(final("hey vector"))
	time.Sleep(100 * time.Millisecond)
	if len(sender.sent) != 0 {
		t.Fatalf("woke the robot again: %v", sender.sent)
	}
	if _, ok := pending.Take("esn1"); ok {
		t.Fatal("stored an utterance the robot already handled")
	}
}
