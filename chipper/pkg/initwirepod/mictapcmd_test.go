package initwirepod

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap/mictappb"
	mictapserver "github.com/kercre123/wire-pod/chipper/pkg/servers/mictap"
)

type recordingSender struct {
	esn string
	cmd *mictappb.Command
	err error
}

func (r *recordingSender) Send(esn string, cmd *mictappb.Command) error {
	r.esn, r.cmd = esn, cmd
	return r.err
}

func call(t *testing.T, s commandSender, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	micTapCommandHandler(s)(rec, httptest.NewRequest(http.MethodGet, "/api-mictap/command?"+query.Encode(), nil))
	return rec
}

func TestCommandEndpointSendsAnIntent(t *testing.T) {
	s := &recordingSender{}
	rec := call(t, s, url.Values{"serial": {"0dd1b652"}, "intent": {"intent_clock_time"}, "params": {`{"x":1}`}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	r := s.cmd.GetIntentResult()
	if s.esn != "0dd1b652" || r.GetIntent() != "intent_clock_time" || r.GetParametersJson() != `{"x":1}` {
		t.Fatalf("sent %q %v", s.esn, s.cmd)
	}
}

func TestCommandEndpointSendsTextToSay(t *testing.T) {
	s := &recordingSender{}
	rec := call(t, s, url.Values{"serial": {"0dd1b652"}, "say": {"hola, soy Vector"}})
	if rec.Code != http.StatusOK || s.cmd.GetKnowledge().GetAnswer() != "hola, soy Vector" {
		t.Fatalf("status %d, sent %v", rec.Code, s.cmd)
	}
}

func TestCommandEndpointRejectsBadRequests(t *testing.T) {
	cases := map[string]url.Values{
		"no serial":           {"intent": {"intent_clock_time"}},
		"no intent or say":    {"serial": {"0dd1b652"}},
		"both intent and say": {"serial": {"0dd1b652"}, "intent": {"intent_clock_time"}, "say": {"hi"}},
		"params not JSON":     {"serial": {"0dd1b652"}, "intent": {"intent_clock_time"}, "params": {"{oops"}},
	}
	for name, q := range cases {
		s := &recordingSender{}
		if rec := call(t, s, q); rec.Code != http.StatusBadRequest || s.cmd != nil {
			t.Errorf("%s: status %d, sent %v", name, rec.Code, s.cmd)
		}
	}
}

func TestCommandEndpointReportsARobotWithoutSession(t *testing.T) {
	s := &recordingSender{err: mictapserver.ErrNoSession}
	if rec := call(t, s, url.Values{"serial": {"0dd1b652"}, "say": {"hi"}}); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	s = &recordingSender{err: errors.New("transport closed")}
	if rec := call(t, s, url.Values{"serial": {"0dd1b652"}, "say": {"hi"}}); rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
}
