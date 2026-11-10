package downloader

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractOfficialUnixTarball(t *testing.T) {
	p := filepath.Join(t.TempDir(), "scrcpy.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	w := tar.NewWriter(gz)
	data := []byte("binary")
	if err = w.WriteHeader(&tar.Header{Name: "scrcpy-linux-x86_64/scrcpy", Mode: 0755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "scrcpy")
	if err = ExtractArchiveAtomically(context.Background(), p, dest, nil, "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "scrcpy"))
	if err != nil || string(got) != "binary" {
		t.Fatalf("tar payload %q: %v", got, err)
	}
}
