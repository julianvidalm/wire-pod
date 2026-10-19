package initwirepod

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/mictapgate"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/wirepodbrain"
	"github.com/julianvidalm/wire-pod/chipper/pkg/logger"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vad"
)

// The PC-led conversation: after a wake the cortex captures what the user says from the MicTap stream,
// transcribes it, decides the answer and sends it to the robot over its MicTap session.
const (
	conversationEnv  = "CORTEX_CONVERSATION" // on or off (the default): off keeps attend-only wakes
	cortexMarginEnv  = "CORTEX_WAKE_MARGIN"  // Go duration the capture starts before the wake's end, default 1s (whisper hears "hey jarvis")
	cortexVADModeEnv = "CORTEX_VAD_MODE"     // webrtc VAD aggressiveness 0-3, default 3
	cortexPersonaEnv = "CORTEX_PERSONA"      // the robot's name in the LLM prompt, default Jarvis; "-" keeps Vector
	// attendEndIntentEnv is the intent sent when a capture ends without an answer, so the robot stops listening
	// (L63); set but empty sends nothing.
	attendEndIntentEnv = "CORTEX_ATTEND_END_INTENT"

	defaultAttendEndIntent = "intent_cortex_listening_end"

	defaultVADMode = 3
	defaultPersona = "Jarvis"
	intentDataDir  = "intent-data" // wire-pod runs in chipper
)

// CortexTranscriber builds the conversation's transcriber and describes it. The whisper.cpp program sets it
// (cmd/experimental/whisper.cpp); a build without whisper.cpp keeps the conversation off.
var CortexTranscriber func() (cortex.Transcriber, string, error)

var (
	conversationOnce sync.Once
	conversationVal  *cortex.Conversation
)

// liveConversation is the one conversation both TLS listeners and the wake handler share, or nil when off.
func liveConversation() *cortex.Conversation {
	conversationOnce.Do(func() {
		c, desc, err := newConversation(conversationDeps{
			transcriber: CortexTranscriber,
			sender:      sessionsSender{},
			logf:        logger.Println,
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
	sender      commandSender
	logf        func(a ...any)
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
	rate, err := envFloat(owwMicRateEnv, owwDefaultMicRate)
	if err != nil {
		return nil, "", err
	}
	capture.MicRate = int(rate)
	if v := os.Getenv(cortexMarginEnv); v != "" {
		if capture.WakeMargin, err = time.ParseDuration(v); err != nil {
			return nil, "", fmt.Errorf("%s: %w", cortexMarginEnv, err)
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
		persona := envOr(cortexPersonaEnv, defaultPersona)
		if persona == "-" {
			persona = ""
		}
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
	endIntent, set := os.LookupEnv(attendEndIntentEnv)
	if !set {
		endIntent = defaultAttendEndIntent
	}
	tr, trDesc, err := d.transcriber()
	if err != nil {
		return nil, "", err
	}
	c := cortex.NewConversation(cortex.Config{
		Capture:     capture,
		NewVAD:      newVAD,
		Transcriber: tr,
		Brain:       brain,
		Robot:       robotCommands{d.sender},
		Logf:        d.logf,
		EndIntent:   endIntent,
	})
	desc := fmt.Sprintf("%s, %s, %s; capture from %v before the wake, ends after %v of silence, at most %v, "+
		"gives up after %v without speech; mic %d Hz", trDesc, brainDesc, vadDesc, capture.WakeMargin,
		capture.EndSilence, capture.MaxLength, capture.NoSpeech, capture.MicRate)
	if endIntent != "" {
		desc += "; " + endIntent + " when nothing is answered"
	}
	return c, desc, nil
}

// robotCommands adapts the MicTap sessions to the cortex Robot port.
type robotCommands struct{ commandSender }

func (r robotCommands) SendIntentResult(esn, intent, paramsJSON string) error {
	return r.Send(esn, intentCommand(intent, paramsJSON))
}

func (r robotCommands) SendKnowledge(esn, answer, queryText string) error {
	return r.Send(esn, knowledgeCommand(answer, queryText))
}

func knowledgeCommand(answer, queryText string) *mictappb.Command {
	return &mictappb.Command{Command: &mictappb.Command_Knowledge{
		Knowledge: &mictappb.KnowledgeResponse{Answer: answer, QueryText: queryText},
	}}
}

// micTapAudio is the cortex AudioSource adapter: it hands every robot's MicTap mic frames to the conversation.
type micTapAudio struct{ conv *cortex.Conversation }

func (a micTapAudio) Start(esn string, _ mictap.Hello) { a.conv.Open(esn) }

func (a micTapAudio) Frame(esn string, f mictap.Frame) {
	if f.Type != mictap.FrameMic {
		return
	}
	a.conv.Feed(esn, cortex.Frame{
		Samples:       f.Samples(),
		Timestamp:     f.Timestamp,
		SpeakerActive: f.Flags&mictap.MicFlagSpeakerActive != 0,
	})
}

func (a micTapAudio) End(esn string, _ error) { a.conv.Close(esn) }

// micRate is the mic's true rate (OWW_MIC_RATE), or its default when the setting is invalid.
func micRate() int {
	rate, err := envFloat(owwMicRateEnv, owwDefaultMicRate)
	if err != nil || rate <= 0 {
		return owwDefaultMicRate
	}
	return int(rate)
}

// detectorSink runs the wake word detector on every stream and, with the conversation on, hands the stream to
// it first, so the pre-roll already holds each frame when the detector reports a wake in it. With half duplex
// on (hd not nil) both hear silence while the robot talks; logSink (and the recorder in it) gets the real mic.
func detectorSink(logSink, detector mictap.Sink, conv *cortex.Conversation, hd *cortex.HalfDuplexConfig) mictap.Sink {
	var listeners mictap.Sink = detector
	if conv != nil {
		listeners = mictap.Tee(micTapAudio{conv}, detector)
	}
	if hd == nil {
		if conv == nil {
			return mictap.Tee(logSink, detector)
		}
		return mictap.Tee(logSink, micTapAudio{conv}, detector)
	}
	return mictap.Tee(logSink, mictapgate.New(listeners, *hd, micRate(), logger.Println))
}
