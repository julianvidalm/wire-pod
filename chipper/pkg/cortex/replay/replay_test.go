package replay

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
)

func writeWAV(t *testing.T, path string, rate int, samples []int16) {
	t.Helper()
	b := make([]byte, 44+2*len(samples))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(samples)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(2*rate))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(samples)))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(s))
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func ramp(n int) []int16 {
	s := make([]int16, n)
	for i := range s {
		s[i] = int16(i%1000 + 1)
	}
	return s
}

type sink struct{ frames []mictap.Frame }

func (s *sink) Start(string, mictap.Hello)     {}
func (s *sink) Frame(_ string, f mictap.Frame) { s.frames = append(s.frames, f) }
func (s *sink) End(string, error)              {}

// A mic recording alone replays exactly as the replay commands always did: 10 ms frames numbered from 0,
// timestamped at the true mic rate from the first replayed sample.
func TestMicOnlyRecordingReplaysAsBefore(t *testing.T) {
	dir := t.TempDir()
	mic := filepath.Join(dir, "s_mic.wav")
	writeWAV(t, mic, 16000, ramp(15662))
	r, err := Load(mic, 15662)
	if err != nil {
		t.Fatal(err)
	}
	if r.Speaker != nil || r.Alignment != "" {
		t.Fatalf("speaker %d samples, alignment %q", len(r.Speaker), r.Alignment)
	}
	s := &sink{}
	r.Play(s, "replay", 320, 15662, 0, 1e9)
	if len(s.frames) != (15662-320)/160 {
		t.Fatalf("%d frames", len(s.frames))
	}
	for n, f := range s.frames {
		i := 320 + 160*n
		if f.Type != mictap.FrameMic || f.Seq != uint32(n) || f.Timestamp != uint64(i)*1000/15662 || len(f.Payload) != 320 ||
			int16(binary.LittleEndian.Uint16(f.Payload)) != r.Mic[i] {
			t.Fatalf("frame %d: %+v", n, f)
		}
	}
}

// With the speaker recording and both frame logs, every frame carries the robot timestamp it was recorded
// with, and each speaker frame is sent before the mic frames at or after its time.
func TestSpeakerAndFrameLogsReplayOnTheRobotClock(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "s")
	writeWAV(t, base+"_mic.wav", 16000, ramp(1600))
	writeWAV(t, base+"_speaker.wav", 32000, ramp(4096))
	var micLog, spkLog string
	for i := 0; i < 10; i++ {
		micLog += fmt.Sprintf(`{"seq":%d,"ts_ms":%d,"sample":%d}`+"\n", 100+i, 5000+10*i, 160*i)
	}
	for i := 0; i < 4; i++ {
		spkLog += fmt.Sprintf(`{"seq":%d,"ts_ms":%d,"sample":%d}`+"\n", 7+i, 4990+32*i, 1024*i)
	}
	os.WriteFile(base+"_mic_frames.jsonl", []byte(micLog), 0o600)
	os.WriteFile(base+"_speaker_frames.jsonl", []byte(spkLog), 0o600)
	r, err := Load(base+"_mic.wav", 16000)
	if err != nil {
		t.Fatal(err)
	}
	if r.Alignment != "frame logs" || len(r.Speaker) != 4096 {
		t.Fatalf("alignment %q, speaker %d", r.Alignment, len(r.Speaker))
	}
	if got := r.MicTime(480); got != 5030 {
		t.Fatalf("MicTime(480) = %d, want 5030", got)
	}
	s := &sink{}
	r.Play(s, "replay", 0, 1600, 0, 1e9)
	var mics, spks []uint64
	for _, f := range s.frames {
		switch f.Type {
		case mictap.FrameMic:
			mics = append(mics, f.Timestamp)
		case mictap.FrameSpeaker:
			if len(f.Payload) != 2048 {
				t.Fatalf("speaker payload %d bytes", len(f.Payload))
			}
			if n := len(mics); n > 0 && mics[n-1] >= f.Timestamp {
				t.Fatalf("speaker frame at %d ms sent after the mic frame at %d ms", f.Timestamp, mics[n-1])
			}
			spks = append(spks, f.Timestamp)
		}
	}
	if fmt.Sprint(spks) != "[4990 5022 5054 5086]" || len(mics) != 10 || mics[0] != 5000 || mics[9] != 5090 {
		t.Fatalf("speaker %v, mic %v", spks, mics)
	}
}

// Without frame logs the mic and speaker recordings are aligned by their sample clocks from the session start.
func TestSpeakerWithoutFrameLogsUsesTheSampleClocks(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "s")
	writeWAV(t, base+"_mic.wav", 16000, ramp(1600))
	writeWAV(t, base+"_speaker.wav", 32000, ramp(3200))
	r, err := Load(base+"_mic.wav", 16000)
	if err != nil {
		t.Fatal(err)
	}
	if r.Alignment != "sample clocks" {
		t.Fatalf("alignment %q", r.Alignment)
	}
	s := &sink{}
	r.Play(s, "replay", 0, 1600, 0, 1e9)
	var spks []uint64
	for _, f := range s.frames {
		if f.Type == mictap.FrameSpeaker {
			spks = append(spks, f.Timestamp)
		}
	}
	if fmt.Sprint(spks) != "[0 32 64]" {
		t.Fatalf("speaker frames at %v", spks)
	}
}

// Tail adds silent mic frames after the recording, continuing its timestamps.
func TestPlayAddsASilentTail(t *testing.T) {
	dir := t.TempDir()
	writeWAV(t, filepath.Join(dir, "s_mic.wav"), 16000, ramp(1600))
	r, _ := Load(filepath.Join(dir, "s_mic.wav"), 16000)
	s := &sink{}
	r.Play(s, "replay", 0, 1600, 320, 1e9)
	if n := len(s.frames); n != 12 || s.frames[11].Timestamp != 110 || binary.LittleEndian.Uint16(s.frames[11].Payload) != 0 {
		t.Fatalf("%d frames, last %+v", n, s.frames[n-1])
	}
}

// MicOnly drops the speaker recording and the frame logs: the replay is timed exactly as before them.
func TestMicOnlyIgnoresSpeakerAndFrameLogs(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "s")
	writeWAV(t, base+"_mic.wav", 16000, ramp(1600))
	writeWAV(t, base+"_speaker.wav", 32000, ramp(4096))
	os.WriteFile(base+"_mic_frames.jsonl", []byte(`{"seq":1,"ts_ms":5000,"sample":0}`+"\n"), 0o600)
	os.WriteFile(base+"_speaker_frames.jsonl", []byte(`{"seq":1,"ts_ms":4990,"sample":0}`+"\n"), 0o600)
	r, err := Load(base+"_mic.wav", 16000)
	if err != nil {
		t.Fatal(err)
	}
	m := r.MicOnly()
	s := &sink{}
	m.Play(s, "replay", 0, 1600, 0, 1e9)
	if m.Speaker != nil || m.Alignment != "" || len(s.frames) != 10 || s.frames[3].Timestamp != 30 || m.MicSample(30) != 480 {
		t.Fatalf("speaker %v alignment %q, %d frames, frame 3 at %d", m.Speaker != nil, m.Alignment, len(s.frames), s.frames[3].Timestamp)
	}
}
