package adb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("VIRDECK_TEST_ADB_HELPER") != "1" {
		return
	}
	marker := os.Getenv("VIRDECK_TEST_ADB_MARKER")
	args := strings.Join(os.Args[1:], " ")
	switch {
	case strings.HasSuffix(args, "devices"):
		fmt.Println("List of devices attached")
		fmt.Println("emulator-5554 offline")
		os.Exit(0)
	case strings.HasSuffix(args, "shell getprop sys.boot_completed"):
		b, _ := os.ReadFile(marker)
		if string(b) == "ready" {
			fmt.Println("1")
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "adb: device unauthorized")
		os.Exit(1)
	case strings.HasSuffix(args, "kill-server"):
		if err := os.WriteFile(marker, []byte("killed"), 0600); err != nil {
			os.Exit(2)
		}
	case strings.HasSuffix(args, "start-server"):
		b, _ := os.ReadFile(marker)
		if string(b) != "killed" {
			os.Exit(2)
		}
		if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
			os.Exit(2)
		}
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func testADB(t *testing.T, isolated bool) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "server-state")
	t.Setenv("VIRDECK_TEST_ADB_HELPER", "1")
	t.Setenv("VIRDECK_TEST_ADB_MARKER", marker)
	c := NewClient(exe)
	env := os.Environ()
	if isolated {
		env = append(env, "ANDROID_ADB_SERVER_PORT=5137", "ADB_VENDOR_KEYS="+filepath.Join(dir, "adbkey"))
	}
	c.SetEnv(env)
	return c, marker
}

func TestWaitForBootRecoversUnauthorizedDedicatedServer(t *testing.T) {
	c, _ := testADB(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.WaitForBoot(ctx, 5554, 10*time.Millisecond); err != nil {
		t.Fatalf("boot should recover from stale server keys: %v", err)
	}
}

func TestWaitForBootPreservesUnauthorizedWithoutRestartingDefaultServer(t *testing.T) {
	c, marker := testADB(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := c.WaitForBoot(ctx, 5554, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("want diagnostic containing last ADB error, got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("must not restart the default/shared ADB server")
	}
}

func TestHasEmulatorsCountsOfflineAndReportsFailure(t *testing.T) {
	c, _ := testADB(t, true)
	active, err := c.HasEmulators(context.Background())
	if err != nil || !active {
		t.Fatalf("offline emulator must block cleanup: %v, %v", active, err)
	}
	c.SetPath(filepath.Join(t.TempDir(), "missing-adb.exe"))
	if _, err := c.HasEmulators(context.Background()); err == nil {
		t.Fatal("detection failure must be reported")
	}
}
