package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"virdeck/core/adb"
)

type pipelineFakeADB struct {
	*fakeADB
	mu             sync.Mutex
	pmChecks       int
	pmErr          error
	installOpts    []adb.InstallOptions
	installMulti   [][]string
	installErr     error
	resolvedMap    map[string]string
	resolveCalls   int
	launched       []string
	launchErr      error
}

func (p *pipelineFakeADB) InstallMultipleAPKsWithOptions(ctx context.Context, port int, apkPaths []string, opts adb.InstallOptions) (string, error) {
	p.mu.Lock()
	p.installOpts = append(p.installOpts, opts)
	p.installMulti = append(p.installMulti, append([]string(nil), apkPaths...))
	err := p.installErr
	p.mu.Unlock()
	if err != nil {
		return "", err
	}
	return "Success", nil
}

func (p *pipelineFakeADB) CheckPackageManagerReady(ctx context.Context, port int) error {
	p.mu.Lock()
	p.pmChecks++
	err := p.pmErr
	p.mu.Unlock()
	return err
}

func (p *pipelineFakeADB) InstallAPKWithOptions(ctx context.Context, port int, apkPath string, opts adb.InstallOptions) (string, error) {
	p.mu.Lock()
	p.installOpts = append(p.installOpts, opts)
	p.installed = append(p.installed, apkPath)
	err := p.installErr
	p.mu.Unlock()
	if err != nil {
		return "", err
	}
	return "Success", nil
}

func (p *pipelineFakeADB) ResolveLauncherActivity(ctx context.Context, port int, pkg string) (string, error) {
	p.mu.Lock()
	p.resolveCalls++
	comp := p.resolvedMap[pkg]
	p.mu.Unlock()
	return comp, nil
}

func (p *pipelineFakeADB) LaunchActivity(ctx context.Context, port int, component string) error {
	p.mu.Lock()
	p.launched = append(p.launched, component)
	err := p.launchErr
	p.mu.Unlock()
	return err
}

func buildMinimalAXMLForTest(pkg string) []byte {
	strs := []string{"manifest", "package", pkg}
	var pool bytes.Buffer
	offsets := make([]uint32, len(strs))
	for i, s := range strs {
		offsets[i] = uint32(pool.Len())
		u16 := utf16.Encode([]rune(s))
		_ = binary.Write(&pool, binary.LittleEndian, uint16(len(u16)))
		for _, u := range u16 {
			_ = binary.Write(&pool, binary.LittleEndian, u)
		}
		_ = binary.Write(&pool, binary.LittleEndian, uint16(0))
	}
	for pool.Len()%4 != 0 {
		pool.WriteByte(0)
	}
	stringsStart := 28 + len(offsets)*4
	poolSize := stringsStart + pool.Len()
	var pc bytes.Buffer
	_ = binary.Write(&pc, binary.LittleEndian, uint16(0x0001))
	_ = binary.Write(&pc, binary.LittleEndian, uint16(28))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(poolSize))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(len(strs)))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(0))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(0))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(stringsStart))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(0))
	for _, off := range offsets {
		_ = binary.Write(&pc, binary.LittleEndian, off)
	}
	pc.Write(pool.Bytes())

	var ec bytes.Buffer
	_ = binary.Write(&ec, binary.LittleEndian, uint16(0x0102))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(16))
	_ = binary.Write(&ec, binary.LittleEndian, uint32(16+20+20))
	_ = binary.Write(&ec, binary.LittleEndian, uint32(1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(-1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(-1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(0))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(20))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(20))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ec, binary.LittleEndian, int32(-1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(2))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(8))
	ec.WriteByte(0)
	ec.WriteByte(0x03)
	_ = binary.Write(&ec, binary.LittleEndian, uint32(2))

	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(0x0003))
	_ = binary.Write(&out, binary.LittleEndian, uint16(8))
	_ = binary.Write(&out, binary.LittleEndian, uint32(8+pc.Len()+ec.Len()))
	out.Write(pc.Bytes())
	out.Write(ec.Bytes())
	return out.Bytes()
}

func writeTestAPKFile(t *testing.T, pkg string, abis ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.apk")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("AndroidManifest.xml")
	_, _ = w.Write(buildMinimalAXMLForTest(pkg))
	for _, abi := range abis {
		lw, _ := zw.Create("lib/" + abi + "/libx.so")
		_, _ = lw.Write([]byte("ELF"))
	}
	_ = zw.Close()
	_ = f.Close()
	return p
}

