package mictap

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RecordSink saves each robot's stream as two 16-bit PCM WAV files in a directory, one for the mic and one for
// the speaker, to replay real sessions through the wake word detector and the recognizers offline. Recording
// problems are logged and never disturb the stream.
type RecordSink struct {
	dir  string
	logf func(a ...any)
	now  func() time.Time

	mu       sync.Mutex
	sessions map[string]*recording
}

type recording struct {
	mic, speaker *wavFile
	clock        *os.File // one JSON line per mic second: PC time against mic samples written

	nextMicSeq uint32
	seenMic    bool
	micSamples int64
	micWrites  int
}

// maxGapFrames bounds the silence written for a seq jump, so a corrupt seq cannot fill the disk.
const maxGapFrames = 500

func NewRecordSink(dir string, logf func(a ...any)) *RecordSink {
	return &RecordSink{dir: dir, logf: logf, now: time.Now, sessions: map[string]*recording{}}
}

func (r *RecordSink) Start(esn string, h Hello) {
	base := filepath.Join(r.dir, filepath.Base(esn)+"_"+r.now().Format("20060102-150405"))
	rec := &recording{
		mic:     r.create(base+"_mic.wav", h.MicRate),
		speaker: r.create(base+"_speaker.wav", h.SpeakerRate),
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
		rec.writeMic(f, r.now())
	case FrameSpeaker:
		rec.speaker.write(f.Payload)
	}
}

func (r *RecordSink) End(esn string, _ error) {
	r.mu.Lock()
	rec := r.sessions[esn]
	delete(r.sessions, esn)
	r.mu.Unlock()
	rec.close()
}

func (r *RecordSink) create(path string, rate uint32) *wavFile {
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		r.logf("MicTap: cannot record: ", err)
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		r.logf("MicTap: cannot record: ", err)
		return nil
	}
	w := &wavFile{f: f, rate: rate}
	if err := w.header(); err != nil {
		r.logf("MicTap: cannot record: ", err)
		f.Close()
		return nil
	}
	return w
}

// writeMic keeps the mic file on the robot's timeline: frames the robot dropped become silence, so a time in
// the recording stays a time in the session.
func (rec *recording) writeMic(f Frame, now time.Time) {
	if rec.seenMic && f.Seq > rec.nextMicSeq {
		gap := min(f.Seq-rec.nextMicSeq, maxGapFrames)
		silence := make([]byte, int(gap)*len(f.Payload))
		rec.mic.write(silence)
		rec.micSamples += int64(len(silence) / 2)
	}
	rec.seenMic, rec.nextMicSeq = true, f.Seq+1
	rec.mic.write(f.Payload)
	rec.micSamples += int64(len(f.Payload) / 2)
	if rec.micWrites%headerEvery == 0 && rec.clock != nil {
		fmt.Fprintf(rec.clock, "{\"unix_ms\":%d,\"mic_samples\":%d}\n", now.UnixMilli(), rec.micSamples)
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
}

// headerEvery: the WAV header is rewritten with the current length every this many writes (about a second of
// mic frames), so a recording can be read while the session still runs.
const headerEvery = 100

// wavFile is a mono 16-bit PCM WAV whose header follows the length written so far.
type wavFile struct {
	mu     sync.Mutex
	f      *os.File
	rate   uint32
	data   uint32
	writes int
}

func (w *wavFile) header() error {
	le := binary.LittleEndian
	h := make([]byte, 0, 44)
	h = append(h, "RIFF"...)
	h = le.AppendUint32(h, 36+w.data)
	h = append(h, "WAVEfmt "...)
	h = le.AppendUint32(h, 16)
	h = le.AppendUint16(h, 1) // PCM
	h = le.AppendUint16(h, 1) // mono
	h = le.AppendUint32(h, w.rate)
	h = le.AppendUint32(h, 2*w.rate)
	h = le.AppendUint16(h, 2)
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
