package mictap

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RecordSink saves each robot's stream as 16-bit PCM WAV files in a directory, one for the mic, one for the
// speaker and, when the robot sends raw mic frames, one 4-channel file for the raw microphones, to replay real
// sessions through the wake word detector, the echo canceller and the recognizers offline. Each WAV has a
// _frames.jsonl companion with one FrameLogLine per frame (the robot's seq and timestamp against the position in
// the file), so the files can be aligned on the robot's clock. Frames the robot dropped become silence, so a time
// in a recording stays a time in the session. Recording problems are logged and never disturb the stream.
type RecordSink struct {
	dir  string
	logf func(a ...any)
	now  func() time.Time

	mu       sync.Mutex
	sessions map[string]*recording
}

// FrameLogLine is one line of a _frames.jsonl file: the frame's seq and robot timestamp, the sample (per channel)
// where its audio starts in the WAV, how many dropped frames were filled with silence just before it (Gap) and,
// in the raw mic file only, how many missing frames were not filled because the jump was too large to be drops
// (Skipped: the robot's raw switch was off; the file is discontinuous there, realign on TimestampMs).
type FrameLogLine struct {
	Seq         uint32 `json:"seq"`
	TimestampMs uint64 `json:"ts_ms"`
	Sample      int64  `json:"sample"`
	Gap         uint32 `json:"gap,omitempty"`
	Skipped     uint32 `json:"skipped,omitempty"`
}

type recording struct {
	base    string
	micRate uint32

	mic, speaker, raw *track
	rawFailed         bool     // the raw file could not be created or a raw frame was malformed; logged once
	clock             *os.File // one JSON line per mic second: PC time against mic samples written
	micWrites         int
}

// track is one WAV file on the robot's timeline plus its frame log.
type track struct {
	wav      *wavFile
	frames   *os.File
	channels int
	// skipLargeGaps leaves a seq jump over maxGapFrames unfilled instead of writing maxGapFrames of silence.
	skipLargeGaps bool

	next    uint32
	seen    bool
	samples int64 // per channel
}

// maxGapFrames bounds the silence written for a seq jump, so a corrupt seq cannot fill the disk.
const maxGapFrames = 500

func NewRecordSink(dir string, logf func(a ...any)) *RecordSink {
	return &RecordSink{dir: dir, logf: logf, now: time.Now, sessions: map[string]*recording{}}
}

func (r *RecordSink) Start(esn string, h Hello) {
	base := filepath.Join(r.dir, filepath.Base(esn)+"_"+r.now().Format("20060102-150405"))
	rec := &recording{
		base:    base,
		micRate: h.MicRate,
		mic:     r.track(base+"_mic", h.MicRate, 1),
		speaker: r.track(base+"_speaker", h.SpeakerRate, 1),
	}
	if rec.mic != nil {
		clock, err := os.OpenFile(base+"_clock.jsonl", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			r.logf("MicTap: cannot record the clock: ", err)
		}
		rec.clock = clock
	}
	r.mu.Lock()
	old := r.sessions[esn]
	r.sessions[esn] = rec
	r.mu.Unlock()
	old.close()
	if rec.mic != nil {
		r.logf("MicTap: recording ", esn, " to ", base+"_{mic,speaker}.wav")
	}
}

func (r *RecordSink) Frame(esn string, f Frame) {
	r.mu.Lock()
	rec := r.sessions[esn]
	r.mu.Unlock()
	if rec == nil {
		return
	}
	switch f.Type {
	case FrameMic:
		rec.mic.write(f)
		rec.logClock(r.now())
	case FrameSpeaker:
		rec.speaker.write(f)
	case FrameRawMic:
		r.writeRaw(esn, rec, f)
	}
}

func (r *RecordSink) End(esn string, _ error) {
	r.mu.Lock()
	rec := r.sessions[esn]
	delete(r.sessions, esn)
	r.mu.Unlock()
	rec.close()
}

// writeRaw creates the raw file on the first raw frame, so sessions from robots with the raw switch off have none.
func (r *RecordSink) writeRaw(esn string, rec *recording, f Frame) {
	if rec.rawFailed {
		return
	}
	if len(f.Payload)%(2*RawMicChannels) != 0 {
		rec.rawFailed = true
		r.logf("MicTap: ", esn, " raw mic frame of ", len(f.Payload), " bytes is not 4-channel s16; raw recording stopped")
		return
	}
	if rec.raw == nil {
		if rec.raw = r.track(rec.base+"_raw4ch", rec.micRate, RawMicChannels); rec.raw == nil {
			rec.rawFailed = true
			return
		}
		// the raw seq advances even while the robot's raw switch is off, so only short jumps are drops
		rec.raw.skipLargeGaps = true
		r.logf("MicTap: recording ", esn, " raw mic to ", rec.base+"_raw4ch.wav")
	}
	rec.raw.write(f)
}

