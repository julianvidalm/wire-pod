package initwirepod

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	pb "github.com/digital-dream-labs/api/go/chipperpb"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vtt"
	"google.golang.org/grpc"
)

// fakeChipperStream is the robot's chipper stream of one voice request: it records every response wire-pod sends.
type fakeChipperStream struct {
	grpc.ServerStream
	mu   sync.Mutex
	sent []*pb.IntentGraphResponse
}

func (s *fakeChipperStream) Send(r *pb.IntentGraphResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, r)
	return nil
}

func (s *fakeChipperStream) Recv() (*pb.StreamingIntentGraphRequest, error) {
	return nil, errors.New("the test stream has no audio")
}

func (s *fakeChipperStream) Context() context.Context { return context.Background() }

func (s *fakeChipperStream) responses() []*pb.IntentGraphResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*pb.IntentGraphResponse(nil), s.sent...)
}

// recBrain answers every transcript with answer (or err) and records what it got.
type recBrain struct {
	mu     sync.Mutex
	got    []cortex.Transcript
	answer cortex.Answer
	err    error
}

func (b *recBrain) Answer(_ context.Context, _ string, t cortex.Transcript) (cortex.Answer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, t)
	return b.answer, b.err
}

func (b *recBrain) transcripts() []cortex.Transcript {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]cortex.Transcript(nil), b.got...)
}

// speechPCM is a second of 16 kHz audio the test VAD hears as speech (about -30 dBFS), or silence.
func speechPCM(speech bool) []int16 {
	pcm := make([]int16, 16000)
	if speech {
		for i := range pcm {
			pcm[i] = 1000
		}
	}
	return pcm
}

type voiceRequestTest struct {
	stream  *fakeChipperStream
	session bigSender
	log     *lineLog
	brain   *recBrain
	handler *voiceRequests
}

// newVoiceRequestTest builds the voice request handler over a conversation whose transcriber writes text and whose
// brain is b; voice, when set, is the cortex's voice (otherwise the robot speaks). The request's audio is pcm.
func newVoiceRequestTest(t *testing.T, text string, b *recBrain, voice func() (cortex.Voice, string, error),
	pcm []int16) *voiceRequestTest {
	t.Helper()
	t.Setenv(conversationEnv, "on")
	t.Setenv(voiceRequestsEnv, "on")
	t.Setenv(thinkingIntentEnv, "")
	t.Setenv(owwMicRateEnv, "16000")
	vt := &voiceRequestTest{stream: &fakeChipperStream{}, session: bigSender{got: make(chan *mictappb.Command, 4096)},
		log: &lineLog{}, brain: b}
	conv, _, err := newConversation(conversationDeps{
		transcriber: stubTranscriberFunc(text),
		brain:       b,
		newVAD:      func() cortex.VAD { return loudVAD{} },
		sender:      vt.session,
		logf:        vt.log.logf,
		voice:       voice,
	})
	if err != nil || conv == nil {
		t.Fatalf("conversation %v, %v", conv, err)
	}
	h, err := newVoiceRequests(conv, vt.log.logf)
	if err != nil || h == nil {
		t.Fatalf("voice requests %v, %v", h, err)
	}
	h.listen = func(any, time.Duration) ([]int16, cortex.EndReason, error) { return pcm, cortex.EndSilence, nil }
	vt.handler = h
	return vt
}

func (vt *voiceRequestTest) ask() {
	vt.handler.Handle(&vtt.IntentGraphRequest{Device: "0dd1b652", Session: "s1", Stream: vt.stream,
		FirstReq: &pb.StreamingIntentGraphRequest{}})
}

// only returns the one chipper response the request got, failing when it got none or more (L6: exactly one).
func (vt *voiceRequestTest) only(t *testing.T) *pb.IntentGraphResponse {
	t.Helper()
	time.Sleep(100 * time.Millisecond) // a second response would come now
	got := vt.stream.responses()
	if len(got) != 1 {
		t.Fatalf("chipper responses %v, want exactly one; log %q", got, vt.log.lines)
	}
	return got[0]
}

// sessionCommands returns the MicTap session commands sent so far, waiting until done says they are complete.
func (vt *voiceRequestTest) sessionCommands(t *testing.T, done func([]*mictappb.Command) bool) []*mictappb.Command {
	t.Helper()
	var got []*mictappb.Command
	deadline := time.After(5 * time.Second)
	for !done(got) {
		select {
		case cmd := <-vt.session.got:
			got = append(got, cmd)
		case <-deadline:
			t.Fatalf("session commands so far: %v", got)
		}
	}
	return got
}

