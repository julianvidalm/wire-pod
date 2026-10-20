package cortex

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Config wires a Conversation to its adapters.
type Config struct {
	Capture     CaptureConfig
	NewVAD      func() VAD    // one VAD per robot stream
	Echo        EchoCanceller // nil: PassThrough
	Transcriber Transcriber
	Brain       Brain
	Robot       Robot
	Logf        func(a ...any)
	Now         func() time.Time // measures how long transcribing and answering take; defaults to time.Now
	// Queue is how many captured utterances may wait per robot while one is being answered; more are dropped.
	Queue int
	// Timeout bounds transcribing and answering one utterance.
	Timeout time.Duration
	// EndIntent is sent to the robot as an intent result when a capture ends without an answer (no speech,
	// nothing heard, an error, a dropped utterance), so it stops listening (L63). Empty sends nothing. It is
	// skipped when a newer wake already started a capture (L67), and goes through SignalThrough's Signaler.
	EndIntent string
	// PromptIntent is sent, the same way, when nobody has spoken Capture.PromptAfter after the wake: the robot
	// shows it is still listening (S6). Empty sends nothing.
	PromptIntent string
	// ThinkingIntent is sent, the same way, when a capture ends with speech, before it is transcribed: the
	// robot shows its thinking face until the answer (L80). Empty sends nothing.
	ThinkingIntent string
	// Speaker, when set, speaks text answers with the cortex's own voice (S7); nil makes the robot say them
	// (Robot.SendKnowledge).
	Speaker Speaker
}

// Speaker speaks a text answer on one robot: answer is the reply to query, lang the language of the question
// (a whisper code). It returns once the answer was handed to the robot, by whatever voice.
type Speaker interface {
	Say(esn, answer, query, lang string) error
}

// Signaler sends an intent to a robot after the sends already queued for it (the attend), unless stale says, when
// its turn comes, that it no longer applies. It must not block. The Attender is one.
type Signaler interface {
	Signal(esn, intent string, stale func() bool)
}

// sentWaiter is a Signaler that can say when the signals queued so far were sent (the Attender): answers wait for
// it, so the thinking face never comes after the answer.
type sentWaiter interface {
	Sent(esn string) <-chan struct{}
}

// signalWait bounds how long an answer waits for the signals ahead of it.
const signalWait = 2 * time.Second

// afterSignals waits until the signals queued for the robot so far were sent, at most signalWait.
func (c *Conversation) afterSignals(esn string) {
	c.mu.Lock()
	w, ok := c.signals.(sentWaiter)
	c.mu.Unlock()
	if !ok {
		return
	}
	select {
	case <-w.Sent(esn):
	case <-time.After(signalWait):
		c.cfg.Logf("Cortex: ", esn, " signals still not sent after ", signalWait, ", answering anyway")
	}
}

// Conversation is the cortex's listening loop: after a wake it captures what the user says from the robot's
// mic stream, transcribes it, asks the brain for the answer and sends it to the robot.
//
// Feed and Wake never wait for the transcriber, the brain or the robot: each robot has one worker that answers
// its utterances in order, behind a bounded queue that drops (and logs) what does not fit.
type Conversation struct {
	cfg     Config
	mu      sync.Mutex
	streams map[string]*stream
	workers map[string]*worker
	wakes   map[string]uint64 // wakes that started a capture, per robot
	signals Signaler          // nil: the end intent goes straight to the Robot
}

// stream is one robot's mic stream: its pre-roll and capture.
type stream struct {
	mu      sync.Mutex
	capture *Capture
	wake    uint64 // the wake of the capture in progress
}

type worker struct {
	queue   chan Utterance
	dropped uint64 // only touched under the stream's lock

	mu sync.Mutex
	// endAfter is the wake of the newest dropped utterance: its end is sent once the utterances ahead of it are
	// done, unless one of them was answered (any answer ends the robot's listening).
	endAfter uint64
}

