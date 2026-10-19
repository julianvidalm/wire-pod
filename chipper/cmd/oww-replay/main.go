// Command oww-replay replays a MicTap recording through the openWakeWord detector (pkg/oww and its Python
// sidecar) exactly as wire-pod runs it for a live robot, half-duplex gate included, and prints every wake, to
// check the detector offline.
//
//	oww-replay -python ~/.cache/v3ctor/corpus-venv/bin/python -model hey_jarvis_v3ctor.onnx \
//	    [-threshold 0.7] [-mic-rate 15662] [-speed 20] [-half-duplex=false] [-hangover 600ms] [-half-duplex-db -60] \
//	    session_mic.wav
//
// The WAV's samples are sent as 10 ms MicTap mic frames (160 samples) paced at -speed times real time. When
// session_speaker.wav sits next to it, its 32 ms speaker frames are interleaved by robot time (from the
// _mic_frames.jsonl and _speaker_frames.jsonl logs when present, else counting samples from the session start)
// and the half-duplex gate silences the mic while the robot talks, as wire-pod does with CORTEX_HALF_DUPLEX=on.
// Each wake is printed with its time and mic sample position in the file.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/mictapgate"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/replay"
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
	halfDuplex := flag.Bool("half-duplex", true, "silence the mic while the recorded speaker sounds (needs _speaker.wav)")
	hd := cortex.DefaultHalfDuplexConfig()
	flag.DurationVar(&hd.Hangover, "hangover", hd.Hangover, "half duplex: mic stays silenced this long after the sound")
	flag.Float64Var(&hd.Threshold, "half-duplex-db", hd.Threshold, "half duplex: speaker level (dBFS) that counts as talking")
	flag.Parse()
	if *model == "" || flag.NArg() != 1 || *speed <= 0 {
		fmt.Fprintln(os.Stderr, "usage: oww-replay -model <model.onnx> [-python p] [-threshold 0.7] [-speed 20] <mic.wav>")
		os.Exit(2)
	}
	rec, err := replay.Load(flag.Arg(0), *micRate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "oww-replay:", err)
		os.Exit(1)
	}
	audio := float64(len(rec.Mic)) / float64(*micRate)
	fmt.Printf("%s: %.0f s of audio at %d Hz, threshold %.2f, %gx real time\n", flag.Arg(0), audio, *micRate, *threshold, *speed)
	logf := func(a ...any) { fmt.Fprintln(os.Stderr, a...) }
	switch {
	case rec.Speaker == nil:
		fmt.Println("no speaker recording: half duplex off")
	case *halfDuplex:
		fmt.Printf("half duplex on (speaker aligned by %s): above %g dBFS, hangover %v\n", rec.Alignment, hd.Threshold, hd.Hangover)
	default:
		fmt.Println("half duplex off")
	}

	h := &printingHandler{rec: rec, first: rec.MicTime(0)}
	det := oww.New(oww.Config{
		Command: []string{*python, *sidecar, "--model", *model, "--in-rate", strconv.Itoa(*micRate),
			"--report", strconv.FormatFloat(*threshold, 'f', -1, 64)},
		Word:         "hey jarvis",
		Threshold:    *threshold,
		RefractoryMs: 2000,
		StopGrace:    time.Minute,
		Handler:      h,
		Logf:         logf,
	})
	var sink mictap.Sink = det
	if rec.Speaker != nil && *halfDuplex {
		sink = mictapgate.New(det, hd, logf)
	}
	began := time.Now()
	// Trailing silence flushes the sidecar's last frames; only silence may be left in the queue at the end.
	rec.Play(sink, "replay", 0, len(rec.Mic), 3**micRate, *speed)
	took := time.Since(began)
	fmt.Printf("%d wakes in %.0f s of audio (replayed in %.0f s)\n", h.count(), audio, took.Seconds())
}

type printingHandler struct {
	rec   *replay.Recording
	first uint64 // robot time of the recording's first sample
	mu    sync.Mutex
	n     int
}

func (h *printingHandler) OnWake(d wakeword.Detection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n++
	fmt.Printf("WAKE at %.2f s (mic sample %d, robot time %d ms) score %.3f\n", float64(d.FrameTimestamp-h.first)/1000,
		h.rec.MicSample(d.FrameTimestamp), d.FrameTimestamp, d.Word.Conf)
}

func (h *printingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}
