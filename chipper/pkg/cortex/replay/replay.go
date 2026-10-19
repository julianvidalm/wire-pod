// Package replay plays a recorded MicTap session (pkg/mictap's recorder: <base>_mic.wav and, when present,
// <base>_speaker.wav and the <base>_mic_frames.jsonl / <base>_speaker_frames.jsonl frame logs) into a
// mictap.Sink, as the robot streamed it, so the offline tools (cmd/cortex-replay, cmd/oww-replay) run the same
// pipeline wire-pod runs live, the half-duplex gate included.
package replay

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
)

const (
	micFrameSamples     = 160
	speakerFrameSamples = 1024
	SpeakerRate         = 32000
)

// Recording is one session's audio and its timeline.
type Recording struct {
	Mic     []int16
	MicRate int     // the mic's true rate
	Speaker []int16 // nil without a _speaker.wav
	// Alignment says how mic and speaker times were found: "frame logs" (the robot's timestamps), "sample
	// clocks" (both counted from the session start) or "" (no speaker recording).
	Alignment string

	micLog, speakerLog []logPoint // nil: sample clock
}

type logPoint struct {
	Sample int64  `json:"sample"`
	TsMs   uint64 `json:"ts_ms"`
}

// SampleClockWarning is printed by the replays when a session has no frame logs: recorders before the frame
// logs did not fill speaker gaps, so the speaker timeline drifts against the mic (by -1 to +4 s over session
// 20261016-220357) and the gate silences the wrong spans.
const SampleClockWarning = "warning: no frame logs, speaker aligned by sample clocks; on recordings made before " +
	"the frame logs the speaker drifts by seconds and the half-duplex gate silences the wrong audio (-half-duplex=false)"

// Load reads micPath (a <base>_mic.wav) and the speaker recording and frame logs next to it.
func Load(micPath string, micRate int) (*Recording, error) {
	mic, err := ReadWAV(micPath)
	if err != nil {
		return nil, err
	}
	r := &Recording{Mic: mic, MicRate: micRate}
	base, ok := strings.CutSuffix(micPath, "_mic.wav")
	if !ok {
		return r, nil
	}
	if _, err := os.Stat(base + "_speaker.wav"); err != nil {
		return r, nil
	}
	if r.Speaker, err = ReadWAV(base + "_speaker.wav"); err != nil {
		return nil, err
	}
	r.Alignment = "sample clocks"
	micLog, err1 := readLog(base + "_mic_frames.jsonl")
	spkLog, err2 := readLog(base + "_speaker_frames.jsonl")
	if err1 == nil && err2 == nil && len(micLog) > 0 && len(spkLog) > 0 {
		r.micLog, r.speakerLog, r.Alignment = micLog, spkLog, "frame logs"
	}
	return r, nil
}

func readLog(path string) ([]logPoint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []logPoint
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		var p logPoint
		if err := json.Unmarshal(lines.Bytes(), &p); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, p)
	}
	return out, lines.Err()
}

// timeAt is the robot time of sample i on a timeline: the last logged frame at or before it, plus the samples
// since at rate. Without a log the time counts samples from 0.
func timeAt(log []logPoint, i int, rate int) uint64 {
	if len(log) == 0 {
		return uint64(i) * 1000 / uint64(rate)
	}
	k := sort.Search(len(log), func(k int) bool { return log[k].Sample > int64(i) }) - 1
	if k < 0 {
		k = 0
	}
	return uint64(int64(log[k].TsMs) + (int64(i)-log[k].Sample)*1000/int64(rate))
}

// MicTime is the robot time (ms) of mic sample i, the timestamp the replayed frame starting there carries.
func (r *Recording) MicTime(i int) uint64 { return timeAt(r.micLog, i, r.MicRate) }

// MicSample is the mic sample heard at robot time ts (the inverse of MicTime).
func (r *Recording) MicSample(ts uint64) int {
	if len(r.micLog) == 0 {
		return int(ts * uint64(r.MicRate) / 1000)
	}
	k := sort.Search(len(r.micLog), func(k int) bool { return r.micLog[k].TsMs > ts }) - 1
	if k < 0 {
		k = 0
	}
	p := r.micLog[k]
	return int(p.Sample + (int64(ts)-int64(p.TsMs))*int64(r.MicRate)/1000)
}

