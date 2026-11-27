package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"virdeck/config"
	"virdeck/core/adb"
	"virdeck/core/apk"
	"virdeck/core/catalog"
	"virdeck/core/downloader"
	"virdeck/core/emu"
	"virdeck/core/instance"
	"virdeck/core/screenlayout"
)

var (
	DefaultMinPort       = config.Values.Runtime.MinPort
	DefaultMaxPort       = config.Values.Runtime.MaxPort
	DefaultMaxConcurrent = config.Values.Runtime.MaxConcurrent
	DefaultMemoryMB      = config.Values.Create.MemoryMB
	DefaultCores         = config.Values.Create.Cores
	DefaultStopGrace     = time.Duration(config.Values.Runtime.StopGraceSeconds) * time.Second
	DefaultBootTimeout   = time.Duration(config.Values.Runtime.BootTimeoutSeconds) * time.Second
)

func validateDisplayName(name string) error {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 64 {
		return errors.New("实例名称需为 1–64 个字符")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("实例名称不能包含换行或控制字符")
		}
	}
	return nil
}

// emuBackend abstracts emu.Controller for unit testing.
type emuBackend interface {
	InspectEnv(ctx context.Context) emu.EnvInfo
	ListSystemImages() []string
	CreateAVD(ctx context.Context, name, systemImage string, memoryMB, cores int) error
	DeleteAVD(ctx context.Context, name string) error
	StartProcess(opts emu.LaunchOptions) (*emu.Process, error)
	StartProjectionWithSettings(context.Context, string, int, string, emu.ProjectionSettings) (bool, error)
	RestartProjection(context.Context, string, int, string, emu.ProjectionSettings) error
	ScrcpyDisplays() ([]screenlayout.Display, error)
	ScrcpyWindows() ([]emu.ProjectionWindow, error)
	ArrangeScrcpy(ctx context.Context, ids []string, displayID string, height, gap int) (int, error)
	ArrangeProjection(context.Context, []string, emu.ScreenLayoutOptions) (int, error)
	RestoreProjection(context.Context, []string, []emu.SavedProjection) (int, error)
}

// adbBackend abstracts adb.Client for unit testing.
type adbBackend interface {
	SetPath(p string)
	WaitForBoot(ctx context.Context, port int, interval time.Duration) error
	EmuKill(ctx context.Context, port int) error
	BatchInstallAPK(ctx context.Context, targets map[string]int, apkPath string) error
	BatchShell(ctx context.Context, targets map[string]int, shellCmd string) (map[string]string, error)
	DiscoverEmulators(ctx context.Context) map[string]int
	HasEmulators(ctx context.Context) (bool, error)
}

// Config configures the core Manager.
type Config struct {
	DataDir       string
	RegistryPath  string
	CatalogPath   string
	SettingsPath  string
	SDKRoot       string
	AVDHome       string
	MaxConcurrent int
	MinPort       int
	MaxPort       int
	StopGrace           time.Duration
	BootTimeout         time.Duration
	SkipTCPCheck        bool // used in unit tests
	DetachedSOCKSBridge bool // detach local HTTP->SOCKS5 bridge subprocess for CLI lifetimes
}

type runningEntry struct {
	id         string
	port       int
	proc       *emu.Process
	bridge     *emu.SOCKSBridge
	bootCancel context.CancelFunc
	stopping   bool
	exited     chan struct{}
	launchedAt time.Time
	gpuMode    string
	gpuRetries int
	launchOpts emu.LaunchOptions
}

type eventSubscriber struct {
	mu     sync.Mutex
	stateQ []Event
	otherQ []Event
	notify chan struct{}
	out    chan Event
	closed bool
}

func newEventSubscriber() *eventSubscriber {
	s := &eventSubscriber{
		notify: make(chan struct{}, 1),
		out:    make(chan Event, 128),
	}
	go s.pump()
	return s
}

func (s *eventSubscriber) enqueue(ev Event) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if ev.Type == EventStateChanged {
		s.stateQ = append(s.stateQ, ev)
	} else if len(s.otherQ) < 256 {
		s.otherQ = append(s.otherQ, ev)
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *eventSubscriber) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *eventSubscriber) pump() {
	defer close(s.out)
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		var ev Event
		has := false
		if len(s.stateQ) > 0 {
			ev = s.stateQ[0]
			s.stateQ = s.stateQ[1:]
			has = true
		} else if len(s.otherQ) > 0 {
			ev = s.otherQ[0]
			s.otherQ = s.otherQ[1:]
			has = true
		}
		s.mu.Unlock()

		if !has {
			<-s.notify
			continue
		}
		select {
		case s.out <- ev:
		case <-s.notify:
			// Check whether we woke up because subscriber closed.
			s.mu.Lock()
			closed := s.closed
			if !closed {
				// Put back at front of priority queue if state change, or drop nothing.
				if ev.Type == EventStateChanged {
					s.stateQ = append([]Event{ev}, s.stateQ...)
				} else {
					s.otherQ = append([]Event{ev}, s.otherQ...)
				}
			}
			s.mu.Unlock()
			if closed {
				return
			}
		}
	}
}

// Manager implements the Service interface.
type Manager struct {
	groupMu        sync.Mutex
	screenLayoutMu sync.Mutex
	namesMu        sync.Mutex
	syncOpMu       sync.Mutex
	syncMu         sync.Mutex
	syncSession    *inputSyncSession
	syncHooks      *inputSyncHooks
	maintenance    sync.RWMutex // excludes cleanup from installs and instance mutations
	cfg            Config
	settingsPath   string
	settings       Settings
	registry       *instance.Registry
	cat            *catalog.Store
	dl             *downloader.Client
	emu            emuBackend
	adb            adbBackend

	mu          sync.Mutex
	running     map[string]*runningEntry // instanceID -> runningEntry
	starting    map[string]struct{}      // instanceID -> reserving start slot before running[id] is added
	instanceOps map[string]string        // instanceID -> exclusive lifecycle/offline op name
	usedPorts   map[int]string           // port -> instanceID

	subMu  sync.RWMutex
	nextID int
	subs   map[int]*eventSubscriber
}