func noSessionCommand(t *testing.T, vt *voiceRequestTest) {
	t.Helper()
	select {
	case cmd := <-vt.session.got:
		t.Fatalf("session command %v, want none", cmd)
	case <-time.After(100 * time.Millisecond):
	}
}

func wantIntent(t *testing.T, r *pb.IntentGraphResponse, intent string) {
	t.Helper()
	if r.GetResponseType() != pb.IntentGraphMode_INTENT || r.GetIntentResult().GetAction() != intent {
		t.Fatalf("response %v, want intent %s", r, intent)
	}
}

// (a) A brain intent is the request's one chipper response, with its parameters; nothing goes over the session.
func TestAVoiceRequestBrainIntentIsOneIntentResponse(t *testing.T) {
	b := &recBrain{answer: cortex.Answer{Intent: "intent_clock_settimer_extend", ParamsJSON: `{"timer_duration":"300"}`}}
	vt := newVoiceRequestTest(t, "pon un temporizador de cinco minutos", b, nil, speechPCM(true))
	vt.ask()
	r := vt.only(t)
	wantIntent(t, r, "intent_clock_settimer_extend")
	if p := r.GetIntentResult().GetParameters(); len(p) != 1 || p["timer_duration"] != "300" {
		t.Fatalf("parameters %v", p)
	}
	if q := r.GetIntentResult().GetQueryText(); q != "pon un temporizador de cinco minutos" {
		t.Fatalf("query %q", q)
	}
	noSessionCommand(t, vt)
	if !vt.log.has("Cortex: 0dd1b652 transcribed (es, ") || !vt.log.has("answer intent intent_clock_settimer_extend") {
		t.Fatalf("log %q", vt.log.lines)
	}
}

// (b) A text answer with the cortex's voice: the one chipper response is the knowledge bypass with external_voice,
// sent first; then the piper audio goes over the session, and no intent or knowledge command does.
func TestAVoiceRequestSpokenAnswerIsOneBypassThenSessionAudio(t *testing.T) {
	b := &recBrain{answer: cortex.Answer{Text: "Estoy muy bien."}}
	vt := newVoiceRequestTest(t, "¿cómo estás?", b, toneVoice, speechPCM(true))
	vt.ask()
	if got := vt.stream.responses(); len(got) != 1 {
		t.Fatalf("the response is not out when the handler returns: %v", got)
	}
	cmds := vt.sessionCommands(t, hasComplete)
	r := vt.only(t)
	wantIntent(t, r, "intent_knowledge_response_extend_bypass")
	p := r.GetIntentResult().GetParameters()
	ms, err := strconv.Atoi(p["expected_duration_ms"])
	if p["external_voice"] != "true" || err != nil || ms <= 0 || p["answer"] != "Estoy muy bien." ||
		p["answer_type"] != "VOICE_COMMAND" || p["query_text"] != "¿cómo estás?" {
		t.Fatalf("parameters %v", p)
	}
	if cmds[0].GetAudio().GetPrepare() == nil {
		t.Fatalf("first session command %v, want the audio prepare", cmds[0])
	}
	for _, c := range cmds {
		if c.GetAudio() == nil {
			t.Fatalf("session command %v, want only audio", c)
		}
	}
}

// Without the cortex's voice a text answer is the knowledge graph response the robot says itself.
func TestAVoiceRequestTextAnswerWithoutVoiceIsTheKnowledgeResponse(t *testing.T) {
	t.Setenv("CORTEX_TTS", "off")
	b := &recBrain{answer: cortex.Answer{Text: "Ottawa."}}
	vt := newVoiceRequestTest(t, "capital of Canada", b, nil, speechPCM(true))
	vt.ask()
	r := vt.only(t)
	if r.GetResponseType() != pb.IntentGraphMode_KNOWLEDGE_GRAPH || r.GetSpokenText() != "Ottawa." ||
		r.GetQueryText() != "capital of Canada" {
		t.Fatalf("response %v", r)
	}
	noSessionCommand(t, vt)
}

