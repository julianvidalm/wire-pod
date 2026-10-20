package cortex

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// AudioState is what the robot reports about one of the cortex's audio streams (MicTap Chunk.audio_status).
type AudioState int

const (
	AudioStarted   AudioState = iota + 1 // the prepare reached the engine
	AudioCompleted                       // playback finished
	AudioCancelled                       // cancelled by the cortex, replaced, or cut short on the robot
	AudioFailed                          // the engine could not play it
	AudioOverflow                        // audio arrived too far ahead of playback; the robot cancelled it
)

func (s AudioState) String() string {
	switch s {
	case AudioStarted:
		return "started"
	case AudioCompleted:
		return "completed"
	case AudioCancelled:
		return "cancelled"
	case AudioFailed:
		return "failed"
	case AudioOverflow:
		return "overflow"
	}
	return fmt.Sprintf("state %d", int(s))
}

// AudioRobot is the Robot port's voice side: the robot shows its talking face for a knowledge answer the cortex
// speaks itself, and plays the cortex's audio stream on its speaker.
type AudioRobot interface {
	// SendKnowledgeVoice is SendKnowledge with the cortex's voice: the robot does not speak answer, it shows its
	// talking face until the audio stream ends or expected (plus a margin) passes.
	SendKnowledgeVoice(esn, answer, queryText string, expected time.Duration) error
	PrepareAudio(esn string, stream uint32, rate, volume int, expected time.Duration) error
	// SendAudio sends s16le mono samples, at most MaxAudioChunk bytes.
	SendAudio(esn string, stream uint32, samples []byte) error
	CompleteAudio(esn string, stream uint32) error
	CancelAudio(esn string, stream uint32) error
}

// MaxAudioChunk is the most bytes one audio chunk may carry (the robot engine's limit): 512 samples, 32 ms.
const MaxAudioChunk = 1024

// TalkerConfig wires a Talker.
type TalkerConfig struct {
	Voice Voice
	Robot AudioRobot
	// RobotSay makes the robot say the answer with its own voice (Robot.SendKnowledge): the fallback when the
	// cortex's voice cannot start.
	RobotSay func(esn, answer, queryText string) error
	Volume   int           // SDK playback volume 1-100; 0 means 100
	MaxLead  time.Duration // how far the stream may run ahead of playback; default 3 s (the robot overflows past ~6.2 s)
	Burst    time.Duration // audio sent at once so playback starts (the robot buffers 0.2 s first); default 0.2 s
	// StatusWait is how long, after the last chunk, the talker still waits for the robot's first report so a
	// stream that failed before playing falls back to the robot's voice; default 1.5 s.
	StatusWait time.Duration
	Timeout    time.Duration // bounds synthesizing one answer; default 2 min
	FirstID    uint32        // first stream id; 0 starts from the clock, so ids differ across restarts
	Logf       func(a ...any)
	Now        func() time.Time      // defaults to time.Now
	Sleep      func(d time.Duration) // defaults to time.Sleep
}

// Talker is the Speaker that speaks answers with the cortex's Voice on the robot's speaker (S7): it sends the
// answer with external voice, then streams the speech sentence by sentence as soon as the first one is
// synthesized, at most MaxLead ahead of playback. When the voice cannot start (synthesis fails before the first
// sentence, or the robot reports the stream failed before it started), the robot says the answer itself; when it
// fails later the stream is cancelled, never spoken twice.
type Talker struct {
	cfg  TalkerConfig
	mu   sync.Mutex
	next uint32
	open map[string]*talkStream // the stream each robot is playing
}

type talkStream struct {
	id     uint32
	status chan AudioState
}

func NewTalker(cfg TalkerConfig) *Talker {
	if cfg.Volume <= 0 || cfg.Volume > 100 {
		cfg.Volume = 100
	}
	if cfg.MaxLead <= 0 {
		cfg.MaxLead = 3 * time.Second
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 200 * time.Millisecond
	}
	if cfg.StatusWait <= 0 {
		cfg.StatusWait = 1500 * time.Millisecond
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute
	}
	if cfg.Logf == nil {
		cfg.Logf = func(...any) {}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = time.Sleep
	}
	next := cfg.FirstID
	if next == 0 {
		next = uint32(cfg.Now().Unix())
	}
	return &Talker{cfg: cfg, next: next, open: map[string]*talkStream{}}
}

// Status takes the robot's report on one of its audio streams; reports on other streams are ignored. It never
// blocks.
func (t *Talker) Status(esn string, stream uint32, state AudioState) {
	t.mu.Lock()
	s := t.open[esn]
	t.mu.Unlock()
	if s == nil || s.id != stream {
		return
	}
	select {
	case s.status <- state:
	default:
		t.cfg.Logf(fmt.Sprintf("Cortex voice: %s stream %d report %s dropped", esn, stream, state))
	}
}

