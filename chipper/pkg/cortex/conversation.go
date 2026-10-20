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

// reply transcribes one utterance, asks the brain and sends its answer over the robot's MicTap session (Robot,
// Speaker); it says whether an answer was sent.
func (c *Conversation) reply(esn string, u Utterance) bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.Timeout)
	defer cancel()
	return c.replyTo(ctx, esn, u, nil, &Result{}) == Answered
}

// Result is what Respond made of one utterance, for diagnosis.
type Result struct {
	Outcome    Outcome
	Raw        string        // whisper's text
	Text       string        // what the brain got: Raw without the wake phrase
	Language   string        // whisper's language code
	Answer     string        // "intent <name>", "voice", "knowledge", or empty when nothing was answered
	Transcribe time.Duration // how long whisper took
	Think      time.Duration // how long the brain took
}

func (o Outcome) String() string {
	switch o {
	case Answered:
		return "answered"
	case NothingHeard:
		return "nothing heard"
	case Failed:
		return "failed"
	case SelfAnswered:
		return "self answered"
	}
	return fmt.Sprintf("outcome %d", int(o))
}

// Outcome is how one answer ended.
type Outcome int

const (
	Answered     Outcome = iota + 1 // the answer was sent
	NothingHeard                    // no speech, no words, only the wake phrase, or one of whisper's silence phrases
	Failed                          // the transcriber or the brain failed
	SelfAnswered                    // the brain handled the request itself; nothing was sent
)

// KnowledgeTarget is where a text answer goes: the robot says it (Knowledge), or shows its talking face for
// expected while the cortex speaks it (KnowledgeVoice).
type KnowledgeTarget interface {
	Knowledge(answer, query string) error
	KnowledgeVoice(answer, query string, expected time.Duration) error
}

// Reply is where the answer to one voice request goes instead of the robot's MicTap session: the request's own
// response (the robot woke itself and streamed its question), which takes the intent or the knowledge answer.
// The cortex's voice still reaches the robot as an audio stream over the session.
type Reply interface {
	KnowledgeTarget
	Intent(intent, paramsJSON, query string) error
}

// TargetSpeaker is a Speaker that sends its knowledge answer to a given target (the Talker).
type TargetSpeaker interface {
	SayTo(esn, answer, query, lang string, to KnowledgeTarget) error
}

// RequestCapture cuts the question out of a robot's own voice request: audio that starts at the robot's wake, at
// one rate, without timestamps. It applies the conversation's capture rules (MinSpeech of speech before the user
// counts as speaking, EndSilence after it, MaxLength from its onset, NoSpeech) through a Capture, with no margin
// before the wake and no listening prompt. The audio starts at the wake, so its first WakeGrace holds the robot's
// own earcon and get-in sound: its frames go through the Capture as heard under the half-duplex gate, and a speech
// run there needs GatedMinSpeech to start the utterance, as on the MicTap path (L110). It is not safe for
// concurrent use.
type RequestCapture struct {
	c    *Capture
	rate int
	ts   uint64 // ms of audio fed so far: the frames' synthetic robot time
	rest []int16
}

// NewRequestCapture starts the capture of one voice request whose audio comes at rate (mono).
func (c *Conversation) NewRequestCapture(rate int) *RequestCapture {
	cfg := c.cfg.Capture
	cfg.MicRate, cfg.WakeMargin, cfg.PreRoll, cfg.PromptAfter = rate, 0, 0, 0
	r := &RequestCapture{c: NewCapture(cfg, c.cfg.NewVAD()), rate: rate}
	r.c.Start(0)
	return r
}

// Feed adds samples; it returns the utterance once the capture rules end it.
func (r *RequestCapture) Feed(samples []int16) (Utterance, bool) {
	frame := r.rate / 100 // 10 ms, as the MicTap frames
	r.rest = append(r.rest, samples...)
	for frame > 0 && len(r.rest) >= frame && r.c.Capturing() {
		f := Frame{Samples: append([]int16(nil), r.rest[:frame]...), Timestamp: r.ts + 10}
		if r.c.inWakeGrace(f.Timestamp) {
			f.Real = f.Samples // the robot's wake sound: heard, but only GatedMinSpeech starts the utterance
		}
		r.ts += 10
		r.rest = r.rest[frame:]
		if u, done := r.c.Feed(f); done {
			return u, true
		}
	}
	return Utterance{}, false
}

// End ends a capture its rules have not ended (the stream ended, or the request ran out of time) with reason;
// when the user never spoke (never MinSpeech of speech in a row) the reason is EndNoSpeech.
func (r *RequestCapture) End(reason EndReason) Utterance {
	if !r.c.spoke {
		reason = EndNoSpeech
	}
	u, _ := r.c.Abort()
	u.Reason = reason
	return u
}