// NewManager creates and initializes a new core Service Manager rooted at VirDeckData.
func NewManager(cfg Config) (*Manager, error) {
	if config.LoadError != nil {
		return nil, config.LoadError
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		if cfg.RegistryPath != "" {
			cfg.DataDir = filepath.Dir(filepath.Dir(cfg.RegistryPath))
		} else {
			cfg.DataDir = emu.DefaultDataDir()
		}
	}
	if abs, err := filepath.Abs(cfg.DataDir); err == nil {
		cfg.DataDir = abs
	}

	if cfg.SettingsPath == "" {
		cfg.SettingsPath = filepath.Join(cfg.DataDir, "config", "settings.json")
	}
	settings := loadSettings(cfg.SettingsPath)
	if _, err := os.Stat(cfg.SettingsPath); errors.Is(err, os.ErrNotExist) {
		if err := saveSettings(cfg.SettingsPath, settings); err != nil {
			return nil, fmt.Errorf("初始化用户设置: %w", err)
		}
	} else if err != nil {
		return nil, err
	}

	if cfg.RegistryPath == "" {
		cfg.RegistryPath = filepath.Join(cfg.DataDir, "config", "instances.json")
		// Seamless one-time migration from legacy <exeDir>/data/instances.json if present and valid.
		if _, err := os.Stat(cfg.RegistryPath); errors.Is(err, os.ErrNotExist) {
			if exePath, eerr := os.Executable(); eerr == nil {
				legacyPath := filepath.Join(filepath.Dir(exePath), "data", "instances.json")
				if data, lerr := os.ReadFile(legacyPath); lerr == nil && len(strings.TrimSpace(string(data))) > 0 {
					var parsed []instance.Record
					if json.Unmarshal(data, &parsed) == nil {
						_ = writeFileAtomic(cfg.RegistryPath, data, 0o700, 0o600)
					}
				}
			}
		}
	}
	if cfg.CatalogPath == "" {
		cfg.CatalogPath = filepath.Join(cfg.DataDir, "config", "images_catalog.json")
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = settings.MaxConcurrent
	}
	if cfg.MinPort <= 0 {
		cfg.MinPort = DefaultMinPort
	}
	if cfg.MaxPort <= 0 {
		cfg.MaxPort = DefaultMaxPort
	}
	if cfg.StopGrace <= 0 {
		cfg.StopGrace = DefaultStopGrace
	}
	if cfg.BootTimeout <= 0 {
		cfg.BootTimeout = DefaultBootTimeout
	}

	reg, err := instance.NewRegistry(cfg.RegistryPath)
	if err != nil {
		return nil, err
	}

	emuCtrl := emu.NewControllerWithDataDir(cfg.DataDir, cfg.SDKRoot, cfg.AVDHome, settings.ADBServerPort)
	_, adbBin, _, _, _ := emuCtrl.ResolvePaths()
	adbClient := adb.NewClient(adbBin)
	adbClient.SetEnv(emuCtrl.IsolatedEnv())

	catStore, err := catalog.NewStore(cfg.CatalogPath, emuCtrl.SDKRoot())
	if err != nil {
		return nil, err
	}
	dlClient := downloader.New(filepath.Join(cfg.DataDir, "downloads"), settings.DownloadProxy)

	mgr := &Manager{
		cfg:          cfg,
		settingsPath: cfg.SettingsPath,
		settings:     settings,
		registry:     reg,
		cat:          catStore,
		dl:           dlClient,
		emu:          emuCtrl,
		adb:          adbClient,
		running:     make(map[string]*runningEntry),
		starting:    make(map[string]struct{}),
		instanceOps: make(map[string]string),
		usedPorts:   make(map[int]string),
		subs:         make(map[int]*eventSubscriber),
	}
	emuCtrl.SetProjectionLogger(mgr.emitLog)
	if adbBin != "" {
		mgr.Reconcile(context.Background())
	}
	return mgr, nil
}

// Reconcile queries adb for already-running emulators and updates registry states and ports.
func (m *Manager) Reconcile(ctx context.Context) {
	activeByName := m.adb.DiscoverEmulators(ctx)
	if len(activeByName) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.registry.List() {
		if port, ok := activeByName[inst.InternalName()]; ok {
			m.usedPorts[port] = inst.ID
			_, _ = m.registry.Update(inst.ID, func(r *Instance) error {
				r.State = Running
				r.Port = port
				r.AppliedProxy = r.Proxy // best effort: launch-time proxy is unknown for adopted emulators
				r.LastError = ""
				return nil
			})
		}
	}
}

// CheckEnv inspects the local Android SDK, tools, WHPX acceleration, path health, and installed system images.
func (m *Manager) CheckEnv(ctx context.Context) (EnvStatus, error) {
	info := m.emu.InspectEnv(ctx)
	if info.ADBPath != "" {
		m.adb.SetPath(info.ADBPath)
	}
	if m.cat != nil && info.SDKRoot != "" {
		m.cat.SetSDKRoot(info.SDKRoot)
	}
	status := EnvStatus{
		DataDir:          info.DataDir,
		EffectiveDataDir: info.EffectiveDataDir,
		PathCompatMode:   info.PathCompatMode,
		SDKRoot:          info.SDKRoot,
		AVDHome:          info.AVDHome,
		EmulatorPath:     info.EmulatorPath,
		ADBPath:          info.ADBPath,
		AVDManagerPath:   info.AVDManagerPath,
		SDKManagerPath:   info.SDKManagerPath,
		ScrcpyPath:       info.ScrcpyPath,
		AccelOK:          info.AccelOK,
		AccelMessage:     info.AccelMessage,
		PathASCIIOK:      info.PathASCIIOK,
		PathWritableOK:   info.PathWritableOK,
		PathWarning:      info.PathWarning,
		SystemImages:     info.SystemImages,
	}
	return status, nil
}

// SuggestPureASCIIDataDir returns a recommended writable pure-ASCII data directory on Windows.
func (m *Manager) SuggestPureASCIIDataDir() string {
	return emu.SuggestPureASCIIDataDir()
}

// SetDataDirOverride validates newDir and writes the startup pointer file so the next launch uses it.
func (m *Manager) SetDataDirOverride(_ context.Context, newDir string) (string, error) {
	ptrPath, err := emu.SaveDataDirOverride(newDir)
	if err != nil {
		return "", err
	}
	m.emitLog("", fmt.Sprintf("已配置下次启动改用纯英文数据目录: %s（配置记录: %s）", newDir, ptrPath))
	return ptrPath, nil
}

