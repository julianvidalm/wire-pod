package initwirepod

import (
	"strings"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
)

// With CORTEX_TTS=piper a voice request's text answer is spoken as one audio stream over the robot's session: a
// prepare at 16 kHz and CORTEX_TTS_VOLUME, chunks of at most 1024 bytes, then the complete; the robot's audio
// stream reports are routed to the talker.
func TestTTSOnSpeaksTheAnswerAsAnAudioStream(t *testing.T) {
	t.Setenv(ttsVolumeEnv, "70")
	t.Setenv(conversationEnv, "on")
	t.Setenv(voiceRequestsEnv, "on")
	t.Setenv(voiceDumpDirEnv, "")
	session := bigSender{got: make(chan *mictappb.Command, 4096)}
	var status func(string, *mictappb.AudioStatus)
	conv, _, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("¿cómo estás?"),
		brain: stubBrain{cortex.Answer{Text: "Estoy bien. ¿Y tú?"}}, newVAD: func() cortex.VAD { return loudVAD{} },
		sender: session, voice: toneVoice, audioStatus: func(f func(string, *mictappb.AudioStatus)) { status = f }})
	if err != nil {
		t.Fatal(err)
	}
	h, err := newVoiceRequests(conv, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.audio = func(_ any, stop <-chan struct{}) <-chan voiceAudio { return chunksOf(speechPCM(true), stop) }
	vt := &voiceRequestTest{stream: &fakeChipperStream{}, session: session, log: &lineLog{}, handler: h}
	vt.ask()
	cmds := vt.sessionCommands(t, hasComplete)
	p := cmds[0].GetAudio().GetPrepare()
	if p.GetSampleRate() != 16000 || p.GetVolume() != 70 || p.GetExpectedDurationMs() < 2000 {
		t.Fatalf("first command %v, want the prepare", cmds[0])
	}
	id := cmds[0].GetAudio().GetStreamId()
	bytes := 0
	for _, c := range cmds[1 : len(cmds)-1] {
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

func TestTheStartupLineNamesTheVoice(t *testing.T) {
	t.Setenv(conversationEnv, "on")
	unsetEnv(t, ttsVolumeEnv)
	_, desc, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"), brain: stubBrain{},
		newVAD: func() cortex.VAD { return loudVAD{} }, sender: newChanSender(), voice: toneVoice})
	if err != nil || !strings.Contains(desc, "answers spoken by the cortex: tone voice; volume 100") {
		t.Fatalf("got %q, %v", desc, err)
	}
}
