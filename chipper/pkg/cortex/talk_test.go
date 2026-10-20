package cortex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// T8d (S7): the cortex speaks knowledge answers with its own voice on the robot's speaker.

// virtualClock is a clock that only moves when the talker sleeps.
type virtualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newVirtualClock() *virtualClock { return &virtualClock{now: time.Unix(1000, 0)} }

func (c *virtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *virtualClock) Sleep(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// audioEvent is one command the robot got, at virtual time at.
type audioEvent struct {
	kind     string // knowledge-voice, prepare, chunk, complete, cancel, say
	at       time.Time
	stream   uint32
	bytes    int
	expected time.Duration
	rate     int
	volume   int
	text     string
}

// fakeAudioRobot records every command; onPrepare, when set, runs after a prepare (the robot's report).
type fakeAudioRobot struct {
	clock     *virtualClock
	mu        sync.Mutex
	events    []audioEvent
	onPrepare func(stream uint32)
	onChunk   func(stream uint32, n int) // n: chunks so far
	chunkErr  error
}

func (r *fakeAudioRobot) add(e audioEvent) {
	e.at = r.clock.Now()
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *fakeAudioRobot) SendKnowledgeVoice(_, answer, _ string, expected time.Duration) error {
	r.add(audioEvent{kind: "knowledge-voice", text: answer, expected: expected})
	return nil
}

func (r *fakeAudioRobot) PrepareAudio(_ string, stream uint32, rate, volume int, expected time.Duration) error {
	r.add(audioEvent{kind: "prepare", stream: stream, rate: rate, volume: volume, expected: expected})
	if r.onPrepare != nil {
		r.onPrepare(stream)
	}
	return nil
}

func (r *fakeAudioRobot) SendAudio(_ string, stream uint32, samples []byte) error {
	r.add(audioEvent{kind: "chunk", stream: stream, bytes: len(samples)})
	if r.onChunk != nil {
		r.onChunk(stream, len(r.kinds("chunk")))
	}
	return r.chunkErr
}

func (r *fakeAudioRobot) CompleteAudio(_ string, stream uint32) error {
	r.add(audioEvent{kind: "complete", stream: stream})
	return nil
}

func (r *fakeAudioRobot) CancelAudio(_ string, stream uint32) error {
	r.add(audioEvent{kind: "cancel", stream: stream})
	return nil
}

func (r *fakeAudioRobot) say(_, answer, _ string) error {
	r.add(audioEvent{kind: "say", text: answer})
	return nil
}

func (r *fakeAudioRobot) kinds(only ...string) []audioEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []audioEvent
	for _, e := range r.events {
		if len(only) == 0 || e.kind == only[0] {
			out = append(out, e)
		}
	}
	return out
}

func (r *fakeAudioRobot) sequence() string {
	var b []string
	last := ""
	for _, e := range r.kinds() {
		if e.kind == "chunk" && last == "chunk" {
			continue
		}
		b = append(b, e.kind)
		last = e.kind
	}
	return strings.Join(b, " ")
}

// scriptedVoice speaks each sentence as the given number of samples; failAt (1-based) fails that sentence.
type scriptedVoice struct {
	sentences []int
	failAt    int
	langs     []string
	mu        sync.Mutex
}

func (v *scriptedVoice) Speak(ctx context.Context, text, lang string) *Speech {
	v.mu.Lock()
	v.langs = append(v.langs, lang)
	v.mu.Unlock()
	s := &Speech{chunks: make(chan SpeechChunk, len(v.sentences)), done: make(chan struct{})}
	parts := SplitSentences(text, lang)
	go func() {
		defer close(s.done)
		defer close(s.chunks)
		for i, n := range v.sentences {
			if i+1 == v.failAt {
				s.err = errors.New("piper died")
				return
			}
			pcm := make([]int16, n)
			for j := range pcm {
				pcm[j] = int16(j%200 - 100)
			}
			txt := ""
			if i < len(parts) {
				txt = parts[i]
			}
			s.chunks <- SpeechChunk{Index: i, Text: txt, Samples: pcm}
			s.res.Sentences++
			s.res.Duration += samplesDuration(n)
		}
	}()
	return s
}

