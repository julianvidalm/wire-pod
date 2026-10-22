//go:build !linux

package piper

import (
	"os"
	"os/exec"
)

// stopWithParent: outside Linux the sidecar exits at the end of its stdin, when wire-pod exits.
func stopWithParent(*exec.Cmd) {}

// killTree kills the sidecar.
func killTree(p *os.Process) { p.Kill() }
