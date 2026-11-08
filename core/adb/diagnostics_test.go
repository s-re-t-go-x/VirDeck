package adb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("VIRDECK_STALL_ADB") == "1" {
		_, _ = os.Stdout.WriteString("last useful adb detail\n")
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
}
func TestADBCommandTimeoutKeepsCancellationCause(t *testing.T) {
	source, _ := os.Executable()
	raw, _ := os.ReadFile(source)
	bin := filepath.Join(t.TempDir(), "adb.exe")
	if err := os.WriteFile(bin, raw, 0755); err != nil {
		t.Fatal(err)
	}
	c := NewClient(bin)
	c.SetEnv(append(os.Environ(), "VIRDECK_STALL_ADB=1"))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := c.Shell(ctx, 5554, "wait")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "adb") {
		t.Fatalf("lost timeout cause %v", err)
	}
}
