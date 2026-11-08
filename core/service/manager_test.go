package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"virdeck/core/emu"
	"virdeck/core/instance"
	"virdeck/core/screenlayout"
)

type fakeEmu struct {
	lastScreenOptions                          emu.ScreenLayoutOptions
	restored                                   []emu.SavedProjection
	screenStart                                func(string)
	displays                                   []screenlayout.Display
	screens                                    []emu.ProjectionWindow
	arrangedIDs                                []string
	arrangedDisplay                            string
	arrangedHeight, arrangedGap, arrangedCount int
	arrangeErr                                 error
	mu                                         sync.Mutex
	created                                    []string
	cloned                                     []string
	deleted                                    []string
	procs                                      map[int]chan error
	killErr                                    error
	killOn                                     bool // if false, EmuKill won't close proc; ForceKillTree will
	killed                                     map[int]bool
	lastOpts                                   map[string]emu.LaunchOptions
}

func newFakeEmu() *fakeEmu {
	return &fakeEmu{
		procs:    make(map[int]chan error),
		killed:   make(map[int]bool),
		lastOpts: make(map[string]emu.LaunchOptions),
		killOn:   true,
	}
}

func (f *fakeEmu) InspectEnv(ctx context.Context) emu.EnvInfo {
	return emu.EnvInfo{
		SDKRoot:      "C:\\Android\\Sdk",
		AccelOK:      true,
		AccelMessage: "WHPX (10.0.22631) is installed and usable.",
		SystemImages: []string{"system-images;android-34;google_apis_playstore;x86_64"},
	}
}

func (f *fakeEmu) ListSystemImages() []string {
	return []string{"system-images;android-34;google_apis_playstore;x86_64"}
}

func (f *fakeEmu) CreateAVD(ctx context.Context, name, systemImage string, memoryMB, cores int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, name)
	return nil
}

func (f *fakeEmu) CloneAVD(srcName, dstName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cloned = append(f.cloned, dstName)
	return nil
}

func (f *fakeEmu) DeleteAVD(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	return nil
}

func (f *fakeEmu) StartProcess(opts emu.LaunchOptions) (*emu.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	done := make(chan error, 1)
	f.procs[opts.Port] = done
	f.lastOpts[opts.AVDName] = opts
	port := opts.Port
	return emu.NewFakeProcess(done, "", func() error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.killed[port] = true
		if f.killErr != nil {
			return f.killErr
		}
		if ch, ok := f.procs[port]; ok {
			close(ch)
			delete(f.procs, port)
		}
		return nil
	}), nil
}

func (f *fakeEmu) StartScrcpy(ctx context.Context, id string, port int, title string) error {
	if f.screenStart != nil {
		f.screenStart(id)
	}
	return nil
}

type fakeADB struct {
	emu       *fakeEmu
	bootReady chan struct{}
	installed []string
}

func (a *fakeADB) SetPath(p string) {}

