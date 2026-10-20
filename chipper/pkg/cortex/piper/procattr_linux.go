package piper

import (
	"os"
	"os/exec"
	"syscall"
)

// stopWithParent makes the sidecar die with wire-pod even when wire-pod is killed (it also exits at the end of its
// stdin, when wire-pod exits normally).
func stopWithParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
}

// killTree kills the sidecar's process group (its own group, from stopWithParent), so nothing it started keeps
// its stdout open.
func killTree(p *os.Process) {
	if syscall.Kill(-p.Pid, syscall.SIGKILL) != nil {
		p.Kill()
	}
}
