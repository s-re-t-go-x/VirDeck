package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestRedactProxyURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "   ", want: ""},
		{name: "no creds", in: "http://127.0.0.1:7890", want: "http://127.0.0.1:7890"},
		{name: "user only", in: "http://alice@127.0.0.1:7890", want: "http://alice@127.0.0.1:7890"},
		{name: "user and pass", in: "http://alice:s3cr3t@127.0.0.1:7890", want: "http://alice:***@127.0.0.1:7890"},
		{name: "socks5 with empty pass", in: "socks5://alice:@proxy.local:1080", want: "socks5://alice:***@proxy.local:1080"},
		{name: "malformed url with creds", in: "http://bob:pw@bad host:7890/path", want: "http://bob:***@bad host:7890/path"},
		{name: "pass with slash", in: "http://alice:p/ss@127.0.0.1:7890", want: "http://alice:***@127.0.0.1:7890"},
		{name: "pass with question", in: "http://alice:p?ss@127.0.0.1:7890", want: "http://alice:***@127.0.0.1:7890"},
		{name: "pass with hash", in: "http://alice:p#ss@127.0.0.1:7890", want: "http://alice:***@127.0.0.1:7890"},
		{name: "path with at", in: "http://example.com:8080/foo/@bar", want: "http://example.com:8080/foo/@bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactProxyURL(tc.in); got != tc.want {
				t.Fatalf("RedactProxyURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactErrorHidesProxyCredentials(t *testing.T) {
	_, parseErr := url.Parse("http://user:SuperSecretPass@127.0.0.1:badport")
	if parseErr == nil {
		t.Fatal("expected url.Parse error")
	}
	got := RedactError(parseErr)
	if strings.Contains(got, "SuperSecretPass") || !strings.Contains(got, "user:***@127.0.0.1:badport") {
		t.Fatalf("RedactError leaked or failed to mask password: %q", got)
	}
	wrapped := fmt.Errorf("同步镜像失败: %w", parseErr)
	gotWrapped := RedactError(wrapped)
	if !strings.HasPrefix(gotWrapped, "同步镜像失败:") || strings.Contains(gotWrapped, "SuperSecretPass") {
		t.Fatalf("RedactError dropped outer prefix or leaked password: %q", gotWrapped)
	}
	if RedactError(errors.New("connect http://a:MyPass123@127.0.0.1:8080 failed")) != "connect http://a:***@127.0.0.1:8080 failed" {
		t.Fatalf("unexpected RedactError inline scrub: %q", RedactError(errors.New("connect http://a:MyPass123@127.0.0.1:8080 failed")))
	}
	if gotText := RedactText("proxy http://a:p/s?q#h@127.0.0.1:8080 err"); strings.Contains(gotText, "p/s?q#h") || !strings.Contains(gotText, "http://a:***@127.0.0.1:8080") {
		t.Fatalf("RedactText failed to scrub special-char password: %q", gotText)
	}
}

func TestPinnedFavoritesIncludeLegacyAndModernVersions(t *testing.T) {
	for _, host := range []struct {
		os, arch string
		wantIDs  []string
	}{
		{
			os:   "windows",
			arch: "amd64",
			wantIDs: []string{
				"system-images;android-30;google_apis_playstore;x86_64",
				"system-images;android-34;google_apis_playstore;x86_64",
				"system-images;android-28;google_apis_playstore;x86_64",
				"system-images;android-25;google_apis_playstore;x86",
			},
		},
		{
			os:   "darwin",
			arch: "arm64",
			wantIDs: []string{
				"system-images;android-30;google_apis_playstore;arm64-v8a",
				"system-images;android-34;google_apis_playstore;arm64-v8a",
			},
		},
	} {
		d, err := ForHost(host.os, host.arch)
		if err != nil {
			t.Fatalf("ForHost(%s/%s): %v", host.os, host.arch, err)
		}
		got := map[string]ImagePreset{}
		for _, img := range d.Images {
			got[img.ID] = img
		}
		for _, id := range host.wantIDs {
			p, ok := got[id]
			if !ok || !p.Favorite || p.Note == "" {
				t.Fatalf("%s/%s missing pinned favorite %s: %+v", host.os, host.arch, id, p)
			}
		}
	}
}
