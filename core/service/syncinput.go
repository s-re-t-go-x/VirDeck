package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"virdeck/core/emu"
	"virdeck/core/syncinput"
)

type inputSyncHooks struct {
	captureSupported func() bool
	captureReason    func() string
	locator          func() syncinput.WindowLocator
	newCapture       func() syncinput.CaptureBackend
	detectVersion    func(context.Context) (string, string, string, []string, error)
	querySize        func(context.Context, string, []string, int) (int, int, error)
	dialSink         func(context.Context, syncinput.ScrcpyControlConfig) (syncinput.ControlSink, error)
}

type inputSyncSession struct {
	cfg              InputSyncConfig
	masterName       string
	captureSupported bool
	captureHint      string
	broadcaster      *syncinput.Broadcaster
	capture          syncinput.CaptureBackend
	paused           atomic.Bool
	cancel           context.CancelFunc
	slaveOrder       []string
	slaves           map[string]InputSyncSlaveStatus
}

func (s *inputSyncSession) statusSnapshot() InputSyncStatus {
	slaves := make([]InputSyncSlaveStatus, 0, len(s.slaveOrder))
	for _, id := range s.slaveOrder {
		if st, ok := s.slaves[id]; ok {
			slaves = append(slaves, st)
		}
	}
	return InputSyncStatus{
		Active:           true,
		Paused:           s.broadcaster.IsPaused(),
		CaptureSupported: s.captureSupported,
		CaptureHint:      s.captureHint,
		MasterID:         s.cfg.MasterID,
		MasterName:       s.masterName,
		JitterPx:         s.cfg.JitterPx,
		MaxDelayMS:       s.cfg.MaxDelayMS,
		Slaves:           slaves,
	}
}

func (s *inputSyncSession) close() error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	var capErr error
	if s.capture != nil {
		capErr = s.capture.Stop()
	}
	var bcErr error
	if s.broadcaster != nil {
		bcErr = s.broadcaster.Close()
	}
	return errors.Join(capErr, bcErr)
}

func (m *Manager) effectiveSyncHooks() inputSyncHooks {
	if m.syncHooks != nil {
		return *m.syncHooks
	}
	return inputSyncHooks{
		captureSupported: syncinput.CaptureSupported,
		captureReason:    syncinput.CaptureUnsupportedReason,
		locator:          syncinput.NewNativeWindowLocator,
		newCapture:       syncinput.NewNativeCapture,
		detectVersion:    m.resolveSyncToolsAndVersion,
		querySize:        syncinput.QueryDeviceSize,
		dialSink: func(ctx context.Context, cfg syncinput.ScrcpyControlConfig) (syncinput.ControlSink, error) {
			return syncinput.DialScrcpyControl(ctx, cfg)
		},
	}
}

func (m *Manager) resolveSyncToolsAndVersion(ctx context.Context) (adbBin, scrcpyServerPath, version string, adbEnv []string, err error) {
	provider, ok := m.emu.(interface {
		ResolvePaths() (emulatorBin, adbBin, avdMgrBin, sdkMgrBin, scrcpyBin string)
		IsolatedEnv() []string
	})
	if !ok {
		return "", "", "", nil, errors.New("当前后端不支持解析 ADB 与 scrcpy 路径")
	}
	_, adbBin, _, _, scrcpyBin := provider.ResolvePaths()
	if adbBin == "" {
		return "", "", "", nil, errors.New("未找到 adb，请先在「镜像与环境」补齐基础工具")
	}
	if scrcpyBin == "" {
		return "", "", "", nil, errors.New("未找到 scrcpy，请先在「镜像与环境」补齐投屏组件")
	}
	serverPath := findScrcpyServer(scrcpyBin)
	if serverPath == "" {
		return "", "", "", nil, errors.New("未找到 scrcpy-server 组件文件")
	}
	ver, err := syncinput.DetectScrcpyVersion(ctx, scrcpyBin)
	if err != nil {
		return "", "", "", nil, err
	}
	return adbBin, serverPath, ver, provider.IsolatedEnv(), nil
}

