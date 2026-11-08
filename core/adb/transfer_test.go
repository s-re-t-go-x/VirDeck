package adb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("VIRDECK_TRANSFER_HELPER") != "1" {
		return
	}
	args := os.Args[1:]
	b, _ := json.Marshal(args)
	f, _ := os.OpenFile(os.Getenv("VIRDECK_TRANSFER_RECORD"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if f != nil {
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
	if os.Getenv("VIRDECK_TRANSFER_MODE") == "slow" {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	if os.Getenv("VIRDECK_TRANSFER_MODE") == "large" {
		fmt.Print(strings.Repeat("x", 2<<20))
		os.Exit(1)
	}
	if len(args) > 2 && args[2] == "shell" {
		script := args[len(args)-1]
		switch {
		case strings.Contains(script, "virdeck:reserve"):
			fmt.Print("/storage/emulated/0/Download/VirDeck/transfer-abc")
		case strings.Contains(script, "virdeck:list"):
			fmt.Print("d\x00资料 & 照片\x00f\x00it's $(touch nope).txt\x00")
		case strings.Contains(script, "virdeck:receive-tree"):
			if os.Getenv("VIRDECK_TRANSFER_MODE") == "tree-large" {
				fmt.Print(strings.Repeat("x", 2<<20))
				os.Exit(0)
			}
			if os.Getenv("VIRDECK_TRANSFER_MODE") == "tree-slow" {
				time.Sleep(10 * time.Second)
				os.Exit(0)
			}
			data, err := os.ReadFile(os.Getenv("VIRDECK_TRANSFER_TREE"))
			if err != nil {
				os.Exit(2)
			}
			_, _ = os.Stdout.Write(data)
		default:
			fmt.Print(os.Getenv("VIRDECK_TRANSFER_CANONICAL"))
		}
	}
	os.Exit(0)
}

func recordedTransferPulls(t *testing.T, record string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var pulls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		if len(args) > 2 && args[2] == "pull" {
			pulls = append(pulls, args)
		}
	}
	return pulls
}

func TestTransferReceiveRejectsWindowsUnsafeTreeBeforePull(t *testing.T) {
	for _, entry := range []string{
		"f\x00report.txt.\x00", "f\x00report.txt \x00", "f\x00NUL.txt\x00", "d\x00COM1\x00", "f\x00LPT¹.log\x00", "f\x00CONIN$\x00",
		"f\x00report.txt:stream\x00", "f\x00bad\\name\x00", "f\x00bad?name\x00", "f\x00bad\x01name\x00",
		"f\x00report.txt\x00f\x00REPORT.TXT\x00", "d\x00Photos\x00d\x00photos\x00", "f\x00longfilename.txt\x00f\x00LONGFI~1.TXT\x00",
		"d\x00safe\x00l\x00safe/link\x00", "f\x00bad\xffname\x00",
	} {
		t.Run(fmt.Sprintf("%q", entry), func(t *testing.T) {
			c, record := transferClient(t)
			tree := "d\x00/storage/emulated/0/Download\x00"
			fields := strings.Split(entry, "\x00")
			for i := 0; i+1 < len(fields); i += 2 {
				tree += fields[i] + "\x00/storage/emulated/0/Download/" + fields[i+1] + "\x00"
			}
			fixture := filepath.Join(t.TempDir(), "tree")
			if err := os.WriteFile(fixture, []byte(tree), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("VIRDECK_TRANSFER_TREE", fixture)
			c.SetEnv(os.Environ())
			if err := c.TransferReceive(context.Background(), 5554, "/sdcard/Download", t.TempDir()); err == nil {
				t.Error("unsafe Android tree accepted")
			}
			if pulls := recordedTransferPulls(t, record); len(pulls) != 0 {
				t.Fatalf("pull started before validation: %q", pulls)
			}
		})
	}
}

func TestTransferReceiveAcceptsUnicodeSpacesAndShellMetacharacters(t *testing.T) {
	c, record := transferClient(t)
	root := "/storage/emulated/0/Download"
	tree := "d\x00" + root + "\x00d\x00" + root + "/资料 & 照片\x00f\x00" + root + "/资料 & 照片/it's $(touch nope) [1].txt\x00"
	fixture := filepath.Join(t.TempDir(), "tree")
	if err := os.WriteFile(fixture, []byte(tree), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIRDECK_TRANSFER_TREE", fixture)
	c.SetEnv(os.Environ())
	local := t.TempDir()
	if err := c.TransferReceive(context.Background(), 5554, "/sdcard/Download", local); err != nil {
		t.Fatal(err)
	}
	pulls := recordedTransferPulls(t, record)
	if len(pulls) != 1 || pulls[0][3] != root || pulls[0][4] != local {
		t.Fatalf("pull args=%q", pulls)
	}
}

func TestTransferReceiveManifestBoundAndCancellationPreventPull(t *testing.T) {
	for _, mode := range []string{"tree-large", "tree-slow"} {
		t.Run(mode, func(t *testing.T) {
			c, record := transferClient(t)
			t.Setenv("VIRDECK_TRANSFER_MODE", mode)
			c.SetEnv(os.Environ())
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := c.TransferReceive(ctx, 5554, "/sdcard/Download", t.TempDir())
			if err == nil {
				t.Fatal("unsafe manifest read accepted")
			}
			if mode == "tree-large" && !strings.Contains(err.Error(), "64 KiB") {
				t.Fatalf("unbounded manifest error %v", err)
			}
			if mode == "tree-slow" && !strings.Contains(err.Error(), "deadline exceeded") {
				t.Fatalf("cancellation lost: %v", err)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("manifest cancellation stalled")
			}
			if pulls := recordedTransferPulls(t, record); len(pulls) != 0 {
				t.Fatalf("pull after manifest failure: %q", pulls)
			}
		})
	}
}

func transferClient(t *testing.T) (*Client, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "args.jsonl")
	t.Setenv("VIRDECK_TRANSFER_HELPER", "1")
	t.Setenv("VIRDECK_TRANSFER_RECORD", record)
	t.Setenv("VIRDECK_TRANSFER_CANONICAL", "/storage/emulated/0/Download")
	c := NewClient(exe)
	c.SetEnv(os.Environ())
	return c, record
}

func TestTransferSendPreservesNamesAndAvoidsDuplicateDestinations(t *testing.T) {
	c, record := transferClient(t)
	a := filepath.Join(t.TempDir(), "资料 & it's.txt")
	b := filepath.Join(t.TempDir(), "资料 & it's.txt")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dest, err := c.TransferSend(context.Background(), 5554, []string{a, b}, "/sdcard/Download/VirDeck")
	if err != nil {
		t.Fatal(err)
	}
	if dest != "/storage/emulated/0/Download/VirDeck/transfer-abc" {
		t.Fatalf("destination %q", dest)
	}
	raw, _ := os.ReadFile(record)
	var pushes [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var args []string
		_ = json.Unmarshal([]byte(line), &args)
		if len(args) > 2 && args[2] == "push" {
			pushes = append(pushes, args)
		}
	}
	if len(pushes) != 2 || pushes[0][3] != a || pushes[1][3] != b || pushes[0][4] == pushes[1][4] {
		t.Fatalf("unsafe push arguments: %q", pushes)
	}
	if !strings.HasSuffix(pushes[0][4], "/资料 & it's.txt") || !strings.HasSuffix(pushes[1][4], "/资料 & it's (2).txt") {
		t.Fatalf("changed filenames: %q", pushes)
	}
}

func TestTransferRejectsPrivateStorageAndCanonicalEscape(t *testing.T) {
	c, _ := transferClient(t)
	for _, p := range []string{"/data/data/app", "/sdcard/../../data", "/sdcardX/a", "/storage/emulated/0/../../../data"} {
		if _, err := c.TransferList(context.Background(), 5554, p); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
	t.Setenv("VIRDECK_TRANSFER_CANONICAL", "/data/private")
	c.SetEnv(os.Environ())
	if err := c.TransferReceive(context.Background(), 5554, "/sdcard/link", t.TempDir()); err == nil {
		t.Fatal("followed link outside shared storage")
	}
}

func TestTransferListNULProtocolAndQuotedRemotePath(t *testing.T) {
	c, record := transferClient(t)
	entries, err := c.TransferList(context.Background(), 5554, "/sdcard/Download/it's $(touch nope)")
	if err != nil || len(entries) != 2 || entries[0].Name != "资料 & 照片" || !entries[0].IsDir || entries[1].Name != "it's $(touch nope).txt" {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	raw, _ := os.ReadFile(record)
	if !strings.Contains(string(raw), `'\"'\"'`) {
		t.Fatalf("apostrophe not shell quoted: %s", raw)
	}
}

func TestTransferBoundsDiagnosticsAndHonorsCancellation(t *testing.T) {
	c, _ := transferClient(t)
	t.Setenv("VIRDECK_TRANSFER_MODE", "large")
	c.SetEnv(os.Environ())
	_, err := c.TransferList(context.Background(), 5554, "/sdcard/Download")
	if err == nil || len(err.Error()) > 70000 {
		t.Fatalf("unbounded or missing diagnostic: %d", len(fmt.Sprint(err)))
	}
	t.Setenv("VIRDECK_TRANSFER_MODE", "slow")
	c.SetEnv(os.Environ())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.TransferList(ctx, 5554, "/sdcard/Download"); err == nil {
		t.Fatal("ignored cancellation")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancellation blocked")
	}
}
