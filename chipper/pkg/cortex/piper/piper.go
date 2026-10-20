// Package piper is the cortex Synthesizer adapter over piper neural voices. It runs tts-sidecar/tts_sidecar.py
// once, which keeps one voice per language loaded in onnxruntime, and asks it for one sentence at a time:
// a fresh process per sentence would spend about 1 s loading before the first sound.
package piper

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
)

// Config is the cortex TTS configuration (CORTEX_TTS*).
type Config struct {
	Enabled bool              // CORTEX_TTS=piper
	Python  string            // CORTEX_TTS_PYTHON: Python with onnxruntime, piper_phonemize and numpy
	Script  string            // CORTEX_TTS_SIDECAR
	Voices  map[string]string // language -> piper .onnx (CORTEX_TTS_VOICE_ES, CORTEX_TTS_VOICE_EN)
	LevelDB float64           // CORTEX_TTS_LEVEL_DB: speech RMS in dBFS
	// SentenceTimeout bounds one sentence (CORTEX_TTS_SENTENCE_TIMEOUT, default 3s); past it the sidecar is
	// killed and the next sentence starts a new one.
	SentenceTimeout time.Duration
}

// ConfigFromEnv reads the CORTEX_TTS* variables through getenv (os.Getenv in wire-pod).
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	or := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	dir := filepath.Join(getenv("HOME"), ".cache/v3ctor/piper-voices")
	cfg := Config{
		Python: or("CORTEX_TTS_PYTHON", "python3"),
		Script: or("CORTEX_TTS_SIDECAR", "tts-sidecar/tts_sidecar.py"),
		Voices: map[string]string{
			"es": or("CORTEX_TTS_VOICE_ES", filepath.Join(dir, "es_MX-claude-high.onnx")),
			"en": or("CORTEX_TTS_VOICE_EN", filepath.Join(dir, "en_US-lessac-medium.onnx")),
		},
		LevelDB:         -20,
		SentenceTimeout: 3 * time.Second,
	}
	switch mode := or("CORTEX_TTS", "off"); mode {
	case "off":
	case "piper":
		cfg.Enabled = true
	default:
		return cfg, fmt.Errorf("CORTEX_TTS=%q: want piper or off", mode)
	}
	if s := getenv("CORTEX_TTS_LEVEL_DB"); s != "" {
		db, err := strconv.ParseFloat(s, 64)
		if err != nil || db >= 0 || db < -60 {
			return cfg, fmt.Errorf("CORTEX_TTS_LEVEL_DB=%q: want dBFS in [-60, 0)", s)
		}
		cfg.LevelDB = db
	}
	if v := getenv("CORTEX_TTS_SENTENCE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("CORTEX_TTS_SENTENCE_TIMEOUT=%q: want a positive duration", v)
		}
		cfg.SentenceTimeout = d
	}
	return cfg, nil
}

// Describe is the startup log line.
func (c Config) Describe() string {
	langs := make([]string, 0, len(c.Voices))
	for l := range c.Voices {
		langs = append(langs, l)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(langs))) // es first
	var b strings.Builder
	b.WriteString("cortex: TTS piper")
	for _, l := range langs {
		fmt.Fprintf(&b, ", %s %s", l, c.Voices[l])
	}
	fmt.Fprintf(&b, ", level %g dBFS", c.LevelDB)
	return b.String()
}

// NewVoice starts the sidecar and returns the cortex Voice over it, logging the configuration. Close the
// Sidecar when done.
func NewVoice(cfg Config) (*cortex.SentenceVoice, *Sidecar, error) {
	side, err := Start(cfg)
	if err != nil {
		return nil, nil, err
	}
	log.Println(cfg.Describe())
	return cortex.NewSentenceVoice(side, cfg.LevelDB).WithSentenceTimeout(cfg.SentenceTimeout), side, nil
}

const readyTimeout = 30 * time.Second

// Sidecar is one running tts_sidecar.py. It serves one sentence at a time and restarts itself on the next
// request after it dies.
type Sidecar struct {
	cfg  Config
	turn chan struct{} // holds one token: whoever has it talks to the process

	cmd    *exec.Cmd
	proc   atomic.Pointer[os.Process] // the running process, for killing it from outside the turn
	stdin  io.WriteCloser
	stdout *bufio.Reader
	rates  map[string]int
}

// Start runs the sidecar and waits until its voices are loaded.
func Start(cfg Config) (*Sidecar, error) {
	s := &Sidecar{cfg: cfg, turn: make(chan struct{}, 1)}
	if err := s.start(); err != nil {
		return nil, err
	}
	s.turn <- struct{}{}
	return s, nil
}

