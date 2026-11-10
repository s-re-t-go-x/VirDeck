//go:build linux || darwin

package emu

import (
	"bufio"
	"io"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestUnixLaunchUsesOwnedProcessGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & echo ready; wait")
	hideConsoleKeepGUI(cmd)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil || pgid != cmd.Process.Pid {
		t.Fatalf("emulator did not receive its own process group: %d, %v", pgid, err)
	}
	reader := bufio.NewReader(pipe)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, reader); close(closed) }()
	p := &Process{cmd: cmd}
	if err := p.ForceKillTree(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("descendant retained emulator output pipe after tree kill")
	}
}

func TestForceKillTreeDoesNotKillForeignGroup(t *testing.T) {
	guard := exec.Command("sleep", "30")
	guard.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := guard.Start(); err != nil {
		t.Fatal(err)
	}
	guardDone := make(chan error, 1)
	go func() { guardDone <- guard.Wait() }()
	t.Cleanup(func() { _ = guard.Process.Kill(); <-guardDone })
	child := exec.Command("sleep", "30")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: guard.Process.Pid}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	if err := (&Process{cmd: child}).ForceKillTree(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-guardDone:
		// Restore the completion notification for cleanup.
		guardDone <- err
		t.Fatalf("foreign group leader was killed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}
