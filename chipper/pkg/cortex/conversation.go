package cortex

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Config wires a Conversation to its adapters.
type Config struct {
	Capture     CaptureConfig
	NewVAD      func() VAD // one VAD per request
	Transcriber Transcriber
	Brain       Brain
	Logf        func(a ...any)
	Now         func() time.Time // measures how long transcribing and answering take; defaults to time.Now
	// Speaker, when set, speaks text answers with the cortex's own voice (S7); nil makes the robot say them
	// (Reply.Knowledge).
	Speaker TargetSpeaker
}

// Conversation is the cortex's answer to the robot's own voice requests: after the robot's wake ("hey vector")
// it cuts the question out of the request's audio (RequestCapture), transcribes it, asks the brain for the answer
// and sends it to the request's reply (Respond).
type Conversation struct {
	cfg Config
}

func NewConversation(cfg Config) *Conversation {
	if cfg.Logf == nil {
		cfg.Logf = func(...any) {}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Conversation{cfg: cfg}
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

// Reply is where the answer to one voice request goes: the request's own response (the robot woke itself and
// streamed its question), which takes the intent or the knowledge answer. The cortex's voice reaches the robot as
// an audio stream over its MicTap session.
type Reply interface {
	KnowledgeTarget
	Intent(intent, paramsJSON, query string) error
}

// TargetSpeaker speaks a text answer on one robot and sends its knowledge answer to a given target (the Talker):
// answer is the reply to query, lang the language of the question (a whisper code). It returns once the answer
// was handed to the robot, by whatever voice.
type TargetSpeaker interface {
	SayTo(esn, answer, query, lang string, to KnowledgeTarget) error
}

// RequestCapture cuts the question out of a robot's own voice request: audio that starts at the robot's wake, at
// one rate, without timestamps. It applies the conversation's capture rules (MinSpeech of speech before the user
// counts as speaking, EndSilence after it, MaxLength from its onset, NoSpeech) through a Capture, with no margin
// before the wake and no listening prompt. The audio starts at the wake, so its first WakeGrace holds the robot's
// own earcon and get-in sound: its frames go through the Capture as gated frames, and a speech run there needs
// GatedMinSpeech to start the utterance (L110), or 600 ms of speech within 1 s (WindowSpeech), so a question said
// in the same breath as the wake word is not lost. It is not safe for concurrent use.
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
	// a question said in the wake's breath: 600 ms of speech within 1 s starts it, as on the robot
	cfg.WindowSpeech, cfg.SpeechWindow = 600*time.Millisecond, time.Second
	r := &RequestCapture{c: NewCapture(cfg, c.cfg.NewVAD()), rate: rate}
	r.c.Start(0)
	return r
}

// Feed adds samples; it returns the utterance once the capture rules end it.
func (r *RequestCapture) Feed(samples []int16) (Utterance, bool) {
	frame := r.rate / 100 // 10 ms frames, as the capture rules were tuned on
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
// within ctx: it transcribes it, drops the wake phrase and whisper's silence phrases and asks the brain. It never
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

// replyTo transcribes one utterance, asks the brain and sends its answer to to (the cortex's voice, when set, over
// the robot's session). It records what it heard and answered in r.
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
	switch {
	case a.Intent != "":
		r.Answer = "intent " + a.Intent
		err = to.Intent(a.Intent, a.ParamsJSON, t.Text)
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answer intent %s %s (%.2f s), %s", esn, a.Intent, a.ParamsJSON, took, outcome(err)))
	case strings.TrimSpace(a.Text) != "" && c.cfg.Speaker != nil:
		r.Answer = "voice"
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answer %q (%.2f s), speaking it", esn, a.Text, took))
		if err = c.cfg.Speaker.SayTo(esn, a.Text, t.Text, t.Language, to); err != nil {
			c.cfg.Logf(fmt.Sprintf("Cortex: %s answer not spoken: %v", esn, err))
		}
	case strings.TrimSpace(a.Text) != "":
		r.Answer = "knowledge"
		err = to.Knowledge(a.Text, t.Text)
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answer %q (%.2f s), %s", esn, a.Text, took, outcome(err)))
	default:
		c.cfg.Logf(fmt.Sprintf("Cortex: %s answered by the brain itself (%.2f s), nothing sent", esn, took))
		return SelfAnswered
	}
	return Answered
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