// MicOnly is the recording without its speaker audio and frame logs: it replays timed by mic samples from the
// file's start, as before the frame logs existed.
func (r *Recording) MicOnly() *Recording {
	return &Recording{Mic: r.Mic, MicRate: r.MicRate}
}

func (r *Recording) speakerTime(j int) uint64 { return timeAt(r.speakerLog, j, SpeakerRate) }

// Play sends mic samples [from, to) as 10 ms mic frames (seq from 0), then tail samples of silence, with the
// speaker frames of the same span (from 2 s before) interleaved by robot time: each speaker frame goes before
// the mic frames at or after its timestamp. It paces at speed times real time and calls Start and End.
func (r *Recording) Play(sink mictap.Sink, esn string, from, to, tail int, speed float64) {
	sink.Start(esn, mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: SpeakerRate})
	spk, spkSeq := 0, uint32(0)
	if r.Speaker != nil {
		start := r.MicTime(from)
		for spk+speakerFrameSamples <= len(r.Speaker) && r.speakerTime(spk)+2000 < start {
			spk += speakerFrameSamples
		}
	}
	frameDur := time.Duration(float64(time.Second) * micFrameSamples / float64(r.MicRate) / speed)
	began := time.Now()
	end := to + tail
	for i, n := from, 0; i+micFrameSamples <= end; i, n = i+micFrameSamples, n+1 {
		ts := r.MicTime(i)
		for r.Speaker != nil && spk+speakerFrameSamples <= len(r.Speaker) && r.speakerTime(spk) <= ts {
			sink.Frame(esn, mictap.Frame{Type: mictap.FrameSpeaker, Seq: spkSeq, Timestamp: r.speakerTime(spk),
				Payload: encode(r.Speaker[spk : spk+speakerFrameSamples])})
			spk, spkSeq = spk+speakerFrameSamples, spkSeq+1
		}
		var chunk []int16
		if i+micFrameSamples <= to {
			chunk = r.Mic[i : i+micFrameSamples]
		} else {
			chunk = make([]int16, micFrameSamples)
		}
		sink.Frame(esn, mictap.Frame{Type: mictap.FrameMic, Seq: uint32(n), Timestamp: ts, Payload: encode(chunk)})
		if n%100 == 99 {
			if ahead := time.Duration(n+1)*frameDur - time.Since(began); ahead > 0 {
				time.Sleep(ahead)
			}
		}
	}
	sink.End(esn, nil)
}

func encode(samples []int16) []byte {
	p := make([]byte, 2*len(samples))
	for j, s := range samples {
		binary.LittleEndian.PutUint16(p[2*j:], uint16(s))
	}
	return p
}

// ReadWAV returns the samples of a mono 16-bit PCM WAV file, whatever rate its header claims.
func ReadWAV(path string) ([]int16, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil || string(riff[:4]) != "RIFF" || string(riff[8:]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return nil, errors.New("no data chunk")
		}
		size := binary.LittleEndian.Uint32(hdr[4:])
		switch string(hdr[:4]) {
		case "fmt ":
			chunk := make([]byte, size)
			if _, err := io.ReadFull(f, chunk); err != nil {
				return nil, err
			}
			if ch, bits := binary.LittleEndian.Uint16(chunk[2:]), binary.LittleEndian.Uint16(chunk[14:]); ch != 1 || bits != 16 {
				return nil, fmt.Errorf("need mono 16-bit, got %d ch %d bit", ch, bits)
			}
		case "data":
			// A recording still being written may claim a size of 0 or more than it holds: read what is there.
			data, _ := io.ReadAll(f)
			if size > 0 && int(size) < len(data) {
				data = data[:size]
			}
			out := make([]int16, len(data)/2)
			for i := range out {
				out[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
			}
			return out, nil
		default:
			if _, err := f.Seek(int64(size+size%2), io.SeekCurrent); err != nil {
				return nil, err
			}
		}
	}
}
