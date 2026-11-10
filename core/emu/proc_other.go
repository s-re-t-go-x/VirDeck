//go:build !windows

package emu

import (
	"os/exec"
	"syscall"
)

func hideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
}
func hideConsoleKeepGUI(cmd *exec.Cmd) { hideConsole(cmd) }

func forceKillTree(cmd *exec.Cmd) error {
	// Only a group created by this launch is ours. Never signal an inherited
	// group (which may contain VirDeck, a terminal, or unrelated processes).
	if attr := cmd.SysProcAttr; attr != nil && attr.Setpgid && attr.Pgid == 0 {
		pid := cmd.Process.Pid
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid && pgid > 0 {
			return syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}
	return cmd.Process.Kill()
}
