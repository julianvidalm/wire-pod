package initwirepod

import (
	"strings"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
)

// CORTEX_CONVERSATION unset or off builds no conversation (the voice requests then stay wire-pod's own).
func TestConversationOffBuildsNothing(t *testing.T) {
	for _, v := range []string{"", "off", "OFF"} {
		t.Setenv(conversationEnv, v)
		conv, _, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("hola"), sender: newChanSender()})
		if err != nil || conv != nil {
			t.Fatalf("%q: got conversation %v, error %v; want neither", v, conv, err)
		}
	}
}

func TestATextAnswerIsSentAsKnowledge(t *testing.T) {
	s := newChanSender()
	if err := (robotCommands{s}).SendKnowledge("0dd1b652", "Estoy bien.", "¿cómo estás?"); err != nil {
		t.Fatal(err)
	}
	k := (<-s.got).GetKnowledge()
	if k.GetAnswer() != "Estoy bien." || k.GetQueryText() != "¿cómo estás?" {
		t.Fatalf("sent %v", k)
	}
}

func TestConversationOnNeedsATranscriber(t *testing.T) {
	t.Setenv(conversationEnv, "on")
	if _, _, err := newConversation(conversationDeps{sender: newChanSender()}); err == nil ||
		!strings.Contains(err.Error(), "whisper.cpp") {
		t.Fatalf("got %v, want an error naming whisper.cpp", err)
	}
}

func TestUnknownConversationSettingIsAnError(t *testing.T) {
	t.Setenv(conversationEnv, "maybe")
	if _, _, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x")}); err == nil ||
		!strings.Contains(err.Error(), conversationEnv) {
		t.Fatalf("got %v, want a %s error", err, conversationEnv)
	}
}

// T3g (S6): the no-speech end and the longest question are settings, named in the startup line.
func TestTheListeningTimelineSettings(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{}, "at most 8s from the speech start, gives up after 9s without speech"},
		{map[string]string{noSpeechEnv: "7s", captureMaxEnv: "12s"},
			"at most 12s from the speech start, gives up after 7s without speech"},
	} {
		t.Setenv(conversationEnv, "on")
		for _, k := range []string{noSpeechEnv, captureMaxEnv} {
			if v, ok := tc.env[k]; ok {
				t.Setenv(k, v)
			} else {
				unsetEnv(t, k)
			}
		}
		_, desc, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"),
			brain: stubBrain{}, newVAD: func() cortex.VAD { return loudVAD{} }, sender: newChanSender()})
		if err != nil || !strings.Contains(desc, tc.want) {
			t.Fatalf("%v: got %q, %v; want %q", tc.env, desc, err, tc.want)
		}
	}
	t.Setenv(noSpeechEnv, "soon")
	if _, _, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"), brain: stubBrain{},
		newVAD: func() cortex.VAD { return loudVAD{} }, sender: newChanSender()}); err == nil ||
		!strings.Contains(err.Error(), noSpeechEnv) {
		t.Fatalf("got %v, want a %s error", err, noSpeechEnv)
	}
}
