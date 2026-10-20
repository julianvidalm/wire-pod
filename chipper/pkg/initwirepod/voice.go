package initwirepod

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/piper"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
)

// The cortex's own voice (S7): CORTEX_TTS=piper speaks knowledge answers with piper on the robot's speaker; off
// (the default) lets the robot say them, as before.
const (
	ttsVolumeEnv     = "CORTEX_TTS_VOLUME" // SDK playback volume 1-100, default 100
	defaultTTSVolume = 100
)

// newTalker reads the CORTEX_TTS* settings and builds the Speaker for the conversation, or nil when the robot keeps
// its own voice. voice, when set, replaces piper (tests). A voice that cannot start leaves the robot's voice on.
func newTalker(voice func() (cortex.Voice, string, error), robot robotCommands, logf func(a ...any)) (*cortex.Talker, string) {
	if voice == nil {
		cfg, err := piper.ConfigFromEnv(os.Getenv)
		if err != nil {
			logf("Cortex voice off (the robot speaks):", err)
			return nil, ""
		}
		if !cfg.Enabled {
			return nil, ""
		}
		voice = func() (cortex.Voice, string, error) {
			v, _, err := piper.NewVoice(cfg) // the sidecar restarts itself on the next sentence after it dies
			return v, cfg.Describe(), err
		}
	}
	v, desc, err := voice()
	if err != nil {
		logf("Cortex voice off (the robot speaks):", err)
		return nil, ""
	}
	volume := defaultTTSVolume
	if s := strings.TrimSpace(os.Getenv(ttsVolumeEnv)); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			logf(fmt.Sprintf("Cortex voice: %s=%q is not 1-100, using %d", ttsVolumeEnv, s, defaultTTSVolume))
		} else {
			volume = n
		}
	}
	t := cortex.NewTalker(cortex.TalkerConfig{
		Voice:    v,
		Robot:    robot,
		RobotSay: robot.SendKnowledge,
		Volume:   volume,
		Logf:     logf,
	})
	return t, fmt.Sprintf("%s; volume %d, at most 3s ahead", desc, volume)
}

// audioStatusTo routes the robots' audio stream reports to the talker.
func audioStatusTo(t *cortex.Talker) func(esn string, st *mictappb.AudioStatus) {
	return func(esn string, st *mictappb.AudioStatus) {
		var state cortex.AudioState
		switch st.GetState() {
		case mictappb.AudioStatus_STARTED:
			state = cortex.AudioStarted
		case mictappb.AudioStatus_COMPLETED:
			state = cortex.AudioCompleted
		case mictappb.AudioStatus_CANCELLED:
			state = cortex.AudioCancelled
		case mictappb.AudioStatus_FAILED:
			state = cortex.AudioFailed
		case mictappb.AudioStatus_OVERFLOW:
			state = cortex.AudioOverflow
		default:
			return
		}
		t.Status(esn, st.GetStreamId(), state)
	}
}

func (r robotCommands) SendKnowledgeVoice(esn, answer, queryText string, expected time.Duration) error {
	cmd := knowledgeCommand(answer, queryText)
	cmd.GetKnowledge().ExternalVoice = true
	cmd.GetKnowledge().ExpectedDurationMs = uint32(expected.Milliseconds())
	return r.Send(esn, cmd)
}

func (r robotCommands) PrepareAudio(esn string, stream uint32, rate, volume int, expected time.Duration) error {
	return r.audio(esn, &mictappb.Audio{StreamId: stream, Step: &mictappb.Audio_Prepare{Prepare: &mictappb.AudioPrepare{
		SampleRate: uint32(rate), Volume: uint32(volume), ExpectedDurationMs: uint32(expected.Milliseconds())}}})
}

func (r robotCommands) SendAudio(esn string, stream uint32, samples []byte) error {
	return r.audio(esn, &mictappb.Audio{StreamId: stream, Step: &mictappb.Audio_Chunk{
		Chunk: &mictappb.AudioChunk{Samples: samples}}})
}

func (r robotCommands) CompleteAudio(esn string, stream uint32) error {
	return r.audio(esn, &mictappb.Audio{StreamId: stream, Step: &mictappb.Audio_Complete{Complete: &mictappb.AudioComplete{}}})
}

func (r robotCommands) CancelAudio(esn string, stream uint32) error {
	return r.audio(esn, &mictappb.Audio{StreamId: stream, Step: &mictappb.Audio_Cancel{Cancel: &mictappb.AudioCancel{}}})
}

func (r robotCommands) audio(esn string, a *mictappb.Audio) error {
	return r.Send(esn, &mictappb.Command{Command: &mictappb.Command_Audio{Audio: a}})
}

var _ cortex.AudioRobot = robotCommands{}
