// Package voskrec adapts Vosk (offline speech recognition) to wakeword.Recognizer.
//
// The Spanish small model (vosk-model-small-es-0.42) recognized "vector" best on Vector's own microphone in
// the T1 recordings, including in English phrases; the English model mangled Spanish-accented speech.
package voskrec

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/julianvidalm/wire-pod/chipper/pkg/wakeword"
	vosk "github.com/kercre123/vosk-api/go"
)

const sampleRate = 16000

// partialEvery asks Vosk for a partial result every this many Accept calls (10 x 10 ms frames = 100 ms):
// often enough to wake quickly, rarely enough to keep the CPU cost low.
const partialEvery = 10

// Model is a loaded Vosk model; one model serves the recognizers of every robot.
type Model struct {
	m *vosk.VoskModel
}

// modelFiles are present in every Vosk model; the binding cannot report a failed load (it keeps a NULL model
// that crashes later), so LoadModel checks for them first.
var modelFiles = []string{"am/final.mdl", "conf/model.conf"}

func LoadModel(dir string) (*Model, error) {
	for _, f := range modelFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return nil, fmt.Errorf("%s is not a Vosk model: %w", dir, err)
		}
	}
	vosk.SetLogLevel(-1)
	m, err := vosk.NewModel(dir)
	if err != nil {
		return nil, fmt.Errorf("loading vosk model %s: %w", dir, err)
	}
	return &Model{m: m}, nil
}

// NewRecognizer is a wakeword.RecognizerFactory.
func (m *Model) NewRecognizer() (wakeword.Recognizer, error) {
	rec, err := vosk.NewRecognizer(m.m, sampleRate)
	if err != nil {
		return nil, err
	}
	rec.SetWords(1)
	rec.SetPartialWords(1)
	return &recognizer{rec: rec}, nil
}

type recognizer struct {
	rec   *vosk.VoskRecognizer
	buf   []byte
	calls int
}

func (r *recognizer) Accept(pcm []int16) ([]wakeword.Word, bool, error) {
	r.buf = pcmBytes(r.buf[:0], pcm)
	if r.rec.AcceptWaveform(r.buf) != 0 {
		words, err := parseResult(r.rec.Result())
		return words, true, err
	}
	if r.calls++; r.calls%partialEvery != 0 {
		return nil, false, nil
	}
	words, err := parseResult(r.rec.PartialResult())
	return words, false, err
}

func (r *recognizer) Close() {
	r.rec.Free()
}

func pcmBytes(dst []byte, pcm []int16) []byte {
	for _, s := range pcm {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(s))
	}
	return dst
}

func parseResult(js string) ([]wakeword.Word, error) {
	type word struct {
		Word  string  `json:"word"`
		Conf  float64 `json:"conf"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	}
	var r struct {
		Result        []word `json:"result"`
		PartialResult []word `json:"partial_result"`
	}
	if err := json.Unmarshal([]byte(js), &r); err != nil {
		return nil, fmt.Errorf("vosk result: %w", err)
	}
	all := append(r.Result, r.PartialResult...)
	words := make([]wakeword.Word, len(all))
	for i, w := range all {
		words[i] = wakeword.Word{Text: w.Word, Conf: w.Conf, Start: w.Start, End: w.End}
	}
	return words, nil
}
