package processreqs

import (
	"context"
	"errors"
	"sync"
	"testing"

	pb "github.com/digital-dream-labs/api/go/chipperpb"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vtt"
	sr "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/speechrequest"
	"google.golang.org/grpc"
)

type graphStream struct {
	grpc.ServerStream
	mu   sync.Mutex
	sent []*pb.IntentGraphResponse
}

func (s *graphStream) Send(r *pb.IntentGraphResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, r)
	return nil
}

func (s *graphStream) Recv() (*pb.StreamingIntentGraphRequest, error) {
	return nil, errors.New("no audio")
}

func (s *graphStream) Context() context.Context { return context.Background() }

type intentStream struct {
	grpc.ServerStream
	sent []*pb.IntentResponse
}

func (s *intentStream) Send(r *pb.IntentResponse) error { s.sent = append(s.sent, r); return nil }

func (s *intentStream) Recv() (*pb.StreamingIntentRequest, error) { return nil, errors.New("no audio") }

func (s *intentStream) Context() context.Context { return context.Background() }

// stockSTT stands in for wire-pod's STT and counts its calls; it hears nothing.
func stockSTT(t *testing.T) *int {
	t.Helper()
	calls := 0
	prevSTT, prevSti, prevHook := sttHandler, isSti, VoiceRequest
	t.Cleanup(func() { sttHandler, isSti, VoiceRequest = prevSTT, prevSti, prevHook })
	sttHandler = func(sr.SpeechRequest) (string, error) { calls++; return "", nil }
	isSti = false
	return &calls
}

// (f) Without the cortex hook (CORTEX_VOICE_REQUESTS unset) both voice request paths run wire-pod's own STT and
// answer as before: nothing heard is intent_system_noaudio.
func TestVoiceRequestsWithoutTheCortexHookRunTheStockPath(t *testing.T) {
	calls := stockSTT(t)
	VoiceRequest = nil
	gs := &graphStream{}
	if _, err := (&Server{}).ProcessIntentGraph(&vtt.IntentGraphRequest{Device: "esn", Stream: gs,
		FirstReq: &pb.StreamingIntentGraphRequest{}}); err != nil {
		t.Fatal(err)
	}
	is := &intentStream{}
	if _, err := (&Server{}).ProcessIntent(&vtt.IntentRequest{Device: "esn", Stream: is,
		FirstReq: &pb.StreamingIntentRequest{}}); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || len(gs.sent) != 1 || gs.sent[0].GetIntentResult().GetAction() != "intent_system_noaudio" ||
		len(is.sent) != 1 || is.sent[0].GetIntentResult().GetAction() != "intent_system_noaudio" {
		t.Fatalf("stt calls %d, graph %v, intent %v", *calls, gs.sent, is.sent)
	}
}

// With the hook set, it answers both voice request paths and wire-pod's STT never runs.
func TestVoiceRequestsWithTheCortexHookGoToIt(t *testing.T) {
	calls := stockSTT(t)
	var got []any
	VoiceRequest = func(req any) { got = append(got, req) }
	gr := &vtt.IntentGraphRequest{Device: "esn", Stream: &graphStream{}, FirstReq: &pb.StreamingIntentGraphRequest{}}
	ir := &vtt.IntentRequest{Device: "esn", Stream: &intentStream{}, FirstReq: &pb.StreamingIntentRequest{}}
	(&Server{}).ProcessIntentGraph(gr)
	(&Server{}).ProcessIntent(ir)
	if *calls != 0 || len(got) != 2 || got[0] != any(gr) || got[1] != any(ir) {
		t.Fatalf("stt calls %d, hook got %v", *calls, got)
	}
}
