package mictap

import (
	"errors"
	"io"
)

// Sink receives one robot's MicTap stream: Start once after a valid hello, Frame for every frame in order, and
// End once when the stream stops (err is nil for a clean end).
type Sink interface {
	Start(esn string, h Hello)
	Frame(esn string, f Frame)
	End(esn string, err error)
}

type Summary struct {
	MicFrames     uint64
	SpeakerFrames uint64
}

// Consume reads one stream (a hello, then frames until EOF) and hands it to sink. A bad hello is returned
// without touching the sink; any later read error is returned and also passed to End.
func Consume(r io.Reader, esn string, sink Sink) (Summary, error) {
	var sum Summary
	reader := NewReader(r)
	hello, err := reader.ReadHello()
	if err != nil {
		return sum, err
	}
	sink.Start(esn, hello)
	for {
		f, err := reader.ReadFrame()
		if errors.Is(err, io.EOF) {
			sink.End(esn, nil)
			return sum, nil
		}
		if err != nil {
			sink.End(esn, err)
			return sum, err
		}
		switch f.Type {
		case FrameMic:
			sum.MicFrames++
		case FrameSpeaker:
			sum.SpeakerFrames++
		}
		sink.Frame(esn, f)
	}
}
