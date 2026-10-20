package initwirepod

import (
	"context"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
)

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

func micFrame(seq uint32, ts uint64, speech bool) mictap.Frame {
	p := make([]byte, 320)
	if speech { // every sample, so the speech is about -30 dBFS, above the nothing-heard level (L42)
		for i := 0; i < len(p); i += 2 {
			binary.LittleEndian.PutUint16(p[i:], 1000)
		}
	}
	return mictap.Frame{Type: mictap.FrameMic, Seq: seq, Timestamp: ts, Payload: p}
}

type nopSink struct{}

func (nopSink) Start(string, mictap.Hello) {}
func (nopSink) Frame(string, mictap.Frame) {}
func (nopSink) End(string, error)          {}

// With CORTEX_CONVERSATION off (or unset) a wake does exactly what it did before the conversation existed: it
// logs the wake line and sends the attend intent, and the MicTap stream only reaches the log and the detector.
func TestConversationOffKeepsTheAttendOnlyWake(t *testing.T) {
	for _, v := range []string{"", "off", "OFF"} {
		t.Setenv(conversationEnv, v)
		t.Setenv(attendIntentEnv, "intent_greeting_hello")
		t.Setenv(attendRefractoryEnv, "")
		s, log := newChanSender(), &lineLog{}
		conv, _, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("hola"), sender: s, logf: log.logf})
		if err != nil || conv != nil {
			t.Fatalf("%q: got conversation %v, error %v; want neither", v, conv, err)
		}
		if got, want := detectorSink(nopSink{}, nopSink{}, conv, nil), mictap.Tee(nopSink{}, nopSink{}); !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: sink %#v, want %#v", v, got, want)
		}
		h, err := newWakeHandler(log.logf, s)
		if err != nil {
			t.Fatal(err)
		}
		h = h.withConversation(conv)
		h.OnWake(testDetection)
		if cmd := <-s.got; cmd.GetIntentResult().GetIntent() != "intent_greeting_hello" {
			t.Fatalf("%q: sent %v", v, cmd)
		}
		select {
		case cmd := <-s.got:
			t.Fatalf("%q: a second command %v", v, cmd)
		case <-time.After(100 * time.Millisecond):
		}
		if !log.has(testWakeLine) || log.has("Cortex:") {
			t.Fatalf("%q: log %q", v, log.lines)
		}
	}
}

