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

	// The robot streams its question for at most about 6 s after the wake, and vic-cloud gives up on the request
	// 9 s after it (L6): the capture ends at voiceListenMax, and transcribing and answering at voiceAnswerBy, both
	// from the start of the request.
	voiceListenMax = 6500 * time.Millisecond
	voiceAnswerBy  = 8500 * time.Millisecond
	voiceRate      = 16000 // the request's Opus decodes to 16 kHz mono

	knowledgeBypassIntent = "intent_knowledge_response_extend_bypass"
)

// voiceRequests answers the robot's voice requests with the cortex conversation.
type voiceRequests struct {
	conv *cortex.Conversation
	logf func(a ...any)
	// listen reads the request's audio until the speech ends, the stream ends or max passes (tests replace it).
	listen func(req any, max time.Duration) ([]int16, cortex.EndReason, error)
	now    func() time.Time
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
	return &voiceRequests{conv: conv, logf: logf, listen: listenChipper, now: time.Now}, nil
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
	logger.Println(fmt.Sprintf("Cortex voice requests on: the robot's wakes are answered by the conversation; "+
		"listening at most %v, answering within %v of the request", voiceListenMax, voiceAnswerBy))
}

// Handle answers one voice request: it reads the question, has the conversation answer it into the request's
// reply and returns once the request's one response was sent (the cortex's voice may still be streaming).
func (v *voiceRequests) Handle(req any) {
	began := v.now()
	to := newChipperReply(req, began, v.logf)
	samples, reason, err := v.listen(req, voiceListenMax)
	if err != nil {
		v.logf("Cortex: ", to.esn, " voice request stream failed: ", err)
		to.end(cortex.Failed)
		return
	}
	u := v.conv.Heard(to.esn, samples, voiceRate, reason)
	v.logf(fmt.Sprintf("Cortex: %s voice request: %.1f s of audio, %s after %.2f s", to.esn, u.Duration().Seconds(),
		reason, v.now().Sub(began).Seconds()))
	ctx, cancel := context.WithDeadline(context.Background(), began.Add(voiceAnswerBy))
	done := make(chan cortex.Outcome, 1)
	go func() {
		defer cancel()
		done <- v.conv.Respond(ctx, u, to)
	}()
	select {
	case <-to.answered:
	case o := <-done:
		to.end(o)
	}
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
	r.sent = true
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

// listenChipper reads a voice request's Opus stream as 16 kHz PCM until wire-pod's VAD hears the speech end, the
// robot ends the stream, or max passes; the stream's own reader stops when the request ends.
func listenChipper(req any, max time.Duration) ([]int16, cortex.EndReason, error) {
	sreq := sr.ReqToSpeechRequest(req)
	pcm := append([]byte(nil), sreq.DecodedMicData...)
	type chunk struct {
		pcm []byte
		end bool
		err error
	}
	chunks := make(chan chunk)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			b, err := sreq.GetNextStreamChunk()
			end := false
			if err == nil {
				end, _ = sreq.DetectEndOfSpeech()
			}
			select {
			case chunks <- chunk{append([]byte(nil), b...), end, err}:
			case <-stop:
				return
			}
			if err != nil || end {
				return
			}
		}
	}()
	timer := time.NewTimer(max)
	defer timer.Stop()
	for {
		select {
		case c := <-chunks:
			pcm = append(pcm, c.pcm...)
			switch {
			case errors.Is(c.err, io.EOF):
				return sr.BytesToSamples(pcm), cortex.EndStream, nil
			case c.err != nil:
				return nil, cortex.EndStream, c.err
			case c.end:
				return sr.BytesToSamples(pcm), cortex.EndSilence, nil
			}
		case <-timer.C:
			return sr.BytesToSamples(pcm), cortex.EndMaxLen, nil
		}
	}
}

var _ cortex.Reply = (*chipperReply)(nil)