func (a *fakeADB) WaitForBoot(ctx context.Context, port int, interval time.Duration) error {
	select {
	case <-a.bootReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *fakeADB) EmuKill(ctx context.Context, port int) error {
	a.emu.mu.Lock()
	defer a.emu.mu.Unlock()
	if a.emu.killOn {
		if ch, ok := a.emu.procs[port]; ok {
			close(ch)
			delete(a.emu.procs, port)
		}
	}
	return nil
}

func (a *fakeADB) BatchInstallAPK(ctx context.Context, targets map[string]int, apkPath string) error {
	a.installed = append(a.installed, apkPath)
	return nil
}

func (a *fakeADB) BatchShell(ctx context.Context, targets map[string]int, shellCmd string) (map[string]string, error) {
	out := make(map[string]string, len(targets))
	for id := range targets {
		out[id] = "ok:" + shellCmd
	}
	return out, nil
}

func (a *fakeADB) DiscoverEmulators(ctx context.Context) map[string]int {
	return nil
}

func newTestManager(t *testing.T, maxConc int, grace time.Duration) (*Manager, *fakeEmu, *fakeADB) {
	t.Helper()
	regPath := filepath.Join(t.TempDir(), "instances.json")
	reg, err := instance.NewRegistry(regPath)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	fe := newFakeEmu()
	fa := &fakeADB{emu: fe, bootReady: make(chan struct{})}
	m := &Manager{
		cfg: Config{
			RegistryPath:  regPath,
			MaxConcurrent: maxConc,
			MinPort:       5554,
			MaxPort:       5560,
			StopGrace:     grace,
			BootTimeout:   5 * time.Second,
			SkipTCPCheck:  true,
		},
		registry:  reg,
		emu:       fe,
		adb:       fa,
		running:   make(map[string]*runningEntry),
		usedPorts: make(map[int]string),
		subs:      make(map[int]chan Event),
	}
	return m, fe, fa
}

func TestManagerLifecycleAndDynamicPortPool(t *testing.T) {
	m, fe, fa := newTestManager(t, 2, 200*time.Millisecond)
	ctx := context.Background()

	evCh, cancelSub := m.Subscribe()
	defer cancelSub()

	// 1. Create 3 clean instances
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "social",
		Count:       3,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		MemoryMB:    3072,
		Cores:       2,
		Proxy:       "http://127.0.0.1:7890",
		Headless:    true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if len(created) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(created))
	}
	if created[0].Name != "social_01" || created[1].Name != "social_02" || created[2].Name != "social_03" {
		t.Fatalf("unexpected names: %+v", created)
	}

	// 2. Start first 2 instances -> should allocate ports 5554 and 5556
	if err := m.Start(ctx, created[0].ID, created[1].ID); err != nil {
		t.Fatalf("Start 2 instances failed: %v", err)
	}
	// Starting 3rd instance should fail due to MaxConcurrent = 2
	if err := m.Start(ctx, created[2].ID); err == nil {
		t.Fatalf("expected concurrency limit error when starting 3rd instance")
	}

	// Unblock boot completion
	close(fa.bootReady)

	// Wait for Running state event
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list, _ := m.List(ctx)
		if list[0].State == Running && list[1].State == Running {
			if list[0].Port != 5554 || list[1].Port != 5556 {
				t.Fatalf("expected ports 5554 and 5556, got %d and %d", list[0].Port, list[1].Port)
			}
			break
		}
		select {
		case <-evCh:
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Verify proxy was passed to LaunchOptions
	fe.mu.Lock()
	opt0 := fe.lastOpts[created[0].InternalName()]
	fe.mu.Unlock()
	if opt0.Proxy != "http://127.0.0.1:7890" || !opt0.Headless {
		t.Fatalf("unexpected launch options: %+v", opt0)
	}

	// 3. Batch InstallAPK & Shell on running instances
	if err := m.InstallAPK(ctx, []string{created[0].ID, created[1].ID}, "whatsapp.apk"); err != nil {
		t.Fatalf("InstallAPK failed: %v", err)
	}
	res, err := m.Shell(ctx, []string{created[0].ID, created[1].ID}, "getprop ro.build.version.release")
	if err != nil || len(res) != 2 {
		t.Fatalf("Shell failed: %v, res=%v", err, res)
	}

	// 4. Stop first instance -> port 5554 should be freed and recycled when starting 3rd instance
	if err := m.Stop(ctx, created[0].ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	list, _ := m.List(ctx)
	if list[0].State != Stopped || list[0].Port != 0 {
		t.Fatalf("expected instance 0 Stopped with Port 0, got %+v", list[0])
	}

	// Start 3rd instance -> should reuse port 5554!
	if err := m.Start(ctx, created[2].ID); err != nil {
		t.Fatalf("Start 3rd instance failed: %v", err)
	}
	list, _ = m.List(ctx)
	if list[2].Port != 5554 {
		t.Fatalf("expected recycled port 5554 for 3rd instance, got %d", list[2].Port)
	}

	// 5. Test fallback ForceKillTree when EmuKill does not exit within grace period
	fe.mu.Lock()
	fe.killOn = false
	fe.mu.Unlock()

	if err := m.Stop(ctx, created[1].ID, created[2].ID); err != nil {
		t.Fatalf("Stop with fallback ForceKillTree failed: %v", err)
	}
	fe.mu.Lock()
	killed5556 := fe.killed[5556]
	killed5554 := fe.killed[5554]
	fe.mu.Unlock()
	if !killed5556 || !killed5554 {
		t.Fatalf("expected ForceKillTree to be called for hung processes: 5554=%v, 5556=%v", killed5554, killed5556)
	}
}

func TestAppliedProxyTracksLaunchValue(t *testing.T) {
	m, _, fa := newTestManager(t, 2, 200*time.Millisecond)
	ctx := context.Background()
	close(fa.bootReady)

	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "proxy",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		Proxy:       "http://127.0.0.1:7890",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := created[0].ID
	if got, _ := m.registry.Get(id); got.AppliedProxy != "" {
		t.Fatalf("stopped instance must have no applied proxy, got %q", got.AppliedProxy)
	}

	if err := m.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got, _ := m.registry.Get(id); got.AppliedProxy != "http://127.0.0.1:7890" {
		t.Fatalf("applied proxy = %q, want launch-time proxy", got.AppliedProxy)
	}

	if err := m.SetProxy(ctx, id, "http://127.0.0.1:1080"); err != nil {
		t.Fatalf("SetProxy: %v", err)
	}
	got, _ := m.registry.Get(id)
	if got.Proxy != "http://127.0.0.1:1080" || got.AppliedProxy != "http://127.0.0.1:7890" {
		t.Fatalf("saved/applied proxy mismatch: %+v", got)
	}

	if err := m.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got, _ := m.registry.Get(id); got.AppliedProxy != "" {
		t.Fatalf("applied proxy must be cleared after stop, got %q", got.AppliedProxy)
	}
}

func TestStopPreservesForceKillError(t *testing.T) {
	m, fe, fa := newTestManager(t, 1, time.Millisecond)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{NamePrefix: "kill_error", Count: 1, SystemImage: "system-images;android-34;google_apis_playstore;x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0].ID
	if err := m.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	inst, _ := m.registry.Get(id)
	killErr := errors.New("access denied to emulator process")
	fe.mu.Lock()
	fe.killOn = false
	fe.killErr = killErr
	fe.mu.Unlock()
	m.mu.Lock()
	exited := m.running[id].exited
	m.mu.Unlock()
	t.Cleanup(func() {
		fe.mu.Lock()
		fe.killOn = true
		fe.killErr = nil
		fe.mu.Unlock()
		_ = fa.EmuKill(ctx, inst.Port)
		select {
		case <-exited:
		case <-time.After(time.Second):
			t.Error("watcher cleanup timed out")
		}
	})
	if err := m.Stop(ctx, id); !errors.Is(err, killErr) {
		t.Fatalf("Stop error = %v, want underlying force-kill failure", err)
	}
	if got, _ := m.registry.Get(id); got.State == Stopped || got.Port == 0 {
		t.Fatalf("failed kill must not release a live instance: %+v", got)
	}
}

func TestBootTimeoutFailsAndReleasesExitedProcess(t *testing.T) {
	m, _, _ := newTestManager(t, 1, time.Millisecond)
	m.cfg.BootTimeout = 30 * time.Millisecond
	created, err := m.Create(context.Background(), CreateSpec{NamePrefix: "boot_timeout", Count: 1, SystemImage: "system-images;android-34;google_apis_playstore;x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0].ID
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	exited := m.running[id].exited
	m.mu.Unlock()
	t.Cleanup(func() { _ = m.Stop(context.Background(), id); <-exited })
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("boot deadline left instance stuck in starting with its process alive")
	}
	got, _ := m.registry.Get(id)
	if got.State != Failed || got.LastError == "" || got.Port != 0 {
		t.Fatalf("want failed startup with diagnostic and released port, got %+v", got)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.running) != 0 || len(m.usedPorts) != 0 {
		t.Fatal("exited boot failure still reserves capacity")
	}
}

func TestBootTimeoutKillFailureKeepsResourcesUntilStopped(t *testing.T) {
	m, fe, _ := newTestManager(t, 1, time.Millisecond)
	m.cfg.BootTimeout = 30 * time.Millisecond
	fe.killErr = errors.New("test kill denied")
	created, err := m.Create(context.Background(), CreateSpec{NamePrefix: "boot_kill_error", Count: 1, SystemImage: "system-images;android-34;google_apis_playstore;x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0].ID
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	exited := m.running[id].exited
	m.mu.Unlock()
	t.Cleanup(func() { _ = m.Stop(context.Background(), id); <-exited })
	deadline := time.Now().Add(time.Second)
	for {
		got, _ := m.registry.Get(id)
		if got.State == Failed && strings.Contains(got.LastError, "test kill denied") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed boot must expose kill error, got %+v", got)
		}
		time.Sleep(time.Millisecond)
	}
	got, _ := m.registry.Get(id)
	if got.Port == 0 {
		t.Fatal("must retain a live process's port")
	}
	m.mu.Lock()
	reserved := len(m.running) == 1 && len(m.usedPorts) == 1
	m.mu.Unlock()
	if !reserved {
		t.Fatal("must retain capacity until the process actually exits")
	}
	if err := m.Start(context.Background(), id); err == nil {
		t.Fatal("retry must not report success while the failed process is alive")
	}
	if _, err := m.Clone(context.Background(), id, 1); err == nil {
		t.Fatal("service must refuse cloning a failed process that is still alive")
	}
	if err := m.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("manual stop must still clean up failed boot")
	}
}

func (a *fakeADB) HasEmulators(ctx context.Context) (bool, error) { return false, nil }

func (f *fakeEmu) ScrcpyDisplays() ([]screenlayout.Display, error) { return f.displays, nil }
func (f *fakeEmu) ScrcpyWindows() ([]emu.ProjectionWindow, error)  { return f.screens, nil }
func (f *fakeEmu) ArrangeScrcpy(ctx context.Context, ids []string, displayID string, height, gap int) (int, error) {
	f.arrangedIDs = append([]string{}, ids...)
	f.arrangedDisplay = displayID
	f.arrangedHeight = height
	f.arrangedGap = gap
	return f.arrangedCount, f.arrangeErr
}

func (f *fakeEmu) ArrangeProjection(ctx context.Context, ids []string, opt emu.ScreenLayoutOptions) (int, error) {
	f.lastScreenOptions = opt
	return f.ArrangeScrcpy(ctx, ids, opt.DisplayID, opt.Height, opt.Gap)
}
func (f *fakeEmu) RestoreProjection(ctx context.Context, ids []string, records []emu.SavedProjection) (int, error) {
	f.arrangedIDs = append([]string(nil), ids...)
	f.restored = append([]emu.SavedProjection(nil), records...)
	return f.arrangedCount, f.arrangeErr
}

func (f *fakeEmu) StartProjection(ctx context.Context, id string, port int, title string) (bool, error) {
	created := true
	for _, p := range f.screens {
		if p.InstanceID == id {
			created = false
			break
		}
	}
	return created, f.StartScrcpy(ctx, id, port, title)
}
