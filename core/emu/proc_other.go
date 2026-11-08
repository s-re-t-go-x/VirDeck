//go:build !windows

package emu

import "os/exec"

func hideConsole(cmd *exec.Cmd)        {}
func hideConsoleKeepGUI(cmd *exec.Cmd) {}
