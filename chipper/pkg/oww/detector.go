// Package oww is the openWakeWord wake word detector: a mictap.Sink that streams each robot's mic audio to a
// Python sidecar (chipper/oww-sidecar/oww_sidecar.py) running an openWakeWord ONNX model, and turns the scores
// it reports into wakeword.Detection events for the same Handler the Vosk detector uses.
//
// Sidecar protocol: raw mono s16le mic samples on stdin; on stdout one JSON line per scored frame,
// {"score": 0.93, "sample": 123456}, where sample is the input position (samples since that process started)
// at the end of the frame; status and errors on stderr, which is logged.
package oww

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
)

const (
	micRate         = 16000 // the rate MicTap announces; the sidecar is told the true rate separately
	samplesPerFrame = 160   // one MicTap mic frame
	maxMarks        = 3000  // written frames remembered to time a report: 30 s of audio
	stableRun       = 30 * time.Second
)

type Config struct {
	// Command is the sidecar's argv, e.g. python oww_sidecar.py --model m.onnx --in-rate 15625.
	Command []string
	// Word names the wake word in detections (Detection.Word.Text and Detection.Text).
	Word string
	// Threshold is the lowest score that wakes.
	Threshold float64
	// RefractoryMs ignores further wakes for this long after one (robot time), so one utterance wakes once.
	RefractoryMs uint64
	// QueueFrames bounds the mic frames waiting for the sidecar; when it is full, frames are dropped and counted.
	QueueFrames int
	// MinBackoff and MaxBackoff space the restarts of a sidecar that keeps exiting.
	MinBackoff, MaxBackoff time.Duration
	// StopGrace is how long a stopping sidecar may take to exit after its stdin closes before it is killed.
	StopGrace time.Duration
	Handler   wakeword.Handler
	Logf      func(a ...any)
}

// Detector implements mictap.Sink: one sidecar process per robot stream.
type Detector struct {
	cfg      Config
	mu       sync.Mutex
	sessions map[string]*session
}

