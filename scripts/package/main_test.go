package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactNamingConvention(t *testing.T) {
	cases := []struct {
		product, version, goos, arch string
		wantGUI, wantCLI             string
	}{
		{
			product: "VirDeck",
			version: "0.7.0",
			goos:    "windows",
			arch:    "amd64",
			wantGUI: "VirDeck-v0.7.0-windows-amd64.exe",
			wantCLI: "virdeck-cli-v0.7.0-windows-amd64.exe",
		},
		{
			product: "VirDeck",
			version: "v0.9.0",
			goos:    "linux",
			arch:    "amd64",
			wantGUI: "VirDeck-v0.9.0-linux-amd64",
			wantCLI: "virdeck-cli-v0.9.0-linux-amd64",
		},
		{
			product: "VirDeck",
			version: "0.9.0",
			goos:    "darwin",
			arch:    "arm64",
			wantGUI: "VirDeck-v0.9.0-darwin-arm64",
			wantCLI: "virdeck-cli-v0.9.0-darwin-arm64",
		},
	}

	for _, tc := range cases {
		gui, cli := artifactNames(tc.product, tc.version, tc.goos, tc.arch)
		if gui != tc.wantGUI {
			t.Errorf("artifactNames(%s, %s, %s, %s) GUI = %q, want %q", tc.product, tc.version, tc.goos, tc.arch, gui, tc.wantGUI)
		}
		if cli != tc.wantCLI {
			t.Errorf("artifactNames(%s, %s, %s, %s) CLI = %q, want %q", tc.product, tc.version, tc.goos, tc.arch, cli, tc.wantCLI)
		}
	}
}

func TestArchiveContainsOnlyExplicitFilesWithExecutableModes(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "VirDeck")
	if err := os.WriteFile(bin, []byte("binary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private-settings.json"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "bundle.zip")
	if err := writeArchive(archive, "bundle", []packageFile{{bin, "VirDeck.app/Contents/MacOS/VirDeck", 0755}}); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 1 || z.File[0].Name != "bundle/VirDeck.app/Contents/MacOS/VirDeck" || z.File[0].Mode().Perm() != 0755 {
		t.Fatalf("incorrect package contents or execute bits: %+v", z.File)
	}
}
