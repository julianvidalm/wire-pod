// Package mictap decodes MicTap, the stream of what Vector hears and plays, sent by the robot to the cortex.
//
// Wire format v1 (little-endian), produced by animProcess/src/cozmoAnim/micData/micTap.cpp in
// julianvidalm/wire-os-victor:
//
//	hello : "VMT1" | u16 version | u32 mic rate | u32 speaker rate | u16 reserved
//	frame : u8 type | u8 flags | u16 reserved | u32 seq | u64 timestamp_ms | u32 payload bytes | payload
//
// Mic frames carry 160 s16 samples at 16 kHz (flags: bit0 speaker active, bit1 robot moving), speaker frames
// 1024 s16 samples at 32 kHz (flags: bit0 buffer has audio), stats frames the robot's dropped-frame totals.
// Raw mic frames (type 4, sent only when the robot's raw-mic switch is on) carry the four microphones before
// SignalEssence: 160 s16 samples per channel, interleaved ch0 ch1 ch2 ch3 ch0 ..., 1280 bytes, at the mic rate.
// Mic and speaker timestamps share one clock; seq counts per type and skips when frames are dropped.
// Consumers ignore frame types they do not know.
package mictap

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	FrameMic     = 1
	FrameSpeaker = 2
	FrameStats   = 3
	FrameRawMic  = 4

	RawMicChannels   = 4
	RawMicFrameBytes = 160 * RawMicChannels * 2

	MicFlagSpeakerActive = 1 << 0
	MicFlagRobotMoving   = 1 << 1
	SpeakerFlagHasData   = 1 << 0

	helloSize       = 16
	frameHeaderSize = 20
	// Largest payload the robot sends is one speaker buffer (1024 samples); reject anything absurd.
	maxPayloadBytes = 64 * 1024
)

type Hello struct {
	Version     uint16
	MicRate     uint32
	SpeakerRate uint32
}

type Frame struct {
	Type      uint8
	Flags     uint8
	Seq       uint32
	Timestamp uint64 // ms, same clock for mic and speaker frames
	Payload   []byte
}

// Samples decodes a mic or speaker payload as signed 16-bit PCM.
func (f Frame) Samples() []int16 {
	out := make([]int16, len(f.Payload)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(f.Payload[2*i:]))
	}
	return out
}

// Stats decodes a stats payload: total mic and speaker frames dropped on the robot.
func (f Frame) Stats() (micDropped, speakerDropped uint32, err error) {
	if f.Type != FrameStats || len(f.Payload) != 8 {
		return 0, 0, fmt.Errorf("not a stats frame (type %d, %d bytes)", f.Type, len(f.Payload))
	}
	return binary.LittleEndian.Uint32(f.Payload), binary.LittleEndian.Uint32(f.Payload[4:]), nil
}

// EncodeHello and EncodeFrame produce the robot's byte layout; used by tests and simulators.
func EncodeHello(version uint16, micRate, speakerRate uint32) []byte {
	b := make([]byte, helloSize)
	copy(b, "VMT1")
	binary.LittleEndian.PutUint16(b[4:], version)
	binary.LittleEndian.PutUint32(b[6:], micRate)
	binary.LittleEndian.PutUint32(b[10:], speakerRate)
	return b
}

func EncodeFrame(f Frame) []byte {
	b := make([]byte, frameHeaderSize+len(f.Payload))
	b[0], b[1] = f.Type, f.Flags
	binary.LittleEndian.PutUint32(b[4:], f.Seq)
	binary.LittleEndian.PutUint64(b[8:], f.Timestamp)
	binary.LittleEndian.PutUint32(b[16:], uint32(len(f.Payload)))
	copy(b[frameHeaderSize:], f.Payload)
	return b
}

type Reader struct {
	r *bufio.Reader
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 64*1024)}
}

func (r *Reader) ReadHello() (Hello, error) {
	var buf [helloSize]byte
	if _, err := io.ReadFull(r.r, buf[:]); err != nil {
		return Hello{}, fmt.Errorf("reading hello: %w", err)
	}
	if string(buf[:4]) != "VMT1" {
		return Hello{}, fmt.Errorf("bad magic %q", buf[:4])
	}
	h := Hello{
		Version:     binary.LittleEndian.Uint16(buf[4:]),
		MicRate:     binary.LittleEndian.Uint32(buf[6:]),
		SpeakerRate: binary.LittleEndian.Uint32(buf[10:]),
	}
	if h.Version != 1 {
		return h, fmt.Errorf("unsupported protocol version %d", h.Version)
	}
	return h, nil
}

var ErrPayloadTooLarge = errors.New("frame payload too large")

// ReadFrame returns io.EOF only at a clean frame boundary; a stream cut inside a frame is an error.
func (r *Reader) ReadFrame() (Frame, error) {
	var hdr [frameHeaderSize]byte
	if _, err := io.ReadFull(r.r, hdr[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Frame{}, fmt.Errorf("reading frame header: %w", err)
		}
		return Frame{}, err
	}
	f := Frame{
		Type:      hdr[0],
		Flags:     hdr[1],
		Seq:       binary.LittleEndian.Uint32(hdr[4:]),
		Timestamp: binary.LittleEndian.Uint64(hdr[8:]),
	}
	n := binary.LittleEndian.Uint32(hdr[16:])
	if n > maxPayloadBytes {
		return Frame{}, fmt.Errorf("%w: %d bytes", ErrPayloadTooLarge, n)
	}
	f.Payload = make([]byte, n)
	if _, err := io.ReadFull(r.r, f.Payload); err != nil {
		return Frame{}, fmt.Errorf("reading payload: %w", err)
	}
	return f, nil
}
