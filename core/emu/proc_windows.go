//go:build windows

package emu

import (
	"os/exec"
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

// hideConsoleKeepGUI suppresses the attached console box without setting SW_HIDE on GUI windows (scrcpy, windowed emulator).
func hideConsoleKeepGUI(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
	}
}