// Respond answers one utterance of a robot's own voice request (RequestCapture) into to, the request's reply,
// within ctx: the same transcription, wake phrase and silence filters and brain as a MicTap capture. It never
// sends anything over the robot's session but the cortex voice's audio. An utterance without speech (the user
// never spoke, or quieter than MinLevel) is nothing heard without being transcribed.
func (c *Conversation) Respond(ctx context.Context, u Utterance, to Reply) Result {
	switch {
	case u.Reason == EndNoSpeech:
		c.cfg.Logf(fmt.Sprintf("Cortex: %s nothing heard: no speech in %.1f s (%s), nothing transcribed", u.ESN,
			u.Duration().Seconds(), speech(u)))
		return Result{Outcome: NothingHeard}
	case u.Level < c.cfg.Capture.MinLevel:
		c.cfg.Logf(fmt.Sprintf("Cortex: %s nothing heard in %.1f s (%s, %s), nothing transcribed", u.ESN,
			u.Duration().Seconds(), u.Reason, speech(u)))
		return Result{Outcome: NothingHeard}
	}
	c.cfg.Logf(fmt.Sprintf("Cortex: %s heard %.1f s (%s, %s)", u.ESN, u.Duration().Seconds(), u.Reason, speech(u)))
	var r Result
	r.Outcome = c.replyTo(ctx, u.ESN, u, to, &r)
	return r
}

// replyTo transcribes one utterance, asks the brain and sends its answer to to, or over the robot's session
// (Robot, Speaker) when to is nil. It records what it heard and answered in r.
func (c *Conversation) replyTo(ctx context.Context, esn string, u Utterance, to Reply, r *Result) Outcome {
	began := c.cfg.Now()
	t, err := c.cfg.Transcriber.Transcribe(ctx, u)
	if err != nil {
		c.cfg.Logf("Cortex: ", esn, " transcription failed: ", err)
		return Failed
	}
	raw := strings.TrimSpace(t.Text)
	t.Text = stripWakePhrase(raw)
	heard := c.cfg.Now()
	r.Raw, r.Text, r.Language, r.Transcribe = raw, t.Text, t.Language, heard.Sub(began)
	if !hasWord(t.Text) && hasWord(raw) {
		c.cfg.Logf(fmt.Sprintf("Cortex: %s nothing heard after the wake phrase %q (%.2f s), nothing sent", esn, raw,
			heard.Sub(began).Seconds()))
		return NothingHeard
	}
	if !hasWord(t.Text) {
		c.cfg.Logf(fmt.Sprintf("Cortex: %s transcribed no words %q (%.2f s), nothing sent", esn, t.Text,
			heard.Sub(began).Seconds()))
		return NothingHeard
	}
	if silencePhrase(t.Text) && u.Level < c.cfg.Capture.ClearLevel {
		c.cfg.Logf(fmt.Sprintf("Cortex: %s nothing heard: %q is whisper's word for quiet audio (%s), nothing sent",
			esn, t.Text, speech(u)))
		return NothingHeard
	}
	line := fmt.Sprintf("Cortex: %s transcribed (%s, %.2f s): %s", esn, t.Language, heard.Sub(began).Seconds(), t.Text)
	if t.Text != raw {
		line += " [raw: " + raw + "]"
	}
	c.cfg.Logf(line)
	a, err := c.cfg.Brain.Answer(ctx, esn, t)
	if err != nil {
		c.cfg.Logf("Cortex: ", esn, " brain failed: ", err)
		return Failed
	}
	r.Think = c.cfg.Now().Sub(heard)
	took := r.Think.Seconds()
	if to == nil {
		c.afterSignals(esn)
	}
	switch {
	case a.Intent != "":
		r.Answer = "intent " + a.Intent
		if to != nil {
			err = to.Intent(a.Intent, a.ParamsJSON, t.Text)
		} else {
			err = c.cfg.Robot.SendIntentResult(esn, a.Intent, a.ParamsJSON)
		}
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answer intent %s %s (%.2f s), %s", esn, a.Intent, a.ParamsJSON, took, outcome(err)))
	case strings.TrimSpace(a.Text) != "" && c.cfg.Speaker != nil && (to == nil || isTargetSpeaker(c.cfg.Speaker)):
		r.Answer = "voice"
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answer %q (%.2f s), speaking it", esn, a.Text, took))
		if to != nil {
			err = c.cfg.Speaker.(TargetSpeaker).SayTo(esn, a.Text, t.Text, t.Language, to)
		} else {
			err = c.cfg.Speaker.Say(esn, a.Text, t.Text, t.Language)
		}
		if err != nil {
			c.cfg.Logf(fmt.Sprintf("Cortex: %s answer not spoken: %v", esn, err))
		}
	case strings.TrimSpace(a.Text) != "":
		r.Answer = "knowledge"
		if to != nil {
			err = to.Knowledge(a.Text, t.Text)
		} else {
			err = c.cfg.Robot.SendKnowledge(esn, a.Text, t.Text)
		}
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answer %q (%.2f s), %s", esn, a.Text, took, outcome(err)))
	default:
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answered by the brain itself (%.2f s), nothing sent", esn, took))
		return SelfAnswered
	}
	return Answered
}

func isTargetSpeaker(s Speaker) bool {
	_, ok := s.(TargetSpeaker)
	return ok
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
