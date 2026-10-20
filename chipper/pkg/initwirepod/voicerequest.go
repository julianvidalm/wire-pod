package initwirepod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/logger"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vtt"
	wp "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/preqs"
	sr "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/speechrequest"
	ttr "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/ttr"
)

// The robot's own wake (T2b): after "hey vector" the robot streams the question to wire-pod as a stock voice
// request. With CORTEX_VOICE_REQUESTS=on the cortex answers it (its whisper, brain and voice) instead of wire-pod's
// STT and intent matching, with exactly one response on the request's stream (L6).
const (
	voiceRequestsEnv = "CORTEX_VOICE_REQUESTS" // on or off (the default): off keeps wire-pod's own voice requests

	// The robot streams its question for at most about 14 s after the wake (room for its '?' prompt and give-up,
	// robot-wake T3), and vic-cloud gives up on the request 20 s after it: the capture ends at voiceListenMax, and
	// transcribing and answering at voiceAnswerBy, both from the start of the request.
	voiceListenMax = 14500 * time.Millisecond
	voiceAnswerBy  = 19 * time.Second
	voiceRate      = 16000 // the request's Opus decodes to 16 kHz mono

	knowledgeBypassIntent = "intent_knowledge_response_extend_bypass"
)

// voiceRequests answers the robot's voice requests with the cortex conversation.
type voiceRequests struct {
	conv *cortex.Conversation
	logf func(a ...any)
	// audio streams the request's audio as 16 kHz PCM until the robot ends it or stop closes (tests replace it).
	audio func(req any, stop <-chan struct{}) <-chan voiceAudio
	now   func() time.Time
	dump  *voiceDump // nil: CORTEX_VOICE_DUMP_DIR unset
}

// voiceRequestsOn reads CORTEX_VOICE_REQUESTS.
func voiceRequestsOn() (bool, error) {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(voiceRequestsEnv))); v {
	case "", "off":
		return false, nil
	case "on":
		return true, nil
	default:
		return false, fmt.Errorf("unknown %s %q (want on or off)", voiceRequestsEnv, v)
	}
}

// newVoiceRequests returns the voice request handler, or nil when CORTEX_VOICE_REQUESTS is off or unset. On, it
// needs the cortex conversation (its transcriber and brain).
func newVoiceRequests(conv *cortex.Conversation, logf func(a ...any)) (*voiceRequests, error) {
	on, err := voiceRequestsOn()
	if err != nil || !on {
		return nil, err
	}
	if conv == nil {
		return nil, fmt.Errorf("%s=on needs the cortex conversation (%s=on, STT_SERVICE=whisper.cpp)",
			voiceRequestsEnv, conversationEnv)
	}
	if logf == nil {
		logf = func(...any) {}
	}
	return &voiceRequests{conv: conv, logf: logf, audio: chipperAudio, now: time.Now, dump: newVoiceDump(logf)}, nil
}

// installVoiceRequests hands wire-pod's voice requests to the cortex when CORTEX_VOICE_REQUESTS=on; otherwise
// wire-pod keeps answering them itself.
func installVoiceRequests() {
	on, err := voiceRequestsOn()
	switch {
	case err != nil:
		logger.Println("Cortex voice requests off:", err)
		return
	case !on:
		logger.Println("Cortex voice requests off (" + voiceRequestsEnv + "=on answers them)")
		return
	}
	h, err := newVoiceRequests(liveConversation(), logger.Println)
	if err != nil {
		logger.Println("Cortex voice requests off:", err)
		return
	}
	wp.VoiceRequest = h.Handle
	if h.dump != nil {
		logger.Println(fmt.Sprintf("Cortex voice requests dumped to %s (at most %d MB)", h.dump.dir, h.dump.maxBytes>>20))
	}
	logger.Println(fmt.Sprintf("Cortex voice requests on: the robot's wakes are answered by the conversation; "+
		"listening at most %v, answering within %v of the request", voiceListenMax, voiceAnswerBy))
}

