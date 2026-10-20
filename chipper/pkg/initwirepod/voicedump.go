package initwirepod

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
)

// Voice request dumps, to tell whether the audio, the endpointing or whisper is at fault: with
// CORTEX_VOICE_DUMP_DIR set each voice request writes <esn>_<YYYYMMDD-HHMMSS.mmm>_request.wav (the whole request
// audio received, 16 kHz mono, before endpointing) and _request.json (voiceDumpRecord). The directory is capped at
// CORTEX_VOICE_DUMP_MAX_MB (default 200, 0 no limit), oldest requests first.
const (
	voiceDumpDirEnv       = "CORTEX_VOICE_DUMP_DIR"
	voiceDumpMaxMBEnv     = "CORTEX_VOICE_DUMP_MAX_MB"
	defaultVoiceDumpBytes = 200 << 20
)

// voiceDumpRecord is a request's JSON dump. Sample offsets count samples of the request's WAV.
type voiceDumpRecord struct {
	ESN            string    `json:"esn"`
	Started        time.Time `json:"started"`
	Rate           int       `json:"rate"`
	Samples        int       `json:"samples"`
	EndReason      string    `json:"end_reason"`
	UtteranceStart int       `json:"utterance_start_sample"`
	UtteranceEnd   int       `json:"utterance_end_sample"`
	SpeechOnset    int       `json:"speech_onset_sample"` // -1: the user never spoke
	VoicedMs       int64     `json:"voiced_ms"`
	LevelDBFS      *float64  `json:"level_dbfs"` // null without speech
	TranscriptRaw  string    `json:"transcript_raw"`
	Transcript     string    `json:"transcript"`
	Language       string    `json:"language"`
	Outcome        string    `json:"outcome"`
	Answer         string    `json:"answer"`   // "intent <name>", "voice", "knowledge" or empty
	Response       string    `json:"response"` // the request's one chipper response
	Error          string    `json:"error,omitempty"`
	CaptureMs      int64     `json:"capture_ms"` // from the request's start to the end of the capture
	TranscribeMs   int64     `json:"transcribe_ms"`
	ThinkMs        int64     `json:"think_ms"`
	TotalMs        int64     `json:"total_ms"` // from the request's start to the end of the answer
}

// newVoiceDumpRecord describes a request's capture: u, cut out of audio at rate, which started at began.
func newVoiceDumpRecord(esn string, began time.Time, rate int, audio []int16, u cortex.Utterance) voiceDumpRecord {
	d := voiceDumpRecord{ESN: esn, Started: began, Rate: rate, Samples: len(audio), EndReason: string(u.Reason),
		SpeechOnset: -1, VoicedMs: u.Voiced.Milliseconds()}
	if u.Start > 0 { // RequestCapture's frames are 10 ms apart from 10 ms on
		d.UtteranceStart = int(u.Start-10) * rate / 1000
	}
	d.UtteranceEnd = d.UtteranceStart + len(u.Samples)
	if u.Reason != cortex.EndNoSpeech && u.Reason != "" {
		d.SpeechOnset = int(u.Onset * time.Duration(rate) / time.Second)
	}
	if !math.IsInf(u.Level, 0) && !math.IsNaN(u.Level) && u.Voiced > 0 {
		level := math.Round(u.Level*10) / 10
		d.LevelDBFS = &level
	}
	return d
}

// answered adds what the conversation made of the utterance.
func (d *voiceDumpRecord) answered(r cortex.Result) {
	d.TranscriptRaw, d.Transcript, d.Language, d.Answer = r.Raw, r.Text, r.Language, r.Answer
	d.Outcome = r.Outcome.String()
	d.TranscribeMs, d.ThinkMs = r.Transcribe.Milliseconds(), r.Think.Milliseconds()
}

type voiceDump struct {
	dir      string
	maxBytes int64
	logf     func(a ...any)
	failed   atomic.Bool // a failure was logged; later ones are not
}

// newVoiceDump reads CORTEX_VOICE_DUMP_DIR and CORTEX_VOICE_DUMP_MAX_MB; nil when the directory is unset.
func newVoiceDump(logf func(a ...any)) *voiceDump {
	dir := os.Getenv(voiceDumpDirEnv)
	if dir == "" {
		return nil
	}
	if logf == nil {
		logf = func(...any) {}
	}
	max, err := voiceDumpMaxBytes(os.Getenv(voiceDumpMaxMBEnv))
	if err != nil {
		logf("Cortex:", err)
	}
	return &voiceDump{dir: dir, maxBytes: max, logf: logf}
}

// voiceDumpMaxBytes reads CORTEX_VOICE_DUMP_MAX_MB: empty is 200 MB, 0 is no limit, an invalid value falls back
// to 200 MB.
func voiceDumpMaxBytes(env string) (int64, error) {
	if env == "" {
		return defaultVoiceDumpBytes, nil
	}
	mb, err := strconv.ParseFloat(env, 64)
	if err != nil || mb < 0 {
		return defaultVoiceDumpBytes, fmt.Errorf("%s=%q is not a size in MB, using 200", voiceDumpMaxMBEnv, env)
	}
	return int64(mb * (1 << 20)), nil
}

// voiceDumpFile matches a dump file and captures its request: <esn>_<YYYYMMDD-HHMMSS.mmm>.
var voiceDumpFile = regexp.MustCompile(`^(.+_(\d{8}-\d{6}\.\d{3}))_request\.`)

func voiceDumpRequest(name string) (string, string, bool) {
	m := voiceDumpFile.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// write saves one request's audio and record, then deletes the oldest requests over the cap. A failure is logged
// once and never returned: the dump never disturbs the answer.
func (d *voiceDump) write(rec voiceDumpRecord, audio []int16) {
	if d == nil {
		return
	}
	id := filepath.Base(rec.ESN) + "_" + rec.Started.Format("20060102-150405.000")
	base := filepath.Join(d.dir, id+"_request")
	err := os.MkdirAll(d.dir, 0o755)
	if err == nil {
		err = mictap.WriteWAV(base+".wav", uint32(rec.Rate), audio)
	}
	if err == nil {
		var js []byte
		if js, err = json.MarshalIndent(rec, "", "  "); err == nil {
			err = os.WriteFile(base+".json", append(js, '\n'), 0o600)
		}
	}
	if err != nil {
		if d.failed.CompareAndSwap(false, true) {
			d.logf("Cortex: voice dump to ", d.dir, " failed (further failures are not logged): ", err)
		}
		return
	}
	deleted, freed := mictap.PruneOldest(d.dir, d.maxBytes, voiceDumpRequest, map[string]bool{id: true}, nil)
	if deleted > 0 {
		d.logf("Cortex: deleted ", deleted, " old voice request dumps (", freed>>20, " MB) to stay under ",
			d.maxBytes>>20, " MB")
	}
}
