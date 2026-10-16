package mictap

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestReadHello(t *testing.T) {
	h, err := NewReader(bytes.NewReader(EncodeHello(1, 16000, 32000))).ReadHello()
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 1 || h.MicRate != 16000 || h.SpeakerRate != 32000 {
		t.Fatalf("got %+v", h)
	}
}

func TestReadHelloRejectsBadMagicAndVersion(t *testing.T) {
	bad := EncodeHello(1, 16000, 32000)
	copy(bad, "XXXX")
	if _, err := NewReader(bytes.NewReader(bad)).ReadHello(); err == nil {
		t.Fatal("bad magic accepted")
	}
	if _, err := NewReader(bytes.NewReader(EncodeHello(2, 16000, 32000))).ReadHello(); err == nil {
		t.Fatal("version 2 accepted")
	}
}

func TestReadFramesDecodesSamplesAndStats(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(EncodeFrame(Frame{Type: FrameMic, Flags: MicFlagSpeakerActive | MicFlagRobotMoving, Seq: 7, Timestamp: 1000, Payload: pcm(1, -2, 32767)}))
	stats := make([]byte, 8)
	binary.LittleEndian.PutUint32(stats, 3)
	binary.LittleEndian.PutUint32(stats[4:], 4)
	stream.Write(EncodeFrame(Frame{Type: FrameStats, Timestamp: 1005, Payload: stats}))

	r := NewReader(&stream)
	mic, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if mic.Type != FrameMic || mic.Seq != 7 || mic.Timestamp != 1000 || mic.Flags != 3 {
		t.Fatalf("mic header %+v", mic)
	}
	if got := mic.Samples(); len(got) != 3 || got[0] != 1 || got[1] != -2 || got[2] != 32767 {
		t.Fatalf("samples %v", got)
	}
	st, err := r.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if m, s, err := st.Stats(); err != nil || m != 3 || s != 4 {
		t.Fatalf("stats %d %d %v", m, s, err)
	}
	if _, err := r.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF after last frame, got %v", err)
	}
}

func TestReadFrameRejectsHugePayload(t *testing.T) {
	hdr := EncodeFrame(Frame{Type: FrameMic})
	binary.LittleEndian.PutUint32(hdr[16:], maxPayloadBytes+1)
	if _, err := NewReader(bytes.NewReader(hdr)).ReadFrame(); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func pcm(samples ...int16) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}
