// Command oww-replay replays a MicTap mic recording through the openWakeWord detector (pkg/oww and its Python
// sidecar) exactly as wire-pod runs it for a live robot, and prints every wake, to check the detector offline.
//
//	oww-replay -python ~/.cache/v3ctor/corpus-venv/bin/python -model hey_jarvis_v3ctor.onnx \
//	    [-threshold 0.7] [-mic-rate 15662] [-speed 20] session_mic.wav
//
// The WAV's samples are sent as 10 ms MicTap mic frames (160 samples), timestamped at the true mic rate, paced
// at -speed times real time. Each wake is printed with its time and mic sample position in the file.
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/oww"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
)

func main() {
	python := flag.String("python", "python3", "python with openwakeword, onnxruntime, soxr and numpy")
	sidecar := flag.String("sidecar", "oww-sidecar/oww_sidecar.py", "sidecar script")
	model := flag.String("model", "", "openWakeWord .onnx model")
	threshold := flag.Float64("threshold", 0.7, "lowest score that wakes")
	micRate := flag.Int("mic-rate", 15662, "true rate of the recording (MicTap labels it 16000)")
	speed := flag.Float64("speed", 20, "replay speed, times real time")
	flag.Parse()
	if *model == "" || flag.NArg() != 1 || *speed <= 0 {
		fmt.Fprintln(os.Stderr, "usage: oww-replay -model <model.onnx> [-python p] [-threshold 0.7] [-speed 20] <mic.wav>")
		os.Exit(2)
	}
	samples, err := readWAV(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "oww-replay:", err)
		os.Exit(1)
	}
	audio := float64(len(samples)) / float64(*micRate)
	fmt.Printf("%s: %.0f s of audio at %d Hz, threshold %.2f, %gx real time\n", flag.Arg(0), audio, *micRate, *threshold, *speed)

	h := &printingHandler{micRate: *micRate}
	det := oww.New(oww.Config{
		Command: []string{*python, *sidecar, "--model", *model, "--in-rate", strconv.Itoa(*micRate),
			"--report", strconv.FormatFloat(*threshold, 'f', -1, 64)},
		Word:         "hey jarvis",
		Threshold:    *threshold,
		RefractoryMs: 2000,
		StopGrace:    time.Minute,
		Handler:      h,
		Logf:         func(a ...any) { fmt.Fprintln(os.Stderr, a...) },
	})
	det.Start("replay", mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	// Trailing silence flushes the sidecar's last frames; only silence may be left in the queue at the end.
	samples = append(samples, make([]int16, 3**micRate)...)
	frameDur := time.Duration(float64(time.Second) * 160 / float64(*micRate) / *speed)
	began := time.Now()
	for i := 0; i+160 <= len(samples); i += 160 {
		n := i / 160
		payload := make([]byte, 320)
		for j, s := range samples[i : i+160] {
			binary.LittleEndian.PutUint16(payload[2*j:], uint16(s))
		}
		ts := uint64(i) * 1000 / uint64(*micRate)
		det.Frame("replay", mictap.Frame{Type: mictap.FrameMic, Seq: uint32(n), Timestamp: ts, Payload: payload})
		if n%100 == 99 {
			if ahead := time.Duration(n+1)*frameDur - time.Since(began); ahead > 0 {
				time.Sleep(ahead)
			}
		}
	}
	det.End("replay", nil)
	took := time.Since(began)
	fmt.Printf("%d wakes in %.0f s of audio (replayed in %.0f s)\n", h.count(), audio, took.Seconds())
}

type printingHandler struct {
	micRate int
	mu      sync.Mutex
	n       int
}

func (h *printingHandler) OnWake(d wakeword.Detection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n++
	fmt.Printf("WAKE at %.2f s (mic sample %d) score %.3f\n", float64(d.FrameTimestamp)/1000,
		d.FrameTimestamp*uint64(h.micRate)/1000, d.Word.Conf)
}

func (h *printingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}

// readWAV returns the samples of a mono 16-bit PCM WAV file, whatever rate its header claims.
func readWAV(path string) ([]int16, error) {
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
