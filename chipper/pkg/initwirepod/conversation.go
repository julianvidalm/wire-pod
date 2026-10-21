package initwirepod

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/wirepodbrain"
	"github.com/julianvidalm/wire-pod/chipper/pkg/logger"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vad"
)

// The cortex conversation: its whisper, brain and voice answer the robot's own voice requests (voicerequest.go).
// The robot wakes itself ("hey vector") and streams the question to wire-pod; the conversation cuts it with its
// capture rules, transcribes it, decides the answer and speaks it over the robot's MicTap session.
const (
	conversationEnv  = "CORTEX_CONVERSATION" // on or off (the default): builds the conversation the voice requests use
	cortexVADModeEnv = "CORTEX_VAD_MODE"     // webrtc VAD aggressiveness 0-3, default 3
	cortexPersonaEnv = "CORTEX_PERSONA"      // the robot's name in the LLM prompt; unset or "-" keeps Vector (S1)
	noSpeechEnv      = "CORTEX_NO_SPEECH"    // Go duration without speech after the wake before giving up, default 9s
	captureMaxEnv    = "CORTEX_CAPTURE_MAX"  // Go duration of the longest question from its start, default 8s

	defaultVADMode = 3
	defaultPersona = "-"           // S1: the prompt's own robot, Vector
	intentDataDir  = "intent-data" // wire-pod runs in chipper
)

// CortexTranscriber builds the conversation's transcriber and describes it. The whisper.cpp program sets it
// (cmd/experimental/whisper.cpp); a build without whisper.cpp keeps the conversation off.
var CortexTranscriber func() (cortex.Transcriber, string, error)

var (
	conversationOnce sync.Once
	conversationVal  *cortex.Conversation
)

// liveConversation is the one conversation the voice requests share, or nil when off.
func liveConversation() *cortex.Conversation {
	conversationOnce.Do(func() {
		c, desc, err := newConversation(conversationDeps{
			transcriber: CortexTranscriber,
			sender:      sessionsSender{},
			logf:        logger.Println,
			audioStatus: func(f func(string, *mictappb.AudioStatus)) { liveAudioStatus.Store(&f) },
		})
		switch {
		case err != nil:
			logger.Println("Cortex conversation off:", err)
		case c == nil:
			logger.Println("Cortex conversation off (" + conversationEnv + "=on turns it on)")
		default:
			logger.Println("Cortex conversation on:", desc)
			conversationVal = c
		}
	})
	return conversationVal
}

type conversationDeps struct {
	transcriber func() (cortex.Transcriber, string, error)
	brain       cortex.Brain      // nil: wire-pod's intents and LLM
	newVAD      func() cortex.VAD // nil: webrtc with CORTEX_VAD_MODE
	sender      commandSender     // the robots' MicTap sessions, for the cortex's voice
	logf        func(a ...any)
	// voice replaces piper as the cortex's voice (tests); nil reads CORTEX_TTS.
	voice func() (cortex.Voice, string, error)
	// audioStatus registers the function that gets the robots' audio stream reports (the MicTap server's
	// OnAudioStatus); nil drops them.
	audioStatus func(func(esn string, st *mictappb.AudioStatus))
}

