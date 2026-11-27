//go:build windows

package emu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// hideConsole suppresses console window creation for CLI commands (avdmanager, taskkill, emulator -accel-check).
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

func forceKillTree(cmd *exec.Cmd) error {
	taskkillBin := "taskkill"
	if sysRoot := os.Getenv("SystemRoot"); sysRoot != "" {
		cand := filepath.Join(sysRoot, "System32", "taskkill.exe")
		if _, err := os.Stat(cand); err == nil {
			taskkillBin = cand
		}
	}
	killCmd := exec.Command(taskkillBin, "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
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

func detachBackgroundProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createNewProcessGroup | detachedProcess,
	}
}
