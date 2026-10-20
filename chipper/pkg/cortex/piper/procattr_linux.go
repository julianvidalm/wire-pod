package piper

import (
	"os/exec"
	"syscall"
)

// stopWithParent makes the sidecar die with wire-pod even when wire-pod is killed (it also exits at the end of its
// stdin, when wire-pod exits normally).
func stopWithParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