func newTestTalker(v Voice, r *fakeAudioRobot) *Talker {
	return NewTalker(TalkerConfig{Voice: v, Robot: r, RobotSay: r.say, FirstID: 40, Now: r.clock.Now,
		Sleep: r.clock.Sleep})
}

// started makes the fake robot report the stream started right after its prepare, like vic-cloud.
func started(tk **Talker) func(uint32) {
	return func(id uint32) { (*tk).Status("esn1", id, AudioStarted) }
}

const twoSentences = "Son las tres de la tarde. Hoy hace sol en la ciudad."

func TestTheAnswerGoesWithExternalVoiceThenPrepareChunksComplete(t *testing.T) {
	r := &fakeAudioRobot{clock: newVirtualClock()}
	var tk *Talker
	r.onPrepare = started(&tk)
	tk = newTestTalker(&scriptedVoice{sentences: []int{16000, 24000}}, r)
	if err := tk.Say("esn1", twoSentences, "¿qué hora es?", "es"); err != nil {
		t.Fatal(err)
	}
	if got := r.sequence(); got != "knowledge-voice prepare chunk complete" {
		t.Fatalf("commands %q", got)
	}
	ev := r.kinds()
	if ev[0].text != twoSentences || ev[0].expected <= 0 {
		t.Fatalf("knowledge %+v", ev[0])
	}
	if p := ev[1]; p.rate != 16000 || p.volume != 100 || p.expected != ev[0].expected || p.stream != 41 {
		t.Fatalf("prepare %+v", p)
	}
	total := 0
	for _, c := range r.kinds("chunk") {
		if c.stream != 41 {
			t.Fatalf("chunk of stream %d", c.stream)
		}
		total += c.bytes
	}
	if total != 2*(16000+24000) {
		t.Fatalf("%d bytes streamed, want %d", total, 2*(16000+24000))
	}
}

func TestChunksAreAtMost1024EvenBytes(t *testing.T) {
	r := &fakeAudioRobot{clock: newVirtualClock()}
	tk := newTestTalker(&scriptedVoice{sentences: []int{1001, 513}}, r)
	tk.Say("esn1", twoSentences, "", "es")
	for _, c := range r.kinds("chunk") {
		if c.bytes > 1024 || c.bytes%2 != 0 || c.bytes == 0 {
			t.Fatalf("chunk of %d bytes", c.bytes)
		}
	}
}

func TestTheStreamStaysAtMost3SecondsAheadAfterABurst(t *testing.T) {
	r := &fakeAudioRobot{clock: newVirtualClock()}
	tk := newTestTalker(&scriptedVoice{sentences: []int{5 * 16000, 5 * 16000}}, r)
	tk.Say("esn1", twoSentences, "", "es")
	chunks := r.kinds("chunk")
	t0 := chunks[0].at
	sent := time.Duration(0)
	for _, c := range chunks {
		played := c.at.Sub(t0)
		if sent < 200*time.Millisecond && played != 0 {
			t.Fatalf("chunk at %v of audio waited %v: the first 0.2 s must go at once", sent, played)
		}
		if lead := sent + samplesDuration(c.bytes/2) - played; lead > 3*time.Second+32*time.Millisecond {
			t.Fatalf("chunk at %v of audio sent %v after the start: %v ahead", sent, played, lead)
		}
		sent += samplesDuration(c.bytes / 2)
	}
	if last := chunks[len(chunks)-1].at.Sub(t0); last < 6*time.Second {
		t.Fatalf("10 s of audio all sent within %v: not paced", last)
	}
}

