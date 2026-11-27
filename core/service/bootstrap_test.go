package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"virdeck/core/downloader"
)

func TestSetProxyRedactsPasswordInEvents(t *testing.T) {
	m, _, _ := newTestManager(t, 2, 200*time.Millisecond)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "proxy_redact",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
	})
	if err != nil {
		t.Fatal(err)
	}

	evCh, cancel := m.Subscribe()
	defer cancel()

	rawProxy := "http://alice:supersecret@127.0.0.1:7890"
	if err := m.SetProxy(ctx, created[0].ID, rawProxy); err != nil {
		t.Fatalf("SetProxy: %v", err)
	}

	got, _ := m.registry.Get(created[0].ID)
	if got.Proxy != rawProxy {
		t.Fatalf("stored proxy should remain intact, got %q", got.Proxy)
	}

	select {
	case ev := <-evCh:
		if strings.Contains(ev.Message, "supersecret") {
			t.Fatalf("event message leaked proxy password: %q", ev.Message)
		}
		if !strings.Contains(ev.Message, "http://alice:***@127.0.0.1:7890") {
			t.Fatalf("expected redacted proxy in event message, got %q", ev.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SetProxy event")
	}
}

func TestAutoDeleteDownloadsRemovesOnlyDownloadedArchives(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("x86_64/system.img")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("dummy-img")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	payload := buf.Bytes()
	sum256 := sha256.Sum256(payload)
	hex256 := hex.EncodeToString(sum256[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	sdkDir := filepath.Join(dataDir, "sdk")
	avdDir := filepath.Join(dataDir, "avd")
	m, err := NewManager(Config{
		DataDir:      dataDir,
		SDKRoot:      sdkDir,
		AVDHome:      avdDir,
		SkipTCPCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	st := m.GetSettings()
	st.AutoDeleteDownloads = true
	if err := m.UpdateSettings(st); err != nil {
		t.Fatal(err)
	}

	imgID := "system-images;android-35;google_apis_playstore;x86_64"
	if err := m.SaveCustomImage(context.Background(), ImageItem{
		ID:     imgID,
		Name:   "Android 15 AutoDelete",
		URL:    srv.URL + "/a15-auto.zip",
		SHA256: hex256,
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.DownloadSystemImage(context.Background(), imgID); err != nil {
		t.Fatalf("DownloadSystemImage: %v", err)
	}
	dlFile := filepath.Join(dataDir, "downloads", "a15-auto.zip")
	if _, err := os.Stat(dlFile); !os.IsNotExist(err) {
		t.Fatalf("expected downloaded archive %s to be auto-deleted, err=%v", dlFile, err)
	}

	// Local imports must never be deleted even when AutoDeleteDownloads is true.
	localZip := filepath.Join(t.TempDir(), "local-import.zip")
	if err := os.WriteFile(localZip, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportSystemImageZip(context.Background(), imgID, localZip); err != nil {
		t.Fatalf("ImportSystemImageZip: %v", err)
	}
	if _, err := os.Stat(localZip); err != nil {
		t.Fatalf("local imported zip must not be deleted: %v", err)
	}
	_ = downloader.FallbackScrcpyChecksum
}