// ListSystemImages returns installed Android system image package strings.
func (m *Manager) ListSystemImages(ctx context.Context) ([]string, error) {
	return m.emu.ListSystemImages(), nil
}

// List returns all registered instances.
func (m *Manager) List(ctx context.Context) ([]Instance, error) {
	return m.registry.List(), nil
}

// Create creates one or more clean AVD instances via pure-Go AVD generation (zero JDK dependency).
func (m *Manager) Create(ctx context.Context, spec CreateSpec) ([]Instance, error) {
	m.namesMu.Lock()
	defer m.namesMu.Unlock()
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	prefix := strings.TrimSpace(spec.NamePrefix)
	if prefix == "" {
		return nil, errors.New("实例名称前缀不能为空")
	}
	if err := validateDisplayName(prefix); err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.SystemImage) == "" {
		return nil, errors.New("请选择或输入系统镜像包路径（例如 system-images;android-34;google_apis_playstore;x86_64）")
	}
	if spec.Count <= 0 {
		spec.Count = config.Values.Create.Count
	}
	if spec.MemoryMB <= 0 {
		spec.MemoryMB = DefaultMemoryMB
	}
	if spec.Cores <= 0 {
		spec.Cores = DefaultCores
	}
	storageGB := spec.StorageGB
	if storageGB == 0 {
		storageGB = 6
	}
	if storageGB < 4 || storageGB > 128 {
		return nil, errors.New("实例存储容量需在 4–128 GB 之间")
	}
	resSpec := strings.TrimSpace(spec.Resolution)
	if resSpec != "" {
		if _, err := emu.ParseResolution(resSpec); err != nil {
			return nil, err
		}
	} else {
		resSpec = config.Values.Create.Resolution
	}
	gpuMode, err := normalizeAndValidateGPUMode(spec.GPUMode)
	if err != nil {
		return nil, err
	}
	note, err := normalizeAndValidateNote(spec.Note)
	if err != nil {
		return nil, err
	}

	created := make([]Instance, 0, spec.Count)
	for i := 1; i <= spec.Count; i++ {
		name := prefix
		if spec.Count > 1 || m.registry.HasName(name) {
			name = m.nextAvailableName(prefix, i)
		}
		id := newShortID()
		avdName := "avd_" + id
		m.emitLog("", fmt.Sprintf("正在创建实例 %s (镜像: %s)...", name, spec.SystemImage))

		var err error
		if optsCreator, ok := m.emu.(interface {
			CreateAVDWithOptions(context.Context, string, string, emu.CreateAVDOptions) error
		}); ok {
			err = optsCreator.CreateAVDWithOptions(ctx, avdName, spec.SystemImage, emu.CreateAVDOptions{
				MemoryMB:   spec.MemoryMB,
				Cores:      spec.Cores,
				Resolution: resSpec,
				StorageGB:  storageGB,
				QuickBoot:  spec.QuickBoot,
			})
		} else if ctrl, ok := m.emu.(*emu.Controller); ok && resSpec != "" {
			err = ctrl.CreateAVDWithSpec(ctx, avdName, spec.SystemImage, spec.MemoryMB, spec.Cores, resSpec)
		} else {
			err = m.emu.CreateAVD(ctx, avdName, spec.SystemImage, spec.MemoryMB, spec.Cores)
		}
		if err != nil {
			m.emitLog("", fmt.Sprintf("创建实例 %s 失败: %v", name, err))
			return created, err
		}
		inst := Instance{
			ID:          id,
			AVDName:     avdName,
			Name:        name,
			SystemImage: spec.SystemImage,
			MemoryMB:    spec.MemoryMB,
			Cores:       spec.Cores,
			Resolution:  resSpec,
			StorageGB:   storageGB,
			QuickBoot:   spec.QuickBoot,
			GPUMode:     gpuMode,
			Note:        note,
			Port:        0,
			Proxy:       strings.TrimSpace(spec.Proxy),
			Headless:    spec.Headless,
			State:       Stopped,
			CreatedAt:   time.Now().Format(time.RFC3339),
		}
		if err := m.registry.Add(inst); err != nil {
			return created, err
		}
		created = append(created, inst)
		m.emitStateChanged(inst, fmt.Sprintf("已创建实例 %s", inst.Name))
	}
	return created, nil
}

// Start launches one or more instances, dynamically allocating even console ports and enforcing concurrency limits.
func (m *Manager) Start(ctx context.Context, ids ...string) error {
	var errs []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := m.startOne(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) ensureLifecycleMapsLocked() {
	if m.running == nil {
		m.running = make(map[string]*runningEntry)
	}
	if m.starting == nil {
		m.starting = make(map[string]struct{})
	}
	if m.instanceOps == nil {
		m.instanceOps = make(map[string]string)
	}
	if m.usedPorts == nil {
		m.usedPorts = make(map[int]string)
	}
}

func (m *Manager) canSafelyCleanLocks(ctx context.Context, avdName string) bool {
	if strict, ok := m.adb.(interface {
		DiscoverEmulatorsStrict(context.Context) (map[string]int, int, error)
	}); ok {
		active, unknown, err := strict.DiscoverEmulatorsStrict(ctx)
		return err == nil && unknown == 0 && active[avdName] == 0
	}
	has, err := m.adb.HasEmulators(ctx)
	if err != nil || has {
		return false
	}
	active := m.adb.DiscoverEmulators(ctx)
	return active[avdName] == 0
}

func (m *Manager) acquireOfflineOp(ctx context.Context, id, op string) (Instance, func(), error) {
	m.mu.Lock()
	m.ensureLifecycleMapsLocked()
	if cur := m.instanceOps[id]; cur != "" {
		m.mu.Unlock()
		return Instance{}, func() {}, fmt.Errorf("实例当前正在执行「%s」，请稍后再试", cur)
	}
	if _, active := m.running[id]; active {
		m.mu.Unlock()
		return Instance{}, func() {}, errors.New("请先停止该实例再执行此操作")
	}
	if _, busy := m.starting[id]; busy {
		m.mu.Unlock()
		return Instance{}, func() {}, errors.New("实例正在启动中，请稍后再执行此操作")
	}
	inst, ok := m.registry.Get(id)
	if !ok {
		m.mu.Unlock()
		return Instance{}, func() {}, fmt.Errorf("实例不存在: %s", id)
	}
	if inst.State == Running || inst.State == Starting || inst.Port > 0 {
		m.mu.Unlock()
		return Instance{}, func() {}, errors.New("请先停止该实例再执行此操作")
	}
	m.instanceOps[id] = op
	m.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			m.mu.Lock()
			if m.instanceOps != nil && m.instanceOps[id] == op {
				delete(m.instanceOps, id)
			}
			m.mu.Unlock()
		})
	}

	if strict, ok := m.adb.(interface {
		DiscoverEmulatorsStrict(context.Context) (map[string]int, int, error)
	}); ok {
		active, _, err := strict.DiscoverEmulatorsStrict(ctx)
		if err == nil && active[inst.InternalName()] > 0 {
			release()
			return Instance{}, func() {}, errors.New("检测到该实例仍在后台运行，请先停止")
		}
	}
	return inst, release, nil
}

