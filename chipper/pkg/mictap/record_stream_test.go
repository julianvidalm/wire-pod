package mictap

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests replay a whole robot stream through Consume into a RecordSink, the way a live session reaches it.

// streamPCM is n deterministic samples that differ per seed.
func streamPCM(n, seed int) []byte {
	b := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(int16(seed*31+i*7-1000)))
	}
	return b
}

const (
	streamMicFrames      = 300
	streamMicSamples     = 160
	streamSpeakerFrames  = 99 // one every third mic frame, except at the dropped mic seq 51
	streamSpeakerSamples = 1024
)

// robotStream is a session as the robot sends it: mic seq 0..299 with 50..52 dropped, a speaker frame every
// third mic frame (seq 0..98, 40..41 dropped when speakerGap), a stats frame and an unknown type 9 frame at mic
// seq 100, and the given raw frames, one per mic frame from mic seq 150.
func robotStream(speakerGap bool, raw []Frame) []byte {
	var buf bytes.Buffer
	buf.Write(EncodeHello(1, 16000, 32000))
	sp, ri := 0, 0
	for s := uint32(0); s < streamMicFrames; s++ {
		if s >= 50 && s <= 52 {
			continue
		}
		buf.Write(EncodeFrame(Frame{Type: FrameMic, Flags: uint8(s % 4), Seq: s, Timestamp: 1000 + uint64(s)*10, Payload: streamPCM(streamMicSamples, int(s))}))
		if s%3 == 0 && sp < streamSpeakerFrames {
			if !(speakerGap && (sp == 40 || sp == 41)) {
				buf.Write(EncodeFrame(Frame{Type: FrameSpeaker, Flags: SpeakerFlagHasData, Seq: uint32(sp), Timestamp: 1000 + uint64(s)*10, Payload: streamPCM(streamSpeakerSamples, 5000+sp)}))
			}
			sp++
		}
		if s == 100 {
			stats := make([]byte, 8)
			binary.LittleEndian.PutUint32(stats, 3)
			buf.Write(EncodeFrame(Frame{Type: FrameStats, Payload: stats}))
			buf.Write(EncodeFrame(Frame{Type: 9, Seq: 7, Payload: []byte{1, 2, 3}}))
		}
		if ri < len(raw) && s >= 150 {
			buf.Write(EncodeFrame(raw[ri]))
			ri++
		}
	}
	return buf.Bytes()
}

// logLines collects what a RecordSink logs.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(a...))
}

func (l *logLines) count(substr string) (n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// recordStream consumes data as robot esn1 into dir and returns the recording's base path.
func recordStream(t *testing.T, dir string, data []byte) (base string, logs *logLines) {
	t.Helper()
	logs = &logLines{}
	r := NewRecordSink(dir, logs.logf)
	r.now = func() time.Time { return time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC) }
	if _, err := Consume(bytes.NewReader(data), "esn1", r); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "esn1_20261016-170000"), logs
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// The mic and speaker files of a session without raw frames are pinned byte for byte (taken before the raw mic
// track existed in this form), so raw mic handling cannot change them.
func TestRecordingOfAMicAndSpeakerStreamMatchesItsGoldenFiles(t *testing.T) {
	base, _ := recordStream(t, t.TempDir(), robotStream(false, nil))

	for suffix, want := range map[string]string{
		"_mic.wav":     "3b8062598017599799217273eb71c17b2928b440634d05d1ceb66dfce088055b",
		"_speaker.wav": "adac1cb3e6b63976f2ea4355da4200330d927efc081dcc9f2cf21e29f4a26c86",
	} {
		if got := sha256File(t, base+suffix); got != want {
			t.Errorf("%s sha256 %s, want %s", suffix, got, want)
		}
	}
	if _, err := os.Stat(base + "_raw4ch.wav"); !os.IsNotExist(err) {
		t.Fatalf("raw file exists without raw frames: %v", err)
	}
	if rate, mic := readWAV(t, base+"_mic.wav"); rate != 16000 || len(mic) != streamMicFrames*streamMicSamples {
		t.Fatalf("mic %d Hz %d samples, want 16000 Hz %d (dropped 50..52 filled)", rate, len(mic), streamMicFrames*streamMicSamples)
	}
}

func TestRecordingOfAStreamFillsDroppedSpeakerFramesWithSilence(t *testing.T) {
	base, _ := recordStream(t, t.TempDir(), robotStream(true, nil))

	rate, speaker := readWAV(t, base+"_speaker.wav")
	if rate != 32000 || len(speaker) != streamSpeakerFrames*streamSpeakerSamples {
		t.Fatalf("speaker %d Hz %d samples, want 32000 Hz %d", rate, len(speaker), streamSpeakerFrames*streamSpeakerSamples)
	}
	for i, v := range speaker[40*streamSpeakerSamples : 42*streamSpeakerSamples] {
		if v != 0 {
			t.Fatalf("dropped speaker frames 40..41: sample %d = %d, want silence", i, v)
		}
	}
	if want := streamPCM(streamSpeakerSamples, 5000+42); !bytes.Equal(int16Bytes(speaker[42*streamSpeakerSamples:43*streamSpeakerSamples]), want) {
		t.Fatalf("speaker frame 42 is not where the timeline puts it")
	}
	got := readFrameLog(t, base+"_speaker_frames.jsonl")
	if len(got) != streamSpeakerFrames-2 || got[40] != (FrameLogLine{Seq: 42, TimestampMs: 1000 + 129*10, Sample: 42 * streamSpeakerSamples, Gap: 2}) {
		t.Fatalf("speaker frames: %d lines, after the gap %+v", len(got), got[min(40, len(got)-1)])
	}
}

func int16Bytes(s []int16) []byte {
	b := make([]byte, 2*len(s))
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	return b
}

