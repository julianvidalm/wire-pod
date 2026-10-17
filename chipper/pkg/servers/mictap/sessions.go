package mictap

import (
	"errors"
	"sync"

	"github.com/julianvidalm/wire-pod/chipper/pkg/mictap/mictappb"
)

// ErrNoSession means the robot has no open Session (MicTap off, an older robot using Stream, or offline).
var ErrNoSession = errors.New("robot has no open MicTap session")

// Sessions holds the open Session of each robot so the cortex can send it commands.
type Sessions struct {
	mu   sync.Mutex
	open map[string]*sessionEntry
}

type sessionEntry struct {
	mu     sync.Mutex // gRPC allows one Send at a time per stream
	stream mictappb.MicTap_SessionServer
}

func newSessions() *Sessions {
	return &Sessions{open: map[string]*sessionEntry{}}
}

// add registers a robot's session; a newer session replaces an older one (the robot reconnected).
func (s *Sessions) add(esn string, stream mictappb.MicTap_SessionServer) *sessionEntry {
	e := &sessionEntry{stream: stream}
	s.mu.Lock()
	s.open[esn] = e
	s.mu.Unlock()
	return e
}

// remove forgets the session if it is still the robot's current one.
func (s *Sessions) remove(esn string, e *sessionEntry) {
	s.mu.Lock()
	if s.open[esn] == e {
		delete(s.open, esn)
	}
	s.mu.Unlock()
}

// Send delivers a command to the robot's open session.
func (s *Sessions) Send(esn string, cmd *mictappb.Command) error {
	s.mu.Lock()
	e := s.open[esn]
	s.mu.Unlock()
	if e == nil {
		return ErrNoSession
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stream.Send(cmd)
}
