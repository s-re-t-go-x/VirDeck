package service

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"virdeck/core/emu"
)

type stabilityFakeEmu struct {
	*fakeEmu
	mu           sync.Mutex
	cleanedLocks []string
	clearedSnaps []string
	launchCount  int
	failFirstGPU bool
	updateCalls  int
}

func (s *stabilityFakeEmu) CleanStaleLocks(avdName string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanedLocks = append(s.cleanedLocks, avdName)
	return []string{"hardware-qemu.ini.lock"}, nil
}

func (s *stabilityFakeEmu) ClearSnapshots(avdName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearedSnaps = append(s.clearedSnaps, avdName)
	return nil
}

func (s *stabilityFakeEmu) CreateAVDWithOptions(ctx context.Context, name, systemImage string, opts emu.CreateAVDOptions) error {
	return s.CreateAVD(ctx, name, systemImage, opts.MemoryMB, opts.Cores)
}

func (s *stabilityFakeEmu) UpdateAVDConfig(avdName string, memoryMB, cores int, resolution string, quickBoot bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalls++
	return nil
}

func (s *stabilityFakeEmu) StartProcess(opts emu.LaunchOptions) (*emu.Process, error) {
	s.mu.Lock()
	s.launchCount++
	n := s.launchCount
	failGPU := s.failFirstGPU && n == 1
	s.mu.Unlock()

	if failGPU {
		s.fakeEmu.mu.Lock()
		s.fakeEmu.lastOpts[opts.AVDName] = opts
		s.fakeEmu.mu.Unlock()
		done := make(chan error, 1)
		done <- context.DeadlineExceeded
		close(done)
		return emu.NewFakeProcess(done, "ERROR: Could not open OpenGL library", func() error { return nil }), nil
	}
	return s.fakeEmu.StartProcess(opts)
}

type stabilityFakeADB struct {
	*fakeADB
	active map[string]int
	abis   []string
}

func (a *stabilityFakeADB) DiscoverEmulators(ctx context.Context) map[string]int {
	if len(a.active) > 0 {
		return a.active
	}
	return a.fakeADB.DiscoverEmulators(ctx)
}

func (a *stabilityFakeADB) GetABIList(ctx context.Context, port int) ([]string, error) {
	return a.abis, nil
}

