package initwirepod

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
	mictapserver "github.com/julianvidalm/wire-pod/chipper/pkg/servers/mictap"
)

var (
	micTapServerOnce sync.Once
	micTapServerVal  *mictapserver.Server
)

// micTapServer is the one MicTap gRPC service shared by both TLS listeners, so a command finds the robot's
// session whichever listener the robot connected to.
func micTapServer() *mictapserver.Server {
	micTapServerOnce.Do(func() { micTapServerVal = mictapserver.New(micTapSink()) })
	return micTapServerVal
}

type commandSender interface {
	Send(esn string, cmd *mictappb.Command) error
}

// micTapCommandHandler sends one command to a robot over its MicTap session, to try the robot side of the
// PC-led conversation by hand:
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
