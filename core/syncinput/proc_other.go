//go:build !windows

package syncinput

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
