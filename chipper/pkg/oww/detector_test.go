package oww

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
)

// The test binary doubles as a fake sidecar: with OWW_FAKE set it runs fakeSidecar instead of the tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("OWW_FAKE"); mode != "" {
		os.Exit(fakeSidecar(mode))
	}
	os.Exit(m.Run())
}

// fakeSidecar modes:
//   - script: writes each "score@sample" of OWW_FAKE_LINES once it has read that many samples, then reads to EOF;
//   - crash: reads one mic frame, reports a 0.9 score at its end, complains on stderr and exits 1;
//   - stall: never reads stdin.
func fakeSidecar(mode string) int {
	switch mode {
	case "script":
		var lines []string
		if s := os.Getenv("OWW_FAKE_LINES"); s != "" {
			lines = strings.Split(s, ",")
		}
		read := 0
		buf := make([]byte, 320)
		for {
			for len(lines) > 0 {
				score, sample, _ := strings.Cut(lines[0], "@")
				n, _ := strconv.Atoi(sample)
				if read < n {
					break
				}
				fmt.Printf("{\"score\": %s, \"sample\": %d}\n", score, n)
				lines = lines[1:]
			}
			k, err := os.Stdin.Read(buf)
			read += k / 2
			if err != nil {
				return 0
			}
		}
	case "crash":
		io.ReadFull(os.Stdin, make([]byte, 320))
		fmt.Println(`{"score": 0.9, "sample": 160}`)
		fmt.Fprintln(os.Stderr, "boom")
		return 1
	case "stall":
		time.Sleep(time.Hour)
	}
	return 2
}

type recordingHandler struct {
	mu   sync.Mutex
	wake []wakeword.Detection
}

func (h *recordingHandler) OnWake(d wakeword.Detection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.wake = append(h.wake, d)
}

func (h *recordingHandler) detections() []wakeword.Detection {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]wakeword.Detection(nil), h.wake...)
}

type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (l *logRecorder) Logf(a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(a...))
}

func (l *logRecorder) contains(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

func newTestDetector(t *testing.T, mode string, h wakeword.Handler, logs *logRecorder, queue int) *Detector {
	t.Helper()
	t.Setenv("OWW_FAKE", mode)
	return New(Config{
		Command:      []string{os.Args[0]},
		Word:         "hey jarvis",
		Threshold:    0.7,
		RefractoryMs: 2000,
		QueueFrames:  queue,
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		StopGrace:    200 * time.Millisecond,
		Handler:      h,
		Logf:         logs.Logf,
	})
}

var hello = mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000}

// micFrame is frame i of a stream: 160 samples, timestamped 10 ms apart.
func micFrame(i int) mictap.Frame {
	payload := make([]byte, 320)
	for j := 0; j < 160; j++ {
		binary.LittleEndian.PutUint16(payload[2*j:], uint16(int16(j)))
	}
	return mictap.Frame{Type: mictap.FrameMic, Seq: uint32(i), Timestamp: uint64(10 * i), Payload: payload}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for ", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDetectorAppliesTheThresholdAndTheRefractoryPeriod(t *testing.T) {
	// 0.5 is below the threshold; 0.9 comes 1.6 s after the first wake (refractory); 0.75 comes 2.3 s after it.
	t.Setenv("OWW_FAKE_LINES", "0.5@1600,0.8@3200,0.9@28800,0.75@40000")
	h, logs := &recordingHandler{}, &logRecorder{}
	d := newTestDetector(t, "script", h, logs, 1000)
	d.Start("esn1", hello)
	for i := 0; i < 300; i++ {
		d.Frame("esn1", micFrame(i))
	}
	waitFor(t, "two detections", func() bool { return len(h.detections()) >= 2 })
	d.End("esn1", nil)

	got := h.detections()
	if len(got) != 2 {
		t.Fatalf("got %d detections, want 2: %+v", len(got), got)
	}
	// sample 3200 ends mic frame 19 (10 ms frames), sample 40000 ends frame 249
	want := []struct {
		conf float64
		ts   uint64
	}{{0.8, 190}, {0.75, 2490}}
	for i, w := range want {
		g := got[i]
		if g.ESN != "esn1" || g.Word.Text != "hey jarvis" || g.Word.Conf != w.conf || g.FrameTimestamp != w.ts {
			t.Errorf("detection %d = %+v, want conf %.2f at %d ms", i, g, w.conf, w.ts)
		}
	}
}

func TestDetectorRestartsACrashedSidecar(t *testing.T) {
	h, logs := &recordingHandler{}, &logRecorder{}
	d := newTestDetector(t, "crash", h, logs, 1000)
	d.cfg.RefractoryMs = 0
	d.Start("esn1", hello)
	stop := make(chan struct{})
	fed := make(chan struct{})
	go func() {
		defer close(fed)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			d.Frame("esn1", micFrame(i))
			time.Sleep(time.Millisecond)
		}
	}()
	waitFor(t, "detections from two sidecar runs", func() bool { return len(h.detections()) >= 2 })
	close(stop)
	<-fed
	d.End("esn1", nil)
	if !logs.contains("boom") {
		t.Error("the sidecar's stderr was not logged")
	}
	if !logs.contains("restarting") {
		t.Error("the restart was not logged")
	}
}

func TestDetectorNeverBlocksTheMicStream(t *testing.T) {
	h, logs := &recordingHandler{}, &logRecorder{}
	d := newTestDetector(t, "stall", h, logs, 4)
	d.Start("esn1", hello)
	start := time.Now()
	const frames = 2000 // 640 kB: more than the queue plus any pipe buffer
	for i := 0; i < frames; i++ {
		d.Frame("esn1", micFrame(i))
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("feeding %d frames to a stalled sidecar took %v", frames, took)
	}
	d.End("esn1", nil)
	if !logs.contains("queue full") {
		t.Error("dropping frames was not logged")
	}
	if !logs.contains("dropped") {
		t.Error("the dropped frame count was not logged")
	}
}

func TestDetectorIgnoresStreamsItCannotMap(t *testing.T) {
	h, logs := &recordingHandler{}, &logRecorder{}
	d := newTestDetector(t, "script", h, logs, 10)
	d.Start("esn1", mictap.Hello{Version: 1, MicRate: 8000, SpeakerRate: 32000})
	d.Frame("esn1", micFrame(0)) // no session: must not panic or block
	d.End("esn1", nil)
	if !logs.contains("only 16000 Hz") {
		t.Error("an unsupported mic rate was not logged")
	}
}