func TestTheFirstFifthOfASecondGoesAtOnceEvenWithASmallLead(t *testing.T) {
	r := &fakeAudioRobot{clock: newVirtualClock()}
	tk := newTestTalker(&scriptedVoice{sentences: []int{16000}}, r)
	tk.cfg.MaxLead = 50 * time.Millisecond
	tk.Say("esn1", "Son las tres.", "", "es")
	chunks := r.kinds("chunk")
	sent := time.Duration(0)
	for _, c := range chunks {
		if sent < 200*time.Millisecond && c.at != chunks[0].at {
			t.Fatalf("chunk at %v of audio waited %v", sent, c.at.Sub(chunks[0].at))
		}
		if lead := sent - c.at.Sub(chunks[0].at); sent >= 200*time.Millisecond && lead > 50*time.Millisecond {
			t.Fatalf("chunk at %v of audio %v ahead", sent, lead)
		}
		sent += samplesDuration(c.bytes / 2)
	}
}

func TestSynthesisFailingBeforeTheFirstSentenceFallsBackToTheRobotVoice(t *testing.T) {
	r := &fakeAudioRobot{clock: newVirtualClock()}
	tk := newTestTalker(&scriptedVoice{sentences: []int{16000}, failAt: 1}, r)
	if err := tk.Say("esn1", twoSentences, "", "es"); err != nil {
		t.Fatal(err)
	}
	if got := r.sequence(); got != "say" {
		t.Fatalf("commands %q, want only the robot saying it", got)
	}
}

func TestAStreamFailingBeforeItStartedFallsBackToTheRobotVoice(t *testing.T) {
	for _, state := range []AudioState{AudioFailed, AudioOverflow} {
		r := &fakeAudioRobot{clock: newVirtualClock()}
		var tk *Talker
		r.onPrepare = func(id uint32) { tk.Status("esn1", id, state) }
		tk = newTestTalker(&scriptedVoice{sentences: []int{16000, 16000}}, r)
		if err := tk.Say("esn1", twoSentences, "", "es"); err != nil {
			t.Fatal(err)
		}
		if got := r.sequence(); got != "knowledge-voice prepare say" {
			t.Fatalf("%s: commands %q, want the robot to say it after the failed prepare", state, got)
		}
	}
}

func TestAShortAnswerThatFailsBeforeStartingStillFallsBack(t *testing.T) {
	// The whole answer is sent before the robot's report arrives.
	r := &fakeAudioRobot{clock: newVirtualClock()}
	var tk *Talker
	r.onChunk = func(id uint32, n int) {
		if n == 1 {
			go func() { time.Sleep(10 * time.Millisecond); tk.Status("esn1", id, AudioFailed) }()
		}
	}
	tk = newTestTalker(&scriptedVoice{sentences: []int{8000}}, r)
	tk.cfg.Sleep = func(d time.Duration) { r.clock.Sleep(d); time.Sleep(d) }
	tk.Say("esn1", "Son las tres.", "", "es")
	if got := r.sequence(); got != "knowledge-voice prepare chunk complete say" {
		t.Fatalf("commands %q", got)
	}
}

func TestSynthesisFailingMidStreamCancelsWithoutTheRobotVoice(t *testing.T) {
	r := &fakeAudioRobot{clock: newVirtualClock()}
	var tk *Talker
	r.onPrepare = started(&tk)
	tk = newTestTalker(&scriptedVoice{sentences: []int{16000, 16000}, failAt: 2}, r)
	tk.Say("esn1", twoSentences, "", "es")
	if got := r.sequence(); got != "knowledge-voice prepare chunk cancel" {
		t.Fatalf("commands %q, want the stream cancelled and nothing said", got)
	}
}

func TestARobotFailureAfterTheStartStopsWithoutTheRobotVoice(t *testing.T) {
	r := &fakeAudioRobot{clock: newVirtualClock()}
	var tk *Talker
	r.onPrepare = started(&tk)
	r.onChunk = func(id uint32, n int) {
		if n == 200 {
			tk.Status("esn1", id, AudioOverflow)
		}
	}
	tk = newTestTalker(&scriptedVoice{sentences: []int{5 * 16000, 5 * 16000}}, r)
	tk.Say("esn1", twoSentences, "", "es")
	if got := r.sequence(); got != "knowledge-voice prepare chunk" {
		t.Fatalf("commands %q, want the stream to stop", got)
	}
	if n := len(r.kinds("chunk")); n > 201 {
		t.Fatalf("%d chunks sent after the overflow at 200", n)
	}
}