// (c) No words, only the wake phrase, a quiet silence phrase or no speech at all: intent_system_noaudio, and the
// brain is not asked.
func TestAVoiceRequestWithNothingSaidIsNoAudio(t *testing.T) {
	for name, c := range map[string]struct {
		text   string
		speech bool
	}{
		"no words":         {"...", true},
		"only wake phrase": {"Hey Vector.", true},
		"no speech":        {"Thank you.", false},
	} {
		b := &recBrain{answer: cortex.Answer{Intent: "intent_clock_time"}}
		vt := newVoiceRequestTest(t, c.text, b, nil, speechPCM(c.speech))
		vt.ask()
		wantIntent(t, vt.only(t), "intent_system_noaudio")
		if len(b.transcripts()) != 0 {
			t.Fatalf("%s: brain asked %v", name, b.transcripts())
		}
		noSessionCommand(t, vt)
	}
}

// (d) A brain error: intent_system_unmatched, once.
func TestAVoiceRequestBrainErrorIsUnmatched(t *testing.T) {
	b := &recBrain{err: errors.New("LLM down")}
	vt := newVoiceRequestTest(t, "cuéntame un chiste", b, nil, speechPCM(true))
	vt.ask()
	wantIntent(t, vt.only(t), "intent_system_unmatched")
	noSessionCommand(t, vt)
	if !vt.log.has("brain failed") {
		t.Fatalf("log %q", vt.log.lines)
	}
}

// (e) "hey vector" is stripped: the brain gets only the question.
func TestAVoiceRequestStripsHeyVector(t *testing.T) {
	b := &recBrain{answer: cortex.Answer{Intent: "intent_clock_time"}}
	vt := newVoiceRequestTest(t, "hey vector, ¿qué hora es?", b, nil, speechPCM(true))
	vt.ask()
	wantIntent(t, vt.only(t), "intent_clock_time")
	if got := b.transcripts(); len(got) != 1 || got[0].Text != "¿Qué hora es?" {
		t.Fatalf("brain got %v", got)
	}
}

// (f) CORTEX_VOICE_REQUESTS unset or off: no handler, so wire-pod answers voice requests exactly as before.
func TestVoiceRequestsOffByDefault(t *testing.T) {
	t.Setenv(conversationEnv, "on")
	t.Setenv(owwMicRateEnv, "16000")
	conv, _, err := newConversation(conversationDeps{transcriber: stubTranscriberFunc("x"), brain: &recBrain{},
		newVAD: func() cortex.VAD { return loudVAD{} }, sender: newChanSender()})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"", "off", "OFF"} {
		t.Setenv(voiceRequestsEnv, v)
		h, err := newVoiceRequests(conv, nil)
		if h != nil || err != nil {
			t.Fatalf("%q: got %v, %v; want no handler", v, h, err)
		}
	}
	t.Setenv(voiceRequestsEnv, "on")
	if h, err := newVoiceRequests(nil, nil); h != nil || err == nil {
		t.Fatalf("on without the conversation: got %v, %v; want an error", h, err)
	}
	t.Setenv(voiceRequestsEnv, "maybe")
	if h, err := newVoiceRequests(conv, nil); h != nil || err == nil {
		t.Fatalf("maybe: got %v, %v; want an error", h, err)
	}
}

// S1: the LLM persona is Vector again: unset CORTEX_PERSONA keeps wire-pod's prompt (its robot is Vector); a name
// still replaces it, and "-" keeps it too.
func TestThePersonaIsVectorByDefault(t *testing.T) {
	for v, want := range map[string]string{"": "", "-": "", "Rex": "Rex"} {
		t.Setenv(cortexPersonaEnv, v)
		if got := cortexPersona(); got != want {
			t.Fatalf("%q: persona %q, want %q", v, got, want)
		}
	}
}

type brokenSynth struct{}

func (brokenSynth) Synthesize(context.Context, string, string) ([]int16, int, error) {
	return nil, 0, errors.New("piper died")
}

// When the cortex's voice cannot start, the one response is the plain knowledge answer the robot says, and
// nothing goes over the session.
func TestAVoiceRequestWhoseVoiceCannotStartIsTheRobotsKnowledgeAnswer(t *testing.T) {
	broken := func() (cortex.Voice, string, error) {
		return cortex.NewSentenceVoice(brokenSynth{}, -20), "broken", nil
	}
	b := &recBrain{answer: cortex.Answer{Text: "Estoy muy bien."}}
	vt := newVoiceRequestTest(t, "¿cómo estás?", b, broken, speechPCM(true))
	vt.ask()
	r := vt.only(t)
	if r.GetResponseType() != pb.IntentGraphMode_KNOWLEDGE_GRAPH || r.GetSpokenText() != "Estoy muy bien." {
		t.Fatalf("response %v", r)
	}
	noSessionCommand(t, vt)
}
