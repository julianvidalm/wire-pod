// Package cortex is the PC side of the conversation: the robot only streams what it hears and executes the
// commands the cortex sends back.
package cortex

import (
	"sync"
	"time"
)

// Sender is the Robot port's first command: deliver an intent to one robot, as if its cloud had matched it.
type Sender interface {
	SendIntent(esn, intent string) error
}

type AttenderConfig struct {
	Intent string // intent sent on each wake, for example intent_greeting_hello
	// Refractory ignores wakes from the same robot this long after an accepted one, so the attend animation is
	// not retriggered.
	Refractory time.Duration
	Sender     Sender
	Logf       func(a ...any)
	Now        func() time.Time // defaults to time.Now
}

// Attender makes a robot react to its wake word. Attend never blocks: each robot has one worker that sends, and a
// wake that arrives while an attend is still waiting to be sent is dropped. The worker also sends the
// conversation's signals (Signal), in the order they were asked for, so an end of listening never overtakes the
// next attend (L67).
type Attender struct {
	cfg     AttenderConfig
	mu      sync.Mutex
	last    map[string]time.Time
	workers map[string]*attendWorker
}

// attendQueue bounds the sends waiting per robot; more signals are dropped and logged.
const attendQueue = 8

type attendWorker struct {
	queue   chan attendJob
	pending bool // an attend waits in the queue; guarded by Attender.mu
}

// attendJob is one send: the attend (stale nil) or a signal, skipped when stale says so at its turn.
type attendJob struct {
	intent string
	stale  func() bool
}

func NewAttender(cfg AttenderConfig) *Attender {
	if cfg.Logf == nil {
		cfg.Logf = func(...any) {}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Attender{cfg: cfg, last: map[string]time.Time{}, workers: map[string]*attendWorker{}}
}

// worker returns the robot's worker, starting it. Called with a.mu held.
func (a *Attender) worker(esn string) *attendWorker {
	w, ok := a.workers[esn]
	if !ok {
		w = &attendWorker{queue: make(chan attendJob, attendQueue)}
		a.workers[esn] = w
		go a.run(esn, w)
	}
	return w
}

// Attend asks the robot to attend, unless it already did within the refractory period. It is safe to call from a
// detector's reader goroutine.
func (a *Attender) Attend(esn string) {
	now := a.cfg.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if last, ok := a.last[esn]; ok && now.Sub(last) < a.cfg.Refractory {
		return
	}
	w := a.worker(esn)
	if w.pending {
		a.cfg.Logf("attend dropped " + esn + ": the previous send is still in flight")
		return
	}
	select {
	case w.queue <- attendJob{intent: a.cfg.Intent}:
		w.pending = true
		a.last[esn] = now
	default:
		a.cfg.Logf("attend dropped " + esn + ": the previous send is still in flight")
	}
}

// Signal sends intent to the robot after every send already queued for it, unless stale, asked when its turn
// comes, says it no longer applies. It never blocks.
func (a *Attender) Signal(esn, intent string, stale func() bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	select {
	case a.worker(esn).queue <- attendJob{intent: intent, stale: stale}:
	default:
		a.cfg.Logf("signal dropped " + esn + " " + intent + ": too many sends waiting")
	}
}

func (a *Attender) run(esn string, w *attendWorker) {
	for job := range w.queue {
		if job.stale == nil {
			a.mu.Lock()
			w.pending = false
			a.mu.Unlock()
			if err := a.cfg.Sender.SendIntent(esn, job.intent); err != nil {
				a.cfg.Logf("attend failed " + esn + " " + job.intent + ": " + err.Error())
				continue
			}
			a.cfg.Logf("attend sent " + esn + " " + job.intent)
			continue
		}
		if job.stale() {
			a.cfg.Logf("signal skipped " + esn + " " + job.intent + ": a newer wake is listening")
			continue
		}
		if err := a.cfg.Sender.SendIntent(esn, job.intent); err != nil {
			a.cfg.Logf("signal failed " + esn + " " + job.intent + ": " + err.Error())
			continue
		}
		a.cfg.Logf("signal sent " + esn + " " + job.intent)
	}
}
