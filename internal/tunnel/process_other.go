//go:build !linux

package tunnel

import "os/exec"

func prepareProcess(cmd *exec.Cmd) {}
