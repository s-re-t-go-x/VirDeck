//go:build windows

package emu

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNoWindow = 0x08000000

// hideConsole suppresses console window creation for CLI commands (avdmanager, taskkill, emulator -accel-check).
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

func forceKillTree(cmd *exec.Cmd) error {
	killCmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	hideConsole(killCmd)
	if err := killCmd.Run(); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// hideConsoleKeepGUI suppresses the attached console box without setting SW_HIDE on GUI windows (scrcpy, windowed emulator).
func hideConsoleKeepGUI(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
	}
}