// newConversation reads the conversation settings; it returns nil when the conversation is off.
func newConversation(d conversationDeps) (*cortex.Conversation, string, error) {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(conversationEnv))); v {
	case "", "off":
		return nil, "", nil
	case "on":
	default:
		return nil, "", fmt.Errorf("unknown %s %q (want on or off)", conversationEnv, v)
	}
	if d.transcriber == nil {
		return nil, "", fmt.Errorf("this build has no transcriber (it needs STT_SERVICE=whisper.cpp)")
	}
	capture := cortex.DefaultCaptureConfig()
	var err error
	for env, d := range map[string]*time.Duration{noSpeechEnv: &capture.NoSpeech, captureMaxEnv: &capture.MaxLength} {
		if v := os.Getenv(env); v != "" {
			if *d, err = time.ParseDuration(v); err != nil {
				return nil, "", fmt.Errorf("%s: %w", env, err)
			}
		}
	}
	newVAD := d.newVAD
	vadDesc := "test VAD"
	if newVAD == nil {
		mode := defaultVADMode
		if v := os.Getenv(cortexVADModeEnv); v != "" {
			if mode, err = strconv.Atoi(v); err != nil {
				return nil, "", fmt.Errorf("%s: %w", cortexVADModeEnv, err)
			}
		}
		if _, err := vad.NewWebRTC(mode); err != nil {
			return nil, "", fmt.Errorf("%s: %w", cortexVADModeEnv, err)
		}
		newVAD = func() cortex.VAD {
			v, _ := vad.NewWebRTC(mode) // mode checked above
			return v
		}
		vadDesc = fmt.Sprintf("webrtc VAD mode %d", mode)
	}
	brain, brainDesc := d.brain, "test brain"
	if brain == nil {
		intents, err := wirepodbrain.LoadIntents(intentDataDir, map[string]string{"en": "en-US", "es": "es-ES"})
		if err != nil {
			return nil, "", err
		}
		persona := cortexPersona()
		brain = wirepodbrain.New(wirepodbrain.Config{
			Intents:         intents,
			DefaultLanguage: "en",
			LLM:             (&wirepodbrain.LLM{Persona: persona}).Answer,
		})
		brainDesc = "wire-pod intents (en-US, es-ES) then its LLM"
		if persona != "" {
			brainDesc += " as " + persona
		}
	}
	tr, trDesc, err := d.transcriber()
	if err != nil {
		return nil, "", err
	}
	if d.logf == nil {
		d.logf = func(...any) {}
	}
	robot := robotCommands{d.sender}
	var speaker cortex.Speaker
	talker, voiceDesc := newTalker(d.voice, robot, d.logf)
	if talker != nil {
		speaker = talker
		if d.audioStatus != nil {
			d.audioStatus(audioStatusTo(talker))
		}
	}
	c := cortex.NewConversation(cortex.Config{
		Speaker:     speaker,
		Capture:     capture,
		NewVAD:      newVAD,
		Transcriber: tr,
		Brain:       brain,
		Logf:        d.logf,
	})
	desc := fmt.Sprintf("%s, %s, %s; a question ends after %v of silence, at most %v from the speech start, "+
		"gives up after %v without speech", trDesc, brainDesc, vadDesc, capture.EndSilence, capture.MaxLength,
		capture.NoSpeech)
	if talker != nil {
		desc += "; answers spoken by the cortex: " + voiceDesc
	}
	return c, desc, nil
}

// robotCommands adapts the MicTap sessions to the cortex's voice (cortex.AudioRobot).
type robotCommands struct{ commandSender }

// SendKnowledge makes the robot say answer itself: the Talker's fallback when its voice cannot start.
func (r robotCommands) SendKnowledge(esn, answer, queryText string) error {
	return r.Send(esn, knowledgeCommand(answer, queryText))
}

func knowledgeCommand(answer, queryText string) *mictappb.Command {
	return &mictappb.Command{Command: &mictappb.Command_Knowledge{
		Knowledge: &mictappb.KnowledgeResponse{Answer: answer, QueryText: queryText},
	}}
}

func intentCommand(intent, paramsJSON string) *mictappb.Command {
	return &mictappb.Command{Command: &mictappb.Command_IntentResult{
		IntentResult: &mictappb.IntentResult{Intent: intent, ParametersJson: paramsJSON},
	}}
}

// cortexPersona is the robot's name in the LLM prompt (CORTEX_PERSONA), or "" to keep the prompt's own.
func cortexPersona() string {
	persona := envOr(cortexPersonaEnv, defaultPersona)
	if persona == "-" {
		persona = ""
	}
	return persona
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