// streamRawChannels gives channel c of raw frame seq sample i a value unique enough to catch misplaced blocks.
func streamRawChannels(seq uint32) (ch [RawMicChannels][rawMicFrameSamples]int16) {
	for c := range ch {
		for i := range ch[c] {
			ch[c][i] = int16(int(seq%30)*1000 + c*200 + i)
		}
	}
	return ch
}

func streamRawFrame(seq uint32) Frame {
	ch := streamRawChannels(seq)
	p := make([]byte, RawMicFrameBytes)
	for i := range ch[0] {
		for c := range ch {
			binary.LittleEndian.PutUint16(p[2*(RawMicChannels*i+c):], uint16(ch[c][i]))
		}
	}
	return Frame{Type: FrameRawMic, Seq: seq, Timestamp: 5000 + uint64(seq)*10, Payload: p}
}

func TestRecordingOfARawStreamKeepsTheRobotTimeline(t *testing.T) {
	// raw starts far ahead (the raw seq advances with the switch off), then 1001, 1002..1004 dropped, a jump of
	// 600 (the switch was off: not filled), 1607, then a gap of exactly maxGapFrames (filled)
	seqs := []uint32{1000, 1001, 1005, 1606, 1607, 1608 + maxGapFrames}
	var raw []Frame
	for _, s := range seqs {
		raw = append(raw, streamRawFrame(s))
	}
	base, _ := recordStream(t, t.TempDir(), robotStream(false, raw))

	channels, samples := readWAVChannels(t, base+"_raw4ch.wav")
	if channels != RawMicChannels {
		t.Fatalf("raw channels %d", channels)
	}
	const perFrame = RawMicFrameBytes / 2
	timeline := []uint32{1000, 1001, 0, 0, 0, 1005, 1606, 1607} // 0 is a silent frame
	for i := 0; i < maxGapFrames; i++ {
		timeline = append(timeline, 0)
	}
	timeline = append(timeline, 1608+maxGapFrames)
	if len(samples) != len(timeline)*perFrame {
		t.Fatalf("raw %d samples, want %d", len(samples), len(timeline)*perFrame)
	}
	for k, seq := range timeline {
		var ch [RawMicChannels][rawMicFrameSamples]int16
		if seq != 0 {
			ch = streamRawChannels(seq)
		}
		for i := range ch[0] {
			for c := range ch {
				if v := samples[k*perFrame+RawMicChannels*i+c]; v != ch[c][i] {
					t.Fatalf("frame %d (seq %d) ch %d sample %d = %d, want %d", k, seq, c, i, v, ch[c][i])
				}
			}
		}
	}
	const n = perFrame / RawMicChannels
	want := []FrameLogLine{
		{Seq: 1000, TimestampMs: 15000, Sample: 0},
		{Seq: 1001, TimestampMs: 15010, Sample: n},
		{Seq: 1005, TimestampMs: 15050, Sample: 5 * n, Gap: 3},
		{Seq: 1606, TimestampMs: 21060, Sample: 6 * n, Skipped: 600},
		{Seq: 1607, TimestampMs: 21070, Sample: 7 * n},
		{Seq: 1608 + maxGapFrames, TimestampMs: 5000 + (1608+maxGapFrames)*10, Sample: (8 + maxGapFrames) * n, Gap: maxGapFrames},
	}
	if got := readFrameLog(t, base+"_raw4ch_frames.jsonl"); !reflect.DeepEqual(got, want) {
		t.Fatalf("raw frames %+v, want %+v", got, want)
	}
}

// A raw frame that is not exactly one full raw mic frame stops raw recording for the session, logged once; the
// mic and speaker files go on.
func TestRecordingStopsRawOnAFrameThatIsNotAFullRawMicFrame(t *testing.T) {
	good := streamRawFrame(2)
	for name, tc := range map[string]struct {
		raw      []Frame
		rawBytes int // raw samples kept, or -1 for no raw file
	}{
		"odd length first":   {[]Frame{{Type: FrameRawMic, Seq: 1, Payload: make([]byte, RawMicFrameBytes-2)}, good}, -1},
		"half frame first":   {[]Frame{{Type: FrameRawMic, Seq: 1, Payload: make([]byte, RawMicFrameBytes/2)}, good}, -1},
		"empty first":        {[]Frame{{Type: FrameRawMic, Seq: 1}, good}, -1},
		"half frame after":   {[]Frame{good, {Type: FrameRawMic, Seq: 3, Payload: make([]byte, RawMicFrameBytes/2)}, streamRawFrame(4)}, RawMicFrameBytes},
		"double frame after": {[]Frame{good, {Type: FrameRawMic, Seq: 3, Payload: make([]byte, 2*RawMicFrameBytes)}, streamRawFrame(4)}, RawMicFrameBytes},
	} {
		t.Run(name, func(t *testing.T) {
			base, logs := recordStream(t, t.TempDir(), robotStream(false, tc.raw))

			if n := logs.count("raw recording stopped"); n != 1 {
				t.Fatalf("logged the stop %d times, want once: %q", n, logs.lines)
			}
			b, err := os.ReadFile(base + "_raw4ch.wav")
			switch {
			case tc.rawBytes < 0 && !os.IsNotExist(err):
				t.Fatalf("raw file exists after a malformed first frame: %d bytes, %v", len(b), err)
			case tc.rawBytes >= 0 && (err != nil || len(b)-44 != tc.rawBytes):
				t.Fatalf("raw file %d data bytes (%v), want %d", len(b)-44, err, tc.rawBytes)
			}
			if _, mic := readWAV(t, base+"_mic.wav"); len(mic) != streamMicFrames*streamMicSamples {
				t.Fatalf("mic %d samples: a malformed raw frame must not disturb the mic file", len(mic))
			}
		})
	}
}