func NewConversation(cfg Config) *Conversation {
	if cfg.Echo == nil {
		cfg.Echo = PassThrough{}
	}
	if cfg.Logf == nil {
		cfg.Logf = func(...any) {}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Queue <= 0 {
		cfg.Queue = 1
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Conversation{cfg: cfg, streams: map[string]*stream{}, workers: map[string]*worker{},
		wakes: map[string]uint64{}}
}

// Open starts listening to a new stream from the robot, forgetting any previous one.
func (c *Conversation) Open(esn string) {
	s := &stream{capture: NewCapture(c.cfg.Capture, c.cfg.NewVAD())}
	c.mu.Lock()
	c.streams[esn] = s
	c.mu.Unlock()
}

// stream returns the robot's current stream, opening one if the audio source did not.
func (c *Conversation) stream(esn string) *stream {
	c.mu.Lock()
	s := c.streams[esn]
	c.mu.Unlock()
	if s == nil {
		c.Open(esn)
		return c.stream(esn)
	}
	return s
}

// Feed hands the conversation one mic frame. It is called on the robot's mic stream and never blocks on
// anything but the robot's own capture.
func (c *Conversation) Feed(esn string, f Frame) {
	s := c.stream(esn)
	f = c.cfg.Echo.Cancel(esn, f)
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, done := s.capture.Feed(f); done {
		u.Wake = s.wake
		c.finished(esn, u)
	} else if s.capture.PromptDue() {
		c.signal(esn, c.cfg.PromptIntent, s.wake, "listening prompt")
	}
}

// Wake starts capturing what the user says after the wake word whose end the robot heard at robot time ts. A
// wake while a capture is already running is ignored. It is safe to call from a detector's goroutine.
func (c *Conversation) Wake(esn string, ts uint64) {
	s := c.stream(esn)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.capture.Capturing() {
		c.cfg.Logf("Cortex: ", esn, " already listening, wake ignored")
		return
	}
	c.mu.Lock()
	c.wakes[esn]++
	s.wake = c.wakes[esn]
	c.mu.Unlock()
	u, done := s.capture.Start(ts)
	c.cfg.Logf(fmt.Sprintf("Cortex: %s listening from %v before the wake", esn, c.cfg.Capture.WakeMargin))
	if done {
		u.Wake = s.wake
		c.finished(esn, u)
	}
}

// Close stops listening to the robot's stream; a capture in progress is dropped.
func (c *Conversation) Close(esn string) {
	c.mu.Lock()
	s := c.streams[esn]
	delete(c.streams, esn)
	c.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.capture.Abort(); ok {
		u.Wake = s.wake
		c.finished(esn, u)
	}
}

// finished hands a completed capture to the robot's worker without waiting. Called with the stream locked.
func (c *Conversation) finished(esn string, u Utterance) {
	u.ESN = esn
	switch {
	case u.Reason == EndNoSpeech:
		c.cfg.Logf(fmt.Sprintf("Cortex: %s nothing heard: no speech after %.1f s, nothing sent", esn, u.Duration().Seconds()))
		c.endListening(esn, u.Wake)
		return
	case u.Reason == EndStream:
		c.cfg.Logf(fmt.Sprintf("Cortex: %s %s after %.1f s, nothing sent", esn, u.Reason, u.Duration().Seconds()))
		c.endListening(esn, u.Wake)
		return
	case u.Level < c.cfg.Capture.MinLevel:
		c.cfg.Logf(fmt.Sprintf("Cortex: %s nothing heard in %.1f s (%s, %s), nothing sent", esn,
			u.Duration().Seconds(), u.Reason, speech(u)))
		c.endListening(esn, u.Wake)
		return
	}
	c.cfg.Logf(fmt.Sprintf("Cortex: %s heard %.1f s (%s, %s)", esn, u.Duration().Seconds(), u.Reason, speech(u)))
	w := c.worker(esn)
	select {
	case w.queue <- u:
	default:
		w.dropped++
		c.cfg.Logf(fmt.Sprintf("Cortex: %s still answering, utterance dropped (%d dropped)", esn, w.dropped))
		w.mu.Lock()
		w.endAfter = u.Wake
		w.mu.Unlock()
	}
}

func (c *Conversation) worker(esn string) *worker {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.workers[esn]
	if w == nil {
		w = &worker{queue: make(chan Utterance, c.cfg.Queue)}
		c.workers[esn] = w
		go c.run(esn, w)
	}
	return w
}

func (c *Conversation) run(esn string, w *worker) {
	for u := range w.queue {
		answered := c.answer(esn, u)
		w.mu.Lock()
		if answered {
			w.endAfter = 0
		}
		end := uint64(0)
		if len(w.queue) == 0 {
			end, w.endAfter = w.endAfter, 0
		}
		w.mu.Unlock()
		if end != 0 {
			c.endListening(esn, end)
		}
	}
}

// answer shows the robot is thinking, transcribes one utterance, asks the brain and sends the answer to the
// robot; without an answer it ends the robot's listening.
// It says whether an answer was sent.
func (c *Conversation) answer(esn string, u Utterance) bool {
	c.signal(esn, c.cfg.ThinkingIntent, u.Wake, "thinking")
	if c.reply(esn, u) {
		return true
	}
	c.endListening(esn, u.Wake)
	return false
}

// SignalThrough sends the end intent through s (the Attender), in order with the attends. Call it before the
// first wake.
func (c *Conversation) SignalThrough(s Signaler) {
	c.mu.Lock()
	c.signals = s
	c.mu.Unlock()
}

// endListening tells the robot that the capture of wake ended without an answer (Config.EndIntent), unless a
// newer wake already started a capture: the robot is listening for that one now (L67). It never blocks.
func (c *Conversation) endListening(esn string, wake uint64) {
	c.signal(esn, c.cfg.EndIntent, wake, "listening end")
}

// signal sends intent (what names it in the log) for the capture of wake, unless it is empty or a newer wake
// already started a capture. It never blocks.
func (c *Conversation) signal(esn, intent string, wake uint64, what string) {
	if intent == "" {
		return
	}
	stale := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.wakes[esn] != wake
	}
	c.mu.Lock()
	signals := c.signals
	c.mu.Unlock()
	if signals != nil {
		signals.Signal(esn, intent, stale)
		return
	}
	go func() {
		if stale() {
			c.cfg.Logf(fmt.Sprintf("Cortex: %s %s skipped: a newer wake is listening", esn, what))
			return
		}
		err := c.cfg.Robot.SendIntentResult(esn, intent, "")
		c.cfg.Logf(fmt.Sprintf("Cortex: %s %s %s, %s", esn, what, intent, outcome(err)))
	}()
}

// reply transcribes one utterance, asks the brain and sends its answer; it says whether an answer was sent.
func (c *Conversation) reply(esn string, u Utterance) bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.Timeout)
	defer cancel()
	began := c.cfg.Now()
	t, err := c.cfg.Transcriber.Transcribe(ctx, u)
	if err != nil {
		c.cfg.Logf("Cortex: ", esn, " transcription failed: ", err)
		return false
	}
	raw := strings.TrimSpace(t.Text)
	t.Text = stripWakePhrase(raw)
	heard := c.cfg.Now()
	if !hasWord(t.Text) && hasWord(raw) {
		c.cfg.Logf(fmt.Sprintf("Cortex: %s nothing heard after the wake phrase %q (%.2f s), nothing sent", esn, raw,
			heard.Sub(began).Seconds()))
		return false
	}
	if !hasWord(t.Text) {
		c.cfg.Logf(fmt.Sprintf("Cortex: %s transcribed no words %q (%.2f s), nothing sent", esn, t.Text,
			heard.Sub(began).Seconds()))
		return false
	}
	if silencePhrase(t.Text) && u.Level < c.cfg.Capture.ClearLevel {
		c.cfg.Logf(fmt.Sprintf("Cortex: %s nothing heard: %q is whisper's word for quiet audio (%s), nothing sent",
			esn, t.Text, speech(u)))
		return false
	}
	line := fmt.Sprintf("Cortex: %s transcribed (%s, %.2f s): %s", esn, t.Language, heard.Sub(began).Seconds(), t.Text)
	if t.Text != raw {
		line += " [raw: " + raw + "]"
	}
	c.cfg.Logf(line)
	a, err := c.cfg.Brain.Answer(ctx, esn, t)
	if err != nil {
		c.cfg.Logf("Cortex: ", esn, " brain failed: ", err)
		return false
	}
	took := c.cfg.Now().Sub(heard).Seconds()
	c.afterSignals(esn)
	switch {
	case a.Intent != "":
		err = c.cfg.Robot.SendIntentResult(esn, a.Intent, a.ParamsJSON)
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answer intent %s %s (%.2f s), %s", esn, a.Intent, a.ParamsJSON, took, outcome(err)))
	case strings.TrimSpace(a.Text) != "":
		err = c.cfg.Robot.SendKnowledge(esn, a.Text, t.Text)
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answer %q (%.2f s), %s", esn, a.Text, took, outcome(err)))
	default:
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answered by the brain itself (%.2f s), nothing sent", esn, took))
		return false
	}
	return true
}

func outcome(err error) string {
	if err != nil {
		return "send failed: " + err.Error()
	}
	return "sent"
}

// hasWord says whether s holds a letter or digit; whisper writes only punctuation for sounds that are not speech.
func hasWord(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

// speech describes the speech an utterance holds after its wake.
func speech(u Utterance) string {
	return fmt.Sprintf("speech %.1f s at %.0f dBFS", u.Voiced.Seconds(), u.Level)
}

// silencePhrases are what whisper writes for quiet or noisy audio without speech: the closing lines of the
// videos it was trained on. They are compared without case or punctuation. Each is also something a user may
// really say, so it is only dropped when the speech was not clearly loud (CaptureConfig.ClearLevel).
var silencePhrases = map[string]bool{
	"thank you":              true,
	"thank you very much":    true,
	"thanks for watching":    true,
	"thank you for watching": true,
	"you":                    true,
	"bye":                    true,
	"gracias":                true,
	"gracias por ver":        true,
	"muchas gracias":         true,
}

// silencePhrase says whether text is one of whisper's silencePhrases.
func silencePhrase(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return silencePhrases[strings.Join(words, " ")]
}