// track opens base.wav and its base_frames.jsonl, or returns nil (logged) when the WAV cannot be created.
func (r *RecordSink) track(base string, rate uint32, channels int) *track {
	wav := r.create(base+".wav", rate, channels)
	if wav == nil {
		return nil
	}
	frames, err := os.OpenFile(base+"_frames.jsonl", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		r.logf("MicTap: cannot record the frame log: ", err)
	}
	return &track{wav: wav, frames: frames, channels: channels}
}

func (r *RecordSink) create(path string, rate uint32, channels int) *wavFile {
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		r.logf("MicTap: cannot record: ", err)
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		r.logf("MicTap: cannot record: ", err)
		return nil
	}
	w := &wavFile{f: f, rate: rate, channels: uint16(channels)}
	if err := w.header(); err != nil {
		r.logf("MicTap: cannot record: ", err)
		f.Close()
		return nil
	}
	return w
}

// write keeps the file on the robot's timeline: frames the robot dropped (a seq jump) become silence of this
// frame's length, then the frame is logged at the position its audio starts.
func (t *track) write(f Frame) {
	if t == nil {
		return
	}
	var gap, skipped uint32
	if t.seen && f.Seq > t.next && t.skipLargeGaps && f.Seq-t.next > maxGapFrames {
		skipped = f.Seq - t.next
	} else if t.seen && f.Seq > t.next {
		gap = min(f.Seq-t.next, maxGapFrames)
		silence := make([]byte, int(gap)*len(f.Payload))
		t.wav.write(silence)
		t.samples += int64(len(silence) / (2 * t.channels))
	}
	t.seen, t.next = true, f.Seq+1
	if t.frames != nil {
		line, _ := json.Marshal(FrameLogLine{Seq: f.Seq, TimestampMs: f.Timestamp, Sample: t.samples, Gap: gap, Skipped: skipped})
		t.frames.Write(append(line, '\n'))
	}
	t.wav.write(f.Payload)
	t.samples += int64(len(f.Payload) / (2 * t.channels))
}

func (t *track) close() {
	if t == nil {
		return
	}
	if t.frames != nil {
		t.frames.Close()
	}
	t.wav.close()
}

// logClock writes the PC time against the mic samples written, about once a second of mic frames.
func (rec *recording) logClock(now time.Time) {
	if rec.mic == nil {
		return
	}
	if rec.micWrites%headerEvery == 0 && rec.clock != nil {
		fmt.Fprintf(rec.clock, "{\"unix_ms\":%d,\"mic_samples\":%d}\n", now.UnixMilli(), rec.mic.samples)
	}
	rec.micWrites++
}

func (rec *recording) close() {
	if rec == nil {
		return
	}
	if rec.clock != nil {
		rec.clock.Close()
	}
	rec.mic.close()
	rec.speaker.close()
	rec.raw.close()
}

// headerEvery: the WAV header is rewritten with the current length every this many writes (about a second of
// mic frames), so a recording can be read while the session still runs.
const headerEvery = 100

// wavFile is a 16-bit PCM WAV (interleaved when it has several channels) whose header follows the length
// written so far.
type wavFile struct {
	mu       sync.Mutex
	f        *os.File
	rate     uint32
	channels uint16
	data     uint32
	writes   int
}

func (w *wavFile) header() error {
	le := binary.LittleEndian
	h := make([]byte, 0, 44)
	h = append(h, "RIFF"...)
	h = le.AppendUint32(h, 36+w.data)
	h = append(h, "WAVEfmt "...)
	h = le.AppendUint32(h, 16)
	h = le.AppendUint16(h, 1) // PCM
	h = le.AppendUint16(h, w.channels)
	h = le.AppendUint32(h, w.rate)
	h = le.AppendUint32(h, 2*w.rate*uint32(w.channels))
	h = le.AppendUint16(h, 2*w.channels)
	h = le.AppendUint16(h, 16)
	h = append(h, "data"...)
	h = le.AppendUint32(h, w.data)
	_, err := w.f.WriteAt(h, 0)
	return err
}

func (w *wavFile) write(pcm []byte) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.f.WriteAt(pcm, 44+int64(w.data)); err == nil {
		w.data += uint32(len(pcm))
	}
	if w.writes++; w.writes%headerEvery == 0 {
		w.header()
	}
}

func (w *wavFile) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.header()
	w.f.Close()
}
