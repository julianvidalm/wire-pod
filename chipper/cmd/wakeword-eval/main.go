// Command wakeword-eval runs the wake word detector over 16 kHz mono WAV recordings of Vector's microphone and
// prints every recognized utterance and every detection, to tune the detector without a robot.
//
//	wakeword-eval -model ~/vosk-model-small-es-0.42 [-min-conf 0.5] clip1.wav clip2.wav ...
//
// Each file is cut into MicTap mic frames (10 ms) and fed through wakeword.Detector exactly as wire-pod does
// for a live robot stream (no speaker/moving flags, so echo gating stays off).
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kercre123/wire-pod/chipper/pkg/mictap"
	"github.com/kercre123/wire-pod/chipper/pkg/wakeword"
	"github.com/kercre123/wire-pod/chipper/pkg/wakeword/voskrec"
)

func main() {
	modelDir := flag.String("model", "", "Vosk model directory (Spanish small model recommended)")
	minConf := flag.Float64("min-conf", wakeword.DefaultMinConfidence, "minimum word confidence for a detection")
	flag.Parse()
	if *modelDir == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: wakeword-eval -model <vosk model dir> [-min-conf 0.5] <file.wav>...")
		os.Exit(2)
	}
	model, err := voskrec.LoadModel(*modelDir)
	if err != nil {
		fail(err)
	}
	total := 0
	for _, path := range flag.Args() {
		n, err := evaluate(model, *minConf, path)
		if err != nil {
			fail(err)
		}
		total += n
	}
	fmt.Printf("\n%d detections in %d files\n", total, flag.NArg())
}

// printingRecognizer shows every utterance the recognizer finalizes, matched or not.
type printingRecognizer struct {
	wakeword.Recognizer
}

func (p printingRecognizer) Accept(pcm []int16) ([]wakeword.Word, bool, error) {
	words, final, err := p.Recognizer.Accept(pcm)
	if final && len(words) > 0 {
		fmt.Printf("  [%6.2fs] heard:", words[0].Start)
		for _, w := range words {
			fmt.Printf(" %s(%.2f)", w.Text, w.Conf)
		}
		fmt.Println()
	}
	return words, final, err
}

type countingHandler struct{ n int }

func (h *countingHandler) OnWake(d wakeword.Detection) {
	if d.Final {
		fmt.Printf("  UTTERANCE %.1fs of audio for %q\n", float64(len(d.Utterance))/16000, d.Text)
		return
	}
	h.n++
	at := float64(d.FrameTimestamp) / 1000
	fmt.Printf("  WAKE at %.2fs (%.2fs after the word ended): %q (conf %.2f) in %q\n",
		at, at-d.Word.End, d.Word.Text, d.Word.Conf, d.Text)
}

func evaluate(model *voskrec.Model, minConf float64, path string) (int, error) {
	samples, err := readWAV(path)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	fmt.Printf("%s (%.1f s)\n", filepath.Base(path), float64(len(samples))/16000)
	h := &countingHandler{}
	det := wakeword.New(wakeword.Config{
		NewRecognizer: func() (wakeword.Recognizer, error) {
			r, err := model.NewRecognizer()
			return printingRecognizer{r}, err
		},
		Matcher:    wakeword.NewMatcher(wakeword.DefaultWords, minConf),
		Handler:    h,
		CooldownMs: 1500,
		Logf:       func(a ...any) { fmt.Println(a...) },
	})
	det.Start("eval", mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	// Trailing silence lets the recognizer close the last utterance.
	samples = append(samples, make([]int16, 16000)...)
	for i := 0; i+160 <= len(samples); i += 160 {
		payload := make([]byte, 320)
		for j, s := range samples[i : i+160] {
			binary.LittleEndian.PutUint16(payload[2*j:], uint16(s))
		}
		det.Frame("eval", mictap.Frame{Type: mictap.FrameMic, Seq: uint32(i / 160), Timestamp: uint64(i / 16), Payload: payload})
	}
	det.End("eval", nil)
	return h.n, nil
}

// readWAV returns the samples of a 16 kHz mono 16-bit PCM WAV file.
func readWAV(path string) ([]int16, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil || string(riff[:4]) != "RIFF" || string(riff[8:]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return nil, errors.New("no data chunk")
		}
		size := binary.LittleEndian.Uint32(hdr[4:])
		switch string(hdr[:4]) {
		case "fmt ":
			fmtChunk := make([]byte, size)
			if _, err := io.ReadFull(f, fmtChunk); err != nil {
				return nil, err
			}
			channels := binary.LittleEndian.Uint16(fmtChunk[2:])
			rate := binary.LittleEndian.Uint32(fmtChunk[4:])
			bits := binary.LittleEndian.Uint16(fmtChunk[14:])
			if channels != 1 || rate != 16000 || bits != 16 {
				return nil, fmt.Errorf("need 16 kHz mono 16-bit, got %d Hz %d ch %d bit", rate, channels, bits)
			}
		case "data":
			data := make([]byte, size)
			n, _ := io.ReadFull(f, data)
			out := make([]int16, n/2)
			for i := range out {
				out[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
			}
			return out, nil
		default:
			if _, err := f.Seek(int64(size+size%2), io.SeekCurrent); err != nil {
				return nil, err
			}
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "wakeword-eval:", err)
	os.Exit(1)
}
