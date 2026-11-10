package emu

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMacAppDataDirectoryAndOverride(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "VirDeck.app", "Contents", "MacOS")
	want := filepath.Join(home, "Library", "Application Support", "VirDeck")
	if got := defaultDataDirForHost("darwin", base, home, ""); got != want {
		t.Fatalf("app data directory = %q, want %q", got, want)
	}
	override := filepath.Join(home, "custom data")
	if got := defaultDataDirForHost("darwin", base, home, override); got != override {
		t.Fatalf("override = %q", got)
	}
	if got := defaultDataDirForHost("darwin", home, home, ""); got != filepath.Join(home, "VirDeckData") {
		t.Fatalf("portable data directory = %q", got)
	}
}

func TestHostSDKLocations(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		host  string
		parts []string
	}{{"darwin", []string{"Library", "Android", "sdk"}}, {"linux", []string{"Android", "Sdk"}}} {
		paths := hostSDKRoots(tc.host, home, "")
		want := filepath.Join(append([]string{home}, tc.parts...)...)
		found := false
		for _, path := range paths {
			if path == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s host SDK missing %q: %v", tc.host, want, paths)
		}
	}
}

func TestUnixDataDirectoryAcceptsUnicodeAndSpaces(t *testing.T) {
	for _, host := range []string{"darwin", "linux"} {
		path := filepath.Join(t.TempDir(), "用户 data")
		ok, writable, warning := validateDataDirForHost(path, host)
		if !ok || !writable || strings.Contains(warning, "ASCII") {
			t.Fatalf("%s rejected writable Unicode directory: %v %v %s", host, ok, writable, warning)
		}
	}
}
