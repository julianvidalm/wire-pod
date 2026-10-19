package mictap

import (
	"fmt"
	"sync"
)

// LogSink logs each robot's stream: when it starts, when the robot reports new dropped frames, and a summary with
// sequence gaps when it ends. It is the default sink until a consumer (the wake word detector) replaces it.
type LogSink struct {
	logf    func(a ...any)
	mu      sync.Mutex
	streams map[string]*streamLog
}

type streamLog struct {
	mic, speaker, raw  seqCounter
	micDrop, speakDrop uint32
}

type seqCounter struct {
	frames, gaps uint64
	next         uint32
}

func (c *seqCounter) add(seq uint32) {
	if c.frames > 0 && seq != c.next {
		c.gaps++
	}
	c.frames++
	c.next = seq + 1
}

func NewLogSink(logf func(a ...any)) *LogSink {
	return &LogSink{logf: logf, streams: map[string]*streamLog{}}
}

func (s *LogSink) Start(esn string, h Hello) {
	s.mu.Lock()
	s.streams[esn] = &streamLog{}
	s.mu.Unlock()
	s.logf(fmt.Sprintf("MicTap: %s started (v%d, mic %d Hz, speaker %d Hz)", esn, h.Version, h.MicRate, h.SpeakerRate))
}

func (s *LogSink) Frame(esn string, f Frame) {
	s.mu.Lock()
	st := s.streams[esn]
	if st == nil {
		s.mu.Unlock()
		return
	}
	var line string
	switch f.Type {
	case FrameMic:
		st.mic.add(f.Seq)
	case FrameSpeaker:
		st.speaker.add(f.Seq)
	case FrameRawMic:
		st.raw.add(f.Seq)
	case FrameStats:
		if mic, speaker, err := f.Stats(); err == nil && (mic != st.micDrop || speaker != st.speakDrop) {
			st.micDrop, st.speakDrop = mic, speaker
			line = fmt.Sprintf("MicTap: %s robot dropped mic %d speaker %d frames (totals)", esn, mic, speaker)
		}
	}
	s.mu.Unlock()
	if line != "" {
		s.logf(line)
	}
}

func (s *LogSink) End(esn string, err error) {
	s.mu.Lock()
	st := s.streams[esn]
	delete(s.streams, esn)
	s.mu.Unlock()
	if st == nil {
		return
	}
	line := fmt.Sprintf("MicTap: %s ended: mic %d frames (%d gaps), speaker %d frames (%d gaps)",
		esn, st.mic.frames, st.mic.gaps, st.speaker.frames, st.speaker.gaps)
	if st.raw.frames > 0 {
		line += fmt.Sprintf(", raw mic %d frames (%d gaps)", st.raw.frames, st.raw.gaps)
	}
	if err != nil {
		line += ": " + err.Error()
	}
	s.logf(line)
}