func (m *Manager) startOne(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	inst, ok := m.registry.Get(id)
	if !ok {
		return fmt.Errorf("实例不存在: %s", id)
	}

	m.mu.Lock()
	m.ensureLifecycleMapsLocked()
	if _, exists := m.running[id]; exists {
		m.mu.Unlock()
		if inst.State == Failed {
			return fmt.Errorf("实例 %s 上次启动失败的进程尚未退出，请先停止", inst.Name)
		}
		return nil // already running or starting
	}
	if _, starting := m.starting[id]; starting {
		m.mu.Unlock()
		return nil
	}
	if curOp := m.instanceOps[id]; curOp != "" {
		m.mu.Unlock()
		return fmt.Errorf("实例 %s 正在执行「%s」，请稍后再启动", inst.Name, curOp)
	}
	if len(m.running)+len(m.starting) >= m.cfg.MaxConcurrent {
		m.mu.Unlock()
		return fmt.Errorf("已达到最大同时运行实例数上限 (%d)，无法启动 %s", m.cfg.MaxConcurrent, inst.Name)
	}
	port, err := m.allocatePortLocked()
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("为 %s 分配端口失败: %w", inst.Name, err)
	}
	m.usedPorts[port] = id
	m.starting[id] = struct{}{}
	m.instanceOps[id] = "启动"
	m.mu.Unlock()

	committed := false
	var bridge *emu.SOCKSBridge
	defer func() {
		if committed {
			return
		}
		if bridge != nil {
			_ = bridge.Close()
		}
		m.mu.Lock()
		delete(m.starting, id)
		if m.instanceOps[id] == "启动" {
			delete(m.instanceOps, id)
		}
		if m.usedPorts[port] == id {
			delete(m.usedPorts, port)
		}
		m.mu.Unlock()
	}()

	// Safe lock cleanup: only remove *.lock files after confirming via ADB that discovery succeeded
	// with zero unknown devices and this AVD is not active.
	if cleaner, ok := m.emu.(interface {
		CleanStaleLocks(string) ([]string, error)
	}); ok {
		if m.canSafelyCleanLocks(ctx, inst.InternalName()) {
			if removed, lerr := cleaner.CleanStaleLocks(inst.InternalName()); lerr == nil && len(removed) > 0 {
				m.emitLog(id, fmt.Sprintf("已安全清理 %s 残留锁文件: %s", inst.Name, strings.Join(removed, ", ")))
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	launchProxy := inst.Proxy
	if emu.IsSOCKS5Proxy(inst.Proxy) {
		var berr error
		if m.cfg.DetachedSOCKSBridge {
			bridge, berr = emu.StartDetachedSOCKSBridge("", nil, inst.Proxy, port)
		} else {
			bridge, berr = emu.StartSOCKSBridge(inst.Proxy)
		}
		if berr != nil {
			updated, _ := m.registry.Update(id, func(r *Instance) error {
				r.State = Failed
				r.Port = 0
				r.AppliedProxy = ""
				r.LastError = berr.Error()
				return nil
			})
			m.emitStateChanged(updated, fmt.Sprintf("启动 %s 失败: %v", inst.Name, berr))
			return berr
		}
		launchProxy = bridge.HTTPProxyURL()
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	launchOpts := emu.LaunchOptions{
		AVDName:        inst.InternalName(),
		Port:           port,
		MemoryMB:       inst.MemoryMB,
		Cores:          inst.Cores,
		Proxy:          launchProxy,
		GPUMode:        inst.GPUMode,
		Headless:       inst.Headless,
		NoSnapshot:     !inst.QuickBoot,
		NoSnapshotLoad: inst.QuickBoot && inst.ForceColdNext,
	}

	proc, err := m.emu.StartProcess(launchOpts)
	if err != nil {
		updated, _ := m.registry.Update(id, func(r *Instance) error {
			r.State = Failed
			r.Port = 0
			r.AppliedProxy = ""
			r.LastError = err.Error()
			return nil
		})
		m.emitStateChanged(updated, fmt.Sprintf("启动 %s 失败: %v", inst.Name, err))
		return err
	}

	bootCtx, bootCancel := context.WithTimeout(context.Background(), m.cfg.BootTimeout)
	entry := &runningEntry{
		id:         id,
		port:       port,
		proc:       proc,
		bridge:     bridge,
		bootCancel: bootCancel,
		exited:     make(chan struct{}),
		launchedAt: time.Now(),
		gpuMode:    inst.GPUMode,
		launchOpts: launchOpts,
	}

	m.mu.Lock()
	delete(m.starting, id)
	if m.instanceOps[id] == "启动" {
		delete(m.instanceOps, id)
	}
	m.running[id] = entry
	committed = true
	m.mu.Unlock()

	updated, err := m.registry.Update(id, func(r *Instance) error {
		r.State = Starting
		r.Port = port
		r.AppliedProxy = inst.Proxy
		r.LastError = ""
		return nil
	})
	if err != nil {
		m.mu.Lock()
		delete(m.running, id)
		delete(m.usedPorts, port)
		m.mu.Unlock()
		_ = proc.ForceKillTree()
		if bridge != nil {
			_ = bridge.Close()
		}
		return err
	}
	m.emitStateChanged(updated, fmt.Sprintf("实例 %s 正在启动 (端口 %d, PID %d)...", updated.Name, port, proc.PID()))

	go m.watchInstance(entry, bootCtx)
	return nil
}

func (m *Manager) watchInstance(entry *runningEntry, bootCtx context.Context) {
	defer close(entry.exited)
	defer func() {
		if entry.bridge != nil {
			_ = entry.bridge.Close()
		}
	}()

	var procErr error
	var bootFailure string

	for {
		bootCh := make(chan error, 1)
		curBootCtx := bootCtx
		go func() {
			bootCh <- m.adb.WaitForBoot(curBootCtx, entry.port, 1500*time.Millisecond)
		}()

		retryGPU := false
		select {
		case err := <-bootCh:
			m.mu.Lock()
			entry.bootCancel()
			m.mu.Unlock()
			if err == nil {
				if updated, uerr := m.registry.Update(entry.id, func(r *Instance) error {
					if r.State == Starting {
						r.State = Running
					}
					r.ForceColdNext = false
					if entry.gpuRetries > 0 && entry.gpuMode != "" {
						r.GPUMode = entry.gpuMode
					}
					return nil
				}); uerr == nil {
					m.emitStateChanged(updated, fmt.Sprintf("实例 %s 启动完成 (emulator-%d)", updated.Name, entry.port))
					if len(updated.ABIList) == 0 {
						go m.probeInstanceABIList(entry.id, entry.port)
					}
				}
			} else {
				m.mu.Lock()
				stopping := entry.stopping
				m.mu.Unlock()
				if !stopping {
					bootFailure = fmt.Sprintf("启动检测失败: %v", err)
					if errors.Is(err, context.DeadlineExceeded) {
						bootFailure = fmt.Sprintf("启动超时（%s）: %v", m.cfg.BootTimeout, err)
					}
					if updated, uerr := m.registry.Update(entry.id, func(r *Instance) error {
						r.State = Failed
						r.LastError = bootFailure
						if r.QuickBoot {
							r.ForceColdNext = true
						}
						return nil
					}); uerr == nil {
						m.emitStateChanged(updated, fmt.Sprintf("实例 %s %s，正在清理未就绪进程", updated.Name, bootFailure))
					}
					if killErr := entry.proc.ForceKillTree(); killErr != nil {
						bootFailure += fmt.Sprintf("；清理启动进程失败: %v，请手动停止", killErr)
						if updated, uerr := m.registry.Update(entry.id, func(r *Instance) error { r.LastError = bootFailure; return nil }); uerr == nil {
							m.emitStateChanged(updated, bootFailure)
						}
					}
				}
			}

			procErr = <-entry.proc.Done()

		case procErr = <-entry.proc.Done():
			m.mu.Lock()
			entry.bootCancel()
			stopping := entry.stopping
			m.mu.Unlock()

			if !stopping && time.Since(entry.launchedAt) < 10*time.Second && entry.gpuRetries == 0 {
				out := entry.proc.Output()
				if emu.IsGPUStartupFailure(out) {
					if nextGPU := emu.FallbackGPUMode(runtime.GOOS, runtime.GOARCH, entry.gpuMode); nextGPU != "" {
						retryOpts := entry.launchOpts
						retryOpts.GPUMode = nextGPU
						if !retryOpts.NoSnapshot {
							retryOpts.NoSnapshotLoad = true
						}
						m.emitLog(entry.id, fmt.Sprintf("检测到显卡初始化早退，自动切换为 -gpu %s 重试启动...", nextGPU))
						if newProc, rerr := m.emu.StartProcess(retryOpts); rerr == nil {
							nextBootCtx, nextBootCancel := context.WithTimeout(context.Background(), m.cfg.BootTimeout)
							defer nextBootCancel()
							m.mu.Lock()
							stillStopping := entry.stopping
							if !stillStopping {
								entry.proc = newProc
								entry.launchedAt = time.Now()
								entry.gpuMode = nextGPU
								entry.gpuRetries++
								entry.launchOpts = retryOpts
								entry.bootCancel = nextBootCancel
							}
							m.mu.Unlock()
							if stillStopping {
								nextBootCancel()
								_ = newProc.ForceKillTree()
								procErr = <-newProc.Done()
							} else {
								bootCtx = nextBootCtx
								retryGPU = true
							}
						} else {
							m.emitLog(entry.id, fmt.Sprintf("GPU 回退重拉失败: %v", rerr))
						}
					}
				}
			}
		}
		if !retryGPU {
			break
		}
	}

	m.mu.Lock()
	wasStopping := entry.stopping
	delete(m.running, entry.id)
	delete(m.usedPorts, entry.port)
	m.mu.Unlock()

	prev, exists := m.registry.Get(entry.id)
	if !exists {
		return
	}

	finalState := Stopped
	lastErr := ""
	msg := fmt.Sprintf("实例 %s 已停止", prev.Name)

	if bootFailure != "" {
		finalState = Failed
		lastErr = config.RedactText(bootFailure)
		if out := entry.proc.Output(); out != "" {
			lastErr += "；模拟器输出：" + config.RedactText(firstLastLines(out, 320))
		}
		msg = fmt.Sprintf("实例 %s 启动失败: %s", prev.Name, lastErr)
	} else if !wasStopping && prev.State == Starting {
		finalState = Failed
		out := entry.proc.Output()
		if out != "" {
			lastErr = config.RedactText(firstLastLines(out, 320))
		} else if procErr != nil {
			lastErr = config.RedactError(procErr)
		} else {
			lastErr = "模拟器进程在完成引导前意外退出"
		}
		msg = fmt.Sprintf("实例 %s 启动异常退出: %s", prev.Name, lastErr)
	}

	if updated, err := m.registry.Update(entry.id, func(r *Instance) error {
		r.State = finalState
		r.Port = 0
		r.AppliedProxy = ""
		r.LastError = lastErr
		if finalState == Failed && r.QuickBoot {
			r.ForceColdNext = true
		}
		return nil
	}); err == nil {
		m.emitStateChanged(updated, msg)
	}
}

func (m *Manager) probeInstanceABIList(id string, port int) {
	prober, ok := m.adb.(interface {
		GetABIList(context.Context, int) ([]string, error)
	})
	if !ok {
		return
	}
	abis, err := prober.GetABIList(context.Background(), port)
	if err != nil || len(abis) == 0 {
		return
	}
	if updated, uerr := m.registry.Update(id, func(r *Instance) error {
		r.ABIList = abis
		return nil
	}); uerr == nil {
		m.emitLog(id, fmt.Sprintf("已探测实例 %s 支持 ABI: %s", updated.Name, strings.Join(abis, ", ")))
	}
}

// Stop gracefully stops one or more instances via "adb emu kill", falling back to taskkill /T on timeout.
func (m *Manager) Stop(ctx context.Context, ids ...string) error {
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	for _, id := range ids {
		wg.Add(1)
		go func(instID string) {
			defer wg.Done()
			if err := m.stopOne(ctx, instID); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (m *Manager) stopOne(ctx context.Context, id string) error {
	if m.GetSettings().ScreenLayout.RememberPosition {
		m.captureLastWindowPositions([]string{id})
	}
	m.handleInstanceStoppedForSync(id)
	m.mu.Lock()
	entry, ok := m.running[id]
	if !ok {
		if inst, exists := m.registry.Get(id); exists && inst.Port > 0 {
			delete(m.usedPorts, inst.Port)
			m.mu.Unlock()
			_ = m.adb.EmuKill(ctx, inst.Port)
		} else {
			m.mu.Unlock()
		}
		if updated, err := m.registry.Update(id, func(r *Instance) error {
			r.State = Stopped
			r.Port = 0
			r.AppliedProxy = ""
			return nil
		}); err == nil {
			m.emitStateChanged(updated, fmt.Sprintf("实例 %s 已停止", updated.Name))
		}
		return nil
	}
	entry.stopping = true
	entry.bootCancel()
	m.mu.Unlock()

	inst, _ := m.registry.Get(id)
	m.emitLog(id, fmt.Sprintf("正在停止实例 %s (emulator-%d)...", inst.Name, entry.port))

	// Phase 1: Graceful shutdown via adb emu kill.
	_ = m.adb.EmuKill(ctx, entry.port)

	graceTimer := time.NewTimer(m.cfg.StopGrace)
	defer graceTimer.Stop()

	var killErr error
	select {
	case <-entry.exited:
		return nil
	case <-graceTimer.C:
		m.emitLog(id, fmt.Sprintf("实例 %s 优雅停机超时，正在强制终止进程树...", inst.Name))
		if inst.QuickBoot {
			_, _ = m.registry.Update(id, func(r *Instance) error {
				r.ForceColdNext = true
				return nil
			})
		}
		killErr = entry.proc.ForceKillTree()
		if killErr != nil {
			m.emitLog(id, fmt.Sprintf("强制终止实例 %s 失败: %v", inst.Name, killErr))
		}
	case <-ctx.Done():
		_ = entry.proc.ForceKillTree()
		return ctx.Err()
	}

	select {
	case <-entry.exited:
		return nil
	case <-time.After(5 * time.Second):
		if killErr != nil {
			return fmt.Errorf("强制终止实例 %s 失败: %w", inst.Name, killErr)
		}
		return fmt.Errorf("强制终止实例 %s 超时", inst.Name)
	}
}

// Delete stops (if running) and deletes one or more instances from both disk and the registry.
func (m *Manager) Delete(ctx context.Context, ids ...string) error {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	var errs []error
	for _, id := range ids {
		inst, ok := m.registry.Get(id)
		if !ok {
			continue
		}
		m.mu.Lock()
		if _, starting := m.starting[id]; starting {
			m.mu.Unlock()
			errs = append(errs, fmt.Errorf("实例 %s 正在启动中，请稍后再删除", inst.Name))
			continue
		}
		if busyOp := m.instanceOps[id]; busyOp != "" {
			m.mu.Unlock()
			errs = append(errs, fmt.Errorf("实例 %s 正在执行%s，请完成后再删除", inst.Name, busyOp))
			continue
		}
		m.instanceOps[id] = "删除"
		m.mu.Unlock()

		deleteErr := func() error {
			defer func() {
				m.mu.Lock()
				if m.instanceOps[id] == "删除" {
					delete(m.instanceOps, id)
				}
				m.mu.Unlock()
			}()
			if err := m.stopOne(ctx, id); err != nil {
				return err
			}
			m.emitLog(id, fmt.Sprintf("正在删除实例 %s...", inst.Name))
			if err := m.emu.DeleteAVD(ctx, inst.InternalName()); err != nil {
				return err
			}
			if _, err := m.registry.Delete(id); err != nil {
				return err
			}
			return nil
		}()
		if deleteErr != nil {
			errs = append(errs, deleteErr)
			continue
		}
		m.emit(Event{
			Type:       EventStateChanged,
			InstanceID: id,
			Message:    fmt.Sprintf("已删除实例 %s", inst.Name),
			Timestamp:  nowStamp(),
		})
	}
	return errors.Join(errs...)
}

// SetProxy sets the HTTP/SOCKS5 proxy address for an instance (applied on startup).
func (m *Manager) SetProxy(ctx context.Context, id, proxy string) error {
	proxy = strings.TrimSpace(proxy)
	updated, err := m.registry.Update(id, func(r *Instance) error {
		if r.QuickBoot && strings.TrimSpace(r.Proxy) != proxy {
			r.ForceColdNext = true
		}
		r.Proxy = proxy
		return nil
	})
	if err != nil {
		return err
	}
	redacted := config.RedactProxyURL(proxy)
	msg := fmt.Sprintf("实例 %s 代理已更新为 %q", updated.Name, redacted)
	if proxy == "" {
		msg = fmt.Sprintf("实例 %s 已清除代理", updated.Name)
	}
	if updated.State == Running || updated.State == Starting {
		msg += "（重启实例后生效）"
	}
	m.emitStateChanged(updated, msg)
	return nil
}

// SetHeadless configures whether an instance starts with -no-window.
func (m *Manager) SetHeadless(ctx context.Context, id string, headless bool) error {
	updated, err := m.registry.Update(id, func(r *Instance) error {
		if r.QuickBoot && r.Headless != headless {
			r.ForceColdNext = true
		}
		r.Headless = headless
		return nil
	})
	if err != nil {
		return err
	}
	mode := "无窗口省资源模式 (-no-window)"
	if !headless {
		mode = "自带窗口模式"
	}
	m.emitStateChanged(updated, fmt.Sprintf("实例 %s 显示模式已设为 %s（下次启动生效）", updated.Name, mode))
	return nil
}

// InstallAPK installs an APK or split-APK bundle (.apks / .xapk) across all specified running instances concurrently.
func (m *Manager) InstallAPK(ctx context.Context, ids []string, apkPath string) error {
	apkPath = strings.TrimSpace(apkPath)
	if apkPath == "" {
		return errors.New("APK 路径不能为空")
	}
	targets, err := m.resolveRunningPorts(ids)
	if err != nil {
		return err
	}
	if apk.IsSplitBundlePath(apkPath) {
		extractedAPKs, cleanup, err := apk.ExtractSplitAPKs(apkPath)
		if err != nil {
			return err
		}
		defer cleanup()
		m.emitLog("", fmt.Sprintf("开始向 %d 个运行中实例批量安装分包: %s", len(targets), filepath.Base(apkPath)))
		var (
			mu   sync.Mutex
			errs []error
			wg   sync.WaitGroup
		)
		for id, port := range targets {
			wg.Add(1)
			go func(instID string, p int) {
				defer wg.Done()
				var abis []string
				var imgABI string
				compat := catalog.ARMCompatNone
				if inst, ok := m.registry.Get(instID); ok {
					abis = inst.ABIList
					imgABI, compat = imageCompatFromSystemImage(inst.SystemImage)
				}
				selected := apk.SelectSplitAPKsForABIs(extractedAPKs, abis, imgABI, compat)
				if err := m.installMultipleWithOptions(ctx, instID, p, selected, adb.InstallOptions{}); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("[%s] %s", instID, adb.TranslateInstallError(err)))
					mu.Unlock()
				}
			}(id, port)
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			m.emitLog("", fmt.Sprintf("批量安装分包部分失败: %v", err))
			return err
		}
		m.emitLog("", fmt.Sprintf("批量安装分包完成 (%s)", filepath.Base(apkPath)))
		return nil
	}
	m.emitLog("", fmt.Sprintf("开始向 %d 个运行中实例批量安装 APK: %s", len(targets), filepath.Base(apkPath)))
	if err := m.adb.BatchInstallAPK(ctx, targets, apkPath); err != nil {
		m.emitLog("", fmt.Sprintf("批量安装 APK 部分失败: %v", err))
		return err
	}
	m.emitLog("", fmt.Sprintf("批量安装 APK 完成 (%s)", filepath.Base(apkPath)))
	return nil
}

// Shell executes an adb shell command across all specified running instances concurrently.
func (m *Manager) Shell(ctx context.Context, ids []string, cmd string) (map[string]string, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil, errors.New("shell 命令不能为空")
	}
	targets, err := m.resolveRunningPorts(ids)
	if err != nil {
		return nil, err
	}
	m.emitLog("", fmt.Sprintf("向 %d 个实例执行命令: %s", len(targets), cmd))
	out, err := m.adb.BatchShell(ctx, targets, cmd)
	if err != nil {
		m.emitLog("", fmt.Sprintf("命令执行部分报错: %v", err))
	}
	return out, err
}

// OpenScreen opens an independent scrcpy window for a running instance.
func (m *Manager) OpenScreen(ctx context.Context, id string) error { return m.openScreen(ctx, id, nil) }

// OpenScreenAt uses client coordinates for a newly opened projection. Existing
// windows retain their position, as with OpenScreen; explicit bounds bypass auto-fill.
func (m *Manager) OpenScreenAt(ctx context.Context, id string, bounds screenlayout.Rect) error {
	if bounds.Width <= 0 || bounds.Height <= 0 || bounds.Width > 32768 || bounds.Height > 32768 || bounds.X < -32768 || bounds.X > 32768 || bounds.Y < -32768 || bounds.Y > 32768 {
		return errors.New("无效的投屏启动坐标或尺寸")
	}
	return m.openScreen(ctx, id, &bounds)
}
func (m *Manager) openScreen(ctx context.Context, id string, bounds *screenlayout.Rect) error {
	inst, ok := m.registry.Get(id)
	if !ok {
		return fmt.Errorf("实例不存在: %s", id)
	}
	if (inst.State != Running && inst.State != Starting) || inst.Port <= 0 {
		return fmt.Errorf("实例 %s 尚未运行，无法打开投屏", inst.Name)
	}
	title := fmt.Sprintf("%s (emulator-%d)", inst.Name, inst.Port)
	settings, _, err := m.GetProjectionSettings(inst.ID)
	if err != nil {
		return err
	}
	var created bool
	if bounds != nil {
		backend, ok := m.emu.(interface {
			StartProjectionWithBounds(context.Context, string, int, string, emu.ProjectionSettings, screenlayout.Rect) (bool, error)
		})
		if !ok {
			return errors.New("当前投屏后端不支持指定启动坐标")
		}
		created, err = backend.StartProjectionWithBounds(ctx, inst.ID, inst.Port, title, settings, *bounds)
	} else {
		created, err = m.emu.StartProjectionWithSettings(ctx, inst.ID, inst.Port, title, settings)
	}
	if err != nil {
		m.emitLog(id, fmt.Sprintf("打开 %s 投屏失败: %v", inst.Name, err))
		return err
	}
	m.emitLog(id, fmt.Sprintf("已拉起 %s 的 scrcpy 投屏窗口", inst.Name))
	if created && bounds == nil {
		if m.tryRestoreLastScreenPosition(ctx, id) {
			return nil
		}
		return m.fillNewScreen(ctx, id)
	}
	return nil
}

// WaitInstanceReady waits reliably for an instance to reach Running using both event subscription and polling fallback.
func (m *Manager) WaitInstanceReady(ctx context.Context, id string) (Instance, error) {
	evCh, cancel := m.Subscribe()
	defer cancel()

	check := func() (Instance, bool, error) {
		inst, ok := m.registry.Get(id)
		if !ok {
			return Instance{}, false, fmt.Errorf("实例不存在: %s", id)
		}
		switch inst.State {
		case Running:
			return inst, true, nil
		case Failed:
			if inst.LastError != "" {
				return inst, false, fmt.Errorf("实例 %s 启动失败: %s", inst.Name, inst.LastError)
			}
			return inst, false, fmt.Errorf("实例 %s 启动失败", inst.Name)
		case Stopped:
			return inst, false, fmt.Errorf("实例 %s 已停止", inst.Name)
		default:
			return inst, false, nil
		}
	}

	if inst, ready, err := check(); ready || err != nil {
		return inst, err
	}

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Instance{}, ctx.Err()
		case ev, ok := <-evCh:
			if !ok {
				if inst, ready, err := check(); ready || err != nil {
					return inst, err
				}
				return Instance{}, errors.New("事件通道已关闭")
			}
			if ev.Type == EventStateChanged && ev.InstanceID == id {
				if inst, ready, err := check(); ready || err != nil {
					return inst, err
				}
			}
		case <-ticker.C:
			if inst, ready, err := check(); ready || err != nil {
				return inst, err
			}
		}
	}
}

// ClearInstanceSnapshot clears saved quick-boot snapshots and forces a clean cold boot on next start.
func (m *Manager) ClearInstanceSnapshot(ctx context.Context, id string) error {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	inst, release, err := m.acquireOfflineOp(ctx, id, "清除快照")
	if err != nil {
		return err
	}
	defer release()
	if clearer, ok := m.emu.(interface{ ClearSnapshots(string) error }); ok {
		if err := clearer.ClearSnapshots(inst.InternalName()); err != nil {
			return err
		}
	}
	updated, err := m.registry.Update(id, func(r *Instance) error {
		r.ForceColdNext = true
		return nil
	})
	if err != nil {
		return err
	}
	m.emitStateChanged(updated, fmt.Sprintf("已清除实例 %s 的快速启动快照（下次启动将执行冷启动）", updated.Name))
	return nil
}

// Subscribe registers an event listener channel and returns a cancel function.
func (m *Manager) Subscribe() (<-chan Event, func()) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	id := m.nextID
	m.nextID++
	sub := newEventSubscriber()
	m.subs[id] = sub
	cancel := func() {
		m.subMu.Lock()
		s, ok := m.subs[id]
		if ok {
			delete(m.subs, id)
		}
		m.subMu.Unlock()
		if ok {
			s.close()
		}
	}
	return sub.out, cancel
}