func TestInstallAppPipelineAutoStartLaunchAndScreen(t *testing.T) {
	m, fe, fa := newTestManager(t, 2, 100*time.Millisecond)
	pfa := &pipelineFakeADB{
		fakeADB:     fa,
		resolvedMap: map[string]string{"com.example.chat": "com.example.chat/.MainActivity"},
	}
	m.adb = pfa

	var screenOpened []string
	fe.screenStart = func(id string) { screenOpened = append(screenOpened, id) }

	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "dev",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		Headless:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inst := created[0]
	t.Cleanup(func() { _ = m.Stop(context.Background(), inst.ID) })

	apkPath := writeTestAPKFile(t, "com.example.chat", "arm64-v8a")
	meta, err := m.InspectAPK(apkPath)
	if err != nil || meta.Package != "com.example.chat" || len(meta.ABIs) != 1 {
		t.Fatalf("InspectAPK unexpected: meta=%+v err=%v", meta, err)
	}

	evCh, cancel := m.Subscribe()
	defer cancel()

	// Unblock WaitForBoot asynchronously after Start is triggered.
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(fa.bootReady)
	}()

	err = m.InstallApp(ctx, InstallAppRequest{
		InstanceIDs:      []string{inst.ID},
		APKPath:          apkPath,
		GrantPermissions: true,
		LaunchApp:        true,
		OpenScreen:       true,
	})
	if err != nil {
		t.Fatalf("InstallApp failed: %v", err)
	}

	pfa.mu.Lock()
	pmChecks := pfa.pmChecks
	opts := append([]adb.InstallOptions(nil), pfa.installOpts...)
	launched := append([]string(nil), pfa.launched...)
	pfa.mu.Unlock()

	if pmChecks != 1 {
		t.Fatalf("expected 1 CheckPackageManagerReady call, got %d", pmChecks)
	}
	if len(opts) != 1 || !opts[0].GrantPermissions {
		t.Fatalf("expected GrantPermissions=true install, got %+v", opts)
	}
	if len(launched) != 1 || launched[0] != "com.example.chat/.MainActivity" {
		t.Fatalf("expected LaunchActivity call, got %v", launched)
	}
	if len(screenOpened) != 1 || screenOpened[0] != inst.ID {
		t.Fatalf("expected OpenScreen for headless instance, got %v", screenOpened)
	}

	updated, _ := m.registry.Get(inst.ID)
	if len(updated.RecentApps) != 1 || updated.RecentApps[0].Package != "com.example.chat" || updated.RecentApps[0].Component != "com.example.chat/.MainActivity" {
		t.Fatalf("RecentApps not persisted: %+v", updated.RecentApps)
	}

	sawStage := false
	for drain := true; drain; {
		select {
		case ev := <-evCh:
			if ev.Type == EventPipelineStage && ev.InstanceID == inst.ID {
				sawStage = true
			}
		default:
			drain = false
		}
	}
	if !sawStage {
		t.Fatal("expected EventPipelineStage events during InstallApp")
	}
}

func TestInstallAppSkipsOpenScreenWhenNotHeadlessAndHandlesNoLauncher(t *testing.T) {
	m, fe, fa := newTestManager(t, 2, 100*time.Millisecond)
	pfa := &pipelineFakeADB{
		fakeADB:     fa,
		resolvedMap: map[string]string{}, // empty: no LAUNCHER activity (service-only APK)
	}
	m.adb = pfa
	close(fa.bootReady)

	var screenOpened []string
	fe.screenStart = func(id string) { screenOpened = append(screenOpened, id) }

	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "gui",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		Headless:    false,
	})
	if err != nil {
		t.Fatal(err)
	}
	inst := created[0]
	t.Cleanup(func() { _ = m.Stop(context.Background(), inst.ID) })

	apkPath := writeTestAPKFile(t, "com.example.ime")
	if err := m.InstallApp(ctx, InstallAppRequest{
		InstanceIDs: []string{inst.ID},
		APKPath:     apkPath,
		LaunchApp:   true,
		OpenScreen:  true,
	}); err != nil {
		t.Fatalf("InstallApp should succeed when no launcher exists: %v", err)
	}
	if len(screenOpened) != 0 {
		t.Fatalf("OpenScreen must be skipped when Headless == false, got %v", screenOpened)
	}
	pfa.mu.Lock()
	launched := len(pfa.launched)
	pfa.mu.Unlock()
	if launched != 0 {
		t.Fatal("LaunchActivity must not be called when no LAUNCHER activity resolved")
	}
}

