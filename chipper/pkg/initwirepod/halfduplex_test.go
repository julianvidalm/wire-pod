package initwirepod

import (
	"encoding/binary"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap"
)

type frameSink struct {
	mu     sync.Mutex
	frames []mictap.Frame
}

func (s *frameSink) Start(string, mictap.Hello) {}
func (s *frameSink) Frame(_ string, f mictap.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.Type == mictap.FrameMic {
		s.frames = append(s.frames, f)
	}
}
func (s *frameSink) End(string, error) {}

func loudness(f mictap.Frame) bool { return binary.LittleEndian.Uint16(f.Payload) != 0 }

// talkingSession streams 2 s of a robot that talks from 500 ms to 1 s while the user's mic hears something.
func talkingSession(sink mictap.Sink) {
	sink.Start("0dd1b652", mictap.Hello{Version: 1, MicRate: 16000, SpeakerRate: 32000})
	spk := uint64(0)
	for ts := uint64(0); ts < 2000; ts += 10 {
		for ; spk <= ts; spk += 32 {
			p := make([]byte, 2048)
			if spk >= 500 && spk < 1000 {
				for i := 0; i < len(p); i += 2 {
					binary.LittleEndian.PutUint16(p[i:], 9000)
				}
			}
			sink.Frame("0dd1b652", mictap.Frame{Type: mictap.FrameSpeaker, Timestamp: spk, Payload: p})
		}
		sink.Frame("0dd1b652", micFrame(uint32(ts/10), ts, true))
	}
	sink.End("0dd1b652", nil)
}

// The detector and the conversation hear zeros while the robot talks; the recorder (the log side) keeps the
// real mic audio.
func TestHalfDuplexSilencesTheDetectorButNotTheRecorder(t *testing.T) {
	cfg := cortex.DefaultHalfDuplexConfig()
	rec, det := &frameSink{}, &frameSink{}
	talkingSession(detectorSink(rec, det, nil, &cfg))
	if len(rec.frames) != 200 || len(det.frames) != 200 {
		t.Fatalf("recorder %d, detector %d mic frames; want 200 each", len(rec.frames), len(det.frames))
	}
	silenced := 0
	for i := range rec.frames {
		if !loudness(rec.frames[i]) {
			t.Fatalf("recorder got silence at %d ms", rec.frames[i].Timestamp)
		}
		if !loudness(det.frames[i]) {
			silenced++
			if ts := det.frames[i].Timestamp; ts < 500 || ts > 1024+600 { // last loud speaker frame 992-1024 ms
				t.Fatalf("detector silenced at %d ms", ts)
			}
		}
	}
	if silenced == 0 {
		t.Fatal("detector never silenced while the robot talked")
	}
}

// CORTEX_HALF_DUPLEX=off gives the old sink: every mic frame reaches the detector as it came.
func TestHalfDuplexOffKeepsTheOldSink(t *testing.T) {
	t.Setenv(halfDuplexEnv, "off")
	cfg, desc, _, err := halfDuplexConfig()
	if cfg != nil || err != nil || !strings.Contains(desc, "off") {
		t.Fatalf("got %v %q %v", cfg, desc, err)
	}
	if got, want := detectorSink(nopSink{}, nopSink{}, nil, cfg), mictap.Tee(nopSink{}, nopSink{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("sink %#v, want %#v", got, want)
	}
	rec, det := &frameSink{}, &frameSink{}
	talkingSession(detectorSink(rec, det, nil, cfg))
	for _, f := range det.frames {
		if !loudness(f) {
			t.Fatalf("detector silenced at %d ms with half duplex off", f.Timestamp)
		}
	}
}

func TestHalfDuplexSettings(t *testing.T) {
	def := cortex.DefaultHalfDuplexConfig()
	for _, tc := range []struct {
		on, hang, db string
		want         *cortex.HalfDuplexConfig
		desc         string
	}{
		{"", "", "", &def, "while the robot's speaker is above -40 dBFS and for 600ms after"},
		{"ON", "1s", "-50", &cortex.HalfDuplexConfig{Hangover: time.Second, Threshold: -50, MaxDelay: def.MaxDelay,
			StallTimeout: def.StallTimeout}, "above -50 dBFS and for 1s after"},
		{"on", "0s", "-120", &cortex.HalfDuplexConfig{Hangover: 0, Threshold: -120, MaxDelay: def.MaxDelay,
			StallTimeout: def.StallTimeout}, "above -120 dBFS"},
		{"off", "bad", "bad", nil, "off"},
	} {
		t.Setenv(halfDuplexEnv, tc.on)
		t.Setenv(halfDuplexHangoverEnv, tc.hang)
		t.Setenv(halfDuplexDBEnv, tc.db)
		got, desc, warnings, err := halfDuplexConfig()
		if err != nil || len(warnings) != 0 || !reflect.DeepEqual(got, tc.want) || !strings.Contains(desc, tc.desc) {
			t.Fatalf("%+v: got %+v %q %q %v", tc, got, desc, warnings, err)
		}
	}
	t.Setenv(halfDuplexEnv, "maybe")
	if cfg, _, _, err := halfDuplexConfig(); err == nil {
		t.Fatalf("unknown %s: no error, got %+v", halfDuplexEnv, cfg)
	}
}

// A bad hangover or threshold is logged and replaced by its default: the gate stays on.
func TestBadHalfDuplexValuesFallBackToTheDefaults(t *testing.T) {
	def := cortex.DefaultHalfDuplexConfig()
	for _, kv := range [][2]string{{halfDuplexHangoverEnv, "soon"}, {halfDuplexHangoverEnv, "600"}, {halfDuplexHangoverEnv, "-1s"},
		{halfDuplexDBEnv, "loud"}, {halfDuplexDBEnv, "3"}, {halfDuplexDBEnv, "0.5"}, {halfDuplexDBEnv, "-121"},
		{halfDuplexDBEnv, "NaN"}, {halfDuplexDBEnv, "-Inf"}, {halfDuplexDBEnv, "+Inf"}} {
		t.Run(kv[0]+"="+kv[1], func(t *testing.T) {
			t.Setenv(halfDuplexEnv, "on")
			t.Setenv(halfDuplexHangoverEnv, "")
			t.Setenv(halfDuplexDBEnv, "")
			t.Setenv(kv[0], kv[1])
			cfg, desc, warnings, err := halfDuplexConfig()
			if err != nil || cfg == nil || !reflect.DeepEqual(*cfg, def) || !strings.HasPrefix(desc, "on") {
				t.Fatalf("got %+v %q %v, want the defaults on", cfg, desc, err)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], kv[0]) || !strings.Contains(warnings[0], "default") {
				t.Fatalf("warnings %q", warnings)
			}
		})
	}
}