func TestReportsOnOtherStreamsAreIgnored(t *testing.T) {
	r := &fakeAudioRobot{clock: newVirtualClock()}
	var tk *Talker
	r.onPrepare = func(id uint32) { tk.Status("esn1", id-1, AudioFailed); tk.Status("esn2", id, AudioFailed) }
	tk = newTestTalker(&scriptedVoice{sentences: []int{8000}}, r)
	tk.cfg.StatusWait = time.Millisecond
	tk.Say("esn1", "Son las tres.", "", "es")
	if got := r.sequence(); got != "knowledge-voice prepare chunk complete" {
		t.Fatalf("commands %q", got)
	}
}

func TestTheExpectedDurationIsExactForOneSentenceAndGenerousForMore(t *testing.T) {
	one := expectedSpeech(SpeechChunk{Text: "Son las tres.", Samples: make([]int16, 16000)}, "Son las tres.", "es")
	if one != time.Second {
		t.Fatalf("one sentence: %v, want 1s", one)
	}
	first := "Son las tres de la tarde." // 25 letters in 1.5 s: 60 ms each, below the 80 ms floor
	more := expectedSpeech(SpeechChunk{Text: first, Samples: make([]int16, 24000)}, twoSentences, "es")
	if want := time.Duration(float64(1500*time.Millisecond+26*80*time.Millisecond+300*time.Millisecond) * 1.25); more != want.Round(time.Millisecond) {
		t.Fatalf("two sentences: %v, want %v", more, want)
	}
}

func TestTheVoiceFollowsTheAnswerLanguage(t *testing.T) {
	for _, tc := range []struct{ answer, question, want string }{
		{"Son las tres de la tarde.", "es", "es"},
		{"Son las tres de la tarde.", "en", "es"},
		{"It is three in the afternoon.", "es", "en"},
		{"It is three in the afternoon.", "en", "en"},
		{"OK.", "es", "es"},
		{"OK.", "fr", "en"},
		{"OK.", "", "en"},
	} {
		if got := SpeechLanguage(tc.answer, tc.question); got != tc.want {
			t.Errorf("%q asked in %q: %s, want %s", tc.answer, tc.question, got, tc.want)
		}
	}
	r := &fakeAudioRobot{clock: newVirtualClock()}
	v := &scriptedVoice{sentences: []int{8000}}
	newTestTalker(v, r).Say("esn1", "It is three in the afternoon.", "", "es")
	if fmt.Sprint(v.langs) != "[en]" {
		t.Fatalf("voice languages %v", v.langs)
	}
}

// With a Speaker the conversation speaks text answers through it; intent answers keep their robot behavior.
type recordingSpeaker struct{ said chan string }

func (s recordingSpeaker) Say(esn, answer, query, lang string) error {
	s.said <- fmt.Sprintf("%s %q for %q in %s", esn, answer, query, lang)
	return nil
}

func TestTheConversationSpeaksTextAnswersThroughTheSpeaker(t *testing.T) {
	r := newFakeRobot()
	sp := recordingSpeaker{said: make(chan string, 2)}
	c := newTestConversation(&fakeTranscriber{text: "¿cómo estás?"}, &fakeBrain{answer: Answer{Text: "Bien."}}, r, &logLines{})
	c.cfg.Speaker = sp
	speak(c, "esn1", 0)
	select {
	case got := <-sp.said:
		if !strings.Contains(got, `"Bien." for "¿cómo estás?"`) {
			t.Fatalf("said %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing said")
	}
	r.none(t)

	c = newTestConversation(&fakeTranscriber{text: "¿qué hora es?"}, &fakeBrain{answer: Answer{Intent: "intent_clock_time"}}, r, &logLines{})
	c.cfg.Speaker = sp
	speak(c, "esn1", 0)
	if got := r.next(t); got != "intent esn1 intent_clock_time " {
		t.Fatalf("intent answer %q", got)
	}
	if len(sp.said) != 0 {
		t.Fatal("an intent answer went to the speaker")
	}
}
