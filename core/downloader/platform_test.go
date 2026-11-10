package downloader

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRepositoryHostSelection(t *testing.T) {
	data := []byte(`<repository><remotePackage path="emulator"><revision><major>1</major></revision><archives>
 <archive><host-os>macosx</host-os><host-arch>x64</host-arch><complete><url>intel.zip</url></complete></archive>
 <archive><host-os>macosx</host-os><host-arch>aarch64</host-arch><complete><url>arm.zip</url></complete></archive>
 <archive><host-os>linux</host-os><host-arch>x64</host-arch><complete><url>linux.zip</url></complete></archive>
 <archive><host-os>windows</host-os><host-bits>64</host-bits><complete><url>win.zip</url></complete></archive>
 </archives></remotePackage><remotePackage path="platform-tools"><archives><archive><host-os>linux</host-os><complete><url>adb-linux.zip</url></complete></archive></archives></remotePackage></repository>`)
	for _, tt := range []struct {
		os, arch, want string
		count          int
	}{{"darwin", "amd64", "intel.zip", 1}, {"darwin", "arm64", "arm.zip", 1}, {"linux", "amd64", "linux.zip", 2}, {"linux", "arm64", "", 0}, {"windows", "amd64", "win.zip", 1}, {"windows", "arm64", "", 0}} {
		t.Run(tt.os+tt.arch, func(t *testing.T) {
			got, err := ParseRepositoryXMLForHost(data, "https://example.org/", tt.os, tt.arch)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.count {
				t.Fatalf("got %d packages, want %d: %+v", len(got), tt.count, got)
			}
			if tt.want != "" && got[0].URL != "https://example.org/"+tt.want {
				t.Fatalf("wrong archive: %+v", got[0])
			}
		})
	}
}

func TestAppleSiliconCanResolveHostSpecificSystemImages(t *testing.T) {
	data := []byte(`<repository><remotePackage path="system-images;android-30;google_apis_playstore;arm64-v8a"><archives><archive><host-os>macosx</host-os><complete><url>arm64-v8a-30_r10-darwin.zip</url></complete></archive></archives></remotePackage></repository>`)
	got, err := ParseRepositoryXMLForHost(data, "https://example.org/", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("official ARM image unavailable: %+v", got)
	}
}

type zipFixtureEntry struct {
	name, body string
	mode       os.FileMode
}

func orderedZip(t *testing.T, entries []zipFixtureEntry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name}
		h.SetMode(e.mode)
		out, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = out.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestZipRejectsUnsafeLinksRegardlessOfOrder(t *testing.T) {
	for _, entries := range [][]zipFixtureEntry{
		{{"root/link", "../../outside", os.ModeSymlink | 0777}, {"root/file", "safe", 0644}},
		{{"root/link/file", "payload", 0644}, {"root/link", "../outside", os.ModeSymlink | 0777}},
		{{"root/link", "inside", os.ModeSymlink | 0777}, {"root/link/file", "payload", 0644}},
		{{"/absolute/file", "payload", 0644}},
		{{"root/../escape", "payload", 0644}},
		{{"root/dir/back", "..", os.ModeSymlink | 0777}, {"root/sneaky", "dir/back/../outside", os.ModeSymlink | 0777}},
		{{"root/a", "b", os.ModeSymlink | 0777}, {"root/b", "a", os.ModeSymlink | 0777}},
		{{"root/a", "/tmp/outside", os.ModeSymlink | 0777}},
		{{"root/.. /outside", "payload", 0644}},
		{{"root/A", ".", os.ModeSymlink | 0777}, {"root/B", "a/../outside", os.ModeSymlink | 0777}},
		{{"root/A", "inside", os.ModeSymlink | 0777}, {"root/a/file", "payload", 0644}},
		{{"root/A", "safe", 0644}, {"root/a", "different", 0644}},
		{{"root/É", ".", os.ModeSymlink | 0777}, {"root/B", "E\u0301/../outside", os.ModeSymlink | 0777}},
		{{"root/A//B", ".", os.ModeSymlink | 0777}, {"root/C", "A/B/../../outside", os.ModeSymlink | 0777}},
		{{"root/A", ".", os.ModeSymlink | 0777}, {"root/B", "a./../outside", os.ModeSymlink | 0777}},
	} {
		t.Run(entries[0].name, func(t *testing.T) {
			archive := orderedZip(t, entries)
			r, err := zip.OpenReader(archive)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err := validateZipEntries(r.File, detectSingleRootPrefix(r.File)); err == nil {
				t.Fatal("unsafe archive passed preflight")
			}
			if err := ExtractZipAtomically(context.Background(), archive, filepath.Join(t.TempDir(), "out"), nil, "", ""); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestFrameworkLinkGraphPassesPreflightOnEveryHost(t *testing.T) {
	archive := orderedZip(t, []zipFixtureEntry{{"root/App.app/Versions/A/tool", "binary", 0755}, {"root/App.app/Versions/Current", "A", os.ModeSymlink | 0777}, {"root/App.app/tool", "Versions/Current/tool", os.ModeSymlink | 0777}})
	r, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := validateZipEntries(r.File, detectSingleRootPrefix(r.File)); err != nil {
		t.Fatalf("safe framework rejected: %v", err)
	}
}
func TestZipRestoresFrameworkLinksAndExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix symlink/permission semantics")
	}
	p := orderedZip(t, []zipFixtureEntry{{"root/App.app/Versions/A/tool", "binary", 0755}, {"root/App.app/Versions/Current", "A", os.ModeSymlink | 0777}, {"root/App.app/tool", "Versions/Current/tool", os.ModeSymlink | 0777}})
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractZipAtomically(context.Background(), p, dest, nil, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dest, "App.app/tool"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0755 {
		t.Fatalf("executable mode: %v", st.Mode())
	}
	link, err := os.Readlink(filepath.Join(dest, "App.app/Versions/Current"))
	if err != nil || link != "A" {
		t.Fatalf("link %q: %v", link, err)
	}
}