func (m *Manager) resolveRunningPorts(ids []string) (map[string]int, error) {
	targets := make(map[string]int, len(ids))
	for _, id := range ids {
		inst, ok := m.registry.Get(id)
		if !ok {
			return nil, fmt.Errorf("实例不存在: %s", id)
		}
		if inst.State != Running || inst.Port <= 0 {
			return nil, fmt.Errorf("实例 %s 未处于运行就绪状态 (当前: %s)", inst.Name, inst.State)
		}
		targets[id] = inst.Port
	}
	if len(targets) == 0 {
		return nil, errors.New("未选择任何运行中的实例")
	}
	return targets, nil
}

func (m *Manager) allocatePortLocked() (int, error) {
	for port := m.cfg.MinPort; port <= m.cfg.MaxPort; port += 2 {
		if _, used := m.usedPorts[port]; used {
			continue
		}
		if !m.cfg.SkipTCPCheck && !isPortPairFree(port) {
			continue
		}
		return port, nil
	}
	return 0, fmt.Errorf("端口池 [%d..%d] 已满，没有空闲偶数端口", m.cfg.MinPort, m.cfg.MaxPort)
}

func isPortPairFree(evenPort int) bool {
	for _, p := range []int{evenPort, evenPort + 1} {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			return false
		}
		_ = ln.Close()
	}
	return true
}

