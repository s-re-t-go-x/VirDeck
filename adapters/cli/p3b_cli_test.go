package cli

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"virdeck/core/service"
)

type p3bCLIService struct {
	service.Service
	locIDs     []string
	lat, lon   float64
	shotIDs    []string
	shotDir    string
	appsThird  bool
	appOp      string
	appPkg     string
	cleanID    string
	backupID           string
	backupOut          string
	backupIncludeProxy bool
	restoreZip         string
	restoreNm          string
	restoreIncProxy    bool
	createdProxy       string
	setProxyID         string
	setProxyVal        string
}

func (s *p3bCLIService) ListSystemImages(context.Context) ([]string, error) {
	return []string{"system-images;android-34;google_apis_playstore;x86_64"}, nil
}

func (s *p3bCLIService) Create(_ context.Context, spec service.CreateSpec) ([]service.Instance, error) {
	s.createdProxy = spec.Proxy
	return []service.Instance{{ID: "newc1", Name: spec.NamePrefix + "-1"}}, nil
}

func (s *p3bCLIService) SetProxy(_ context.Context, id, proxy string) error {
	s.setProxyID, s.setProxyVal = id, proxy
	return nil
}

func (s *p3bCLIService) List(context.Context) ([]service.Instance, error) {
	return []service.Instance{
		{ID: "run1", Name: "设备一", State: service.Running},
		{ID: "stop1", Name: "设备二", State: service.Stopped},
	}, nil
}

func (s *p3bCLIService) SetLocation(_ context.Context, ids []string, lat, lon float64) error {
	s.locIDs, s.lat, s.lon = ids, lat, lon
	return nil
}

func (s *p3bCLIService) CaptureScreenshots(_ context.Context, ids []string, outDir string) (map[string]string, error) {
	s.shotIDs, s.shotDir = ids, outDir
	return map[string]string{"run1": "/tmp/run1.png"}, nil
}

func (s *p3bCLIService) ListInstalledApps(_ context.Context, _ string, third bool) ([]service.InstalledApp, error) {
	s.appsThird = third
	return []service.InstalledApp{{PackageName: "com.example.demo", ThirdParty: third}}, nil
}

func (s *p3bCLIService) ManageInstalledApp(_ context.Context, _, pkg, op string) error {
	s.appPkg, s.appOp = pkg, op
	return nil
}

func (s *p3bCLIService) CleanInstanceDisk(_ context.Context, id string) (service.DiskCleanResult, error) {
	s.cleanID = id
	return service.DiskCleanResult{Mode: "offline_compact", Message: "已清理 8.0 MB"}, nil
}

func (s *p3bCLIService) BackupInstance(ctx context.Context, id, destZip string) (service.BackupResult, error) {
	return s.BackupInstanceWithOptions(ctx, id, destZip, service.BackupOptions{})
}

func (s *p3bCLIService) BackupInstanceWithOptions(_ context.Context, id, destZip string, opts service.BackupOptions) (service.BackupResult, error) {
	s.backupID, s.backupOut, s.backupIncludeProxy = id, destZip, opts.IncludeProxy
	return service.BackupResult{Path: destZip, SizeBytes: 1024}, nil
}

func (s *p3bCLIService) RestoreInstance(ctx context.Context, srcZip, name string) (service.Instance, error) {
	return s.RestoreInstanceWithOptions(ctx, srcZip, name, service.RestoreOptions{})
}

func (s *p3bCLIService) RestoreInstanceWithOptions(_ context.Context, srcZip, name string, opts service.RestoreOptions) (service.Instance, error) {
	s.restoreZip, s.restoreNm, s.restoreIncProxy = srcZip, name, opts.IncludeProxy
	return service.Instance{ID: "new1", Name: "还原机"}, nil
}