func (t *Talker) start(esn string) *talkStream {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next++
	if t.next == 0 {
		t.next = 1
	}
	s := &talkStream{id: t.next, status: make(chan AudioState, 16)}
	t.open[esn] = s
	return s
}

func (t *Talker) stop(esn string, s *talkStream) {
	t.mu.Lock()
	if t.open[esn] == s {
		delete(t.open, esn)
	}
	t.mu.Unlock()
}

// Say implements Speaker.
func (t *Talker) Say(esn, answer, query, lang string) error {
	lang = SpeechLanguage(answer, lang)
	began := t.cfg.Now()
	ctx, cancel := context.WithTimeout(context.Background(), t.cfg.Timeout)
	defer cancel()
	speech := t.cfg.Voice.Speak(ctx, answer, lang)
	first, ok := <-speech.Chunks()
	if !ok {
		_, err := speech.Wait()
		t.cfg.Logf(fmt.Sprintf("Cortex voice: %s no speech (%v), the robot says it", esn, err))
		return t.cfg.RobotSay(esn, answer, query)
	}
	ready := t.cfg.Now()
	expected := expectedSpeech(first, answer, lang)
	s := t.start(esn)
	defer t.stop(esn, s)
	if err := t.cfg.Robot.SendKnowledgeVoice(esn, answer, query, expected); err != nil {
		return err
	}
	if err := t.cfg.Robot.PrepareAudio(esn, s.id, SpeechRate, t.cfg.Volume, expected); err != nil {
		return err
	}
	p := &playout{t: t, esn: esn, s: s}
	chunk := &first
	for {
		if err := p.send(chunk.Samples); err != nil {
			cancel()
			return p.end(answer, query, err)
		}
		next, ok := <-speech.Chunks()
		if !ok {
			break
		}
		chunk = &next
	}
	res, err := speech.Wait()
	if err != nil {
		t.cfg.Robot.CancelAudio(esn, s.id)
		t.cfg.Logf(fmt.Sprintf("Cortex voice: %s stream %d cancelled after %v of speech: %v", esn, s.id,
			samplesDuration(p.sent), err))
		return nil
	}
	if err := t.cfg.Robot.CompleteAudio(esn, s.id); err != nil {
		return err
	}
	t.cfg.Logf(fmt.Sprintf("Cortex voice: %s stream %d: %d sentences, %.2f s (expected %.2f s, %s); synthesis %d ms, "+
		"first audio %d ms after the answer, max lead %.2f s, %d gaps", esn, s.id, res.Sentences,
		res.Duration.Seconds(), expected.Seconds(), lang, ready.Sub(began).Milliseconds(),
		p.firstAt.Sub(began).Milliseconds(), p.maxLead.Seconds(), p.gaps))
	for waited := time.Duration(0); !p.started && waited < t.cfg.StatusWait; waited += 20 * time.Millisecond {
		if err := p.poll(); err != nil {
			return p.end(answer, query, err)
		}
		if !p.started {
			t.cfg.Sleep(20 * time.Millisecond)
		}
	}
	if err := p.poll(); err != nil {
		return p.end(answer, query, err)
	}
	return nil
}

// playout paces one stream's chunks against the robot's playback.
type playout struct {
	t       *Talker
	esn     string
	s       *talkStream
	t0      time.Time // when playback (of sample 0) started, as far as the cortex can tell
	firstAt time.Time
	sent    int // samples
	started bool
	maxLead time.Duration
	gaps    int
}

// streamFailed is a robot report that ends the stream.
type streamFailed struct {
	state   AudioState
	started bool // the robot had accepted the stream
}

func (e streamFailed) Error() string { return "the robot reported " + e.state.String() }

// poll takes the robot's reports so far.
func (p *playout) poll() error {
	for {
		select {
		case st := <-p.s.status:
			switch st {
			case AudioStarted:
				p.started = true
			case AudioFailed, AudioOverflow, AudioCancelled:
				return streamFailed{st, p.started}
			}
		default:
			return nil
		}
	}
}