func (m *Manager) nextAvailableName(prefix string, startIdx int) string {
	for idx := max(startIdx, 1); ; idx++ {
		suffix := fmt.Sprintf("_%02d", idx)
		runes := []rune(prefix)
		limit := 64 - len(suffix)
		if len(runes) > limit {
			runes = runes[:limit]
		}
		candidate := string(runes) + suffix
		if !m.registry.HasName(candidate) {
			return candidate
		}
	}
}

func (m *Manager) emitStateChanged(inst Instance, msg string) {
	m.emit(Event{
		Type:       EventStateChanged,
		InstanceID: inst.ID,
		Message:    msg,
		Timestamp:  nowStamp(),
		Data:       inst,
	})
}

func (m *Manager) emitLog(instID, msg string) {
	m.emit(Event{
		Type:       EventLog,
		InstanceID: instID,
		Message:    msg,
		Timestamp:  nowStamp(),
	})
}

func (m *Manager) emit(ev Event) {
	m.subMu.RLock()
	subs := make([]*eventSubscriber, 0, len(m.subs))
	for _, s := range m.subs {
		subs = append(subs, s)
	}
	m.subMu.RUnlock()
	for _, s := range subs {
		s.enqueue(ev)
	}
}

func nowStamp() string {
	return time.Now().Format("15:04:05")
}

