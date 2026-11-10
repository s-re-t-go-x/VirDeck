package config

import (
	"strings"
	"testing"
)

func TestHostDefaultsKeepCompatibleImages(t *testing.T) {
	for _, tt := range []struct{ os, arch, abi string }{{"darwin", "arm64", "arm64-v8a"}, {"darwin", "amd64", "x86_64"}, {"linux", "amd64", "x86_64"}, {"windows", "amd64", "x86_64"}} {
		t.Run(tt.os+tt.arch, func(t *testing.T) {
			d, err := ForHost(tt.os, tt.arch)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(d.Create.Image, ";"+tt.abi) {
				t.Fatalf("default image: %s", d.Create.Image)
			}
			for _, p := range d.Images {
				if p.ABI != tt.abi && !(tt.os == "windows" && p.ABI == "x86") {
					t.Fatalf("incompatible preset: %+v", p)
				}
			}
			if d.Sources.PlatformTools == "" || d.Sources.Scrcpy == "" {
				t.Fatal("missing official fallback")
			}
		})
	}
	d, err := ForHost("linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if d.Sources.PlatformTools != "" || d.Sources.Scrcpy != "" {
		t.Fatal("invented Linux ARM binary fallback")
	}
}
