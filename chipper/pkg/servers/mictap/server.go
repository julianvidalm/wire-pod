// Package mictap is the gRPC adapter that receives MicTap streams from robots and hands them to a mictap.Sink.
package mictap

import (
	"context"
	"errors"
	"io"
	"sync/atomic"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ESNMetadataKey carries the robot's ESN in the Stream request metadata.
const ESNMetadataKey = "x-vector-esn"

type Server struct {
	mictappb.UnimplementedMicTapServer
	sink     mictap.Sink
	sessions *Sessions
	status   atomic.Pointer[func(esn string, s *mictappb.AudioStatus)]
}

// OnAudioStatus sets the function that gets the robots' reports on the cortex's audio streams (Chunk.audio_status).
// It is called on the robot's receiving goroutine and must not block.
func (s *Server) OnAudioStatus(f func(esn string, st *mictappb.AudioStatus)) {
	s.status.Store(&f)
}

// audioStatus hands one report to the status function, if any.
func (s *Server) audioStatus(esn string, st *mictappb.AudioStatus) {
	if f := s.status.Load(); f != nil {
		(*f)(esn, st)
	}
}

func New(sink mictap.Sink) *Server {
	return &Server{sink: sink, sessions: newSessions()}
}

// Sessions sends commands to robots that hold an open Session.
func (s *Server) Sessions() *Sessions {
	return s.sessions
}

func esnOf(ctx context.Context) (string, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if esns := md.Get(ESNMetadataKey); len(esns) > 0 && esns[0] != "" {
		return esns[0], nil
	}
	return "", status.Errorf(codes.InvalidArgument, "missing %s metadata", ESNMetadataKey)
}

func (s *Server) consume(r chunkSource, esn string) (mictap.Summary, error) {
	sum, err := mictap.Consume(&chunkReader{stream: r, status: func(st *mictappb.AudioStatus) { s.audioStatus(esn, st) }},
		esn, s.sink)
	if err != nil && status.Code(err) == codes.Unknown {
		err = status.Errorf(codes.InvalidArgument, "mictap stream: %v", err)
	}
	return sum, err // a non-Unknown status means the transport already failed (for example the robot went away)
}

func (s *Server) Stream(stream mictappb.MicTap_StreamServer) error {
	esn, err := esnOf(stream.Context())
	if err != nil {
		return err
	}
	sum, err := s.consume(stream, esn)
	if err != nil {
		return err
	}
	return stream.SendAndClose(&mictappb.StreamSummary{MicFrames: sum.MicFrames, SpeakerFrames: sum.SpeakerFrames})
}

// Session consumes the robot's audio like Stream and, while the call is open, lets the cortex send it
// commands through Sessions().Send.
func (s *Server) Session(stream mictappb.MicTap_SessionServer) error {
	esn, err := esnOf(stream.Context())
	if err != nil {
		return err
	}
	entry := s.sessions.add(esn, stream)
	defer s.sessions.remove(esn, entry)
	_, err = s.consume(stream, esn)
	return err
}

// chunkSource is the receiving side of both Stream and Session.
type chunkSource interface {
	Recv() (*mictappb.Chunk, error)
}

// chunkReader turns the chunk stream back into the robot's byte stream, handing audio stream reports (chunks
// with no data) to status.
type chunkReader struct {
	stream chunkSource
	status func(*mictappb.AudioStatus)
	buf    []byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		chunk, err := r.stream.Recv()
		if errors.Is(err, io.EOF) {
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		if st := chunk.GetAudioStatus(); st != nil && r.status != nil {
			r.status(st)
		}
		r.buf = chunk.GetData()
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