func newShortID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

func firstLastLines(s string, maxLen int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var errLines []string
	var nonEmpty []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		nonEmpty = append(nonEmpty, l)
		upper := strings.ToUpper(l)
		if strings.Contains(upper, "ERROR") || strings.Contains(upper, "FATAL") || strings.Contains(upper, "PANIC") {
			errLines = append(errLines, l)
		}
	}
	chosen := errLines
	if len(chosen) == 0 {
		if len(nonEmpty) > 3 {
			chosen = nonEmpty[len(nonEmpty)-3:]
		} else {
			chosen = nonEmpty
		}
	}
	joined := strings.Join(chosen, " | ")
	if len(joined) <= maxLen {
		return joined
	}
	return joined[:maxLen] + "..."
}

// RenameInstance changes presentation metadata without moving any AVD files.
func (m *Manager) RenameInstance(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if err := validateDisplayName(name); err != nil {
		return err
	}
	m.namesMu.Lock()
	defer m.namesMu.Unlock()
	for _, item := range m.registry.List() {
		if item.ID != id && strings.EqualFold(item.Name, name) {
			return errors.New("该实例名称已存在")
		}
	}
	updated, err := m.registry.Update(id, func(item *Instance) error {
		if item.AVDName == "" {
			item.AVDName = item.Name
		}
		item.Name = name
		return nil
	})
	if err != nil {
		return err
	}
	m.emitStateChanged(updated, "实例已改名为 "+updated.Name)
	return nil
}
