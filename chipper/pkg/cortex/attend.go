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
// wake that arrives while a send is still in flight is dropped.
type Attender struct {
	cfg     AttenderConfig
	mu      sync.Mutex
	last    map[string]time.Time
	workers map[string]chan struct{}
}

func NewAttender(cfg AttenderConfig) *Attender {
	if cfg.Logf == nil {
		cfg.Logf = func(...any) {}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Attender{cfg: cfg, last: map[string]time.Time{}, workers: map[string]chan struct{}{}}
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
	work, ok := a.workers[esn]
	if !ok {
		work = make(chan struct{}, 1)
		a.workers[esn] = work
		go a.run(esn, work)
	}
	select {
	case work <- struct{}{}:
		a.last[esn] = now
	default:
		a.cfg.Logf("attend dropped " + esn + ": the previous send is still in flight")
	}
}

func (a *Attender) run(esn string, work <-chan struct{}) {
	for range work {
		if err := a.cfg.Sender.SendIntent(esn, a.cfg.Intent); err != nil {
			a.cfg.Logf("attend failed " + esn + " " + a.cfg.Intent + ": " + err.Error())
			continue
		}
		a.cfg.Logf("attend sent " + esn + " " + a.cfg.Intent)
	}
}
