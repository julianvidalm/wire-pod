package initwirepod

import (
	"context"
	"encoding/binary"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
)

// heardTranscriber records the utterances it gets.
type heardTranscriber struct {
	mu    sync.Mutex
	heard []cortex.Utterance
}

func (h *heardTranscriber) Transcribe(_ context.Context, u cortex.Utterance) (cortex.Transcript, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.heard = append(h.heard, u)
	return cortex.Transcript{Text: "cuéntame un chiste", Language: "es"}, nil
}

// L101: the user asks in the same breath as the wake word while the robot plays its wake reaction. The
// detector still hears the gate's silence; the conversation's capture hears the question.
func TestTheCaptureHearsTheQuestionUnderTheWakeReaction(t *testing.T) {
	t.Setenv(conversationEnv, "on")
	t.Setenv(attendIntentEnv, "intent_greeting_hello")
	t.Setenv(attendRefractoryEnv, "")
	t.Setenv(owwMicRateEnv, "16000")
	t.Setenv(thinkingIntentEnv, "")
	unsetEnv(t, wakeGraceEnv)
	s, log := newChanSender(), &lineLog{}
	tr := &heardTranscriber{}
	conv, _, err := newConversation(conversationDeps{
		transcriber: func() (cortex.Transcriber, string, error) { return tr, "heard", nil },
		brain:       stubBrain{cortex.Answer{Intent: "intent_clock_time"}},
		newVAD:      func() cortex.VAD { return loudVAD{} },
		sender:      s,
		logf:        log.logf,
	})
	if err != nil || conv == nil {
		t.Fatalf("got %v, %v", conv, err)
	}
	cfg := cortex.DefaultHalfDuplexConfig()
	rec, det := &frameSink{}, &frameSink{}
	sink := detectorSink(rec, det, conv, &cfg)
	h, err := newWakeHandler(log.logf, s)
	if err != nil {
		t.Fatal(err)
	}
	h = h.withConversation(conv)

	sink.Start("0dd1b652", mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	spk := uint64(0)
	for ts := uint64(0); ts < 3200; ts += 10 {
		for ; spk <= ts; spk += 32 {
			p := make([]byte, 2048)
			if spk >= 992 && spk < 1500 { // the earcon (speaker frames are 32 ms)
				for i := 0; i < len(p); i += 2 {
					binary.LittleEndian.PutUint16(p[i:], 9000)
				}
			}
			sink.Frame("0dd1b652", mictap.Frame{Type: mictap.FrameSpeaker, Timestamp: spk, Payload: p})
		}
		sink.Frame("0dd1b652", micFrame(uint32(ts/10), ts, ts >= 1000 && ts < 2000))
		if ts == 990 {
			wake := testDetection
			wake.FrameTimestamp = ts
			h.OnWake(wake)
		}
	}
	if cmd := <-s.got; cmd.GetIntentResult().GetIntent() != "intent_greeting_hello" {
		t.Fatalf("first command %v, want the attend", cmd)
	}
	select {
	case cmd := <-s.got:
		if cmd.GetIntentResult().GetIntent() != "intent_clock_time" {
			t.Fatalf("answer %v, want intent_clock_time", cmd)
		}
	case <-time.After(2 * time.Second):
		log.mu.Lock()
		lines := append([]string(nil), log.lines...)
		log.mu.Unlock()
		t.Fatalf("no answer; log %q", lines)
	}
	tr.mu.Lock()
	u := tr.heard[0]
	tr.mu.Unlock()
	if u.Voiced < 500*time.Millisecond {
		t.Fatalf("voiced %v, want the 1 s question", u.Voiced)
	}
	sink.End("0dd1b652", nil)
	det.mu.Lock()
	defer det.mu.Unlock()
	for _, f := range det.frames {
		if f.Timestamp >= 1000 && f.Timestamp < 1600 && loudness(f) {
			t.Fatalf("detector heard the mic at %d ms while the robot sounded", f.Timestamp)
		}
	}
}

func TestTheWakeGraceSetting(t *testing.T) {
	t.Setenv(conversationEnv, "on")
	for v, want := range map[string]string{"": "the capture hears the mic under the half-duplex gate until 1.5s after the wake",
		"2s": "until 2s after the wake", "0s": "the capture hears the half-duplex gate from the wake on"} {
		if v == "" {
			unsetEnv(t, wakeGraceEnv)
		} else {
			t.Setenv(wakeGraceEnv, v)
		}
		_, desc, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"),
			brain: stubBrain{}, newVAD: func() cortex.VAD { return loudVAD{} }, sender: newChanSender()})
		if err != nil || !strings.Contains(desc, want) {
			t.Fatalf("%q: got %q, %v; want %q", v, desc, err, want)
		}
	}
	t.Setenv(wakeGraceEnv, "soon")
	if _, _, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"), brain: stubBrain{},
		newVAD: func() cortex.VAD { return loudVAD{} }, sender: newChanSender()}); err == nil ||
		!strings.Contains(err.Error(), wakeGraceEnv) {
		t.Fatalf("got %v, want a %s error", err, wakeGraceEnv)
	}
}
