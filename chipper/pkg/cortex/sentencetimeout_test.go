package cortex

import (
	"context"
	"errors"
	"testing"
	"time"
)

// hangingSynth answers the first ok sentences at once, then hangs until its context ends.
type hangingSynth struct{ ok int }

func (h *hangingSynth) Synthesize(ctx context.Context, _, _ string) ([]int16, int, error) {
	if h.ok > 0 {
		h.ok--
		return make([]int16, 8000), 16000, nil
	}
	<-ctx.Done()
	return nil, 0, ctx.Err()
}

// L88 advisory E: a hung synthesizer stops the speech after the sentence timeout, not the answer's 2 minutes.
func TestAHungSentenceStopsTheSpeechAfterTheSentenceTimeout(t *testing.T) {
	v := NewSentenceVoice(&hangingSynth{}, -20).WithSentenceTimeout(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	began := time.Now()
	sp := v.Speak(ctx, "Uno. Dos.", "es")
	for range sp.Chunks() {
	}
	_, err := sp.Wait()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(began) > time.Second {
		t.Fatalf("got %v after %v, want the sentence deadline within 1 s", err, time.Since(began))
	}
}

func talkerOverHangingSynth(ok int) (*Talker, *fakeAudioRobot) {
	r := &fakeAudioRobot{clock: newVirtualClock()}
	var tk *Talker
	r.onPrepare = started(&tk)
	tk = NewTalker(TalkerConfig{Voice: NewSentenceVoice(&hangingSynth{ok: ok}, -20).WithSentenceTimeout(50 * time.Millisecond),
		Robot: r, RobotSay: r.say, FirstID: 40, Now: r.clock.Now, Sleep: r.clock.Sleep, Timeout: 2 * time.Second})
	return tk, r
}

func TestAHungFirstSentenceFallsBackToTheRobotVoice(t *testing.T) {
	tk, r := talkerOverHangingSynth(0)
	began := time.Now()
	tk.Say("esn1", "Uno. Dos.", "", "es")
	if got := r.sequence(); got != "say" || time.Since(began) > time.Second {
		t.Fatalf("commands %q after %v, want the robot to say it at once", got, time.Since(began))
	}
}

func TestAHungLaterSentenceCancelsTheStream(t *testing.T) {
	tk, r := talkerOverHangingSynth(1)
	began := time.Now()
	tk.Say("esn1", "Uno. Dos.", "", "es")
	if got := r.sequence(); got != "knowledge-voice prepare chunk cancel" || time.Since(began) > time.Second {
		t.Fatalf("commands %q after %v, want the stream cancelled at once", got, time.Since(began))
	}
}
