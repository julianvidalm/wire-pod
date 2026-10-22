package mictap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
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

// T8b: the robot reports on the cortex's audio streams in chunks with no data; each report reaches the status
// handler with the robot's ESN, and the audio around it is still consumed whole.
func TestSessionRoutesAudioStatus(t *testing.T) {
	sink := &recordingSink{}
	srv := New(sink)
	type report struct {
		esn   string
		id    uint32
		state mictappb.AudioStatus_State
	}
	got := make(chan report, 4)
	srv.OnAudioStatus(func(esn string, s *mictappb.AudioStatus) { got <- report{esn, s.GetStreamId(), s.GetState()} })
	c := dialServer(t, srv)
	ctx := metadata.AppendToOutgoingContext(context.Background(), ESNMetadataKey, "0dd1b652")
	session, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data := stream()
	session.Send(&mictappb.Chunk{Data: data[:50]})
	session.Send(&mictappb.Chunk{AudioStatus: &mictappb.AudioStatus{StreamId: 7, State: mictappb.AudioStatus_STARTED}})
	session.Send(&mictappb.Chunk{Data: data[50:]})
	session.Send(&mictappb.Chunk{AudioStatus: &mictappb.AudioStatus{StreamId: 7, State: mictappb.AudioStatus_COMPLETED}})
	session.CloseSend()
	session.Recv()
	for _, want := range []report{{"0dd1b652", 7, mictappb.AudioStatus_STARTED}, {"0dd1b652", 7, mictappb.AudioStatus_COMPLETED}} {
		select {
		case r := <-got:
			if r != want {
				t.Fatalf("report %+v, want %+v", r, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no report %+v", want)
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.frames != 6 || !sink.ended {
		t.Fatalf("sink %+v", sink)
	}
}

// openSession opens a robot's Session and forwards every command it receives to the returned channel, which
// closes when the call ends.
func openSession(t *testing.T, c mictappb.MicTapClient, esn string) (mictappb.MicTap_SessionClient, <-chan *mictappb.Command) {
	t.Helper()
	ctx, cancel := context.WithCancel(metadata.AppendToOutgoingContext(context.Background(), ESNMetadataKey, esn))
	t.Cleanup(cancel)
	session, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *mictappb.Command, 64)
	go func() {
		defer close(got)
		for {
			cmd, err := session.Recv()
			if err != nil {
				return
			}
			got <- cmd
		}
	}()
	return session, got
}

// sendUntil sends clockTime to the robot until a command arrives on got, and fails after two seconds.
func sendUntil(t *testing.T, srv *Server, esn string, got <-chan *mictappb.Command) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		srv.Sessions().Send(esn, clockTime())
		select {
		case cmd, ok := <-got:
			if !ok {
				t.Fatal("the session ended")
			}
			if cmd.GetIntentResult().GetIntent() != "intent_clock_time" {
				t.Fatalf("command %v", cmd)
			}
			return
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("no command reached the session")
		}
	}
}

// robot-wake T1 (L5 advisory 5): the robot's command link opens a Session and never sends a hello nor any mic
// audio. It stays registered, commands reach it, and its audio status reports (chunks with no data) still reach
// the status callback; the sink never starts.
func TestASessionWithoutHelloStaysRegisteredAndCarriesCommandsAndStatus(t *testing.T) {
	sink := &recordingSink{}
	srv := New(sink)
	reports := make(chan *mictappb.AudioStatus, 4)
	srv.OnAudioStatus(func(esn string, s *mictappb.AudioStatus) {
		if esn == "0dd1b652" {
			reports <- s
		}
	})
	session, got := openSession(t, dialServer(t, srv), "0dd1b652")
	sendUntil(t, srv, "0dd1b652", got)

	time.Sleep(100 * time.Millisecond) // a quiet link stays open
	if err := srv.Sessions().Send("0dd1b652", clockTime()); err != nil {
		t.Fatalf("the hello-less session is no longer registered: %v", err)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the second command did not reach the session")
	}

	for _, st := range []mictappb.AudioStatus_State{mictappb.AudioStatus_STARTED, mictappb.AudioStatus_COMPLETED} {
		if err := session.Send(&mictappb.Chunk{AudioStatus: &mictappb.AudioStatus{StreamId: 9, State: st}}); err != nil {
			t.Fatal(err)
		}
		select {
		case r := <-reports:
			if r.GetStreamId() != 9 || r.GetState() != st {
				t.Fatalf("report %v, want stream 9 %v", r, st)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no %v report", st)
		}
	}
	sink.mu.Lock()
	if sink.esn != "" || sink.frames != 0 {
		t.Fatalf("the sink started without a hello: %+v", sink)
	}
	sink.mu.Unlock()
	session.CloseSend()
	for range got { // the call ends
	}
	deadline := time.Now().Add(2 * time.Second)
	for !errors.Is(srv.Sessions().Send("0dd1b652", clockTime()), ErrNoSession) {
		if time.Now().After(deadline) {
			t.Fatal("session still registered after the robot closed it")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// robot-wake T1 (L5 advisory 5): a robot that reconnects opens a new Session before the old call ends. The newer
// session replaces the older one, and the old call ending removes only its own entry: commands keep reaching the
// newer session.
func TestASameESNReconnectRemovesOnlyItsOwnEntry(t *testing.T) {
	srv := New(&recordingSink{})
	c := dialServer(t, srv)
	older, olderGot := openSession(t, c, "0dd1b652")
	sendUntil(t, srv, "0dd1b652", olderGot)
	_, newerGot := openSession(t, c, "0dd1b652")
	sendUntil(t, srv, "0dd1b652", newerGot)

	older.CloseSend()
	for range olderGot { // the older call ends and removes its entry
	}
	sendUntil(t, srv, "0dd1b652", newerGot)
	time.Sleep(50 * time.Millisecond) // nothing removes the newer entry later
	if err := srv.Sessions().Send("0dd1b652", clockTime()); err != nil {
		t.Fatalf("the newer session was removed with the older one: %v", err)
	}
}
