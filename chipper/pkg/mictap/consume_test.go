package mictap

import (
	"bytes"
	"errors"
	"testing"
)

// recordingSink keeps everything a stream delivered.
type recordingSink struct {
	esn    string
	hello  Hello
	frames []Frame
	ended  bool
	endErr error
}

func (s *recordingSink) Start(esn string, h Hello) { s.esn, s.hello = esn, h }
func (s *recordingSink) Frame(esn string, f Frame) { s.frames = append(s.frames, f) }
func (s *recordingSink) End(esn string, err error) { s.ended, s.endErr = true, err }

func sampleStream() []byte {
	var b bytes.Buffer
	b.Write(EncodeHello(1, 16000, 32000))
	b.Write(EncodeFrame(Frame{Type: FrameMic, Seq: 0, Timestamp: 10, Payload: pcm(1, 2)}))
	b.Write(EncodeFrame(Frame{Type: FrameSpeaker, Seq: 0, Timestamp: 11, Flags: SpeakerFlagHasData, Payload: pcm(3)}))
	b.Write(EncodeFrame(Frame{Type: FrameMic, Seq: 1, Timestamp: 20, Payload: pcm(4, 5)}))
	return b.Bytes()
}

func TestConsumeDeliversHelloFramesAndEnd(t *testing.T) {
	sink := &recordingSink{}
	sum, err := Consume(bytes.NewReader(sampleStream()), "0dd1b652", sink)
	if err != nil {
		t.Fatal(err)
	}
	if sink.esn != "0dd1b652" || sink.hello.MicRate != 16000 {
		t.Fatalf("start: esn %q hello %+v", sink.esn, sink.hello)
	}
	if len(sink.frames) != 3 || sink.frames[0].Type != FrameMic || sink.frames[1].Type != FrameSpeaker || sink.frames[2].Seq != 1 {
		t.Fatalf("frames %+v", sink.frames)
	}
	if !sink.ended || sink.endErr != nil {
		t.Fatalf("end: %v %v", sink.ended, sink.endErr)
	}
	if sum.MicFrames != 2 || sum.SpeakerFrames != 1 {
		t.Fatalf("summary %+v", sum)
	}
}

func TestConsumeReportsBadHelloWithoutStarting(t *testing.T) {
	bad := sampleStream()
	copy(bad, "XXXX")
	sink := &recordingSink{}
	if _, err := Consume(bytes.NewReader(bad), "esn", sink); err == nil {
		t.Fatal("bad hello accepted")
	}
	if sink.esn != "" || len(sink.frames) != 0 || sink.ended {
		t.Fatalf("sink touched on a rejected stream: %+v", sink)
	}
}

func TestConsumeEndsWithErrorOnTruncatedFrame(t *testing.T) {
	stream := sampleStream()
	sink := &recordingSink{}
	_, err := Consume(bytes.NewReader(stream[:len(stream)-1]), "esn", sink)
	if err == nil || !errors.Is(sink.endErr, err) {
		t.Fatalf("want the read error at End, got err %v endErr %v", err, sink.endErr)
	}
	if len(sink.frames) != 2 {
		t.Fatalf("complete frames before the cut: %d", len(sink.frames))
	}
}

func TestConsumePassesRawMicAndUnknownFramesWithoutCountingThemAsMic(t *testing.T) {
	var b bytes.Buffer
	b.Write(EncodeHello(1, 16000, 32000))
	b.Write(EncodeFrame(Frame{Type: FrameMic, Seq: 0, Timestamp: 10, Payload: pcm(1, 2)}))
	b.Write(EncodeFrame(Frame{Type: FrameRawMic, Seq: 0, Timestamp: 10, Payload: make([]byte, RawMicFrameBytes)}))
	b.Write(EncodeFrame(Frame{Type: 9, Seq: 0, Timestamp: 10, Payload: []byte{1, 2, 3}}))
	b.Write(EncodeFrame(Frame{Type: FrameMic, Seq: 1, Timestamp: 20, Payload: pcm(4, 5)}))
	sink := &recordingSink{}
	sum, err := Consume(bytes.NewReader(b.Bytes()), "esn", sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.frames) != 4 || sink.frames[1].Type != FrameRawMic || len(sink.frames[1].Payload) != 1280 ||
		sink.frames[2].Type != 9 || sink.frames[3].Seq != 1 {
		t.Fatalf("frames %+v", sink.frames)
	}
	if sum.MicFrames != 2 || sum.SpeakerFrames != 0 {
		t.Fatalf("summary %+v", sum)
	}
}