func New(cfg Config) *Detector {
	if cfg.Logf == nil {
		cfg.Logf = func(...any) {}
	}
	if cfg.QueueFrames <= 0 {
		cfg.QueueFrames = 300
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = 500 * time.Millisecond
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.StopGrace <= 0 {
		cfg.StopGrace = 2 * time.Second
	}
	return &Detector{cfg: cfg, sessions: map[string]*session{}}
}

type frame struct {
	pcm []byte
	ts  uint64
}

// mark ties the end of one written frame, in samples since the current process started, to its robot timestamp.
type mark struct {
	end uint64
	ts  uint64
}

type session struct {
	esn     string
	frames  chan frame
	stop    chan struct{}
	done    chan struct{}
	dropped atomic.Uint64 // total
	burst   uint64        // dropped in the current run of full-queue drops; only touched by Frame

	mu       sync.Mutex
	marks    []mark
	woke     bool
	lastWake uint64
}

func (d *Detector) Start(esn string, h mictap.Hello) {
	if h.MicRate != micRate {
		d.cfg.Logf("oww: ", esn, " sends ", h.MicRate, " Hz mic audio; only 16000 Hz is supported")
		return
	}
	s := &session{
		esn:    esn,
		frames: make(chan frame, d.cfg.QueueFrames),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	d.mu.Lock()
	old := d.sessions[esn]
	d.sessions[esn] = s
	d.mu.Unlock()
	if old != nil {
		old.close()
	}
	go d.supervise(s)
}

// Frame queues a mic frame for the sidecar without ever waiting for it.
func (d *Detector) Frame(esn string, f mictap.Frame) {
	if f.Type != mictap.FrameMic || len(f.Payload) != 2*samplesPerFrame {
		return
	}
	d.mu.Lock()
	s := d.sessions[esn]
	d.mu.Unlock()
	if s == nil {
		return
	}
	select {
	case s.frames <- frame{pcm: append([]byte(nil), f.Payload...), ts: f.Timestamp}:
		if s.burst > 0 {
			d.cfg.Logf("oww: ", esn, ": dropped ", s.burst, " mic frames while the queue was full")
			s.burst = 0
		}
	default:
		if s.burst == 0 {
			d.cfg.Logf("oww: ", esn, ": queue full, dropping mic frames")
		}
		s.burst++
		s.dropped.Add(1)
	}
}

func (d *Detector) End(esn string, _ error) {
	d.mu.Lock()
	s := d.sessions[esn]
	delete(d.sessions, esn)
	d.mu.Unlock()
	if s == nil {
		return
	}
	s.close()
	if n := s.dropped.Load(); n > 0 {
		d.cfg.Logf("oww: ", esn, ": dropped ", n, " mic frames in this session")
	}
}

func (s *session) close() {
	close(s.stop)
	<-s.done
}

// supervise keeps one sidecar running for the session, restarting it with backoff whenever it exits.
func (d *Detector) supervise(s *session) {
	defer close(s.done)
	backoff := d.cfg.MinBackoff
	for {
		began := time.Now()
		d.run(s)
		select {
		case <-s.stop:
			return
		default:
		}
		if time.Since(began) > stableRun {
			backoff = d.cfg.MinBackoff
		}
		d.cfg.Logf("oww: ", s.esn, ": sidecar stopped, restarting in ", backoff)
		select {
		case <-s.stop:
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > d.cfg.MaxBackoff {
			backoff = d.cfg.MaxBackoff
		}
	}
}

// run starts one sidecar process and feeds it until it exits or the session stops.
func (d *Detector) run(s *session) {
	cmd := exec.Command(d.cfg.Command[0], d.cfg.Command[1:]...)
	stdin, err1 := cmd.StdinPipe()
	stdout, err2 := cmd.StdoutPipe()
	stderr, err3 := cmd.StderrPipe()
	for _, err := range []error{err1, err2, err3} {
		if err != nil {
			d.cfg.Logf("oww: ", s.esn, ": ", err)
			return
		}
	}
	if err := cmd.Start(); err != nil {
		d.cfg.Logf("oww: ", s.esn, ": cannot start the sidecar: ", err)
		return
	}
	s.mu.Lock()
	s.marks = s.marks[:0]
	s.mu.Unlock()

	var readers sync.WaitGroup
	readers.Add(2)
	exited := make(chan struct{}) // stdout closed: the process is gone or going
	go func() {
		defer readers.Done()
		defer close(exited)
		d.readReports(s, stdout)
	}()
	go func() {
		defer readers.Done()
		lines := bufio.NewScanner(stderr)
		for lines.Scan() {
			d.cfg.Logf("oww: ", s.esn, ": ", lines.Text())
		}
	}()

	// Closing stdin asks the sidecar to finish; it also unblocks a write to a sidecar that stopped reading.
	stopping := make(chan struct{})
	var closeOnce sync.Once
	shutdown := func() { closeOnce.Do(func() { stdin.Close(); close(stopping) }) }
	go func() {
		select {
		case <-s.stop:
			shutdown()
		case <-stopping:
		}
		select {
		case <-exited:
		case <-time.After(d.cfg.StopGrace):
			cmd.Process.Kill()
		}
	}()

	var written uint64
feed:
	for {
		select {
		case <-stopping:
			break feed
		case <-exited:
			break feed
		case f := <-s.frames:
			written += samplesPerFrame
			s.addMark(mark{end: written, ts: f.ts})
			if _, err := stdin.Write(f.pcm); err != nil {
				break feed
			}
		}
	}
	shutdown()
	readers.Wait()
	err := cmd.Wait()
	state := cmd.ProcessState
	cpu := state.UserTime() + state.SystemTime()
	d.cfg.Logf("oww: ", s.esn, ": sidecar exited (", describe(err), ") after ", written/micRate, " s of audio, cpu ",
		cpu.Round(time.Millisecond))
}

func describe(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

type report struct {
	Score  float64 `json:"score"`
	Sample uint64  `json:"sample"`
}

func (d *Detector) readReports(s *session, r io.Reader) {
	lines := bufio.NewScanner(r)
	for lines.Scan() {
		var rep report
		if err := json.Unmarshal(lines.Bytes(), &rep); err != nil {
			d.cfg.Logf("oww: ", s.esn, ": bad sidecar line ", lines.Text())
			continue
		}
		if rep.Score < d.cfg.Threshold {
			continue
		}
		ts, ok := s.timestampAt(rep.Sample)
		if !ok || !s.wake(ts, d.cfg.RefractoryMs) {
			continue
		}
		d.cfg.Handler.OnWake(wakeword.Detection{
			ESN:            s.esn,
			Word:           wakeword.Word{Text: d.cfg.Word, Conf: rep.Score},
			Text:           d.cfg.Word,
			FrameTimestamp: ts,
		})
	}
}

func (s *session) addMark(m mark) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.marks) == maxMarks {
		s.marks = append(s.marks[:0], s.marks[maxMarks/2:]...)
	}
	s.marks = append(s.marks, m)
}

// timestampAt returns the robot timestamp of the written frame holding input sample position pos.
func (s *session) timestampAt(pos uint64) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.marks) == 0 {
		return 0, false
	}
	i := sort.Search(len(s.marks), func(i int) bool { return s.marks[i].end >= pos })
	if i == len(s.marks) {
		i--
	}
	return s.marks[i].ts, true
}

// wake records a wake at robot time ts unless it falls inside the refractory period of the previous one.
func (s *session) wake(ts, refractoryMs uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.woke && ts < s.lastWake+refractoryMs {
		return false
	}
	s.woke, s.lastWake = true, ts
	return true
}