func TestInstallAppErrorTranslationAndLaunchAppHistoryCap(t *testing.T) {
	m, _, fa := newTestManager(t, 2, 100*time.Millisecond)
	pfa := &pipelineFakeADB{
		fakeADB:     fa,
		installErr:  errors.New("Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE]"),
		resolvedMap: map[string]string{},
	}
	m.adb = pfa
	close(fa.bootReady)

	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "err",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		Headless:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inst := created[0]
	t.Cleanup(func() { _ = m.Stop(context.Background(), inst.ID) })

	apkPath := writeTestAPKFile(t, "com.example.sig")
	err = m.InstallApp(ctx, InstallAppRequest{
		InstanceIDs: []string{inst.ID},
		APKPath:     apkPath,
	})
	if err == nil || !strings.Contains(err.Error(), "卸载会清除该应用本地数据") {
		t.Fatalf("expected translated signature conflict error, got %v", err)
	}

	// Verify LaunchApp resolves and caps RecentApps at maxRecentAppsPerInstance (5).
	for i := 1; i <= 6; i++ {
		pkg := fmt.Sprintf("com.example.app%d", i)
		comp := pkg + "/.MainActivity"
		pfa.resolvedMap[pkg] = comp
		if err := m.LaunchApp(ctx, inst.ID, pkg, ""); err != nil {
			t.Fatalf("LaunchApp(%s) failed: %v", pkg, err)
		}
	}
	got, _ := m.registry.Get(inst.ID)
	if len(got.RecentApps) != 5 {
		t.Fatalf("expected RecentApps capped at 5, got %d", len(got.RecentApps))
	}
	if got.RecentApps[0].Package != "com.example.app6" {
		t.Fatalf("expected most recent app first, got %+v", got.RecentApps[0])
	}
}

func buildAPKBytesForServiceTest(t *testing.T, pkg string, abis ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("AndroidManifest.xml")
	_, _ = w.Write(buildMinimalAXMLForTest(pkg))
	for _, abi := range abis {
		lw, _ := zw.Create("lib/" + abi + "/libx.so")
		_, _ = lw.Write([]byte("ELF"))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestInstallAppSupportsXAPKSplitBundleAndFiltersABI(t *testing.T) {
	m, _, fa := newTestManager(t, 2, 100*time.Millisecond)
	pfa := &pipelineFakeADB{
		fakeADB:     fa,
		resolvedMap: map[string]string{"com.example.bundle": "com.example.bundle/.MainActivity"},
	}
	m.adb = pfa
	close(fa.bootReady)

	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "xapk",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		Headless:    false,
	})
	if err != nil {
		t.Fatal(err)
	}
	inst := created[0]
	t.Cleanup(func() { _ = m.Stop(context.Background(), inst.ID) })

	xapkPath := filepath.Join(t.TempDir(), "bundle.xapk")
	f, err := os.Create(xapkPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range map[string][]byte{
		"base.apk":                       buildAPKBytesForServiceTest(t, "com.example.bundle"),
		"config.x86_64.apk":              buildAPKBytesForServiceTest(t, "com.example.bundle", "x86_64"),
		"config.arm64_v8a.apk":           buildAPKBytesForServiceTest(t, "com.example.bundle", "arm64-v8a"),
		"Android/obb/com.example/a.obb":  []byte("obb"),
	} {
		w, _ := zw.Create(name)
		_, _ = w.Write(data)
	}
	_ = zw.Close()
	_ = f.Close()

	if err := m.InstallApp(ctx, InstallAppRequest{
		InstanceIDs: []string{inst.ID},
		APKPath:     xapkPath,
		LaunchApp:   true,
	}); err != nil {
		t.Fatalf("InstallApp(.xapk) failed: %v", err)
	}

	pfa.mu.Lock()
	multi := append([][]string(nil), pfa.installMulti...)
	launched := append([]string(nil), pfa.launched...)
	pfa.mu.Unlock()

	if len(multi) != 1 || len(multi[0]) != 2 {
		t.Fatalf("expected 2 filtered APKs (base + x86_64), got %+v", multi)
	}
	tempDir := filepath.Dir(multi[0][0])
	if _, statErr := os.Stat(tempDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected extracted split temp dir %s to be cleaned up", tempDir)
	}
	joined := strings.Join(multi[0], "|")
	if !strings.Contains(joined, "base.apk") || !strings.Contains(joined, "config.x86_64.apk") || strings.Contains(joined, "arm64_v8a") {
		t.Fatalf("unexpected filtered split APKs: %v", multi[0])
	}
	if len(launched) != 1 || launched[0] != "com.example.bundle/.MainActivity" {
		t.Fatalf("expected launcher launch after split install, got %v", launched)
	}
}
