package mictap

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func readFrameLog(t *testing.T, path string) []FrameLogLine {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []FrameLogLine
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var l FrameLogLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatalf("%s: %q: %v", path, line, err)
		}
		out = append(out, l)
	}
	return out
}

func stamped(f Frame, ts uint64) Frame {
	f.Timestamp = ts
	return f
}

func TestRecordingKeepsSpeakerSeqAndTimestamps(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", stamped(pcmFrame(FrameSpeaker, 4, 1, 2), 1000))
	r.Frame("esn1", stamped(pcmFrame(FrameSpeaker, 5, 3, 4), 1032))
	r.End("esn1", nil)

	got := readFrameLog(t, filepath.Join(dir, "esn1_20261016-170000_speaker_frames.jsonl"))
	want := []FrameLogLine{{Seq: 4, TimestampMs: 1000, Sample: 0}, {Seq: 5, TimestampMs: 1032, Sample: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("speaker frames %+v, want %+v", got, want)
	}
}

func TestRecordingFillsDroppedSpeakerFramesWithSilence(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", stamped(pcmFrame(FrameSpeaker, 7, 1, 2), 100))
	r.Frame("esn1", stamped(pcmFrame(FrameSpeaker, 9, 3, 4), 164)) // robot dropped frame 8
	r.End("esn1", nil)

	_, speaker := readWAV(t, filepath.Join(dir, "esn1_20261016-170000_speaker.wav"))
	if want := []int16{1, 2, 0, 0, 3, 4}; !reflect.DeepEqual(speaker, want) {
		t.Fatalf("speaker %v, want %v", speaker, want)
	}
	got := readFrameLog(t, filepath.Join(dir, "esn1_20261016-170000_speaker_frames.jsonl"))
	want := []FrameLogLine{{Seq: 7, TimestampMs: 100, Sample: 0}, {Seq: 9, TimestampMs: 164, Sample: 4, Gap: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("speaker frames %+v, want %+v", got, want)
	}
}

func TestRecordingKeepsMicSeqAndTimestamps(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", stamped(pcmFrame(FrameMic, 1, 1, 2), 10))
	r.Frame("esn1", stamped(pcmFrame(FrameMic, 3, 3, 4), 30))
	r.End("esn1", nil)

	got := readFrameLog(t, filepath.Join(dir, "esn1_20261016-170000_mic_frames.jsonl"))
	want := []FrameLogLine{{Seq: 1, TimestampMs: 10, Sample: 0}, {Seq: 3, TimestampMs: 30, Sample: 4, Gap: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mic frames %+v, want %+v", got, want)
	}
}

func readWAVChannels(t *testing.T, path string) (channels uint16, samples []int16) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	channels = le.Uint16(b[22:])
	if le.Uint32(b[28:]) != le.Uint32(b[24:])*2*uint32(channels) || le.Uint16(b[32:]) != 2*channels {
		t.Fatalf("%s: byte rate / block align do not match %d channels: % x", path, channels, b[:44])
	}
	_, samples = readWAV(t, path)
	return channels, samples
}

// rawMicFrame is one full raw mic frame whose channel c holds values[c] in every sample.
func rawMicFrame(seq uint32, values [RawMicChannels]int16) Frame {
	samples := make([]int16, 0, RawMicFrameBytes/2)
	for len(samples) < cap(samples) {
		samples = append(samples, values[:]...)
	}
	return pcmFrame(FrameRawMic, seq, samples...)
}

// rawMicSamples is the interleaved 4-channel audio of the rawMicFrame of each values, in order.
func rawMicSamples(values ...[RawMicChannels]int16) (samples []int16) {
	for _, v := range values {
		samples = append(samples, frameSamples(rawMicFrame(0, v))...)
	}
	return samples
}

func frameSamples(f Frame) []int16 {
	s := make([]int16, len(f.Payload)/2)
	for i := range s {
		s[i] = int16(binary.LittleEndian.Uint16(f.Payload[2*i:]))
	}
	return s
}

const rawMicFrameSamples = RawMicFrameBytes / 2 / RawMicChannels // per channel

func TestRecordingWritesRawMicFramesToTheirOwnFourChannelWAV(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", stamped(pcmFrame(FrameMic, 0, 5), 0))
	r.Frame("esn1", stamped(rawMicFrame(2, [4]int16{1, 2, 3, 4}), 20))
	r.Frame("esn1", stamped(rawMicFrame(4, [4]int16{5, 6, 7, 8}), 40)) // robot dropped raw frame 3
	r.End("esn1", nil)

	channels, raw := readWAVChannels(t, filepath.Join(dir, "esn1_20261016-170000_raw4ch.wav"))
	if want := rawMicSamples([4]int16{1, 2, 3, 4}, [4]int16{}, [4]int16{5, 6, 7, 8}); channels != 4 || !reflect.DeepEqual(raw, want) {
		t.Fatalf("raw %d channels %d samples, want 4 channels %d samples", channels, len(raw), len(want))
	}
	got := readFrameLog(t, filepath.Join(dir, "esn1_20261016-170000_raw4ch_frames.jsonl"))
	want := []FrameLogLine{{Seq: 2, TimestampMs: 20, Sample: 0}, {Seq: 4, TimestampMs: 40, Sample: 2 * rawMicFrameSamples, Gap: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("raw frames %+v, want %+v", got, want)
	}
	if _, mic := readWAV(t, filepath.Join(dir, "esn1_20261016-170000_mic.wav")); !reflect.DeepEqual(mic, []int16{5}) {
		t.Fatalf("mic %v: raw frames must not reach the processed mic file", mic)
	}
}

func TestRecordingWithoutRawMicFramesWritesNoRawFile(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", pcmFrame(FrameMic, 0, 5))
	r.End("esn1", nil)
	if _, err := os.Stat(filepath.Join(dir, "esn1_20261016-170000_raw4ch.wav")); !os.IsNotExist(err) {
		t.Fatalf("raw file exists without raw frames: %v", err)
	}
}

func TestRecordingIgnoresUnknownFrameTypes(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", pcmFrame(FrameMic, 0, 5))
	r.Frame("esn1", pcmFrame(9, 0, 7, 7, 7, 7))
	r.Frame("esn1", pcmFrame(FrameMic, 1, 6))
	r.End("esn1", nil)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"esn1_20261016-170000_clock.jsonl", "esn1_20261016-170000_mic.wav",
		"esn1_20261016-170000_mic_frames.jsonl", "esn1_20261016-170000_speaker.wav",
		"esn1_20261016-170000_speaker_frames.jsonl"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	if _, mic := readWAV(t, filepath.Join(dir, "esn1_20261016-170000_mic.wav")); !reflect.DeepEqual(mic, []int16{5, 6}) {
		t.Fatalf("mic %v", mic)
	}
}

func TestRecordingDoesNotFillARawGapLeftByTheRawSwitchBeingOff(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{MicRate: 16000, SpeakerRate: 32000})
	// the raw seq advances every block even while the switch is off: the first raw frame of a session and a
	// frame after the switch was off for a while both jump far ahead
	r.Frame("esn1", stamped(rawMicFrame(60000, [4]int16{1, 2, 3, 4}), 600000))
	r.Frame("esn1", stamped(rawMicFrame(60000+maxGapFrames+2, [4]int16{5, 6, 7, 8}), 605020))
	r.End("esn1", nil)

	_, raw := readWAVChannels(t, filepath.Join(dir, "esn1_20261016-170000_raw4ch.wav"))
	if want := rawMicSamples([4]int16{1, 2, 3, 4}, [4]int16{5, 6, 7, 8}); !reflect.DeepEqual(raw, want) {
		t.Fatalf("raw %d samples, want %d without the skipped frames", len(raw), len(want))
	}
	got := readFrameLog(t, filepath.Join(dir, "esn1_20261016-170000_raw4ch_frames.jsonl"))
	want := []FrameLogLine{{Seq: 60000, TimestampMs: 600000, Sample: 0},
		{Seq: 60000 + maxGapFrames + 2, TimestampMs: 605020, Sample: rawMicFrameSamples, Skipped: maxGapFrames + 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("raw frames %+v, want %+v", got, want)
	}
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingStartDeletesTheOldestSessionsOverTheLimit(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		"esn1_20261016-100000_mic.wav", "esn1_20261016-100000_clock.jsonl",
		"esn2_20261016-110000_mic.wav",
		"esn1_20261016-120000_mic.wav", "esn1_20261016-120000_speaker.wav",
	} {
		writeFile(t, filepath.Join(dir, f), 100)
	}
	writeFile(t, filepath.Join(dir, "notes.txt"), 1000)
	r := NewRecordSink(dir, func(...any) {})
	r.MaxBytes = 250
	r.now = func() time.Time { return time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	r.End("esn1", nil)

	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	want := []string{
		"esn1_20261016-120000_mic.wav", "esn1_20261016-120000_speaker.wav",
		"esn1_20261016-130000_clock.jsonl", "esn1_20261016-130000_mic.wav", "esn1_20261016-130000_mic_frames.jsonl",
		"esn1_20261016-130000_speaker.wav", "esn1_20261016-130000_speaker_frames.jsonl",
		"notes.txt",
	}
	if !reflect.DeepEqual(left, want) {
		t.Fatalf("left %v\nwant %v", left, want)
	}
}

func TestRecordingStartKeepsAnotherRobotsRunningSession(t *testing.T) {
	dir := t.TempDir()
	r := NewRecordSink(dir, func(...any) {})
	r.MaxBytes = 1
	r.now = func() time.Time { return time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	r.Frame("esn1", pcmFrame(FrameMic, 0, 1, 2))
	r.now = func() time.Time { return time.Date(2026, 10, 16, 11, 0, 0, 0, time.UTC) }
	r.Start("esn2", Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	r.End("esn1", nil)
	r.End("esn2", nil)

	if _, err := os.Stat(filepath.Join(dir, "esn1_20261016-100000_mic.wav")); err != nil {
		t.Fatal("running session deleted:", err)
	}
}

func TestRecordingWithoutLimitKeepsEverySession(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "esn1_20261016-100000_mic.wav"), 100)
	r := NewRecordSink(dir, func(...any) {})
	r.now = func() time.Time { return time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC) }
	r.Start("esn1", Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	r.End("esn1", nil)

	if _, err := os.Stat(filepath.Join(dir, "esn1_20261016-100000_mic.wav")); err != nil {
		t.Fatal(err)
	}
}