func (s *Sidecar) start() error {
	args := []string{s.cfg.Script}
	langs := make([]string, 0, len(s.cfg.Voices))
	for l := range s.cfg.Voices {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	for _, l := range langs {
		args = append(args, "--voice", l+"="+s.cfg.Voices[l])
	}
	cmd := exec.Command(s.cfg.Python, args...)
	cmd.Stderr = os.Stderr
	stopWithParent(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tts sidecar: %w", err)
	}
	s.cmd, s.stdin, s.stdout = cmd, stdin, bufio.NewReaderSize(stdout, 1<<16)
	s.proc.Store(cmd.Process)
	ready := make(chan error, 1)
	go func() {
		var hello struct {
			Ready  bool           `json:"ready"`
			Voices map[string]int `json:"voices"`
		}
		line, err := s.stdout.ReadBytes('\n')
		if err == nil {
			err = json.Unmarshal(line, &hello)
		}
		if err == nil && !hello.Ready {
			err = errors.New("not ready")
		}
		s.rates = hello.Voices
		ready <- err
	}()
	select {
	case err = <-ready:
	case <-time.After(readyTimeout):
		err = errors.New("no ready line")
	}
	if err != nil {
		s.kill()
		return fmt.Errorf("tts sidecar did not start: %w", err)
	}
	return nil
}

func (s *Sidecar) kill() {
	if s.cmd != nil {
		s.proc.Store(nil)
		s.stdin.Close()
		s.cmd.Process.Kill()
		s.cmd.Wait()
		s.cmd = nil
	}
}

// Synthesize implements cortex.Synthesizer. lang may carry a region ("en-US"). When ctx ends while the
// sidecar is synthesizing, it returns at once; the sidecar's answer is read and dropped before the next one.
func (s *Sidecar) Synthesize(ctx context.Context, lang, sentence string) ([]int16, int, error) {
	lang = strings.ToLower(strings.SplitN(strings.SplitN(lang, "-", 2)[0], "_", 2)[0])
	if _, ok := s.cfg.Voices[lang]; !ok {
		return nil, 0, fmt.Errorf("piper: no voice for language %q", lang)
	}
	select {
	case <-s.turn:
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	type result struct {
		pcm  []int16
		rate int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { s.turn <- struct{}{} }()
		pcm, rate, err := s.request(lang, sentence)
		done <- result{pcm, rate, err}
	}()
	select {
	case r := <-done:
		return r.pcm, r.rate, r.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// hung: kill it, so the exchange fails, frees the turn and the next sentence starts a new sidecar
			if p := s.proc.Load(); p != nil {
				log.Printf("piper: sentence past its deadline, killing the sidecar (pid %d)", p.Pid)
				killTree(p)
			}
		}
		return nil, 0, ctx.Err()
	}
}

// request runs one exchange; the caller holds the turn.
func (s *Sidecar) request(lang, text string) ([]int16, int, error) {
	if s.cmd == nil {
		if err := s.start(); err != nil {
			return nil, 0, err
		}
	}
	pcm, rate, err := s.exchange(lang, text)
	var reqErr requestError
	if err != nil && !errors.As(err, &reqErr) {
		s.kill() // the pipe is out of step or the process died: start afresh next time
	}
	return pcm, rate, err
}

type requestError string

func (e requestError) Error() string { return "piper: " + string(e) }

func (s *Sidecar) exchange(lang, text string) ([]int16, int, error) {
	req, _ := json.Marshal(map[string]string{"lang": lang, "text": text})
	if _, err := s.stdin.Write(append(req, '\n')); err != nil {
		return nil, 0, fmt.Errorf("tts sidecar: %w", err)
	}
	line, err := s.stdout.ReadBytes('\n')
	if err != nil {
		return nil, 0, fmt.Errorf("tts sidecar: %w", err)
	}
	var head struct {
		Samples int    `json:"samples"`
		Rate    int    `json:"rate"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, 0, fmt.Errorf("tts sidecar: bad header %q", line)
	}
	if head.Error != "" {
		return nil, 0, requestError(head.Error)
	}
	pcm := make([]int16, head.Samples)
	if err := binary.Read(s.stdout, binary.LittleEndian, pcm); err != nil {
		return nil, 0, fmt.Errorf("tts sidecar: %w", err)
	}
	return pcm, head.Rate, nil
}

// Close stops the sidecar after any request in flight.
func (s *Sidecar) Close() error {
	<-s.turn
	s.kill()
	s.turn <- struct{}{}
	return nil
}

var _ cortex.Synthesizer = (*Sidecar)(nil)
