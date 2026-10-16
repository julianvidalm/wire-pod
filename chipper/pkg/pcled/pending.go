// Package pcled runs the PC-led conversation: wire-pod hears the robot all the time (MicTap), detects
// Vector's name anywhere in what the user says, transcribes the whole utterance itself, and then wakes the
// robot. The robot answers through its normal voice request, and the speech-to-text step returns the
// utterance the PC already heard instead of transcribing the robot's (late, short) recording.
package pcled

import (
	"sync"
	"time"

	sr "github.com/kercre123/wire-pod/chipper/pkg/wirepod/speechrequest"
)

// DefaultPendingTTL bounds how long a heard utterance may answer the robot's next voice request: the robot's
// wake takes about a second, so anything older belongs to another conversation.
const DefaultPendingTTL = 10 * time.Second

// Pending holds, per robot, the utterance the PC heard and is about to answer.
type Pending struct {
	mu    sync.Mutex
	ttl   time.Duration
	byESN map[string]pendingUtterance
	now   func() time.Time
}

type pendingUtterance struct {
	text string
	at   time.Time
}

func NewPending(ttl time.Duration) *Pending {
	return &Pending{ttl: ttl, byESN: map[string]pendingUtterance{}, now: time.Now}
}

// Set records what the robot's user just said; a newer utterance replaces an older one.
func (p *Pending) Set(esn, text string) {
	p.mu.Lock()
	p.byESN[esn] = pendingUtterance{text: text, at: p.now()}
	p.mu.Unlock()
}

// Take returns the robot's pending utterance once, if it is still fresh.
func (p *Pending) Take(esn string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.byESN[esn]
	delete(p.byESN, esn)
	if !ok || p.now().Sub(u.at) > p.ttl {
		return "", false
	}
	return u.text, true
}

// WrapSTT returns a speech-to-text handler that answers a robot's voice request with its pending utterance
// when there is one, and otherwise transcribes the robot's audio with stt as before.
func WrapSTT(stt func(sr.SpeechRequest) (string, error), pending *Pending, logf func(a ...any)) func(sr.SpeechRequest) (string, error) {
	if logf == nil {
		logf = func(...any) {}
	}
	return func(req sr.SpeechRequest) (string, error) {
		if text, ok := pending.Take(req.Device); ok {
			logf("Bot " + req.Device + " answered with what the PC heard: " + text)
			return text, nil
		}
		return stt(req)
	}
}
