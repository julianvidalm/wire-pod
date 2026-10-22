package initwirepod

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
)

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

// bigSender records every command; a stream has hundreds.
type bigSender struct{ got chan *mictappb.Command }

func (b bigSender) Send(_ string, cmd *mictappb.Command) error { b.got <- cmd; return nil }

// unsetEnv removes key for this test; t.Setenv restores its old value afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

type stubTranscriber struct{ text string }

func (s stubTranscriber) Transcribe(context.Context, cortex.Utterance) (cortex.Transcript, error) {
	return cortex.Transcript{Text: s.text, Language: "es"}, nil
}

func stubTranscriberFunc(text string) func() (cortex.Transcriber, string, error) {
	return func() (cortex.Transcriber, string, error) { return stubTranscriber{text}, "stub", nil }
}

type stubBrain struct{ answer cortex.Answer }

func (s stubBrain) Answer(context.Context, string, cortex.Transcript) (cortex.Answer, error) {
	return s.answer, nil
}

// loudVAD counts a frame as speech when its first sample is not zero.
type loudVAD struct{}

func (loudVAD) IsSpeech(s []int16) bool { return len(s) > 0 && s[0] != 0 }

// toneSynth synthesizes each sentence as a second of tone at 16 kHz.
type toneSynth struct{}

func (toneSynth) Synthesize(context.Context, string, string) ([]int16, int, error) {
	pcm := make([]int16, 16000)
	for i := range pcm {
		pcm[i] = int16(i%100*200 - 10000)
	}
	return pcm, 16000, nil
}

func toneVoice() (cortex.Voice, string, error) {
	return cortex.NewSentenceVoice(toneSynth{}, -20), "tone voice", nil
}

func hasComplete(cmds []*mictappb.Command) bool {
	return len(cmds) > 0 && cmds[len(cmds)-1].GetAudio().GetComplete() != nil
}
