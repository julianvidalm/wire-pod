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
	mu          sync.Mutex
	ttl         time.Duration
	byESN       map[string]pendingUtterance
	lastRequest map[string]time.Time // the robot's latest voice request
	now         func() time.Time
}

// Utterance is what the robot's user said and the language it was said in ("" when not detected).
type Utterance struct {
	Text string
	Lang string
}

type pendingUtterance struct {
	Utterance
	at time.Time
}

func NewPending(ttl time.Duration) *Pending {
	return &Pending{ttl: ttl, byESN: map[string]pendingUtterance{}, lastRequest: map[string]time.Time{}, now: time.Now}
}

// Set records what the robot's user just said; a newer utterance replaces an older one.
func (p *Pending) Set(esn, text, lang string) {
	p.mu.Lock()
	p.byESN[esn] = pendingUtterance{Utterance: Utterance{Text: text, Lang: lang}, at: p.now()}
	p.mu.Unlock()
}

// Take returns the robot's pending utterance once, if it is still fresh.
func (p *Pending) Take(esn string) (Utterance, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.byESN[esn]
	delete(p.byESN, esn)
	if !ok || p.now().Sub(u.at) > p.ttl {
		return Utterance{}, false
	}
	return u.Utterance, true
}

// markRequest records that the robot just made a voice request.
func (p *Pending) markRequest(esn string) {
	p.mu.Lock()
	p.lastRequest[esn] = p.now()
	p.mu.Unlock()
}

// requestedWithin reports whether the robot made a voice request in the last d.
func (p *Pending) requestedWithin(esn string, d time.Duration) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	at, ok := p.lastRequest[esn]
	return ok && p.now().Sub(at) <= d
}

// WrapSTT returns a speech-to-text handler that answers a robot's voice request with its pending utterance
// when there is one, and otherwise transcribes the robot's audio with stt as before. setLanguage, when not
// nil, receives the pending utterance's language so its intents are matched in that language.
func WrapSTT(stt func(sr.SpeechRequest) (string, error), pending *Pending, setLanguage func(device, lang string),
	logf func(a ...any)) func(sr.SpeechRequest) (string, error) {
	if logf == nil {
		logf = func(...any) {}
	}
	return func(req sr.SpeechRequest) (string, error) {
		pending.markRequest(req.Device)
		if u, ok := pending.Take(req.Device); ok {
			logf("Bot " + req.Device + " answered with what the PC heard: " + u.Text)
			if setLanguage != nil && u.Lang != "" {
				setLanguage(req.Device, u.Lang)
			}
			return u.Text, nil
		}
		return stt(req)
	}
}
