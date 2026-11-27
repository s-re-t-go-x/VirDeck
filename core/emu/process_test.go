package emu

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Act as an emulator whose helper inherits its output pipe and outlives it.
func init() {
	mode := os.Getenv("VIRDECK_TEST_PIPE_HELPER")
	if mode == "" {
		return
	}
	if mode == "child" {
		if err := os.WriteFile(os.Getenv("VIRDECK_TEST_CHILD_PID"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(2)
		}
		time.Sleep(20 * time.Second)
		os.Exit(0)
	}
	exe, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	child := exec.Command(exe)
	child.Env = append(os.Environ(), "VIRDECK_TEST_PIPE_HELPER=child")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	fmt.Println("emulator exited")
	os.Exit(0)
}

func TestStartProcessReportsExitWithInheritedOutputPipe(t *testing.T) {
	dir := t.TempDir()
	sdk := filepath.Join(dir, "sdk")
	bin := filepath.Join(sdk, "emulator", exeName("emulator"))
	if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, data, 0755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "child.pid")
	t.Setenv("VIRDECK_TEST_PIPE_HELPER", "parent")
	t.Setenv("VIRDECK_TEST_CHILD_PID", pidFile)
	c := NewControllerWithDataDir(dir, sdk, filepath.Join(dir, "avd"), DefaultADBServerPort)
	proc, err := c.StartProcess(LaunchOptions{AVDName: "pipe_test", Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Only terminate the helper created by this test, never a real emulator.
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(string(b)); err == nil {
				if child, err := os.FindProcess(pid); err == nil {
					_ = child.Kill()
					_, _ = child.Wait()
				}
			}
		}
		select {
		case <-proc.Done():
		case <-time.After(5 * time.Second):
			t.Error("process cleanup timed out")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("output-holding child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-proc.Done():
		if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
			t.Fatalf("process exit: %v", err)
		}
		if !strings.Contains(proc.Output(), "emulator exited") {
			t.Fatalf("lost emulator output: %q", proc.Output())
		}
	case <-time.After(4 * time.Second):
		t.Fatal("exited emulator still waits for a helper's inherited output pipe")
	}
}

func TestBuildStartArgsProxyDoesNotForcePublicDNS(t *testing.T) {
	args := buildStartArgs(LaunchOptions{
		AVDName:  "test_avd",
		Port:     5554,
		Proxy:    "http://127.0.0.1:7890",
		Headless: true,
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-http-proxy http://127.0.0.1:7890") {
		t.Fatalf("missing -http-proxy in args: %v", args)
	}
	if strings.Contains(joined, "-dns-server") || strings.Contains(joined, "8.8.8.8") {
		t.Fatalf("proxy launch must not force hardcoded public DNS: %v", args)
	}

	withDNS := buildStartArgs(LaunchOptions{
		AVDName:   "test_avd",
		Port:      5554,
		Proxy:     "http://127.0.0.1:7890",
		DNSServer: "1.1.1.1",
	})
	if !strings.Contains(strings.Join(withDNS, " "), "-dns-server 1.1.1.1") {
		t.Fatalf("explicit DNSServer not applied: %v", withDNS)
	}
}
