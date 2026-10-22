package initwirepod

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/julianvidalm/wire-pod/chipper/pkg/logger"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
	mictapserver "github.com/julianvidalm/wire-pod/chipper/pkg/servers/mictap"
)

var (
	micTapServerOnce sync.Once
	micTapServerVal  *mictapserver.Server
	// liveAudioStatus gets the robots' audio stream reports once the conversation has a voice. The conversation
	// is built at startup (installVoiceRequests), independently of the server, so the server reads it per report.
	liveAudioStatus atomic.Pointer[func(esn string, st *mictappb.AudioStatus)]
)

// micTapServer is the one MicTap gRPC service shared by both TLS listeners, so a command finds the robot's
// session whichever listener the robot connected to. The robot's command link (robot-wake T1) sends no mic audio:
// the session carries commands and the cortex's voice down and audio stream reports up; a robot that still streams
// its mic is only logged.
func micTapServer() *mictapserver.Server {
	micTapServerOnce.Do(func() {
		micTapServerVal = mictapserver.New(mictap.NewLogSink(logger.Println))
		micTapServerVal.OnAudioStatus(func(esn string, st *mictappb.AudioStatus) {
			if f := liveAudioStatus.Load(); f != nil {
				(*f)(esn, st)
			}
		})
	})
	return micTapServerVal
}

type commandSender interface {
	Send(esn string, cmd *mictappb.Command) error
}

// micTapCommandHandler sends one command to a robot over its MicTap session, to try the robot side of the
// cortex by hand:
//
//	/api-mictap/command?serial=<esn>&intent=intent_clock_time[&params=<json>]
//	/api-mictap/command?serial=<esn>&say=<text>
func micTapCommandHandler(sender commandSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		esn, intent, say, params := r.FormValue("serial"), r.FormValue("intent"), r.FormValue("say"), r.FormValue("params")
		if esn == "" || (intent == "") == (say == "") {
			http.Error(w, "need serial and exactly one of intent or say", http.StatusBadRequest)
			return
		}
		if params != "" && !json.Valid([]byte(params)) {
			http.Error(w, "params must be JSON", http.StatusBadRequest)
			return
		}
		cmd := knowledgeCommand(say, "")
		if intent != "" {
			cmd = intentCommand(intent, params)
		}
		switch err := sender.Send(esn, cmd); {
		case errors.Is(err, mictapserver.ErrNoSession):
			http.Error(w, err.Error(), http.StatusNotFound)
		case err != nil:
			http.Error(w, fmt.Sprintf("sending to %s: %v", esn, err), http.StatusBadGateway)
		default:
			fmt.Fprintln(w, "sent")
		}
	}
}

func init() {
	http.HandleFunc("/api-mictap/command", micTapCommandHandler(sessionsSender{}))
}

// sessionsSender resolves the shared server lazily, at request time.
type sessionsSender struct{}

func (sessionsSender) Send(esn string, cmd *mictappb.Command) error {
	return micTapServer().Sessions().Send(esn, cmd)
}
