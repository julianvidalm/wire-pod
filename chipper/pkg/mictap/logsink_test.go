package mictap

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

func stats(mic, speaker uint32) Frame {
	p := make([]byte, 8)
	binary.LittleEndian.PutUint32(p, mic)
	binary.LittleEndian.PutUint32(p[4:], speaker)
	return Frame{Type: FrameStats, Payload: p}
}

func TestLogSinkLogsStartDropsGapsAndEnd(t *testing.T) {
	var lines []string
	s := NewLogSink(func(a ...any) { lines = append(lines, fmt.Sprint(a...)) })

	s.Start("esn1", Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	for _, seq := range []uint32{0, 1, 3} { // seq 2 missing: one gap
		s.Frame("esn1", Frame{Type: FrameMic, Seq: seq})
	}
	s.Frame("esn1", Frame{Type: FrameSpeaker, Seq: 0})
	s.Frame("esn1", stats(0, 0)) // unchanged drop totals: no line
	s.Frame("esn1", stats(2, 0)) // the robot dropped frames: one line
	s.End("esn1", nil)

	all := strings.Join(lines, "\n")
	for _, want := range []string{"esn1 started", "16000", "robot dropped mic 2 speaker 0", "esn1 ended: mic 3 frames (1 gaps), speaker 1 frames (0 gaps)"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if len(lines) != 3 {
		t.Errorf("want 3 lines (start, drops, end), got %d:\n%s", len(lines), all)
	}
}

func TestLogSinkReportsEndError(t *testing.T) {
	var lines []string
	s := NewLogSink(func(a ...any) { lines = append(lines, fmt.Sprint(a...)) })
	s.Start("esn1", Hello{Version: 1})
	s.End("esn1", fmt.Errorf("boom"))
	if !strings.Contains(strings.Join(lines, "\n"), "boom") {
		t.Fatalf("end error not logged: %v", lines)
	}
}
