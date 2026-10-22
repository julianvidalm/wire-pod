// Command cortex-say speaks a text with the cortex Voice (piper) into a 16 kHz wav file, to pick voices by ear
// and measure latency. It reads the CORTEX_TTS_* variables (CORTEX_TTS itself is not needed) and prints the
// sidecar start time, the time to the first sentence, the total synthesis time and the real-time factor.
//
//	go run -tags nolibopusfile ./cmd/cortex-say -lang es -o /tmp/hola.wav "Hola. ¿Cómo estás?"
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/piper"
)

func main() {
	lang := flag.String("lang", "es", "language of the text: es or en")
	out := flag.String("o", "cortex-say.wav", "output wav (16 kHz mono s16)")
	voice := flag.String("voice", "", "piper .onnx for -lang (default CORTEX_TTS_VOICE_ES / _EN)")
	level := flag.Float64("level", 0, "speech level in dBFS (default CORTEX_TTS_LEVEL_DB, -20)")
	repeat := flag.Int("repeat", 1, "speak the text this many times on the same sidecar (the first run is cold)")
	flag.Parse()
	text := strings.Join(flag.Args(), " ")
	if text == "" {
		fmt.Fprintln(os.Stderr, "usage: cortex-say [-lang es|en] [-o out.wav] [-voice x.onnx] [-level dB] [-repeat n] text...")
		os.Exit(2)
	}
	cfg, err := piper.ConfigFromEnv(os.Getenv)
	if err != nil {
		fail(err)
	}
	if *voice != "" {
		cfg.Voices[*lang] = *voice
	}
	if *level != 0 {
		cfg.LevelDB = *level
	}
	t0 := time.Now()
	side, err := piper.Start(cfg)
	if err != nil {
		fail(err)
	}
	defer side.Close()
	fmt.Printf("sidecar ready in %v\n", time.Since(t0).Round(time.Millisecond))

	voiceOf := cortex.NewSentenceVoice(side, cfg.LevelDB)
	var pcm []int16
	for run := 1; run <= *repeat; run++ {
		pcm = say(voiceOf, text, *lang, run)
	}
	if err := writeWav(*out, pcm); err != nil {
		fail(err)
	}
	fmt.Println("wrote", *out)
}

// say speaks text once and prints its timing.
func say(v *cortex.SentenceVoice, text, lang string, run int) []int16 {
	start := time.Now()
	speech := v.Speak(context.Background(), text, lang)
	var pcm []int16
	for c := range speech.Chunks() {
		if c.Index == 0 {
			fmt.Printf("run %d: first chunk after %v (%.2f s audio): %q\n", run,
				time.Since(start).Round(time.Millisecond), float64(len(c.Samples))/cortex.SpeechRate, c.Text)
		}
		pcm = append(pcm, c.Samples...)
	}
	res, err := speech.Wait()
	if err != nil {
		fail(err)
	}
	took := time.Since(start)
	fmt.Printf("run %d: %d sentences, %.2f s audio in %v, RTF %.3f\n", run, res.Sentences, res.Duration.Seconds(),
		took.Round(time.Millisecond), took.Seconds()/res.Duration.Seconds())
	return pcm
}

func writeWav(path string, pcm []int16) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	n := uint32(2 * len(pcm))
	hdr := []any{[]byte("RIFF"), 36 + n, []byte("WAVEfmt "), uint32(16), uint16(1), uint16(1),
		uint32(cortex.SpeechRate), uint32(2 * cortex.SpeechRate), uint16(2), uint16(16), []byte("data"), n, pcm}
	for _, v := range hdr {
		if err := binary.Write(f, binary.LittleEndian, v); err != nil {
			f.Close()
			return err
		}
	}
	return f.Close()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "cortex-say:", err)
	os.Exit(1)
}
