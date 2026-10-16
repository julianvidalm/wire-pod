package mictap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap/mictappb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
)

func dialServer(t *testing.T, srv *Server) mictappb.MicTapClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	mictappb.RegisterMicTapServer(g, srv)
	go g.Serve(lis)
	t.Cleanup(g.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return mictappb.NewMicTapClient(conn)
}

func clockTime() *mictappb.Command {
	return &mictappb.Command{Command: &mictappb.Command_IntentResult{IntentResult: &mictappb.IntentResult{Intent: "intent_clock_time"}}}
}

func TestSessionDeliversAudioAndCommands(t *testing.T) {
	sink := &recordingSink{}
	srv := New(sink)
	c := dialServer(t, srv)
	ctx, cancel := context.WithCancel(metadata.AppendToOutgoingContext(context.Background(), ESNMetadataKey, "0dd1b652"))
	defer cancel()
	session, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data := stream()
	for i := 0; i < len(data); i += 64 {
		if err := session.Send(&mictappb.Chunk{Data: data[i:min(i+64, len(data))]}); err != nil {
			t.Fatal(err)
		}
	}
	// The robot's session is registered once its hello arrived; the cortex can now answer it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := srv.Sessions().Send("0dd1b652", clockTime()); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("no session for the robot: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cmd, err := session.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if got := cmd.GetIntentResult().GetIntent(); got != "intent_clock_time" {
		t.Fatalf("command %v", cmd)
	}
	if err := session.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Recv(); err == nil {
		t.Fatal("session still open after the robot closed it")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.esn != "0dd1b652" || sink.frames != 6 || !sink.ended {
		t.Fatalf("sink %+v", sink)
	}
}

func TestSendWithoutASessionFails(t *testing.T) {
	srv := New(&recordingSink{})
	if err := srv.Sessions().Send("nobody", clockTime()); !errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v, want ErrNoSession", err)
	}
}

func TestSessionRequiresESN(t *testing.T) {
	c := dialServer(t, New(&recordingSink{}))
	session, err := c.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session.Send(&mictappb.Chunk{Data: stream()})
	if _, err := session.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument without ESN, got %v", err)
	}
}

func TestSessionIsGoneAfterTheRobotDisconnects(t *testing.T) {
	srv := New(&recordingSink{})
	c := dialServer(t, srv)
	ctx := metadata.AppendToOutgoingContext(context.Background(), ESNMetadataKey, "0dd1b652")
	session, _ := c.Session(ctx)
	session.Send(&mictappb.Chunk{Data: stream()})
	session.CloseSend()
	session.Recv() // wait for the server to finish the call
	deadline := time.Now().Add(2 * time.Second)
	for !errors.Is(srv.Sessions().Send("0dd1b652", clockTime()), ErrNoSession) {
		if time.Now().After(deadline) {
			t.Fatal("session still registered after the robot disconnected")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
