package initwirepod

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
)

var dumpName = regexp.MustCompile(`^0dd1b652_\d{8}-\d{6}\.\d{3}_request\.(wav|json)$`)

// waitForFiles waits until dir holds n files matching dumpName and returns them.
func waitForFiles(t *testing.T, dir string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var got []string
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if dumpName.MatchString(e.Name()) {
				got = append(got, e.Name())
			}
		}
		if len(got) >= n || time.Now().After(deadline) {
			if len(got) != n {
				t.Fatalf("dump files %v, want %d", got, n)
			}
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// With CORTEX_VOICE_DUMP_DIR set, a voice request writes its whole audio as a 16 kHz mono WAV and a JSON of what
// the cortex made of it.
func TestAVoiceRequestDumpsItsAudioAndWhatWasHeard(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(voiceDumpDirEnv, dir)
	b := &recBrain{answer: cortex.Answer{Intent: "intent_clock_time"}}
	pcm := pcmOf(span{false, 300 * time.Millisecond}, span{true, time.Second}, span{false, 2 * time.Second})
	vt := newVoiceRequestTest(t, "Hey Vector, ¿qué hora es?", b, nil, pcm)
	vt.ask()
	wantIntent(t, vt.only(t), "intent_clock_time")
	files := waitForFiles(t, dir, 2)
	base := strings.TrimSuffix(files[0], filepath.Ext(files[0]))

	wav, err := os.ReadFile(filepath.Join(dir, base+".wav"))
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	// the capture ends 700 ms into the final silence; the dump holds what was received up to then
	if string(wav[:4]) != "RIFF" || string(wav[8:16]) != "WAVEfmt " || le.Uint16(wav[22:]) != 1 ||
		le.Uint32(wav[24:]) != 16000 || le.Uint16(wav[34:]) != 16 || string(wav[36:40]) != "data" ||
		int(le.Uint32(wav[40:])) != len(wav)-44 {
		t.Fatalf("wav header % x", wav[:44])
	}
	samples := (len(wav) - 44) / 2
	if samples < 2000*16 || samples > len(pcm) {
		t.Fatalf("%d samples, want the request up to the end of the capture", samples)
	}

	js, err := os.ReadFile(filepath.Join(dir, base+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var d voiceDumpRecord
	if err := json.Unmarshal(js, &d); err != nil {
		t.Fatal(err)
	}
	if d.ESN != "0dd1b652" || d.Started.IsZero() || d.Rate != 16000 || d.Samples != samples ||
		d.EndReason != "end of speech" || d.UtteranceStart != 0 || d.UtteranceEnd <= d.UtteranceStart ||
		d.UtteranceEnd > samples || d.SpeechOnset < 280*16 || d.SpeechOnset > 320*16 ||
		d.VoicedMs < 950 || d.LevelDBFS == nil || *d.LevelDBFS > -25 || *d.LevelDBFS < -35 ||
		d.TranscriptRaw != "Hey Vector, ¿qué hora es?" || d.Transcript != "¿Qué hora es?" || d.Language != "es" ||
		d.Outcome != "answered" || d.Answer != "intent intent_clock_time" || d.Response != "intent_clock_time" ||
		d.CaptureMs < 0 || d.TotalMs < d.CaptureMs {
		t.Fatalf("dump %s", js)
	}
	if !strings.Contains(string(js), `"transcribe_ms"`) || !strings.Contains(string(js), `"think_ms"`) {
		t.Fatalf("dump %s has no stage timings", js)
	}
}

// Nothing heard is dumped too: no onset, no level, no transcript.
func TestAVoiceRequestWithNoSpeechIsDumpedWithoutATranscript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(voiceDumpDirEnv, dir)
	vt := newVoiceRequestTest(t, "Gracias.", &recBrain{}, nil, pcmOf(span{false, time.Second}))
	vt.ask()
	wantIntent(t, vt.only(t), "intent_system_noaudio")
	files := waitForFiles(t, dir, 2)
	js, _ := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(files[0], filepath.Ext(files[0]))+".json"))
	var d voiceDumpRecord
	if err := json.Unmarshal(js, &d); err != nil {
		t.Fatal(err)
	}
	if d.EndReason != "no speech" || d.SpeechOnset != -1 || d.LevelDBFS != nil || d.TranscriptRaw != "" ||
		d.Outcome != "nothing heard" || d.Response != "intent_system_noaudio" || d.Samples != 16000 {
		t.Fatalf("dump %s", js)
	}
}

// The dump directory is capped by CORTEX_VOICE_DUMP_MAX_MB: the oldest request pairs go first.
func TestVoiceDumpsDeleteTheOldestRequestsOverTheCap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(voiceDumpDirEnv, dir)
	t.Setenv(voiceDumpMaxMBEnv, "1")
	for name, size := range map[string]int{
		"0dd1b652_20200101-000000.000_request.wav": 700 << 10, "0dd1b652_20200101-000000.000_request.json": 10,
		"0dd1b652_20200102-000000.000_request.wav": 300 << 10, "0dd1b652_20200102-000000.000_request.json": 10,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := &recBrain{answer: cortex.Answer{Intent: "intent_clock_time"}}
	vt := newVoiceRequestTest(t, "¿qué hora es?", b, nil, pcmOf(span{true, time.Second}, span{false, 2 * time.Second}))
	vt.ask()
	vt.only(t)
	files := waitForFiles(t, dir, 4)
	for _, f := range files {
		if strings.Contains(f, "20200101") {
			t.Fatalf("the oldest request was kept: %v", files)
		}
	}
	if !strings.Contains(strings.Join(files, " "), "20200102") {
		t.Fatalf("a request that fits was deleted: %v", files)
	}
}

// Unset, nothing is dumped; a dump that cannot be written never disturbs the answer and is logged once.
func TestVoiceDumpOffOrFailingNeverDisturbsTheAnswer(t *testing.T) {
	t.Setenv(voiceDumpDirEnv, "")
	if d := newVoiceDump(nil); d != nil {
		t.Fatalf("dump %v with %s unset", d, voiceDumpDirEnv)
	}
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(voiceDumpDirEnv, notDir)
	b := &recBrain{answer: cortex.Answer{Intent: "intent_clock_time"}}
	vt := newVoiceRequestTest(t, "¿qué hora es?", b, nil, speechPCM(true))
	for i := 0; i < 2; i++ {
		vt.stream.mu.Lock()
		vt.stream.sent = nil
		vt.stream.mu.Unlock()
		vt.ask()
		wantIntent(t, vt.only(t), "intent_clock_time")
	}
	time.Sleep(100 * time.Millisecond)
	n := 0
	vt.log.mu.Lock()
	for _, l := range vt.log.lines {
		if strings.Contains(l, "voice dump") {
			n++
		}
	}
	vt.log.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d dump failure lines in %q, want 1", n, vt.log.lines)
	}
}

func TestVoiceDumpMaxBytes(t *testing.T) {
	for env, want := range map[string]int64{"": 200 << 20, "0": 0, "1.5": 3 << 19, "x": 200 << 20, "-1": 200 << 20} {
		got, err := voiceDumpMaxBytes(env)
		if got != want || (err != nil) != (env == "x" || env == "-1") {
			t.Fatalf("%q: got %d, %v; want %d", env, got, err, want)
		}
	}
}
