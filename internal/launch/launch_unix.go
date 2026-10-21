//go:build unix

package launch

import (
	"os/exec"
	"syscall"
)

// detach starts the child in a new session so it has no controlling terminal.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
