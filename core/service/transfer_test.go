package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"virdeck/core/adb"
)

// The external Android boundary uses a real on-disk fixture so the assertions
// catch missing transfer writes, overwritten exports, and incomplete failures.
type transferDiskADB struct {
	*fakeADB
	root    string
	fail    error
	entered chan struct{}
	release chan struct{}
}

func (a *transferDiskADB) TransferSend(ctx context.Context, port int, paths []string, dir string) (string, error) {
	if a.entered != nil {
		close(a.entered)
		select {
		case <-a.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if a.fail != nil {
		return "", a.fail
	}
	dest, err := os.MkdirTemp(a.root, "send-")
	if err != nil {
		return "", err
	}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			return "", e
		}
		if e = os.WriteFile(filepath.Join(dest, filepath.Base(p)), b, 0600); e != nil {
			return "", e
		}
	}
	return dir + "/" + filepath.Base(dest), nil
}
func (a *transferDiskADB) TransferReceive(_ context.Context, _ int, _ string, dest string) error {
	if a.fail != nil {
		return a.fail
	}
	return os.WriteFile(filepath.Join(dest, "资料.txt"), []byte("android"), 0600)
}
func (a *transferDiskADB) TransferList(context.Context, int, string) ([]adb.TransferEntry, error) {
	return []adb.TransferEntry{{Name: "资料.txt"}}, a.fail
}
func transferManager(t *testing.T) (*Manager, *transferDiskADB, string) {
	t.Helper()
	m, _, fa := newTestManager(t, 2, time.Millisecond)
	items, err := m.Create(context.Background(), CreateSpec{NamePrefix: "transfer", Count: 1, SystemImage: "system-images;android-34;google_apis;x86_64", MemoryMB: 2048, Cores: 2})
	if err != nil {
		t.Fatal(err)
	}
	id := items[0].ID
	_, err = m.registry.Update(id, func(i *Instance) error { i.State = Running; i.Port = 5554; return nil })
	if err != nil {
		t.Fatal(err)
	}
	a := &transferDiskADB{fakeADB: fa, root: t.TempDir()}
	m.adb = a
	return m, a, id
}
func TestTransferServiceExportsWithoutOverwritingAndSendsRealFile(t *testing.T) {
	m, a, id := transferManager(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "资料.txt")
	if err := os.WriteFile(src, []byte("windows"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SendFiles(context.Background(), SendFilesRequest{IDs: []string{id}, LocalPaths: []string{src}}); err != nil {
		t.Fatal(err)
	}
	exports, _ := os.ReadDir(a.root)
	if len(exports) != 1 {
		t.Fatalf("send did not create one export: %v", exports)
	}
	b, _ := os.ReadFile(filepath.Join(a.root, exports[0].Name(), "资料.txt"))
	if string(b) != "windows" {
		t.Fatalf("sent bytes=%q", b)
	}
	first, err := m.ReceiveFiles(context.Background(), id, "/sdcard/Download/资料.txt", dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.ReceiveFiles(context.Background(), id, "/sdcard/Download/资料.txt", dir)
	if err != nil || first == second {
		t.Fatalf("export collision %q %q %v", first, second, err)
	}
	for _, p := range []string{first, second} {
		b, e := os.ReadFile(filepath.Join(p, "资料.txt"))
		if e != nil || string(b) != "android" {
			t.Fatalf("export %q: %q %v", p, b, e)
		}
	}
	b, _ = os.ReadFile(src)
	if string(b) != "windows" {
		t.Fatal("existing destination overwritten")
	}
}
func TestTransferServiceRejectsStoppedPrivateAndMissingInputs(t *testing.T) {
	m, a, id := transferManager(t)
	if _, err := m.SendFiles(context.Background(), SendFilesRequest{IDs: []string{id}, LocalPaths: []string{filepath.Join(t.TempDir(), "missing")}}); err == nil {
		t.Fatal("missing local source accepted")
	}
	if _, err := m.ReceiveFiles(context.Background(), id, "/data/data/app", t.TempDir()); err == nil {
		t.Fatal("private path accepted")
	}
	_, _ = m.registry.Update(id, func(i *Instance) error { i.State = Stopped; return nil })
	if _, err := m.ListTransferFiles(context.Background(), id, "/sdcard"); err == nil {
		t.Fatal("stopped instance accepted")
	}
	entries, _ := os.ReadDir(a.root)
	if len(entries) != 0 {
		t.Fatal("invalid request wrote files")
	}
}
func TestTransferServiceFailureKeepsExactDiagnosticAndCancellation(t *testing.T) {
	m, a, id := transferManager(t)
	a.fail = errors.New("device unauthorized: precise reason")
	_, err := m.ReceiveFiles(context.Background(), id, "/sdcard/Download", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "device unauthorized: precise reason") {
		t.Fatalf("lost diagnostic %v", err)
	}
	a.fail = nil
	src := filepath.Join(t.TempDir(), "a")
	_ = os.WriteFile(src, []byte("x"), 0600)
	a.entered = make(chan struct{})
	a.release = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.SendFiles(ctx, SendFilesRequest{IDs: []string{id}, LocalPaths: []string{src}})
		done <- err
	}()
	<-a.entered
	if m.maintenance.TryLock() {
		m.maintenance.Unlock()
		t.Fatal("transfer did not exclude maintenance")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost %v", err)
	}
}