func findScrcpyServer(scrcpyBin string) string {
	candidates := []string{
		strings.TrimSpace(os.Getenv("SCRCPY_SERVER_PATH")),
		filepath.Join(filepath.Dir(scrcpyBin), "scrcpy-server"),
		"/usr/share/scrcpy/scrcpy-server",
		"/usr/local/share/scrcpy/scrcpy-server",
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func (m *Manager) emitSyncChanged(st InputSyncStatus) {
	m.emit(Event{
		Type:      EventInputSyncChanged,
		Timestamp: nowStamp(),
		Data:      st,
	})
}

// GetInputSyncStatus returns the current multi-device input synchronization snapshot.
func (m *Manager) GetInputSyncStatus() InputSyncStatus {
	hooks := m.effectiveSyncHooks()
	m.syncMu.Lock()
	sess := m.syncSession
	if sess != nil {
		st := sess.statusSnapshot()
		m.syncMu.Unlock()
		return st
	}
	m.syncMu.Unlock()

	return InputSyncStatus{
		Active:           false,
		CaptureSupported: hooks.captureSupported(),
		CaptureHint:      hooks.captureReason(),
	}
}

// StartInputSync validates targets, connects scrcpy-server control channels, and starts input broadcasting.
func (m *Manager) StartInputSync(ctx context.Context, cfg InputSyncConfig) (InputSyncStatus, error) {
	if err := ctx.Err(); err != nil {
		return InputSyncStatus{}, err
	}
	m.syncOpMu.Lock()
	defer m.syncOpMu.Unlock()

	masterID := strings.TrimSpace(cfg.MasterID)
	if masterID == "" {
		return InputSyncStatus{}, errors.New("请选择主控实例")
	}
	master, ok := m.registry.Get(masterID)
	if !ok {
		return InputSyncStatus{}, fmt.Errorf("主控实例不存在: %s", masterID)
	}
	if master.State != Running || master.Port <= 0 {
		return InputSyncStatus{}, fmt.Errorf("主控实例 %s 尚未运行就绪", master.Name)
	}

	var slaveInsts []Instance
	seen := map[string]bool{masterID: true}
	for _, rawID := range cfg.SlaveIDs {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		inst, exists := m.registry.Get(id)
		if !exists {
			continue
		}
		if inst.State != Running || inst.Port <= 0 {
			return InputSyncStatus{}, fmt.Errorf("被控实例 %s 尚未运行就绪", inst.Name)
		}
		slaveInsts = append(slaveInsts, inst)
	}
	if len(slaveInsts) == 0 {
		return InputSyncStatus{}, errors.New("请至少选择一台运行中的被控实例")
	}

	if cfg.JitterPx < 0 {
		cfg.JitterPx = 0
	} else if cfg.JitterPx > 10 {
		cfg.JitterPx = 10
	}
	if cfg.MaxDelayMS < 0 {
		cfg.MaxDelayMS = 0
	} else if cfg.MaxDelayMS > 300 {
		cfg.MaxDelayMS = 300
	}
	cfg.MasterID = masterID
	cfg.SlaveIDs = make([]string, len(slaveInsts))
	for i, s := range slaveInsts {
		cfg.SlaveIDs[i] = s.ID
	}

	hooks := m.effectiveSyncHooks()
	capSupported := hooks.captureSupported()
	capHint := hooks.captureReason()

	// Stop any previous session before establishing fresh control sockets.
	_ = m.stopInputSyncLocked(ctx)

	var masterHandle uintptr
	if capSupported {
		hwnd, err := m.ensureMasterProjectionHandle(ctx, master)
		if err != nil {
			return InputSyncStatus{}, err
		}
		masterHandle = hwnd
	}

	adbBin, serverPath, version, adbEnv, err := hooks.detectVersion(ctx)
	if err != nil {
		return InputSyncStatus{}, err
	}

	masterW, masterH := m.resolveInstanceSize(ctx, hooks, adbBin, adbEnv, master)

	type dialResult struct {
		inst Instance
		sink syncinput.ControlSink
		err  error
	}
	results := make([]dialResult, len(slaveInsts))
	var wg sync.WaitGroup
	for i, inst := range slaveInsts {
		wg.Add(1)
		go func(idx int, target Instance) {
			defer wg.Done()
			sw, sh := m.resolveInstanceSize(ctx, hooks, adbBin, adbEnv, target)
			sink, derr := hooks.dialSink(ctx, syncinput.ScrcpyControlConfig{
				InstanceID:       target.ID,
				Port:             target.Port,
				ADBBin:           adbBin,
				ADBEnv:           adbEnv,
				ScrcpyServerPath: serverPath,
				ServerVersion:    version,
				DeviceWidth:      sw,
				DeviceHeight:     sh,
			})
			results[idx] = dialResult{inst: target, sink: sink, err: derr}
		}(i, inst)
	}
	wg.Wait()

	var sinks []syncinput.ControlSink
	slaveOrder := make([]string, 0, len(results))
	slaveStatuses := make(map[string]InputSyncSlaveStatus, len(results))
	var connectErrs []error

	for _, res := range results {
		st := InputSyncSlaveStatus{
			InstanceID: res.inst.ID,
			Name:       res.inst.Name,
		}
		if res.err != nil {
			st.Error = res.err.Error()
			connectErrs = append(connectErrs, fmt.Errorf("%s: %w", res.inst.Name, res.err))
		} else {
			st.Connected = true
			st.Enabled = true
			sinks = append(sinks, res.sink)
		}
		slaveOrder = append(slaveOrder, res.inst.ID)
		slaveStatuses[res.inst.ID] = st
	}
	if len(sinks) == 0 {
		return InputSyncStatus{}, fmt.Errorf("所有被控实例控制通道连接失败: %v", errors.Join(connectErrs...))
	}

	var loc syncinput.WindowLocator
	if hooks.locator != nil {
		loc = hooks.locator()
	}
	broadcaster := syncinput.NewBroadcaster(syncinput.Options{
		MasterID:       master.ID,
		MasterHandle:   masterHandle,
		MasterWidth:    masterW,
		MasterHeight:   masterH,
		JitterPx:       cfg.JitterPx,
		MaxDelayMS:     cfg.MaxDelayMS,
		MoveIntervalMS: 16,
	}, loc, sinks)

	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &inputSyncSession{
		cfg:              cfg,
		masterName:       master.Name,
		captureSupported: capSupported,
		captureHint:      capHint,
		broadcaster:      broadcaster,
		cancel:           cancel,
		slaveOrder:       slaveOrder,
		slaves:           slaveStatuses,
	}

	if capSupported && hooks.newCapture != nil {
		capBackend := hooks.newCapture()
		if scoped, ok := capBackend.(interface {
			SetMasterScope(uintptr, *atomic.Bool)
		}); ok {
			scoped.SetMasterScope(masterHandle, &sess.paused)
		}
		sess.capture = capBackend
		if err := capBackend.Start(sessCtx, broadcaster.HandleMouse, func() {
			m.toggleSyncPauseFromHotkey()
		}); err != nil {
			_ = sess.close()
			return InputSyncStatus{}, err
		}
	}

	m.syncMu.Lock()
	m.syncSession = sess
	status := sess.statusSnapshot()
	m.syncMu.Unlock()

	m.emitSyncChanged(status)
	m.emitLog(master.ID, fmt.Sprintf("已开启多设备操作同步：主控 %s -> %d 台被控", master.Name, len(sinks)))
	return status, nil
}

func (m *Manager) ensureMasterProjectionHandle(ctx context.Context, master Instance) (uintptr, error) {
	find := func() (uintptr, error) {
		wins, err := m.emu.ScrcpyWindows()
		if err != nil {
			return 0, err
		}
		for _, w := range wins {
			if w.InstanceID == master.ID && w.Window.Handle != 0 {
				return w.Window.Handle, nil
			}
		}
		return 0, nil
	}

	if hwnd, err := find(); err == nil && hwnd != 0 {
		return hwnd, nil
	}
	if err := m.OpenScreen(ctx, master.ID); err != nil {
		return 0, fmt.Errorf("打开主控实例 %s 投屏窗口失败: %w", master.Name, err)
	}
	hwnd, err := find()
	if err != nil {
		return 0, err
	}
	if hwnd == 0 {
		return 0, fmt.Errorf("未找到主控实例 %s 的 scrcpy 投屏窗口句柄，请确认投屏已打开", master.Name)
	}
	return hwnd, nil
}

func (m *Manager) resolveInstanceSize(ctx context.Context, hooks inputSyncHooks, adbBin string, adbEnv []string, inst Instance) (int, int) {
	if hooks.querySize != nil {
		if w, h, err := hooks.querySize(ctx, adbBin, adbEnv, inst.Port); err == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	if res, err := emu.ParseResolution(inst.Resolution); err == nil && res.Width > 0 && res.Height > 0 {
		return res.Width, res.Height
	}
	return 720, 1280
}

func (m *Manager) toggleSyncPauseFromHotkey() {
	m.syncMu.Lock()
	sess := m.syncSession
	if sess == nil {
		m.syncMu.Unlock()
		return
	}
	next := !sess.broadcaster.IsPaused()
	sess.paused.Store(next)
	sess.broadcaster.SetPaused(next)
	st := sess.statusSnapshot()
	m.syncMu.Unlock()

	m.emitSyncChanged(st)
	if next {
		m.emitLog("", "快捷键已暂停多设备操作同步")
	} else {
		m.emitLog("", "快捷键已恢复多设备操作同步")
	}
}

// StopInputSync stops the active multi-device synchronization session cleanly.
func (m *Manager) StopInputSync(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	m.syncOpMu.Lock()
	defer m.syncOpMu.Unlock()
	return m.stopInputSyncLocked(ctx)
}

func (m *Manager) stopInputSyncLocked(ctx context.Context) error {
	hooks := m.effectiveSyncHooks()
	m.syncMu.Lock()
	sess := m.syncSession
	m.syncSession = nil
	m.syncMu.Unlock()

	if sess == nil {
		return nil
	}
	err := sess.close()
	m.emitSyncChanged(InputSyncStatus{
		Active:           false,
		CaptureSupported: hooks.captureSupported(),
		CaptureHint:      hooks.captureReason(),
	})
	m.emitLog("", "已停止多设备操作同步")
	return err
}

// SetInputSyncPaused pauses or resumes pointer mirroring (releasing any held pointer on pause).
func (m *Manager) SetInputSyncPaused(paused bool) (InputSyncStatus, error) {
	m.syncMu.Lock()
	sess := m.syncSession
	if sess == nil {
		m.syncMu.Unlock()
		return InputSyncStatus{}, errors.New("当前未开启多设备同步")
	}
	sess.paused.Store(paused)
	sess.broadcaster.SetPaused(paused)
	st := sess.statusSnapshot()
	m.syncMu.Unlock()

	m.emitSyncChanged(st)
	return st, nil
}

// SetInputSyncSlaveEnabled temporarily excludes or re-includes a connected slave instance.
func (m *Manager) SetInputSyncSlaveEnabled(instanceID string, enabled bool) (InputSyncStatus, error) {
	instanceID = strings.TrimSpace(instanceID)
	m.syncMu.Lock()
	sess := m.syncSession
	if sess == nil {
		m.syncMu.Unlock()
		return InputSyncStatus{}, errors.New("当前未开启多设备同步")
	}
	stEntry, ok := sess.slaves[instanceID]
	if !ok {
		m.syncMu.Unlock()
		return InputSyncStatus{}, fmt.Errorf("未找到被控实例: %s", instanceID)
	}
	if !stEntry.Connected {
		m.syncMu.Unlock()
		return InputSyncStatus{}, fmt.Errorf("实例 %s 控制通道未连接", stEntry.Name)
	}
	sess.broadcaster.SetSlaveEnabled(instanceID, enabled)
	stEntry.Enabled = enabled
	sess.slaves[instanceID] = stEntry
	st := sess.statusSnapshot()
	m.syncMu.Unlock()

	m.emitSyncChanged(st)
	return st, nil
}

// BroadcastSyncKey injects a system navigation key ("back", "home", "recent") into all enabled slaves.
func (m *Manager) BroadcastSyncKey(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var keycode uint32
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "back":
		keycode = syncinput.KeyBack
	case "home":
		keycode = syncinput.KeyHome
	case "recent", "appswitch", "overview":
		keycode = syncinput.KeyAppSwitch
	default:
		return fmt.Errorf("不支持的同步按键: %s", key)
	}

	m.syncMu.Lock()
	sess := m.syncSession
	if sess == nil {
		m.syncMu.Unlock()
		return errors.New("当前未开启多设备同步")
	}
	bc := sess.broadcaster
	m.syncMu.Unlock()
	return bc.BroadcastKey(keycode)
}

// BroadcastSyncText injects text into all enabled slaves.
func (m *Manager) BroadcastSyncText(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if text == "" {
		return errors.New("请输入要同步发送的文本")
	}
	m.syncMu.Lock()
	sess := m.syncSession
	if sess == nil {
		m.syncMu.Unlock()
		return errors.New("当前未开启多设备同步")
	}
	bc := sess.broadcaster
	m.syncMu.Unlock()
	return bc.BroadcastText(text)
}

func (m *Manager) handleInstanceStoppedForSync(id string) {
	hooks := m.effectiveSyncHooks()
	m.syncMu.Lock()
	sess := m.syncSession
	if sess == nil {
		m.syncMu.Unlock()
		return
	}
	if sess.cfg.MasterID == id {
		m.syncSession = nil
		m.syncMu.Unlock()
		_ = sess.close()
		m.emitSyncChanged(InputSyncStatus{
			Active:           false,
			CaptureSupported: hooks.captureSupported(),
			CaptureHint:      hooks.captureReason(),
		})
		m.emitLog(id, "主控实例已停止，多设备操作同步已自动结束")
		return
	}
	entry, isSlave := sess.slaves[id]
	if !isSlave {
		m.syncMu.Unlock()
		return
	}
	entry.Enabled = false
	entry.Connected = false
	entry.Error = "实例已停止"
	sess.slaves[id] = entry
	bc := sess.broadcaster
	st := sess.statusSnapshot()
	m.syncMu.Unlock()

	bc.RemoveSlave(id)
	m.emitSyncChanged(st)
}
