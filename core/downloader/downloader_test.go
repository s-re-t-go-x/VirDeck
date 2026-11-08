package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadResumeAndSHA1Verification(t *testing.T) {
	payload := bytes.Repeat([]byte("VirDeck-M4-Resume-Test-Data-Block!"), 1024)
	sum := sha1.Sum(payload)
	expectedHex := hex.EncodeToString(sum[:])

	var sawRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1-etag"`)
		rng := r.Header.Get("Range")
		if strings.HasPrefix(rng, "bytes=") {
			sawRange = rng
			offStr := strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-")
			off, _ := strconv.Atoi(offStr)
			if off > 0 && off < len(payload) && r.Header.Get("If-Range") == `"v1-etag"` {
				w.Header().Set("Content-Length", strconv.Itoa(len(payload)-off))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(payload[off:])
				return
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dlDir := t.TempDir()
	client := New(dlDir, "")

	// Pre-populate partial .part and .meta to test HTTP Range + If-Range resume.
	partPath := filepath.Join(dlDir, "test.zip.part")
	metaPath := filepath.Join(dlDir, "test.zip.meta")
	half := len(payload) / 2
	if err := os.WriteFile(partPath, payload[:half], 0o644); err != nil {
		t.Fatal(err)
	}
	_ = savePartMeta(metaPath, partMeta{
		URL:        srv.URL + "/test.zip",
		ETag:       `"v1-etag"`,
		TotalBytes: int64(len(payload)),
		SHA1:       expectedHex,
	})

	finalPath, err := client.Download(context.Background(), "img1", "Test Image", srv.URL+"/test.zip", expectedHex, nil)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if sawRange != "bytes="+strconv.Itoa(half)+"-" {
		t.Fatalf("expected Range bytes=%d-, got %q", half, sawRange)
	}
	gotBytes, err := os.ReadFile(finalPath)
	if err != nil || !bytes.Equal(gotBytes, payload) {
		t.Fatalf("downloaded file content mismatch")
	}

	// Verify bad SHA-1 is rejected.
	_, err = client.Download(context.Background(), "bad", "Bad SHA", srv.URL+"/bad.zip", "0000000000000000000000000000000000000000", nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-1") {
		t.Fatalf("expected SHA-1 error, got %v", err)
	}
}

func TestExtractZipAtomicallyAndZipSlipGuard(t *testing.T) {
	tmp := t.TempDir()
	goodZip := filepath.Join(tmp, "good.zip")
	writeZip(t, goodZip, map[string]string{
		"x86_64/source.properties": "AndroidVersion.ApiLevel=34\nSystemImage.Abi=x86_64\n",
		"x86_64/system.img":        "dummy-system-image",
	})

	destDir := filepath.Join(tmp, "extracted")
	if err := ExtractZipAtomically(context.Background(), goodZip, destDir, nil, "id", "name"); err != nil {
		t.Fatalf("ExtractZipAtomically failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "source.properties")); err != nil {
		t.Fatalf("expected stripped top-level folder x86_64/ so source.properties is at root: %v", err)
	}

	evilZip := filepath.Join(tmp, "evil.zip")
	writeZip(t, evilZip, map[string]string{
		"../../evil.txt": "pwned",
	})
	if err := ExtractZipAtomically(context.Background(), evilZip, filepath.Join(tmp, "evil_out"), nil, "id", "name"); err == nil {
		t.Fatalf("expected Zip Slip traversal error, got nil")
	}
}

func TestParseRepositoryXML(t *testing.T) {
	sample := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<sdk:sdk-repository xmlns:sdk="http://schemas.android.com/sdk/android/repo/sys-img2/03">
	<remotePackage path="system-images;android-34;google_apis_playstore;x86_64">
		<revision><major>14</major></revision>
		<display-name>Google Play Intel x86_64 Atom System Image</display-name>
		<type-details>
			<api-level>34</api-level>
			<tag><id>google_apis_playstore</id><display>Google Play</display></tag>
			<abi>x86_64</abi>
		</type-details>
		<archives>
			<archive>
				<complete>
					<size>1623456789</size>
					<checksum>aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d</checksum>
					<url>x86_64-34_r14.zip</url>
				</complete>
			</archive>
		</archives>
	</remotePackage>
</sdk:sdk-repository>`)

	pkgs, err := ParseRepositoryXML(sample, "https://dl.google.com/android/repository/sys-img/google_apis_playstore/")
	if err != nil {
		t.Fatalf("ParseRepositoryXML: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	p := pkgs[0]
	if p.PackagePath != "system-images;android-34;google_apis_playstore;x86_64" ||
		p.URL != "https://dl.google.com/android/repository/sys-img/google_apis_playstore/x86_64-34_r14.zip" ||
		p.SHA1 != "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d" {
		t.Fatalf("unexpected parsed package: %+v", p)
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRepositorySkipsUnstableChannelsAndRevisions(t *testing.T) {
	sample := []byte(`<repository>
 <remotePackage path="stable"><display-name>Stable</display-name><channelRef ref="channel-0"/><revision><major>1</major></revision><archives><archive><complete><url>stable.zip</url></complete></archive></archives></remotePackage>
 <remotePackage path="beta"><display-name>Beta</display-name><channelRef ref="channel-1"/><archives><archive><complete><url>beta.zip</url></complete></archive></archives></remotePackage>
 <remotePackage path="preview"><revision><major>1</major><preview>2</preview></revision><archives><archive><complete><url>preview.zip</url></complete></archive></archives></remotePackage>
 </repository>`)
	pkgs, err := ParseRepositoryXML(sample, "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].PackagePath != "stable" {
		t.Fatalf("unstable packages leaked: %+v", pkgs)
	}
}
