package mictap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap"
	"github.com/kercre123/wire-pod/chipper/pkg/mictap/mictappb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type recordingSink struct {
	mu     sync.Mutex
	esn    string
	frames int
	ended  bool
}

func (s *recordingSink) Start(esn string, _ mictap.Hello) { s.mu.Lock(); s.esn = esn; s.mu.Unlock() }
func (s *recordingSink) Frame(string, mictap.Frame)       { s.mu.Lock(); s.frames++; s.mu.Unlock() }
func (s *recordingSink) End(string, error)                { s.mu.Lock(); s.ended = true; s.mu.Unlock() }

func dial(t *testing.T, sink mictap.Sink) mictappb.MicTapClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	mictappb.RegisterMicTapServer(srv, New(sink))
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return mictappb.NewMicTapClient(conn)
}

func stream() []byte {
	var b bytes.Buffer
	b.Write(mictap.EncodeHello(1, 16000, 32000))
	for seq := uint32(0); seq < 5; seq++ {
		b.Write(mictap.EncodeFrame(mictap.Frame{Type: mictap.FrameMic, Seq: seq, Timestamp: uint64(seq) * 10, Payload: make([]byte, 320)}))
	}
	b.Write(mictap.EncodeFrame(mictap.Frame{Type: mictap.FrameSpeaker, Payload: make([]byte, 2048)}))
	return b.Bytes()
}

// send splits data into chunks of n bytes, so frames and the hello straddle chunk boundaries.
func send(t *testing.T, c mictappb.MicTapClient, ctx context.Context, data []byte, n int) (*mictappb.StreamSummary, error) {
	t.Helper()
	s, err := c.Stream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(data); i += n {
		end := min(i+n, len(data))
		// io.EOF means the server already ended the stream; its status comes from CloseAndRecv.
		if err := s.Send(&mictappb.Chunk{Data: data[i:end]}); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	return s.CloseAndRecv()
}

func TestStreamDecodesChunksAcrossBoundaries(t *testing.T) {
	sink := &recordingSink{}
	c := dial(t, sink)
	ctx := metadata.AppendToOutgoingContext(context.Background(), ESNMetadataKey, "0dd1b652")
	sum, err := send(t, c, ctx, stream(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if sum.MicFrames != 5 || sum.SpeakerFrames != 1 {
		t.Fatalf("summary %+v", sum)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.esn != "0dd1b652" || sink.frames != 6 || !sink.ended {
		t.Fatalf("sink %+v", sink)
	}
}

func TestStreamRequiresESN(t *testing.T) {
	c := dial(t, &recordingSink{})
	_, err := send(t, c, context.Background(), stream(), 64)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument without ESN, got %v", err)
	}
}

func TestStreamRejectsBadHello(t *testing.T) {
	sink := &recordingSink{}
	c := dial(t, sink)
	bad := stream()
	copy(bad, "XXXX")
	ctx := metadata.AppendToOutgoingContext(context.Background(), ESNMetadataKey, "0dd1b652")
	if _, err := send(t, c, ctx, bad, 64); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for a bad hello, got %v", err)
	}
	if sink.frames != 0 {
		t.Fatalf("frames delivered from a rejected stream: %d", sink.frames)
	}
}
