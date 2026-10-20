package initwirepod

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
	"google.golang.org/protobuf/proto"
)

// bigSender records every command; a stream has hundreds.
type bigSender struct{ got chan *mictappb.Command }

func (b bigSender) Send(_ string, cmd *mictappb.Command) error { b.got <- cmd; return nil }

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

// answerOnce runs one wake with a question answered by text and returns the commands the robot got.
func answerOnce(t *testing.T, d conversationDeps, text string, until func([]*mictappb.Command) bool) []*mictappb.Command {
	t.Helper()
	t.Setenv(conversationEnv, "on")
	t.Setenv(thinkingIntentEnv, "")
	t.Setenv(owwMicRateEnv, "16000")
	s := bigSender{got: make(chan *mictappb.Command, 4096)}
	d.transcriber, d.sender = stubTranscriberFunc("¿cómo estás?"), s
	d.brain, d.newVAD = stubBrain{cortex.Answer{Text: text}}, func() cortex.VAD { return loudVAD{} }
	conv, desc, err := newConversation(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(desc)
	conv.Wake("0dd1b652", 0)
	for ts := uint64(0); ts < 3000; ts += 10 {
		conv.Feed("0dd1b652", micTapFrame(ts, ts > 100 && ts < 700))
	}
	var got []*mictappb.Command
	deadline := time.After(5 * time.Second)
	for !until(got) {
		select {
		case cmd := <-s.got:
			got = append(got, cmd)
		case <-deadline:
			t.Fatalf("commands so far: %d", len(got))
		}
	}
	return got
}

func micTapFrame(ts uint64, speech bool) cortex.Frame {
	f := cortex.Frame{Samples: make([]int16, 160), Timestamp: ts}
	if speech {
		for i := range f.Samples {
			f.Samples[i] = 1000
		}
	}
	return f
}

func hasComplete(cmds []*mictappb.Command) bool {
	return len(cmds) > 0 && cmds[len(cmds)-1].GetAudio().GetComplete() != nil
}

// With CORTEX_TTS off (the default) a text answer is the same knowledge command, byte for byte, as before T8d.
func TestTTSOffSendsTheSameKnowledgeCommand(t *testing.T) {
	for _, v := range []string{"", "off"} {
		t.Setenv("CORTEX_TTS", v)
		cmds := answerOnce(t, conversationDeps{}, "Estoy bien.", func(c []*mictappb.Command) bool { return len(c) == 1 })
		want, _ := proto.Marshal(&mictappb.Command{Command: &mictappb.Command_Knowledge{
			Knowledge: &mictappb.KnowledgeResponse{Answer: "Estoy bien.", QueryText: "¿cómo estás?"}}})
		got, _ := proto.Marshal(cmds[0])
		if string(got) != string(want) {
			t.Fatalf("CORTEX_TTS=%q: sent %v", v, cmds[0])
		}
	}
}

func TestTTSOnSpeaksTheAnswerAsAnAudioStream(t *testing.T) {
	t.Setenv(ttsVolumeEnv, "70")
	var status func(string, *mictappb.AudioStatus)
	cmds := answerOnce(t, conversationDeps{voice: toneVoice,
		audioStatus: func(f func(string, *mictappb.AudioStatus)) { status = f }}, "Estoy bien. ¿Y tú?", hasComplete)
	k := cmds[0].GetKnowledge()
	if k.GetAnswer() != "Estoy bien. ¿Y tú?" || !k.GetExternalVoice() || k.GetExpectedDurationMs() < 2000 {
		t.Fatalf("first command %v, want the knowledge with external voice", cmds[0])
	}
	p := cmds[1].GetAudio().GetPrepare()
	if p.GetSampleRate() != 16000 || p.GetVolume() != 70 || p.GetExpectedDurationMs() != k.GetExpectedDurationMs() {
		t.Fatalf("second command %v, want the prepare", cmds[1])
	}
	id := cmds[1].GetAudio().GetStreamId()
	bytes := 0
	for _, c := range cmds[2 : len(cmds)-1] {
		ch := c.GetAudio().GetChunk()
		if ch == nil || c.GetAudio().GetStreamId() != id || len(ch.GetSamples()) > 1024 {
			t.Fatalf("command %v, want a chunk of stream %d", c, id)
		}
		bytes += len(ch.GetSamples())
	}
	if bytes != 2*2*16000 {
		t.Fatalf("%d bytes streamed, want two seconds", bytes)
	}
	if status == nil {
		t.Fatal("no audio status handler registered")
	}
}

// A robot that cannot play the stream (reports failed before it started) says the answer with its own voice.
func TestTTSOnFallsBackWhenTheRobotFailsTheStream(t *testing.T) {
	var status func(string, *mictappb.AudioStatus)
	d := conversationDeps{voice: toneVoice, audioStatus: func(f func(string, *mictappb.AudioStatus)) { status = f }}
	d.sender = nil
	cmds := answerOnce(t, d, "Estoy bien.", func(c []*mictappb.Command) bool {
		if len(c) == 2 {
			status("0dd1b652", &mictappb.AudioStatus{StreamId: c[1].GetAudio().GetStreamId(), State: mictappb.AudioStatus_FAILED})
		}
		return len(c) > 0 && c[len(c)-1].GetKnowledge() != nil && !c[len(c)-1].GetKnowledge().GetExternalVoice()
	})
	last := cmds[len(cmds)-1].GetKnowledge()
	if last.GetAnswer() != "Estoy bien." {
		t.Fatalf("fallback %v", last)
	}
}

func TestTheStartupLineNamesTheVoice(t *testing.T) {
	t.Setenv(conversationEnv, "on")
	unsetEnv(t, ttsVolumeEnv)
	_, desc, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"), brain: stubBrain{},
		newVAD: func() cortex.VAD { return loudVAD{} }, sender: newChanSender(), voice: toneVoice})
	if err != nil || !strings.Contains(desc, "answers spoken by the cortex: tone voice; volume 100") {
		t.Fatalf("got %q, %v", desc, err)
	}
}
