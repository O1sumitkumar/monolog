//go:build !unix

package launch

import "os/exec"

// detach is a no-op where there is no session/controlling-terminal model to
// escape; the release targets are darwin and linux only.
func detach(*exec.Cmd) {}
