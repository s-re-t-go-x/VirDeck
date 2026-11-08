package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"virdeck/config"
	"virdeck/core/adb"
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
	CloneAVD(srcName, dstName string) error
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
	StopGrace     time.Duration
	BootTimeout   time.Duration
	SkipTCPCheck  bool // used in unit tests
}

type runningEntry struct {
	id         string
	port       int
	proc       *emu.Process
	bootCancel context.CancelFunc
	stopping   bool
	exited     chan struct{}
}

// Manager implements the Service interface.
type Manager struct {
	groupMu        sync.Mutex
	screenLayoutMu sync.Mutex
	namesMu        sync.Mutex
	maintenance    sync.RWMutex // excludes cleanup from installs and instance mutations
	cfg            Config
	settingsPath   string
	settings       Settings
	registry       *instance.Registry
	cat            *catalog.Store
	dl             *downloader.Client
	emu            emuBackend
	adb            adbBackend

	mu        sync.Mutex
	running   map[string]*runningEntry // instanceID -> runningEntry
	usedPorts map[int]string           // port -> instanceID

	subMu  sync.RWMutex
	nextID int
	subs   map[int]chan Event
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
		// Seamless one-time migration from legacy data/instances.json if present.
		if _, err := os.Stat(cfg.RegistryPath); errors.Is(err, os.ErrNotExist) {
			legacyPath := filepath.Join("data", "instances.json")
			if data, lerr := os.ReadFile(legacyPath); lerr == nil && len(strings.TrimSpace(string(data))) > 0 {
				_ = os.MkdirAll(filepath.Dir(cfg.RegistryPath), 0o755)
				_ = os.WriteFile(cfg.RegistryPath, data, 0o644)
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
		running:      make(map[string]*runningEntry),
		usedPorts:    make(map[int]string),
		subs:         make(map[int]chan Event),
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
		DataDir:        info.DataDir,
		SDKRoot:        info.SDKRoot,
		AVDHome:        info.AVDHome,
		EmulatorPath:   info.EmulatorPath,
		ADBPath:        info.ADBPath,
		AVDManagerPath: info.AVDManagerPath,
		SDKManagerPath: info.SDKManagerPath,
		ScrcpyPath:     info.ScrcpyPath,
		AccelOK:        info.AccelOK,
		AccelMessage:   info.AccelMessage,
		PathASCIIOK:    info.PathASCIIOK,
		PathWritableOK: info.PathWritableOK,
		PathWarning:    info.PathWarning,
		SystemImages:   info.SystemImages,
	}
	return status, nil
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
		if ctrl, ok := m.emu.(*emu.Controller); ok && strings.TrimSpace(spec.Resolution) != "" {
			err = ctrl.CreateAVDWithSpec(ctx, avdName, spec.SystemImage, spec.MemoryMB, spec.Cores, spec.Resolution)
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

// Clone clones a stopped instance into count new instances (optional workflow).
func (m *Manager) Clone(ctx context.Context, srcID string, count int) ([]Instance, error) {
	m.namesMu.Lock()
	defer m.namesMu.Unlock()
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	src, ok := m.registry.Get(srcID)
	if !ok {
		return nil, fmt.Errorf("未找到源实例: %s", srcID)
	}
	if (src.State != Stopped && src.State != Failed) || src.Port > 0 {
		return nil, fmt.Errorf("克隆前请先停止源实例 %s", src.Name)
	}
	if count <= 0 {
		count = config.Values.CloneCount
	}

	cloned := make([]Instance, 0, count)
	for i := 1; i <= count; i++ {
		select {
		case <-ctx.Done():
			return cloned, ctx.Err()
		default:
		}
		dstName := m.nextAvailableName(src.Name+"_clone", i)
		m.emitLog(src.ID, fmt.Sprintf("正在从 %s 克隆实例 %s...", src.Name, dstName))
		id := newShortID()
		avdName := "avd_" + id
		if err := m.emu.CloneAVD(src.InternalName(), avdName); err != nil {
			m.emitLog(src.ID, fmt.Sprintf("克隆 %s 失败: %v", dstName, err))
			return cloned, err
		}
		inst := Instance{
			ID:          id,
			AVDName:     avdName,
			Name:        dstName,
			SystemImage: src.SystemImage,
			MemoryMB:    src.MemoryMB,
			Cores:       src.Cores,
			Port:        0,
			Proxy:       src.Proxy,
			Headless:    src.Headless,
			State:       Stopped,
			CreatedAt:   time.Now().Format(time.RFC3339),
		}
		if err := m.registry.Add(inst); err != nil {
			return cloned, err
		}
		cloned = append(cloned, inst)
		m.emitStateChanged(inst, fmt.Sprintf("已克隆生成实例 %s", inst.Name))
	}
	return cloned, nil
}

// Start launches one or more instances, dynamically allocating even console ports and enforcing concurrency limits.
func (m *Manager) Start(ctx context.Context, ids ...string) error {
	var errs []error
	for _, id := range ids {
		if err := m.startOne(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) startOne(ctx context.Context, id string) error {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	inst, ok := m.registry.Get(id)
	if !ok {
		return fmt.Errorf("实例不存在: %s", id)
	}

	m.mu.Lock()
	if _, exists := m.running[id]; exists {
		m.mu.Unlock()
		if inst.State == Failed {
			return fmt.Errorf("实例 %s 上次启动失败的进程尚未退出，请先停止", inst.Name)
		}
		return nil // already running or starting
	}
	if len(m.running) >= m.cfg.MaxConcurrent {
		m.mu.Unlock()
		return fmt.Errorf("已达到最大同时运行实例数上限 (%d)，无法启动 %s", m.cfg.MaxConcurrent, inst.Name)
	}
	port, err := m.allocatePortLocked()
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("为 %s 分配端口失败: %w", inst.Name, err)
	}
	m.usedPorts[port] = id
	m.mu.Unlock()

	proc, err := m.emu.StartProcess(emu.LaunchOptions{
		AVDName:    inst.InternalName(),
		Port:       port,
		MemoryMB:   inst.MemoryMB,
		Cores:      inst.Cores,
		Proxy:      inst.Proxy,
		Headless:   inst.Headless,
		NoSnapshot: true,
	})
	if err != nil {
		m.mu.Lock()
		delete(m.usedPorts, port)
		m.mu.Unlock()
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
		bootCancel: bootCancel,
		exited:     make(chan struct{}),
	}

	m.mu.Lock()
	m.running[id] = entry
	m.mu.Unlock()

	updated, err := m.registry.Update(id, func(r *Instance) error {
		r.State = Starting
		r.Port = port
		r.AppliedProxy = inst.Proxy
		r.LastError = ""
		return nil
	})
	if err != nil {
		return err
	}
	m.emitStateChanged(updated, fmt.Sprintf("实例 %s 正在启动 (端口 %d, PID %d)...", updated.Name, port, proc.PID()))

	go m.watchInstance(entry, bootCtx)
	return nil
}

func (m *Manager) watchInstance(entry *runningEntry, bootCtx context.Context) {
	defer close(entry.exited)
	defer entry.bootCancel()

	bootCh := make(chan error, 1)
	go func() {
		bootCh <- m.adb.WaitForBoot(bootCtx, entry.port, 1500*time.Millisecond)
	}()

	var procErr error
	var bootFailure string
	select {
	case err := <-bootCh:
		if err == nil {
			if updated, uerr := m.registry.Update(entry.id, func(r *Instance) error {
				if r.State == Starting {
					r.State = Running
				}
				return nil
			}); uerr == nil {
				m.emitStateChanged(updated, fmt.Sprintf("实例 %s 启动完成 (emulator-%d)", updated.Name, entry.port))
			}
		}
		if err != nil {
			m.mu.Lock()
			stopping := entry.stopping
			m.mu.Unlock()
			if !stopping {
				bootFailure = fmt.Sprintf("启动检测失败: %v", err)
				if errors.Is(err, context.DeadlineExceeded) {
					bootFailure = fmt.Sprintf("启动超时（%s）: %v", m.cfg.BootTimeout, err)
				}
				if updated, uerr := m.registry.Update(entry.id, func(r *Instance) error { r.State = Failed; r.LastError = bootFailure; return nil }); uerr == nil {
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
		entry.bootCancel()
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
		lastErr = bootFailure
		if out := entry.proc.Output(); out != "" {
			lastErr += "；模拟器输出：" + firstLastLines(out, 320)
		}
		msg = fmt.Sprintf("实例 %s 启动失败: %s", prev.Name, lastErr)
	} else if !wasStopping && prev.State == Starting {
		finalState = Failed
		out := entry.proc.Output()
		if out != "" {
			lastErr = firstLastLines(out, 320)
		} else if procErr != nil {
			lastErr = procErr.Error()
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
		return nil
	}); err == nil {
		m.emitStateChanged(updated, msg)
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
		if err := m.stopOne(ctx, id); err != nil {
			errs = append(errs, err)
			continue
		}
		m.emitLog(id, fmt.Sprintf("正在删除实例 %s...", inst.Name))
		if err := m.emu.DeleteAVD(ctx, inst.InternalName()); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := m.registry.Delete(id); err != nil {
			errs = append(errs, err)
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

// SetProxy sets the HTTP proxy address for an instance (applied via -http-proxy on startup).
func (m *Manager) SetProxy(ctx context.Context, id, proxy string) error {
	proxy = strings.TrimSpace(proxy)
	updated, err := m.registry.Update(id, func(r *Instance) error {
		r.Proxy = proxy
		return nil
	})
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("实例 %s 代理已更新为 %q", updated.Name, proxy)
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

// InstallAPK installs an APK file across all specified running instances concurrently.
func (m *Manager) InstallAPK(ctx context.Context, ids []string, apkPath string) error {
	apkPath = strings.TrimSpace(apkPath)
	if apkPath == "" {
		return errors.New("APK 路径不能为空")
	}
	targets, err := m.resolveRunningPorts(ids)
	if err != nil {
		return err
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
func (m *Manager) OpenScreen(ctx context.Context, id string) error {
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
	created, err := m.emu.StartProjectionWithSettings(ctx, inst.ID, inst.Port, title, settings)
	if err != nil {
		m.emitLog(id, fmt.Sprintf("打开 %s 投屏失败: %v", inst.Name, err))
		return err
	}
	m.emitLog(id, fmt.Sprintf("已拉起 %s 的 scrcpy 投屏窗口", inst.Name))
	if created {
		return m.fillNewScreen(ctx, id)
	}
	return nil
}

// Subscribe registers an event listener channel and returns a cancel function.
func (m *Manager) Subscribe() (<-chan Event, func()) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	id := m.nextID
	m.nextID++
	ch := make(chan Event, 128)
	m.subs[id] = ch
	cancel := func() {
		m.subMu.Lock()
		defer m.subMu.Unlock()
		if c, ok := m.subs[id]; ok {
			delete(m.subs, id)
			close(c)
		}
	}
	return ch, cancel
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
	defer m.subMu.RUnlock()
	for _, ch := range m.subs {
		select {
		case ch <- ev:
		default:
		}
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
