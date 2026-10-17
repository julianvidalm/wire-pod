package mictap

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readWAV(t *testing.T, path string) (rate uint32, samples []int16) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	if len(b) < 44 || string(b[:4]) != "RIFF" || string(b[36:40]) != "data" ||
		le.Uint32(b[4:]) != uint32(len(b)-8) || le.Uint32(b[40:]) != uint32(len(b)-44) {
		t.Fatalf("%s: bad header % x", path, b[:min(len(b), 44)])
	}
	for i := 44; i+1 < len(b); i += 2 {
		samples = append(samples, int16(le.Uint16(b[i:])))
	}
	return le.Uint32(b[24:]), samples
}

func pcmFrame(typ uint8, seq uint32, values ...int16) Frame {
	payload := make([]byte, 2*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint16(payload[2*i:], uint16(v))
	}
	return Frame{Type: typ, Seq: seq, Payload: payload}
}

func TestRecordSinkWritesMicAndSpeakerWAVsPerSession(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", pcmFrame(FrameMic, 0, 1, 2))
	r.Frame("esn1", pcmFrame(FrameSpeaker, 0, 9))
	r.Frame("esn1", pcmFrame(FrameMic, 1, -3))
	r.Frame("esn1", Frame{Type: FrameStats, Payload: make([]byte, 8)})
	r.End("esn1", nil)

	rate, mic := readWAV(t, filepath.Join(dir, "esn1_20261016-170000_mic.wav"))
	if rate != 16000 || len(mic) != 3 || mic[0] != 1 || mic[2] != -3 {
		t.Fatalf("mic %d Hz %v", rate, mic)
	}
	rate, speaker := readWAV(t, filepath.Join(dir, "esn1_20261016-170000_speaker.wav"))
	if rate != 32000 || len(speaker) != 1 || speaker[0] != 9 {
		t.Fatalf("speaker %d Hz %v", rate, speaker)
	}
}

func TestRecordSinkWithoutDirectoryAccessKeepsTheStreamGoing(t *testing.T) {
	r := NewRecordSink(filepath.Join(t.TempDir(), "missing", "\x00bad"), func(...any) {})
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", pcmFrame(FrameMic, 0, 1))
	r.End("esn1", nil)
}

func TestRecordingIsReadableWhileTheSessionRuns(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	for seq := uint32(0); seq < 2*headerEvery; seq++ {
		r.Frame("esn1", pcmFrame(FrameMic, seq, 5))
	}
	_, mic := readWAV(t, filepath.Join(dir, "esn1_20261016-170000_mic.wav"))
	if len(mic) != 2*headerEvery {
		t.Fatalf("readable samples %d, want %d", len(mic), 2*headerEvery)
	}
	r.End("esn1", nil)
}

func TestRecordingFillsDroppedMicFramesWithSilence(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", pcmFrame(FrameMic, 7, 1, 2))
	r.Frame("esn1", pcmFrame(FrameMic, 10, 3, 4)) // robot dropped frames 8 and 9
	r.End("esn1", nil)

	_, mic := readWAV(t, filepath.Join(dir, "esn1_20261016-170000_mic.wav"))
	want := []int16{1, 2, 0, 0, 0, 0, 3, 4}
	if len(mic) != len(want) {
		t.Fatalf("mic %v, want %v", mic, want)
	}
	for i := range want {
		if mic[i] != want[i] {
			t.Fatalf("mic %v, want %v", mic, want)
		}
	}
}

func TestRecordingLogsThePCClockAgainstTheMicPosition(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	clock := time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	for seq := uint32(0); seq < headerEvery+1; seq++ {
		clock = clock.Add(10 * time.Millisecond)
		r.Frame("esn1", pcmFrame(FrameMic, seq, 5, 5))
	}
	r.End("esn1", nil)

	b, err := os.ReadFile(filepath.Join(dir, "esn1_20261016-170000_clock.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first := clock.Add(-headerEvery * 10 * time.Millisecond).UnixMilli()
	want := fmt.Sprintf("{\"unix_ms\":%d,\"mic_samples\":2}\n{\"unix_ms\":%d,\"mic_samples\":%d}\n",
		first, clock.UnixMilli(), 2*(headerEvery+1))
	if string(b) != want {
		t.Fatalf("clock\n%s\nwant\n%s", b, want)
	}
}
