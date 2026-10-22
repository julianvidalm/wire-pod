// Package vad holds the cortex's voice activity detectors (adapters of the cortex.VAD port).
package vad

import (
	"encoding/binary"
	"fmt"

	"github.com/maxhawkins/go-webrtcvad"
)

const (
	rate        = 16000 // the rate MicTap announces; Vector's true ~15662 Hz is close enough for the detector
	chunkLength = 160   // 10 ms at rate, a frame length the detector accepts
)

// WebRTC is the VAD of libwebrtc (github.com/maxhawkins/go-webrtcvad, which wire-pod's speech requests already
// use). It keeps state between frames, so each robot stream needs its own.
type WebRTC struct {
	v   *webrtcvad.VAD
	buf []byte
}

// NewWebRTC makes a detector with aggressiveness mode 0 (least) to 3 (most likely to call noise non-speech).
func NewWebRTC(mode int) (*WebRTC, error) {
	if mode < 0 || mode > 3 {
		return nil, fmt.Errorf("webrtc vad mode %d is outside 0-3", mode)
	}
	v, err := webrtcvad.New()
	if err != nil {
		return nil, err
	}
	if err := v.SetMode(mode); err != nil {
		return nil, err
	}
	return &WebRTC{v: v, buf: make([]byte, 2*chunkLength)}, nil
}

// IsSpeech says whether any 10 ms chunk of samples holds speech; a trailing part shorter than 10 ms is ignored.
func (w *WebRTC) IsSpeech(samples []int16) bool {
	speech := false
	for len(samples) >= chunkLength {
		for i, s := range samples[:chunkLength] {
			binary.LittleEndian.PutUint16(w.buf[2*i:], uint16(s))
		}
		if active, err := w.v.Process(rate, w.buf); err == nil && active {
			speech = true
		}
		samples = samples[chunkLength:]
	}
	return speech
}
