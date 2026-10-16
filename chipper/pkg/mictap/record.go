package mictap

import (
	"encoding/binary"
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
}

func NewRecordSink(dir string, logf func(a ...any)) *RecordSink {
	return &RecordSink{dir: dir, logf: logf, now: time.Now, sessions: map[string]*recording{}}
}

func (r *RecordSink) Start(esn string, h Hello) {
	base := filepath.Join(r.dir, filepath.Base(esn)+"_"+r.now().Format("20060102-150405"))
	rec := &recording{
		mic:     r.create(base+"_mic.wav", h.MicRate),
		speaker: r.create(base+"_speaker.wav", h.SpeakerRate),
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
		rec.mic.write(f.Payload)
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

func (rec *recording) close() {
	if rec == nil {
		return
	}
	rec.mic.close()
	rec.speaker.close()
}

// wavFile is a mono 16-bit PCM WAV whose header is rewritten with the final length on close.
type wavFile struct {
	mu   sync.Mutex
	f    *os.File
	rate uint32
	data uint32
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