// Handle answers one voice request: it reads the question, has the conversation answer it into the request's
// reply and returns once the request's one response was sent (the cortex's voice may still be streaming).
func (v *voiceRequests) Handle(req any) {
	began := v.now()
	to := newChipperReply(req, began, v.logf)
	u, heard, err := v.capture(req, to.esn)
	rec := newVoiceDumpRecord(to.esn, began, voiceRate, heard, u)
	rec.CaptureMs = v.now().Sub(began).Milliseconds()
	if err != nil {
		v.logf("Cortex: ", to.esn, " voice request stream failed: ", err)
		to.end(cortex.Failed)
		rec.Outcome, rec.Error, rec.Response = cortex.Failed.String(), err.Error(), to.response()
		rec.TotalMs = v.now().Sub(began).Milliseconds()
		v.dump.write(rec, heard)
		return
	}
	v.logf(fmt.Sprintf("Cortex: %s voice request: %.1f s of audio, %s after %.2f s", to.esn, u.Duration().Seconds(),
		u.Reason, v.now().Sub(began).Seconds()))
	ctx, cancel := context.WithDeadline(context.Background(), began.Add(voiceAnswerBy))
	done := make(chan cortex.Outcome, 1)
	go func() {
		defer cancel()
		res := v.conv.Respond(ctx, u, to)
		done <- res.Outcome
		if v.dump == nil {
			return
		}
		<-to.answered // the request's one response, sent here or by Handle
		rec.answered(res)
		rec.Response = to.response()
		rec.TotalMs = v.now().Sub(began).Milliseconds()
		v.dump.write(rec, heard)
	}()
	select {
	case <-to.answered:
	case o := <-done:
		to.end(o)
	}
}

// capture cuts the question out of the request's audio with the conversation's capture rules; the robot ending
// the stream and voiceListenMax end it too. It also returns all the audio it received.
func (v *voiceRequests) capture(req any, esn string) (cortex.Utterance, []int16, error) {
	stop := make(chan struct{})
	defer close(stop)
	c := v.conv.NewRequestCapture(voiceRate)
	audio := v.audio(req, stop)
	timer := time.NewTimer(voiceListenMax)
	defer timer.Stop()
	var u cortex.Utterance
	var heard []int16
	for done := false; !done; {
		select {
		case a, ok := <-audio:
			switch {
			case !ok:
				u, done = c.End(cortex.EndStream), true
			case a.err != nil:
				return cortex.Utterance{}, heard, a.err
			default:
				heard = append(heard, a.samples...)
				u, done = c.Feed(a.samples)
			}
		case <-timer.C:
			u, done = c.End(cortex.EndMaxLen), true
		}
	}
	u.ESN = esn
	return u, heard, nil
}

// chipperReply is one voice request's cortex.Reply: the first answer goes on the request's own stream, any later
// one is refused, so the request gets exactly one response (L6).
type chipperReply struct {
	req   any // *vtt.IntentGraphRequest or *vtt.IntentRequest
	esn   string
	began time.Time
	logf  func(a ...any)

	mu       sync.Mutex
	sent     bool
	what     string        // the response sent
	answered chan struct{} // closed once the response was sent
}

var errAlreadyAnswered = errors.New("the voice request was already answered")

func newChipperReply(req any, began time.Time, logf func(a ...any)) *chipperReply {
	r := &chipperReply{req: req, began: began, logf: logf, answered: make(chan struct{})}
	switch q := req.(type) {
	case *vtt.IntentGraphRequest:
		r.esn = q.Device
	case *vtt.IntentRequest:
		r.esn = q.Device
	}
	return r
}

// send sends the request's response with f, unless it already has one.
func (r *chipperReply) send(what string, f func() error) error {
	r.mu.Lock()
	if r.sent {
		r.mu.Unlock()
		r.logf(fmt.Sprintf("Cortex: %s voice request already answered, %s not sent", r.esn, what))
		return errAlreadyAnswered
	}
	r.sent, r.what = true, what
	r.mu.Unlock()
	err := f()
	result := "sent"
	if err != nil {
		result = "send failed: " + err.Error()
	}
	r.logf(fmt.Sprintf("Cortex: %s voice request response %s %.2f s after the request, %s", r.esn, what,
		time.Since(r.began).Seconds(), result))
	close(r.answered)
	return err
}

