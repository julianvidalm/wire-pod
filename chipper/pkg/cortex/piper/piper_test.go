package piper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestConfigFromEnvDefaultsOff(t *testing.T) {
	cfg, err := ConfigFromEnv(env(map[string]string{"HOME": "/h"}))
	if err != nil || cfg.Enabled {
		t.Fatalf("default: %+v %v, want off", cfg, err)
	}
	if cfg.LevelDB != -20 || cfg.Python != "python3" || cfg.Script != "tts-sidecar/tts_sidecar.py" {
		t.Fatalf("defaults %+v", cfg)
	}
	if cfg.Voices["es"] != "/h/.cache/v3ctor/piper-voices/es_MX-claude-high.onnx" ||
		cfg.Voices["en"] != "/h/.cache/v3ctor/piper-voices/en_US-lessac-medium.onnx" {
		t.Fatalf("default voices %v", cfg.Voices)
	}
}

func TestConfigFromEnvReadsEverySetting(t *testing.T) {
	cfg, err := ConfigFromEnv(env(map[string]string{
		"CORTEX_TTS": "piper", "CORTEX_TTS_VOICE_ES": "/v/es.onnx", "CORTEX_TTS_VOICE_EN": "/v/en.onnx",
		"CORTEX_TTS_LEVEL_DB": "-18.5", "CORTEX_TTS_PYTHON": "/py", "CORTEX_TTS_SIDECAR": "/s.py",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Voices["es"] != "/v/es.onnx" || cfg.Voices["en"] != "/v/en.onnx" ||
		cfg.LevelDB != -18.5 || cfg.Python != "/py" || cfg.Script != "/s.py" {
		t.Fatalf("%+v", cfg)
	}
	if off, _ := ConfigFromEnv(env(map[string]string{"CORTEX_TTS": "off"})); off.Enabled {
		t.Fatal("CORTEX_TTS=off is on")
	}
}

func TestConfigFromEnvRejectsBadValues(t *testing.T) {
	for _, bad := range []map[string]string{
		{"CORTEX_TTS": "espeak"},
		{"CORTEX_TTS_LEVEL_DB": "loud"},
		{"CORTEX_TTS_LEVEL_DB": "3"},
		{"CORTEX_TTS_LEVEL_DB": "-90"},
	} {
		if _, err := ConfigFromEnv(env(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestDescribeNamesTheVoices(t *testing.T) {
	cfg, _ := ConfigFromEnv(env(map[string]string{"CORTEX_TTS": "piper", "CORTEX_TTS_VOICE_ES": "/v/es.onnx",
		"CORTEX_TTS_VOICE_EN": "/v/en.onnx"}))
	if got, want := cfg.Describe(), "cortex: TTS piper, es /v/es.onnx, en /v/en.onnx, level -20 dBFS"; got != want {
		t.Fatalf("Describe() = %q, want %q", got, want)
	}
}

// integration needs the voices and a Python with onnxruntime and piper_phonemize (CORTEX_TTS_PYTHON).
func integration(t *testing.T) Config {
	t.Helper()
	cfg, err := ConfigFromEnv(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CORTEX_TTS_PYTHON") == "" {
		cfg.Python = filepath.Join(os.Getenv("HOME"), ".cache/v3ctor/oww-train-venv/bin/python")
	}
	cfg.Script = "../../../tts-sidecar/tts_sidecar.py"
	for _, p := range append([]string{cfg.Python}, cfg.Voices["es"], cfg.Voices["en"]) {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("piper integration: %v", err)
		}
	}
	return cfg
}

func TestPiperSpeaksBothLanguages(t *testing.T) {
	cfg := integration(t)
	side, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer side.Close()
	voice := cortex.NewSentenceVoice(side, cfg.LevelDB)
	for lang, text := range map[string]string{"es": "Hola. ¿Cómo estás hoy?", "en-US": "Hello there. How are you?"} {
		speech := voice.Speak(context.Background(), text, lang)
		n := 0
		for c := range speech.Chunks() {
			if len(c.Samples) < cortex.SpeechRate/4 {
				t.Errorf("%s chunk %d: %d samples", lang, c.Index, len(c.Samples))
			}
			n++
		}
		res, err := speech.Wait()
		if err != nil || n != 2 || res.Sentences != 2 || res.Duration < time.Second {
			t.Fatalf("%s: %d chunks, %+v, %v", lang, n, res, err)
		}
	}
	if _, _, err := side.Synthesize(context.Background(), "fr", "Bonjour."); err == nil ||
		!strings.Contains(err.Error(), "no voice") {
		t.Fatalf("fr: %v, want no voice", err)
	}
	// The sidecar keeps serving after a cancelled request.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	long := strings.Repeat("This sentence is cancelled while the sidecar is still synthesizing it, ", 8)
	if _, _, err := side.Synthesize(ctx, "en", long); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if pcm, rate, err := side.Synthesize(context.Background(), "en", "Still here."); err != nil || rate != 22050 || len(pcm) == 0 {
		t.Fatalf("after cancel: %d samples at %d, %v", len(pcm), rate, err)
	}
}