func TestConversationOnAnswersAfterTheAttend(t *testing.T) {
	t.Setenv(conversationEnv, "on")
	t.Setenv(attendIntentEnv, "intent_greeting_hello")
	t.Setenv(attendRefractoryEnv, "")
	t.Setenv(owwMicRateEnv, "16000")
	t.Setenv(thinkingIntentEnv, "") // TestACaptureWithSpeechSendsTheThinkingIntent covers it
	s, log := newChanSender(), &lineLog{}
	conv, desc, err := newConversation(conversationDeps{
		transcriber: stubTranscriberFunc("¿qué hora es?"),
		brain:       stubBrain{cortex.Answer{Intent: "intent_clock_time"}},
		newVAD:      func() cortex.VAD { return loudVAD{} },
		sender:      s,
		logf:        log.logf,
	})
	if err != nil || conv == nil {
		t.Fatalf("got %v, %v", conv, err)
	}
	if !strings.Contains(desc, "stub") {
		t.Fatalf("description %q does not name the transcriber", desc)
	}
	sink := detectorSink(nopSink{}, nopSink{}, conv, nil)
	h, err := newWakeHandler(log.logf, s)
	if err != nil {
		t.Fatal(err)
	}
	h = h.withConversation(conv)

	sink.Start("0dd1b652", mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	seq, ts := uint32(0), uint64(0)
	feed := func(n int, speech bool) {
		for i := 0; i < n; i++ {
			sink.Frame("0dd1b652", micFrame(seq, ts, speech))
			seq, ts = seq+1, ts+10
		}
	}
	feed(100, false)
	wake := testDetection
	wake.FrameTimestamp = ts - 10
	h.OnWake(wake)
	feed(60, true)
	feed(100, false)

	if cmd := <-s.got; cmd.GetIntentResult().GetIntent() != "intent_greeting_hello" {
		t.Fatalf("first command %v, want the attend", cmd)
	}
	select {
	case cmd := <-s.got:
		if cmd.GetIntentResult().GetIntent() != "intent_clock_time" {
			t.Fatalf("answer %v, want intent_clock_time", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no answer; log %q", log.lines)
	}
	log.waitFor(t, "Cortex: 0dd1b652 answer intent intent_clock_time")
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

var _ wakeword.Handler = wakeHandler{}

// L61: the live capture starts 1 s before the wake, so whisper hears "hey jarvis"; CORTEX_WAKE_MARGIN still sets it.
func TestTheStartupLineNamesTheCaptureMargin(t *testing.T) {
	for v, want := range map[string]string{"": "capture from 1s before the wake", "300ms": "capture from 300ms before the wake"} {
		t.Setenv(conversationEnv, "on")
		t.Setenv(cortexMarginEnv, v)
		_, desc, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"),
			brain: stubBrain{}, newVAD: func() cortex.VAD { return loudVAD{} }, sender: newChanSender()})
		if err != nil || !strings.Contains(desc, want) {
			t.Fatalf("%s=%q: got %q, %v; want %q", cortexMarginEnv, v, desc, err, want)
		}
	}
}

// L63: a wake with no question ends the robot's listening with CORTEX_ATTEND_END_INTENT; empty sends nothing.
func TestACaptureWithoutAnswerSendsTheEndIntent(t *testing.T) {
	for _, tc := range []struct {
		set   bool
		value string
		want  string
	}{{false, "", "intent_cortex_listening_end"}, {true, "intent_x_end", "intent_x_end"}, {true, "", ""}} {
		t.Setenv(conversationEnv, "on")
		t.Setenv(promptIntentEnv, "") // only the end
		if tc.set {
			t.Setenv(attendEndIntentEnv, tc.value)
		} else {
			unsetEnv(t, attendEndIntentEnv)
		}
		s := newChanSender()
		conv, _, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"),
			brain: stubBrain{}, newVAD: func() cortex.VAD { return loudVAD{} }, sender: s, logf: func(...any) {}})
		if err != nil {
			t.Fatal(err)
		}
		conv.Wake("0dd1b652", 0)
		for ts := uint64(0); ts < 10000; ts += 10 {
			conv.Feed("0dd1b652", cortex.Frame{Samples: make([]int16, 160), Timestamp: ts})
		}
		select {
		case cmd := <-s.got:
			if got := cmd.GetIntentResult().GetIntent(); got != tc.want {
				t.Fatalf("%+v: sent %q, want %q", tc, got, tc.want)
			}
		case <-time.After(time.Second):
			if tc.want != "" {
				t.Fatalf("%+v: nothing sent, want %q", tc, tc.want)
			}
		}
	}
}

// L67: with attend on, the end of a listening goes through the attender's queue, in order with the attends.
func TestTheEndIntentGoesThroughTheAttendQueue(t *testing.T) {
	t.Setenv(conversationEnv, "on")
	t.Setenv(attendIntentEnv, "intent_cortex_listening")
	t.Setenv(attendRefractoryEnv, "")
	unsetEnv(t, attendEndIntentEnv)
	unsetEnv(t, promptIntentEnv)
	s, log := newChanSender(), &lineLog{}
	conv, _, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"),
		brain: stubBrain{}, newVAD: func() cortex.VAD { return loudVAD{} }, sender: s, logf: log.logf})
	if err != nil {
		t.Fatal(err)
	}
	h, err := newWakeHandler(log.logf, s)
	if err != nil {
		t.Fatal(err)
	}
	h = h.withConversation(conv)
	wake := testDetection
	wake.FrameTimestamp = 0
	h.OnWake(wake)
	for ts := uint64(0); ts < 10000; ts += 10 {
		conv.Feed(wake.ESN, cortex.Frame{Samples: make([]int16, 160), Timestamp: ts})
	}
	for _, want := range []string{"intent_cortex_listening", "intent_cortex_listening_prompt", "intent_cortex_listening_end"} {
		if got := (<-s.got).GetIntentResult().GetIntent(); got != want {
			t.Fatalf("sent %q, want %q", got, want)
		}
	}
	log.waitFor(t, "signal sent "+wake.ESN+" intent_cortex_listening_end")
}

