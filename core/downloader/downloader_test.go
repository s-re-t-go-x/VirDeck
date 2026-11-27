package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	cacheBase := filepath.Join(dlDir, cacheFileName("img1", srv.URL+"/test.zip"))
	partPath := cacheBase + ".part"
	metaPath := cacheBase + ".meta"
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

func TestDownloadSHA256VerificationCacheAndValidation(t *testing.T) {
	payload := bytes.Repeat([]byte("VirDeck-SHA256-Payload!"), 512)
	sum256 := sha256.Sum256(payload)
	expected256 := hex.EncodeToString(sum256[:])

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dlDir := t.TempDir()
	client := New(dlDir, "")

	// 1. Invalid checksum length/characters must fail immediately before any HTTP request.
	if _, err := client.Download(context.Background(), "badfmt", "BadFmt", srv.URL+"/tool.zip", "not-a-hex-checksum", nil); err == nil {
		t.Fatal("expected invalid checksum format error, got nil")
	}
	if hits != 0 {
		t.Fatalf("expected 0 HTTP hits on invalid checksum, got %d", hits)
	}

	// 2. Valid 64-char SHA-256 download succeeds.
	finalPath, err := client.Download(context.Background(), "tool256", "Tool256", srv.URL+"/tool256.zip", strings.ToUpper(expected256), nil)
	if err != nil {
		t.Fatalf("SHA-256 download failed: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected 1 HTTP hit, got %d", hits)
	}

	// 3. Cached file reuse passes SHA-256 without hitting the server again.
	reusedPath, err := client.Download(context.Background(), "tool256", "Tool256", srv.URL+"/tool256.zip", expected256, nil)
	if err != nil || reusedPath != finalPath {
		t.Fatalf("cached SHA-256 reuse failed: path=%q err=%v", reusedPath, err)
	}
	if hits != 1 {
		t.Fatalf("expected cache hit with no extra HTTP requests, got %d hits", hits)
	}

	// 4. Mismatched SHA-256 reports SHA-256 failure.
	bad256 := strings.Repeat("0", 64)
	if _, err := client.Download(context.Background(), "mismatch256", "Mismatch256", srv.URL+"/bad256.zip", bad256, nil); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("expected SHA-256 mismatch error, got %v", err)
	}
}

func TestDownloadProxyParseErrorRedactsPassword(t *testing.T) {
	client := New(t.TempDir(), "http://user:SuperSecretPass@127.0.0.1:badport")
	_, err := client.Download(context.Background(), "id", "name", "http://127.0.0.1:1/test.zip", "", nil)
	if err == nil {
		t.Fatal("expected error for invalid proxy port")
	}
	if strings.Contains(err.Error(), "SuperSecretPass") || !strings.Contains(err.Error(), "user:***@127.0.0.1:badport") {
		t.Fatalf("downloader error leaked proxy password: %v", err)
	}
}

func TestDownloadSecurityGuards(t *testing.T) {
	c := New(t.TempDir(), "")

	// 1. Reject non-http/https schemes.
	if _, err := c.Download(context.Background(), "id", "name", "file:///etc/passwd", "", nil); err == nil {
		t.Fatal("expected file:// URL to be rejected")
	}

	// 2. Reject HTTPS -> HTTP redirect downgrade in CheckRedirect.
	httpsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/downgraded.zip", http.StatusFound)
	}))
	defer httpsSrv.Close()
	hc, err := c.httpClient(0)
	if err != nil {
		t.Fatal(err)
	}
	hc.Transport = httpsSrv.Client().Transport
	if _, rerr := hc.Get(httpsSrv.URL + "/start.zip"); rerr == nil || !strings.Contains(rerr.Error(), "降级") {
		t.Fatalf("expected HTTPS->HTTP redirect downgrade rejection, got %v", rerr)
	}

	// 3. HTTP 416 must not recurse infinitely.
	hits := 0
	srv416 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer srv416.Close()
	if _, err := c.Download(context.Background(), "r416", "R416", srv416.URL+"/416.zip", "", nil); err == nil {
		t.Fatal("expected 416 error")
	}
	if hits != 1 {
		t.Fatalf("expected single attempt without recursion when offset==0, got %d hits", hits)
	}

	// 4. World-writable zip entry permissions must be masked to 0o755 on Unix.
	if runtime.GOOS != "windows" {
		p := orderedZip(t, []zipFixtureEntry{{"root/tool", "bin", 0o777}})
		dest := filepath.Join(t.TempDir(), "out")
		if err := ExtractZipAtomically(context.Background(), p, dest, nil, "", ""); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(filepath.Join(dest, "tool"))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o022 != 0 {
			t.Fatalf("extracted binary remained group/world writable: %v", st.Mode().Perm())
		}
	}
}

func TestDownloadURLHashedCacheIsolationAndNoUnverifiedReuse(t *testing.T) {
	v1Body := []byte("archive-version-1-payload")
	v2Body := []byte("archive-version-2-payload")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/v1/archive.zip":
			_, _ = w.Write(v1Body)
		case "/v2/archive.zip":
			_, _ = w.Write(v2Body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dlDir := t.TempDir()
	c := New(dlDir, "")

	// Same filename under two distinct URLs must land in distinct cached paths.
	p1, err := c.Download(context.Background(), "id", "v1", srv.URL+"/v1/archive.zip", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c.Download(context.Background(), "id", "v2", srv.URL+"/v2/archive.zip", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("expected distinct cache paths for distinct URLs, both resolved to %q", p1)
	}
	got1, _ := os.ReadFile(p1)
	got2, _ := os.ReadFile(p2)
	if !bytes.Equal(got1, v1Body) || !bytes.Equal(got2, v2Body) {
		t.Fatalf("cached payloads collided: v1=%q v2=%q", got1, got2)
	}

	// Downloading with an empty expectedChecksum must not blindly trust a tampered/pre-existing cached file.
	if err := os.WriteFile(p1, []byte("stale-unverified-cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	hitsBefore := hits
	p1Again, err := c.Download(context.Background(), "id", "v1", srv.URL+"/v1/archive.zip", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hits != hitsBefore+1 {
		t.Fatalf("expected re-fetch when expectedChecksum is empty, got %d extra HTTP hits", hits-hitsBefore)
	}
	refreshed, _ := os.ReadFile(p1Again)
	if !bytes.Equal(refreshed, v1Body) {
		t.Fatalf("expected refreshed payload %q, got %q", v1Body, refreshed)
	}
}
