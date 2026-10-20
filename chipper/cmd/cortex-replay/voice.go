package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/piper"
)

// fixedBrain answers every question with the same text.
type fixedBrain string

func (b fixedBrain) Answer(context.Context, string, cortex.Transcript) (cortex.Answer, error) {
	return cortex.Answer{Text: string(b)}, nil
}

// newReplayTalker is the cortex's voice over piper (CORTEX_TTS_* settings), speaking into rec.
func newReplayTalker(mode string, rec *recorder, logf func(a ...any)) (*cortex.Talker, error) {
	if mode != "piper" {
		return nil, fmt.Errorf("-tts %q: want piper or off", mode)
	}
	cfg, err := piper.ConfigFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	began := time.Now()
	voice, _, err := piper.NewVoice(cfg)
	if err != nil {
		return nil, err
	}
	logf(fmt.Sprintf("tts sidecar ready in %d ms", time.Since(began).Milliseconds()))
	t := cortex.NewTalker(cortex.TalkerConfig{Voice: voice, Robot: rec, RobotSay: rec.SendKnowledge, Logf: logf})
	rec.talker = t
	return t, nil
}

// voiceStream is what the fake robot saw of one audio stream.
type voiceStream struct {
	answerAt, firstAt time.Time
	chunks, bytes     int
	maxLead, maxWait  time.Duration
	lastAt            time.Time
	expected          time.Duration
}

func (r *recorder) SendKnowledgeVoice(_, answer, _ string, expected time.Duration) error {
	r.answered(fmt.Sprintf("voice %q", answer))
	r.mu.Lock()
	at := r.answerAt
	r.mu.Unlock()
	r.voiceMu.Lock()
	r.voices = append(r.voices, &voiceStream{answerAt: at, expected: expected})
	r.voiceMu.Unlock()
	return nil
}

func (r *recorder) PrepareAudio(_ string, stream uint32, _, _ int, _ time.Duration) error {
	go r.talker.Status(esn, stream, cortex.AudioStarted) // as vic-cloud does once the engine prepared it
	return nil
}

func (r *recorder) SendAudio(_ string, _ uint32, samples []byte) error {
	now := time.Now()
	r.voiceMu.Lock()
	defer r.voiceMu.Unlock()
	v := r.voices[len(r.voices)-1]
	if v.chunks == 0 {
		v.firstAt = now
	} else if w := now.Sub(v.lastAt); w > v.maxWait {
		v.maxWait = w
	}
	v.lastAt = now
	v.chunks++
	v.bytes += len(samples)
	played := now.Sub(v.firstAt)
	if lead := time.Duration(v.bytes/2)*time.Second/cortex.SpeechRate - played; lead > v.maxLead {
		v.maxLead = lead
	}
	return nil
}

func (r *recorder) CompleteAudio(string, uint32) error { return nil }
func (r *recorder) CancelAudio(string, uint32) error   { return nil }

// printVoice prints the timing of every stream the fake robot got.
func (r *recorder) printVoice() {
	r.voiceMu.Lock()
	defer r.voiceMu.Unlock()
	for i, v := range r.voices {
		audio := time.Duration(v.bytes/2) * time.Second / cortex.SpeechRate
		fmt.Printf("VOICE %d: answer -> first chunk %d ms; %d chunks, %.2f s of audio (expected %.2f s) sent over %.2f s; "+
			"max lead %.2f s, longest wait between chunks %d ms\n", i+1, v.firstAt.Sub(v.answerAt).Milliseconds(),
			v.chunks, audio.Seconds(), v.expected.Seconds(), v.lastAt.Sub(v.firstAt).Seconds(), v.maxLead.Seconds(),
			v.maxWait.Milliseconds())
	}
}

var _ cortex.AudioRobot = (*recorder)(nil)
