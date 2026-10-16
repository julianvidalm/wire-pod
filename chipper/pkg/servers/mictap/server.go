// Package mictap is the gRPC adapter that receives MicTap streams from robots and hands them to a mictap.Sink.
package mictap

import (
	"errors"
	"io"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap"
	"github.com/kercre123/wire-pod/chipper/pkg/mictap/mictappb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ESNMetadataKey carries the robot's ESN in the Stream request metadata.
const ESNMetadataKey = "x-vector-esn"

type Server struct {
	mictappb.UnimplementedMicTapServer
	sink mictap.Sink
}

func New(sink mictap.Sink) *Server {
	return &Server{sink: sink}
}

func (s *Server) Stream(stream mictappb.MicTap_StreamServer) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	esns := md.Get(ESNMetadataKey)
	if len(esns) == 0 || esns[0] == "" {
		return status.Errorf(codes.InvalidArgument, "missing %s metadata", ESNMetadataKey)
	}
	sum, err := mictap.Consume(&chunkReader{stream: stream}, esns[0], s.sink)
	if err != nil {
		if status.Code(err) != codes.Unknown {
			return err // the transport already failed (for example the robot went away)
		}
		return status.Errorf(codes.InvalidArgument, "mictap stream: %v", err)
	}
	return stream.SendAndClose(&mictappb.StreamSummary{MicFrames: sum.MicFrames, SpeakerFrames: sum.SpeakerFrames})
}

// chunkReader turns the chunk stream back into the robot's byte stream.
type chunkReader struct {
	stream mictappb.MicTap_StreamServer
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
		r.buf = chunk.GetData()
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
