package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"virdeck/core/adb"
	"virdeck/core/apk"
	"virdeck/core/catalog"
	"virdeck/core/emu"
)

const maxRecentAppsPerInstance = 5

// InspectAPK extracts lightweight package and ABI metadata from a local APK archive.
func (m *Manager) InspectAPK(apkPath string) (APKMetadata, error) {
	return apk.Inspect(apkPath)
}

// InstallApp executes the unified per-instance application workflow:
// start stopped/starting instances -> wait boot + pm path android -> adb install -r [-g] ->
// resolve LAUNCHER activity & am start -n -> open scrcpy screen (only when Headless == true).
func (m *Manager) InstallApp(ctx context.Context, req InstallAppRequest) error {
	apkPath := strings.TrimSpace(req.APKPath)
	if apkPath == "" || strings.HasPrefix(apkPath, "-") {
		return errors.New("APK 路径不能为空或以 - 开头")
	}
	info, err := os.Stat(apkPath)
	if err != nil {
		return fmt.Errorf("无法访问 APK 文件: %w", err)
	}
	if info.IsDir() {
		return errors.New("暂不支持直接安装文件夹，请选择 .apk / .apks / .xapk 文件")
	}

	ids := uniqueIDs(req.InstanceIDs)
	if len(ids) == 0 {
		return errors.New("未选择目标实例")
	}

	meta, inspectErr := apk.Inspect(apkPath)
	if inspectErr != nil && req.LaunchApp {
		m.emitLog("", fmt.Sprintf("未能本地解析 APK 包名 (%v)，安装后将跳过自动启动应用", inspectErr))
	}

	var extractedAPKs []string
	if apk.IsSplitBundlePath(apkPath) {
		var cleanup func()
		extractedAPKs, cleanup, err = apk.ExtractSplitAPKs(apkPath)
		if err != nil {
			return err
		}
		defer cleanup()
		if meta.HasOBB {
			m.emitLog("", "检测到分包内含 OBB 数据包，将安装主程序分包；如需补充 OBB 数据请通过「文件传输」推送至 /sdcard/Android/obb/")
		}
	}

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	for _, id := range ids {
		wg.Add(1)
		go func(instID string) {
			defer wg.Done()
			if err := m.installAppOne(ctx, instID, apkPath, extractedAPKs, meta, req); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (m *Manager) installAppOne(ctx context.Context, id, apkPath string, extractedAPKs []string, meta APKMetadata, req InstallAppRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	totalSteps := 2
	if req.LaunchApp {
		totalSteps++
	}
	if req.OpenScreen {
		totalSteps++
	}
	emitStage := func(step int, text string, pct int) {
		m.emit(Event{
			Type:       EventPipelineStage,
			InstanceID: id,
			Message:    text,
			Timestamp:  nowStamp(),
			Data: PipelineStage{
				InstanceID: id,
				StageText:  text,
				Step:       step,
				TotalSteps: totalSteps,
				Percent:    pct,
			},
		})
	}

	inst, ok := m.registry.Get(id)
	if !ok {
		return fmt.Errorf("实例不存在: %s", id)
	}

	// Step 1: Ensure instance is started and Android package manager is ready (no maintenance lock held).
	switch inst.State {
	case Stopped, Failed:
		emitStage(1, fmt.Sprintf("正在启动 %s 并等待开机...", inst.Name), 15)
		if err := m.Start(ctx, id); err != nil {
			return fmt.Errorf("[%s] 启动失败: %w", inst.Name, err)
		}
		ready, err := m.WaitInstanceReady(ctx, id)
		if err != nil {
			return fmt.Errorf("[%s] %w", inst.Name, err)
		}
		inst = ready
	case Starting:
		emitStage(1, fmt.Sprintf("正在等待 %s 启动就绪...", inst.Name), 20)
		ready, err := m.WaitInstanceReady(ctx, id)
		if err != nil {
			return fmt.Errorf("[%s] %w", inst.Name, err)
		}
		inst = ready
	case Running:
		emitStage(1, fmt.Sprintf("检查 %s 包管理服务...", inst.Name), 25)
	}

	if inst.Port <= 0 {
		return fmt.Errorf("[%s] 实例端口无效", inst.Name)
	}
	if pm, ok := m.adb.(interface {
		CheckPackageManagerReady(context.Context, int) error
	}); ok {
		emitStage(1, fmt.Sprintf("等待 %s 包管理服务就绪...", inst.Name), 35)
		if err := pm.CheckPackageManagerReady(ctx, inst.Port); err != nil {
			return fmt.Errorf("[%s] %w", inst.Name, err)
		}
	}

	// Step 2: Install APK or split bundle (-r [-g]).
	emitStage(2, fmt.Sprintf("正在安装 %s...", filepath.Base(apkPath)), 55)
	opts := adb.InstallOptions{GrantPermissions: req.GrantPermissions}
	var installErr error
	if len(extractedAPKs) > 0 {
		imageABI, imageCompat := imageCompatFromSystemImage(inst.SystemImage)
		targetDPI := emu.LookupResolution(inst.Resolution).Density
		selectedAPKs := apk.SelectSplitAPKs(extractedAPKs, inst.ABIList, imageABI, imageCompat, targetDPI)
		installErr = m.installMultipleWithOptions(ctx, id, inst.Port, selectedAPKs, opts)
	} else {
		installErr = m.installWithOptions(ctx, id, inst.Port, apkPath, opts)
	}
	if installErr != nil {
		msg := adb.TranslateInstallError(installErr)
		m.emitLog(id, fmt.Sprintf("%s 安装失败: %s", inst.Name, msg))
		return fmt.Errorf("[%s] %s", inst.Name, msg)
	}
	m.emitLog(id, fmt.Sprintf("%s 已安装 %s", inst.Name, filepath.Base(apkPath)))
	if meta.Package != "" {
		m.recordRecentApp(id, meta.Package, "")
	}

	// Step 3: Resolve LAUNCHER activity on device and launch via am start -n (no monkey fallback).
	nextStep := 3
	if req.LaunchApp {
		if meta.Package == "" {
			emitStage(nextStep, "已跳过自动启动（无包名元数据）", 75)
		} else {
			emitStage(nextStep, fmt.Sprintf("正在查找并启动 %s...", meta.Package), 80)
			if err := m.resolveAndLaunch(ctx, id, inst.Port, meta.Package, ""); err != nil {
				m.emitLog(id, fmt.Sprintf("%s 启动应用失败: %v", inst.Name, err))
				return fmt.Errorf("[%s] %w", inst.Name, err)
			}
		}
		nextStep++
	}

	// Step 4: Open scrcpy screen only when Headless == true (avoids duplicate window for native QEMU GUI mode).
	if req.OpenScreen {
		if !inst.Headless {
			m.emitLog(id, fmt.Sprintf("实例 %s 为自带窗口模式，跳过 scrcpy 投屏", inst.Name))
			emitStage(nextStep, "自带窗口模式已运行，跳过额外投屏", 95)
		} else {
			emitStage(nextStep, "正在打开投屏窗口...", 95)
			if err := m.OpenScreen(ctx, id); err != nil {
				return fmt.Errorf("[%s] 打开投屏失败: %w", inst.Name, err)
			}
		}
	}

	emitStage(totalSteps, "安装流程已完成", 100)
	return nil
}

// LaunchApp resolves (if needed) and launches an installed package on a running instance, recording it in RecentApps.
func (m *Manager) LaunchApp(ctx context.Context, instanceID, pkg, component string) error {
	inst, ok := m.registry.Get(instanceID)
	if !ok {
		return fmt.Errorf("实例不存在: %s", instanceID)
	}
	if inst.State != Running || inst.Port <= 0 {
		return fmt.Errorf("实例 %s 未处于运行就绪状态", inst.Name)
	}
	pkg = strings.TrimSpace(pkg)
	component = strings.TrimSpace(component)
	if pkg == "" && component == "" {
		return errors.New("请指定要启动的应用包名或组件入口")
	}

	launcher, ok := m.adb.(interface {
		LaunchActivity(context.Context, int, string) error
	})
	if !ok {
		return errors.New("当前 ADB 后端不支持启动应用入口")
	}
	if component == "" {
		resolver, ok := m.adb.(interface {
			ResolveLauncherActivity(context.Context, int, string) (string, error)
		})
		if !ok {
			return errors.New("当前 ADB 后端不支持解析桌面入口")
		}
		resolved, err := resolver.ResolveLauncherActivity(ctx, inst.Port, pkg)
		if err != nil {
			return err
		}
		if resolved == "" {
			return fmt.Errorf("应用 %s 没有可启动的桌面入口", pkg)
		}
		component = resolved
	}
	if err := launcher.LaunchActivity(ctx, inst.Port, component); err != nil {
		return err
	}
	if pkg == "" {
		pkg, _, _ = strings.Cut(component, "/")
	}
	m.recordRecentApp(instanceID, pkg, component)
	m.emitLog(instanceID, fmt.Sprintf("已在 %s 启动应用 %s", inst.Name, component))
	return nil
}

func (m *Manager) installWithOptions(ctx context.Context, id string, port int, apkPath string, opts adb.InstallOptions) error {
	if installer, ok := m.adb.(interface {
		InstallAPKWithOptions(context.Context, int, string, adb.InstallOptions) (string, error)
	}); ok {
		_, err := installer.InstallAPKWithOptions(ctx, port, apkPath, opts)
		return err
	}
	return m.adb.BatchInstallAPK(ctx, map[string]int{id: port}, apkPath)
}

func (m *Manager) installMultipleWithOptions(ctx context.Context, id string, port int, apkPaths []string, opts adb.InstallOptions) error {
	if len(apkPaths) == 1 {
		return m.installWithOptions(ctx, id, port, apkPaths[0], opts)
	}
	if multi, ok := m.adb.(interface {
		InstallMultipleAPKsWithOptions(context.Context, int, []string, adb.InstallOptions) (string, error)
	}); ok {
		_, err := multi.InstallMultipleAPKsWithOptions(ctx, port, apkPaths, opts)
		return err
	}
	return errors.New("当前 ADB 后端不支持多包 install-multiple 安装")
}

func imageCompatFromSystemImage(systemImage string) (string, catalog.ARMCompat) {
	parts := strings.Split(strings.TrimSpace(systemImage), ";")
	if len(parts) < 4 {
		return "", catalog.ARMCompatNone
	}
	return parts[3], catalog.ImageARMCompat(parts[1], parts[2], parts[3])
}

func (m *Manager) resolveAndLaunch(ctx context.Context, id string, port int, pkg, cachedComponent string) error {
	component := strings.TrimSpace(cachedComponent)
	if component == "" {
		resolver, ok := m.adb.(interface {
			ResolveLauncherActivity(context.Context, int, string) (string, error)
		})
		if !ok {
			return nil
		}
		resolved, err := resolver.ResolveLauncherActivity(ctx, port, pkg)
		if err != nil {
			return err
		}
		component = resolved
	}
	if component == "" {
		m.emitLog(id, fmt.Sprintf("应用 %s 无桌面启动入口（可能为输入法或后台服务），已跳过拉起", pkg))
		return nil
	}
	launcher, ok := m.adb.(interface {
		LaunchActivity(context.Context, int, string) error
	})
	if !ok {
		return nil
	}
	if err := launcher.LaunchActivity(ctx, port, component); err != nil {
		return err
	}
	m.recordRecentApp(id, pkg, component)
	return nil
}

func (m *Manager) recordRecentApp(id, pkg, component string) {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" {
		return
	}
	updated, err := m.registry.Update(id, func(r *Instance) error {
		next := make([]RecentApp, 0, maxRecentAppsPerInstance)
		keptComp := strings.TrimSpace(component)
		for _, prev := range r.RecentApps {
			if prev.Package == pkg {
				if keptComp == "" {
					keptComp = prev.Component
				}
				continue
			}
			next = append(next, prev)
		}
		entry := RecentApp{
			Package:    pkg,
			Component:  keptComp,
			LastUsedAt: time.Now().Format(time.RFC3339),
		}
		result := append([]RecentApp{entry}, next...)
		if len(result) > maxRecentAppsPerInstance {
			result = result[:maxRecentAppsPerInstance]
		}
		r.RecentApps = result
		return nil
	})
	if err == nil {
		m.emitStateChanged(updated, "")
	}
}

func uniqueIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
