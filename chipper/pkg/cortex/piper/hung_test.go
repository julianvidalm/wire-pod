package piper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSidecar is a sidecar that hangs on its first sentence ever and answers one sample after, counting starts.
func fakeSidecar(t *testing.T) (Config, string) {
	dir := t.TempDir()
	script := filepath.Join(dir, "sidecar.sh")
	os.WriteFile(script, []byte(`echo start >> "$DIR/starts"
echo '{"ready": true, "voices": {"es": 16000}}'
while read line; do
  if [ ! -e "$DIR/hung" ]; then touch "$DIR/hung"; sleep 30; fi
  echo '{"samples": 1, "rate": 16000}'
  printf '\001\000'
done
`), 0o755)
	t.Setenv("DIR", dir)
	return Config{Python: "/bin/sh", Script: script, Voices: map[string]string{"es": "x"}, LevelDB: -20,
		SentenceTimeout: 200 * time.Millisecond}, dir
}

// L88 advisory E: a sentence past its deadline kills the hung sidecar, and the next sentence starts a new one.
func TestAHungSidecarIsKilledAndTheNextSentenceRestartsIt(t *testing.T) {
	cfg, dir := fakeSidecar(t)
	side, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer side.Close()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.SentenceTimeout)
	_, _, err = side.Synthesize(ctx, "es", "Hola.")
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung sentence: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pcm, _, err := side.Synthesize(ctx, "es", "Hola.")
	if err != nil || len(pcm) != 1 {
		t.Fatalf("next sentence: %v %v, want one sample from a new sidecar", pcm, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "starts")); strings.Count(string(b), "start") != 2 {
		t.Fatalf("sidecar started %d times, want 2", strings.Count(string(b), "start"))
	}
}

func TestTheSentenceTimeoutSetting(t *testing.T) {
	for v, want := range map[string]time.Duration{"": 3 * time.Second, "5s": 5 * time.Second} {
		cfg, err := ConfigFromEnv(env(map[string]string{"HOME": "/h", "CORTEX_TTS_SENTENCE_TIMEOUT": v}))
		if err != nil || cfg.SentenceTimeout != want {
			t.Fatalf("%q: %v %v, want %v", v, cfg.SentenceTimeout, err, want)
		}
	}
	for _, v := range []string{"x", "0s", "-1s"} {
		if _, err := ConfigFromEnv(env(map[string]string{"CORTEX_TTS_SENTENCE_TIMEOUT": v})); err == nil {
			t.Fatalf("%q accepted", v)
		}
	}
}