// send streams samples in chunks of at most MaxAudioChunk bytes, each one no earlier than MaxLead before it plays.
func (p *playout) send(samples []int16) error {
	cfg := &p.t.cfg
	for len(samples) > 0 {
		n := min(len(samples), MaxAudioChunk/2)
		if err := p.poll(); err != nil {
			return err
		}
		at := samplesDuration(p.sent)
		if !p.t0.IsZero() {
			now := cfg.Now()
			if played := now.Sub(p.t0); played > at {
				// the robot ran out of audio: playback resumes with this chunk
				p.gaps++
				cfg.Logf(fmt.Sprintf("Cortex voice: %s stream %d gap of %d ms at %.2f s", p.esn, p.s.id,
					(played - at).Milliseconds(), at.Seconds()))
				p.t0 = now.Add(-at)
			}
			if wait := at - cfg.MaxLead - now.Sub(p.t0); at >= cfg.Burst && wait > 0 {
				cfg.Sleep(wait)
				if err := p.poll(); err != nil {
					return err
				}
			}
		}
		b := make([]byte, 2*n)
		for i, v := range samples[:n] {
			binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
		}
		if err := cfg.Robot.SendAudio(p.esn, p.s.id, b); err != nil {
			return err
		}
		now := cfg.Now()
		if p.t0.IsZero() {
			p.t0, p.firstAt = now, now
		}
		p.sent += n
		if lead := samplesDuration(p.sent) - now.Sub(p.t0); lead > p.maxLead {
			p.maxLead = lead
		}
		samples = samples[n:]
	}
	return nil
}

// end handles a stream that stopped early: before the robot accepted any audio the robot says the answer itself;
// after, the stream is cancelled and logged, never spoken twice.
func (p *playout) end(answer, query string, err error) error {
	cfg := &p.t.cfg
	if f, ok := err.(streamFailed); ok {
		if !f.started && f.state != AudioCancelled {
			cfg.Logf(fmt.Sprintf("Cortex voice: %s stream %d %s before it started, the robot says it", p.esn, p.s.id, f.state))
			return cfg.RobotSay(p.esn, answer, query)
		}
		cfg.Logf(fmt.Sprintf("Cortex voice: %s stream %d %s after %v of speech", p.esn, p.s.id, f.state,
			samplesDuration(p.sent)))
		return nil
	}
	cfg.Robot.CancelAudio(p.esn, p.s.id)
	cfg.Logf(fmt.Sprintf("Cortex voice: %s stream %d cancelled after %v of speech: %v", p.esn, p.s.id,
		samplesDuration(p.sent), err))
	return err
}

func samplesDuration(n int) time.Duration { return time.Duration(n) * time.Second / SpeechRate }

// minSpeechPerRune is the slowest speaking rate expectedSpeech assumes for the sentences not yet synthesized.
const minSpeechPerRune = 80 * time.Millisecond

// expectedSpeech is how long the answer will play, known after its first sentence only: exact for one sentence;
// otherwise the first sentence's duration plus the other sentences' letters at the first sentence's rate (never
// faster than minSpeechPerRune), plus a quarter. The robot keeps its talking face until the stream ends or this
// (plus a margin) passes, so a longer guess is the safer one.
func expectedSpeech(first SpeechChunk, text, lang string) time.Duration {
	firstDur := samplesDuration(len(first.Samples))
	sentences := SplitSentences(text, lang)
	if len(sentences) <= 1 {
		return firstDur
	}
	rest := 0
	for _, s := range sentences[1:] {
		rest += utf8.RuneCountInString(s)
	}
	perRune := minSpeechPerRune
	if n := utf8.RuneCountInString(first.Text); n > 0 && firstDur/time.Duration(n) > perRune {
		perRune = firstDur / time.Duration(n)
	}
	est := firstDur + time.Duration(rest)*perRune
	return (est + est/4).Round(time.Millisecond)
}

// SpeechLanguage picks the voice for an answer: Spanish when the answer reads as Spanish, English when it reads
// as English, otherwise the language the question was asked in (whisper's code; anything but es is English).
// The answer decides because it is what is spoken: the LLM may answer in another language than the question.
func SpeechLanguage(answer, question string) string {
	es, en := 0, 0
	for _, r := range answer {
		if strings.ContainsRune("ñÑ¿¡áéíóúÁÉÍÓÚ", r) {
			es += 2
		}
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(answer), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if spanishWords[w] {
			es++
		}
		if englishWords[w] {
			en++
		}
	}
	switch {
	case es >= 2 && es > 2*en:
		return "es"
	case en >= 2 && en > 2*es:
		return "en"
	case strings.HasPrefix(strings.ToLower(question), "es"):
		return "es"
	}
	return "en"
}

var spanishWords = setOf("el", "la", "los", "las", "de", "del", "que", "y", "en", "un", "una", "es", "por", "para",
	"con", "se", "su", "lo", "al", "como", "más", "pero", "son", "está", "hay", "muy", "también", "yo", "tu",
	"hola", "sí")

var englishWords = setOf("the", "an", "of", "and", "to", "in", "is", "it", "that", "for", "you", "with", "on",
	"are", "this", "be", "was", "not", "but", "have", "i", "my", "your", "what", "hello", "yes")

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

var _ Speaker = (*Talker)(nil)
