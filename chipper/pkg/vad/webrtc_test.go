package vad

import (
	"math"
	"testing"
)

// voiced is a vowel-like 10 ms frame: harmonics of a 140 Hz voice with falling amplitude.
func voiced(frame int) []int16 {
	s := make([]int16, 160)
	for i := range s {
		t := float64(frame*160+i) / 16000
		var v float64
		for h := 1; h <= 20; h++ {
			v += math.Sin(2*math.Pi*140*float64(h)*t) / float64(h)
		}
		s[i] = int16(6000 * v)
	}
	return s
}

func TestWebRTCHearsAVoiceAndNotSilence(t *testing.T) {
	v, err := NewWebRTC(3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if v.IsSpeech(make([]int16, 160)) {
			t.Fatalf("silence frame %d counted as speech", i)
		}
	}
	speech := 0
	for i := 0; i < 50; i++ {
		if v.IsSpeech(voiced(i)) {
			speech++
		}
	}
	if speech < 40 {
		t.Fatalf("only %d of 50 voiced frames counted as speech", speech)
	}
}

func TestWebRTCSplitsLongFramesAndIgnoresShortOnes(t *testing.T) {
	v, err := NewWebRTC(3)
	if err != nil {
		t.Fatal(err)
	}
	if v.IsSpeech(make([]int16, 100)) {
		t.Fatal("a frame shorter than 10 ms counted as speech")
	}
	long := append(append([]int16{}, voiced(0)...), voiced(1)...)
	for i := 0; i < 20; i++ {
		v.IsSpeech(long)
	}
	if !v.IsSpeech(long) {
		t.Fatal("a 20 ms voiced frame was not speech")
	}
}

func TestNewWebRTCRejectsAnUnknownMode(t *testing.T) {
	if _, err := NewWebRTC(7); err == nil {
		t.Fatal("mode 7 accepted")
	}
}