func TestStateChangedNotDroppedUnderLogFloodAndWaitReady(t *testing.T) {
	m, _, fa := newTestManager(t, 2, 200*time.Millisecond)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "flood",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0].ID

	evCh, cancel := m.Subscribe()
	defer cancel()

	// Flood log events well beyond channel capacity before emitting a state change.
	for i := 0; i < 400; i++ {
		m.emitLog(id, "spam log")
	}

	if err := m.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	// Close bootReady shortly after to transition Starting -> Running.
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(fa.bootReady)
	}()

	readyInst, err := m.WaitInstanceReady(ctx, id)
	if err != nil || readyInst.State != Running {
		t.Fatalf("WaitInstanceReady = %+v err=%v", readyInst, err)
	}

	sawRunning := false
	deadline := time.After(time.Second)
	for !sawRunning {
		select {
		case ev := <-evCh:
			if ev.Type == EventStateChanged && ev.InstanceID == id {
				if inst, ok := ev.Data.(Instance); ok && inst.State == Running {
					sawRunning = true
				}
			}
		case <-deadline:
			t.Fatal("EventStateChanged(Running) was dropped under log flood")
		}
	}

	if err := m.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestGPUFallbackSafeLockCleanupQuickBootSelfHealAndABIProbe(t *testing.T) {
	m, fe, fa := newTestManager(t, 2, 200*time.Millisecond)
	se := &stabilityFakeEmu{fakeEmu: fe, failFirstGPU: true}
	sa := &stabilityFakeADB{fakeADB: fa, abis: []string{"x86_64", "arm64-v8a"}}
	m.emu = se
	m.adb = sa
	close(fa.bootReady)

	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "qb_gpu",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		StorageGB:   12,
		Resolution:  "tablet",
		QuickBoot:   true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := created[0].ID
	if created[0].StorageGB != 12 || created[0].Resolution != "tablet" || !created[0].QuickBoot {
		t.Fatalf("Create did not persist new fields: %+v", created[0])
	}

	if err := m.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	readyInst, err := m.WaitInstanceReady(ctx, id)
	if err != nil {
		t.Fatalf("WaitInstanceReady: %v", err)
	}

	wantGPU := emu.FallbackGPUMode(runtime.GOOS, runtime.GOARCH, "")
	if readyInst.GPUMode != wantGPU {
		t.Fatalf("GPUMode = %q, want %q", readyInst.GPUMode, wantGPU)
	}
	se.mu.Lock()
	locksCleaned := len(se.cleanedLocks)
	se.mu.Unlock()
	if locksCleaned != 1 {
		t.Fatalf("expected 1 safe lock cleanup, got %d", locksCleaned)
	}

	// Wait briefly for async ABI probe to persist.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, _ := m.registry.Get(id)
		if len(got.ABIList) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, _ := m.registry.Get(id); len(got.ABIList) != 2 || got.ABIList[1] != "arm64-v8a" {
		t.Fatalf("ABIList not persisted: %+v", got.ABIList)
	}

	// Edit resolution while running -> sets ForceColdNext = true for quick boot self-heal.
	if err := m.EditInstance(ctx, id, InstanceSettings{
		Name:       "qb_gpu",
		MemoryMB:   2048,
		Cores:      2,
		Resolution: "1080p",
		Headless:   true,
		QuickBoot:  true,
	}); err != nil {
		t.Fatalf("EditInstance: %v", err)
	}
	if got, _ := m.registry.Get(id); !got.ForceColdNext {
		t.Fatal("expected ForceColdNext=true after editing quick-boot instance")
	}
	if err := m.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}

	// Verify ClearInstanceSnapshot clears snapshots and keeps ForceColdNext=true.
	if err := m.ClearInstanceSnapshot(ctx, id); err != nil {
		t.Fatalf("ClearInstanceSnapshot: %v", err)
	}
	se.mu.Lock()
	snaps := len(se.clearedSnaps)
	se.mu.Unlock()
	if snaps != 1 {
		t.Fatalf("expected ClearSnapshots to run once, got %d", snaps)
	}

	// Next start must pass NoSnapshot=false, NoSnapshotLoad=true, and clear ForceColdNext on Running.
	se.failFirstGPU = false
	if err := m.Start(ctx, id); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	ready2, err := m.WaitInstanceReady(ctx, id)
	if err != nil {
		t.Fatalf("WaitInstanceReady 2: %v", err)
	}
	if ready2.ForceColdNext {
		t.Fatal("expected ForceColdNext to reset after successful boot")
	}
	fe.mu.Lock()
	lastOpts := fe.lastOpts[ready2.InternalName()]
	fe.mu.Unlock()
	if lastOpts.NoSnapshot || !lastOpts.NoSnapshotLoad {
		t.Fatalf("expected quick boot self-heal launch flags, got %+v", lastOpts)
	}
	if err := m.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestSOCKS5ProxyStartsAndCleansBridge(t *testing.T) {
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()
	go func() {
		for {
			c, err := socksLn.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	m, fe, fa := newTestManager(t, 2, 200*time.Millisecond)
	close(fa.bootReady)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "socks_inst",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		Proxy:       "socks5://127.0.0.1:" + strconv.Itoa(socksLn.Addr().(*net.TCPAddr).Port),
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0].ID
	if err := m.Start(ctx, id); err != nil {
		t.Fatalf("Start with SOCKS5 proxy: %v", err)
	}
	fe.mu.Lock()
	launchedProxy := fe.lastOpts[created[0].InternalName()].Proxy
	fe.mu.Unlock()
	if !strings.HasPrefix(launchedProxy, "http://127.0.0.1:") {
		t.Fatalf("expected local HTTP bridge URL passed to emulator, got %q", launchedProxy)
	}
	m.mu.Lock()
	bridge := m.running[id].bridge
	m.mu.Unlock()
	if bridge == nil {
		t.Fatal("expected SOCKSBridge stored on runningEntry")
	}
	if err := m.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	// Avoid unused import warning if binary/io unused.
	_ = binary.BigEndian
	_ = io.EOF
}