// T3g (S6): the no-speech timeline and the longest question are settings, named in the startup line.
func TestTheListeningTimelineSettings(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{}, "at most 8s from the speech start, intent_cortex_listening_prompt after 6s without speech, " +
			"gives up after 9s without speech"},
		{map[string]string{promptAfterEnv: "4s", noSpeechEnv: "7s", captureMaxEnv: "12s", promptIntentEnv: "intent_x"},
			"at most 12s from the speech start, intent_x after 4s without speech, gives up after 7s without speech"},
		{map[string]string{promptIntentEnv: ""}, "at most 8s from the speech start, gives up after 9s without speech"},
		{map[string]string{promptAfterEnv: "0s"}, "at most 8s from the speech start, gives up after 9s without speech"},
	} {
		t.Setenv(conversationEnv, "on")
		for _, k := range []string{promptAfterEnv, noSpeechEnv, captureMaxEnv, promptIntentEnv} {
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

// L80: when a capture ends with speech the robot gets CORTEX_THINKING_INTENT (default intent_cortex_thinking)
// after its attend and before the answer; set but empty sends nothing.
func TestACaptureWithSpeechSendsTheThinkingIntent(t *testing.T) {
	for _, tc := range []struct {
		set   bool
		value string
		want  []string
	}{
		{false, "", []string{"intent_greeting_hello", "intent_cortex_thinking", "intent_clock_time"}},
		{true, "intent_x_thinking", []string{"intent_greeting_hello", "intent_x_thinking", "intent_clock_time"}},
		{true, "", []string{"intent_greeting_hello", "intent_clock_time"}},
	} {
		t.Setenv(conversationEnv, "on")
		t.Setenv(attendIntentEnv, "intent_greeting_hello")
		t.Setenv(attendRefractoryEnv, "")
		t.Setenv(owwMicRateEnv, "16000")
		if tc.set {
			t.Setenv(thinkingIntentEnv, tc.value)
		} else {
			unsetEnv(t, thinkingIntentEnv)
		}
		s := newChanSender()
		conv, desc, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("¿qué hora es?"),
			brain: stubBrain{cortex.Answer{Intent: "intent_clock_time"}}, newVAD: func() cortex.VAD { return loudVAD{} },
			sender: s, logf: (&lineLog{}).logf})
		if err != nil {
			t.Fatal(err)
		}
		if tc.value != "" || !tc.set {
			if !strings.Contains(desc, tc.want[1]+" when the question ends") {
				t.Fatalf("description %q does not name %s", desc, tc.want[1])
			}
		}
		h, err := newWakeHandler(func(...any) {}, s)
		if err != nil {
			t.Fatal(err)
		}
		h = h.withConversation(conv)
		sink := detectorSink(nopSink{}, nopSink{}, conv, nil)
		sink.Start("0dd1b652", mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
		seq, ts := uint32(0), uint64(0)
		feed := func(n int, speech bool) {
			for i := 0; i < n; i++ {
				sink.Frame("0dd1b652", micFrame(seq, ts, speech))
				seq, ts = seq+1, ts+10
			}
		}
		feed(100, false)
		wake := testDetection
		wake.FrameTimestamp = ts - 10
		h.OnWake(wake)
		feed(60, true)
		feed(100, false)
		for i, want := range tc.want {
			select {
			case cmd := <-s.got:
				if got := cmd.GetIntentResult().GetIntent(); got != want {
					t.Fatalf("%+v: command %d is %q, want %q", tc, i, got, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%+v: no command %d (%s)", tc, i, want)
			}
		}
	}
}