// response is what the request was answered with, or empty.
func (r *chipperReply) response() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.what
}

func (r *chipperReply) intent(intent, query string, params map[string]string) error {
	_, err := ttr.IntentPass(r.req, intent, query, params, len(params) > 0)
	return err
}

// Intent implements cortex.Reply: the brain's intent, with its JSON parameters as chipper's string parameters.
func (r *chipperReply) Intent(intent, paramsJSON, query string) error {
	params, err := stringParams(paramsJSON)
	if err != nil {
		r.logf(fmt.Sprintf("Cortex: %s intent %s parameters %s dropped: %v", r.esn, intent, paramsJSON, err))
	}
	return r.send(intent, func() error { return r.intent(intent, query, params) })
}

// Knowledge implements cortex.KnowledgeTarget: the robot says answer.
func (r *chipperReply) Knowledge(answer, query string) error {
	return r.send("knowledge (robot voice)", func() error {
		if ig, ok := r.req.(*vtt.IntentGraphRequest); ok {
			return ttr.KnowledgeGraphResponseIG(ig, answer, query)
		}
		return r.intent(knowledgeBypassIntent, query, knowledgeParams(answer, query))
	})
}

// KnowledgeVoice implements cortex.KnowledgeTarget: the knowledge answer with external_voice, so the robot shows
// its talking face for expected while the cortex's audio plays (the firmware types the two parameters, T2a).
func (r *chipperReply) KnowledgeVoice(answer, query string, expected time.Duration) error {
	params := knowledgeParams(answer, query)
	params["external_voice"] = "true"
	params["expected_duration_ms"] = strconv.FormatInt(expected.Milliseconds(), 10)
	return r.send("knowledge (cortex voice)", func() error { return r.intent(knowledgeBypassIntent, query, params) })
}

// end sends the response for a request the conversation did not answer: nothing heard (or answered by the brain
// itself) is intent_system_noaudio, a failure intent_system_unmatched. A request already answered gets nothing.
func (r *chipperReply) end(o cortex.Outcome) {
	r.mu.Lock()
	sent := r.sent
	r.mu.Unlock()
	if sent {
		return
	}
	intent := "intent_system_unmatched"
	if o == cortex.NothingHeard || o == cortex.SelfAnswered {
		intent = "intent_system_noaudio"
	}
	r.send(intent, func() error { return r.intent(intent, "", nil) })
}

func knowledgeParams(answer, query string) map[string]string {
	return map[string]string{"answer": answer, "answer_type": "VOICE_COMMAND", "query_text": query}
}

// stringParams turns the brain's JSON parameters into chipper's string parameters.
func stringParams(paramsJSON string) (map[string]string, error) {
	if strings.TrimSpace(paramsJSON) == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &m); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		} else {
			out[k] = fmt.Sprint(v)
		}
	}
	return out, nil
}

// voiceAudio is one chunk of a voice request's audio, or the error that ended its stream.
type voiceAudio struct {
	samples []int16
	err     error
}

// chipperAudio streams a voice request's Opus audio decoded to 16 kHz PCM; the channel closes when the robot ends
// the stream (an error other than its end is sent first) or stop closes.
func chipperAudio(req any, stop <-chan struct{}) <-chan voiceAudio {
	out := make(chan voiceAudio)
	go func() {
		defer close(out)
		send := func(c voiceAudio) bool {
			select {
			case out <- c:
				return true
			case <-stop:
				return false
			}
		}
		sreq := sr.ReqToSpeechRequest(req)
		if !send(voiceAudio{samples: sr.BytesToSamples(sreq.DecodedMicData)}) {
			return
		}
		for {
			b, err := sreq.GetNextStreamChunk()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					send(voiceAudio{err: err})
				}
				return
			}
			if !send(voiceAudio{samples: sr.BytesToSamples(b)}) {
				return
			}
		}
	}()
	return out
}

var _ cortex.Reply = (*chipperReply)(nil)
