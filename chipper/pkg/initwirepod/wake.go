package initwirepod

import (
	"fmt"
	"os"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/logger"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
)

// What the robot does when it hears its wake word: the cortex sends it an intent over its MicTap session.
const (
	attendIntentEnv     = "CORTEX_ATTEND_INTENT"     // intent sent on each wake; set but empty turns attend off
	attendRefractoryEnv = "CORTEX_ATTEND_REFRACTORY" // Go duration, default 3s

	defaultAttendIntent     = "intent_greeting_hello"
	defaultAttendRefractory = 3 * time.Second
)

// wakeHandler logs every detection, makes the robot attend unless attend is off and, with the conversation on,
// starts listening to what the user says next.
type wakeHandler struct {
	logf         func(a ...any)
	attender     *cortex.Attender     // nil: log only
	conversation *cortex.Conversation // nil: CORTEX_CONVERSATION is off
}

// withConversation returns the handler that also starts listening to what the user says after each wake.
func (h wakeHandler) withConversation(c *cortex.Conversation) wakeHandler {
	h.conversation = c
	return h
}

func (h wakeHandler) OnWake(d wakeword.Detection) {
	h.logf(wakeLine(d))
	if h.attender != nil {
		h.attender.Attend(d.ESN)
	}
	if h.conversation != nil {
		h.conversation.Wake(d.ESN, d.FrameTimestamp)
	}
}

func wakeLine(d wakeword.Detection) string {
	return fmt.Sprintf("Wake word: %s heard %q (confidence %.2f) in %q", d.ESN, d.Word.Text, d.Word.Conf, d.Text)
}

// newWakeHandler reads the attend settings. On an error the handler still logs every wake.
func newWakeHandler(logf func(a ...any), sender commandSender) (wakeHandler, error) {
	h := wakeHandler{logf: logf}
	intent, set := os.LookupEnv(attendIntentEnv)
	if !set {
		intent = defaultAttendIntent
	}
	if intent == "" {
		return h, nil
	}
	refractory := defaultAttendRefractory
	if v := os.Getenv(attendRefractoryEnv); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return h, fmt.Errorf("%s: %w", attendRefractoryEnv, err)
		}
		refractory = d
	}
	h.attender = cortex.NewAttender(cortex.AttenderConfig{
		Intent:     intent,
		Refractory: refractory,
		Sender:     intentSender{sender},
		Logf:       logf,
	})
	return h, nil
}

// liveWakeHandler is the handler both detectors use: it sends through the shared MicTap sessions and feeds the
// shared conversation.
func liveWakeHandler() wakeHandler {
	h, err := newWakeHandler(logger.Println, sessionsSender{})
	switch {
	case err != nil:
		logger.Println("Cortex attend off:", err)
	case h.attender == nil:
		logger.Println("Cortex attend off:", attendIntentEnv, "is empty")
	default:
		logger.Println("Cortex attend on wake:", envOr(attendIntentEnv, defaultAttendIntent))
	}
	return h.withConversation(liveConversation())
}

// intentSender adapts a MicTap command sender to the cortex Robot port.
type intentSender struct{ commandSender }

func (s intentSender) SendIntent(esn, intent string) error {
	return s.Send(esn, intentCommand(intent, ""))
}

func intentCommand(intent, paramsJSON string) *mictappb.Command {
	return &mictappb.Command{Command: &mictappb.Command_IntentResult{
		IntentResult: &mictappb.IntentResult{Intent: intent, ParametersJson: paramsJSON},
	}}
}