func TestP3BCLICommandsDispatch(t *testing.T) {
	ctx := context.Background()
	s := &p3bCLIService{}
	var out bytes.Buffer

	if err := Run(ctx, s, []string{"geo", "-lat", "35.68", "-lon", "139.77"}, &out); err != nil {
		t.Fatalf("geo failed: %v", err)
	}
	if !reflect.DeepEqual(s.locIDs, []string{"run1"}) || s.lat != 35.68 || s.lon != 139.77 {
		t.Fatalf("unexpected geo dispatch: ids=%v lat=%v lon=%v", s.locIDs, s.lat, s.lon)
	}

	out.Reset()
	if err := Run(ctx, s, []string{"screenshot", "-out", "/tmp/shots"}, &out); err != nil {
		t.Fatalf("screenshot failed: %v", err)
	}
	if !reflect.DeepEqual(s.shotIDs, []string{"run1"}) || s.shotDir != "/tmp/shots" {
		t.Fatalf("unexpected screenshot dispatch: ids=%v dir=%q", s.shotIDs, s.shotDir)
	}

	out.Reset()
	if err := Run(ctx, s, []string{"apps", "-id", "run1"}, &out); err != nil {
		t.Fatalf("apps list failed: %v", err)
	}
	if !s.appsThird || !strings.Contains(out.String(), "com.example.demo") {
		t.Fatalf("unexpected apps list output: third=%v out=%q", s.appsThird, out.String())
	}

	out.Reset()
	if err := Run(ctx, s, []string{"apps", "-id", "run1", "-clear", "com.example.demo"}, &out); err != nil {
		t.Fatalf("apps clear failed: %v", err)
	}
	if s.appOp != "clear" || s.appPkg != "com.example.demo" {
		t.Fatalf("unexpected apps op: op=%q pkg=%q", s.appOp, s.appPkg)
	}

	out.Reset()
	if err := Run(ctx, s, []string{"disk-clean", "-id", "stop1"}, &out); err != nil {
		t.Fatalf("disk-clean failed: %v", err)
	}
	if s.cleanID != "stop1" || !strings.Contains(out.String(), "已清理 8.0 MB") {
		t.Fatalf("unexpected disk-clean result: id=%q out=%q", s.cleanID, out.String())
	}

	out.Reset()
	if err := Run(ctx, s, []string{"backup", "-id", "stop1", "-out", "bk.zip"}, &out); err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	if s.backupID != "stop1" || s.backupOut != "bk.zip" || s.backupIncludeProxy {
		t.Fatalf("unexpected backup dispatch: id=%q out=%q includeProxy=%v", s.backupID, s.backupOut, s.backupIncludeProxy)
	}

	out.Reset()
	if err := Run(ctx, s, []string{"backup", "-id", "stop1", "-out", "bk2.zip", "-include-proxy"}, &out); err != nil {
		t.Fatalf("backup -include-proxy failed: %v", err)
	}
	if !s.backupIncludeProxy || s.backupOut != "bk2.zip" {
		t.Fatalf("expected backupIncludeProxy=true, got %v (out=%q)", s.backupIncludeProxy, s.backupOut)
	}

	out.Reset()
	if err := Run(ctx, s, []string{"restore", "-zip", "bk.zip", "-name", "新名字"}, &out); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if s.restoreZip != "bk.zip" || s.restoreNm != "新名字" || s.restoreIncProxy {
		t.Fatalf("unexpected restore dispatch: zip=%q name=%q includeProxy=%v", s.restoreZip, s.restoreNm, s.restoreIncProxy)
	}

	out.Reset()
	if err := Run(ctx, s, []string{"restore", "-zip", "bk2.zip", "-include-proxy"}, &out); err != nil {
		t.Fatalf("restore -include-proxy failed: %v", err)
	}
	if !s.restoreIncProxy || s.restoreZip != "bk2.zip" {
		t.Fatalf("expected restoreIncProxy=true, got %v (zip=%q)", s.restoreIncProxy, s.restoreZip)
	}
}

func TestCLIProxyEnvAndStdin(t *testing.T) {
	ctx := context.Background()
	s := &p3bCLIService{}
	var out bytes.Buffer

	// 1. VIRDECK_PROXY env populates create and proxy when CLI flag is omitted.
	t.Setenv("VIRDECK_PROXY", "  http://envuser:envpass@127.0.0.1:7890  ")
	if err := Run(ctx, s, []string{"create", "-name", "envtest"}, &out); err != nil {
		t.Fatalf("create with VIRDECK_PROXY failed: %v", err)
	}
	if s.createdProxy != "http://envuser:envpass@127.0.0.1:7890" {
		t.Fatalf("createdProxy=%q", s.createdProxy)
	}
	out.Reset()
	if err := Run(ctx, s, []string{"proxy", "-id", "stop1"}, &out); err != nil {
		t.Fatalf("proxy with VIRDECK_PROXY failed: %v", err)
	}
	if s.setProxyID != "stop1" || s.setProxyVal != "http://envuser:envpass@127.0.0.1:7890" {
		t.Fatalf("setProxy=(%q, %q)", s.setProxyID, s.setProxyVal)
	}
	if strings.Contains(out.String(), "envpass") {
		t.Fatalf("proxy output leaked password: %q", out.String())
	}

	// 2. Explicit -url "" overrides VIRDECK_PROXY to clear proxy.
	out.Reset()
	if err := Run(ctx, s, []string{"proxy", "-id", "stop1", "-url", ""}, &out); err != nil {
		t.Fatalf("proxy clear failed: %v", err)
	}
	if s.setProxyVal != "" {
		t.Fatalf("expected explicit -url \"\" to override VIRDECK_PROXY, got %q", s.setProxyVal)
	}

	// 3. -proxy-stdin reads from cliStdin (ignoring VIRDECK_PROXY).
	oldStdin := cliStdin
	t.Cleanup(func() { cliStdin = oldStdin })
	cliStdin = strings.NewReader("socks5://stdinuser:stdinpass@127.0.0.1:1080\r\n")
	out.Reset()
	if err := Run(ctx, s, []string{"proxy", "-id", "stop1", "-proxy-stdin"}, &out); err != nil {
		t.Fatalf("proxy -proxy-stdin failed: %v", err)
	}
	if s.setProxyVal != "socks5://stdinuser:stdinpass@127.0.0.1:1080" {
		t.Fatalf("setProxyVal=%q", s.setProxyVal)
	}
	if strings.Contains(out.String(), "stdinpass") {
		t.Fatalf("proxy stdin output leaked password: %q", out.String())
	}

	// 4. Reject conflicting -url and -proxy-stdin.
	cliStdin = strings.NewReader("http://127.0.0.1:8080\n")
	if err := Run(ctx, s, []string{"proxy", "-id", "stop1", "-url", "http://127.0.0.1:7890", "-proxy-stdin"}, &out); err == nil {
		t.Fatal("expected error when both -url and -proxy-stdin are provided")
	}

	// 5. Reject embedded newline in -proxy-stdin.
	cliStdin = strings.NewReader("http://127.0.0.1:8080\nextra-line")
	if err := Run(ctx, s, []string{"create", "-name", "badstdin", "-proxy-stdin"}, &out); err == nil {
		t.Fatal("expected error for multiline -proxy-stdin")
	}
}
