package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type locationADB interface {
	SetLocation(ctx context.Context, port int, lon, lat float64) error
}

type screenshotADB interface {
	Screenshot(ctx context.Context, port int) ([]byte, error)
}

type appManageADB interface {
	ListPackages(ctx context.Context, port int, thirdPartyOnly bool) ([]string, error)
	ForceStopPackage(ctx context.Context, port int, pkg string) error
	ClearPackageData(ctx context.Context, port int, pkg string) error
	UninstallPackage(ctx context.Context, port int, pkg string) error
}

type trimADB interface {
	TrimStorage(ctx context.Context, port int) (string, error)
}

// SetLocation injects virtual GPS coordinates into one or more running instances.
func (m *Manager) SetLocation(ctx context.Context, ids []string, lat, lon float64) error {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		return fmt.Errorf("纬度须在 -90 到 90 之间: %v", lat)
	}
	if math.IsNaN(lon) || math.IsInf(lon, 0) || lon < -180 || lon > 180 {
		return fmt.Errorf("经度须在 -180 到 180 之间: %v", lon)
	}
	ports, err := m.resolveRunningPorts(ids)
	if err != nil {
		return err
	}
	backend, ok := m.adb.(locationADB)
	if !ok {
		return errors.New("当前 ADB 后端不支持虚拟定位")
	}

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	for id, port := range ports {
		wg.Add(1)
		go func(instID string, p int) {
			defer wg.Done()
			if e := backend.SetLocation(ctx, p, lon, lat); e != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("[%s] %w", instID, e))
				mu.Unlock()
				m.emitLog(instID, fmt.Sprintf("设置虚拟定位失败: %v", e))
				return
			}
			m.emitLog(instID, fmt.Sprintf("已应用虚拟定位: 纬度 %.6f, 经度 %.6f", lat, lon))
		}(id, port)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// CaptureScreenshots saves PNG screenshots for the target running instances into outDir
// (defaulting to <DataDir>/screenshots) and returns map[instanceID]savedFilePath.
func (m *Manager) CaptureScreenshots(ctx context.Context, ids []string, outDir string) (map[string]string, error) {
	ports, err := m.resolveRunningPorts(ids)
	if err != nil {
		return nil, err
	}
	backend, ok := m.adb.(screenshotADB)
	if !ok {
		return nil, errors.New("当前 ADB 后端不支持截屏")
	}
	dir := strings.TrimSpace(outDir)
	if dir == "" {
		dir = filepath.Join(m.cfg.DataDir, "screenshots")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建截图目录失败: %w", err)
	}
	stamp := time.Now().Format("20060102_150405")

	var (
		mu    sync.Mutex
		paths = make(map[string]string, len(ports))
		errs  []error
		wg    sync.WaitGroup
	)
	for id, port := range ports {
		wg.Add(1)
		go func(instID string, p int) {
			defer wg.Done()
			pngBytes, e := backend.Screenshot(ctx, p)
			if e != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("[%s] %w", instID, e))
				mu.Unlock()
				m.emitLog(instID, fmt.Sprintf("截图失败: %v", e))
				return
			}
			name := instID
			if inst, exists := m.registry.Get(instID); exists {
				name = sanitizeFileStem(inst.Name)
			}
			filePath := filepath.Join(dir, fmt.Sprintf("%s_%s_%s.png", name, instID, stamp))
			if writeErr := os.WriteFile(filePath, pngBytes, 0o600); writeErr != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("[%s] 保存截图失败: %w", instID, writeErr))
				mu.Unlock()
				return
			}
			mu.Lock()
			paths[instID] = filePath
			mu.Unlock()
			m.emitLog(instID, fmt.Sprintf("已保存截图: %s", filePath))
		}(id, port)
	}
	wg.Wait()
	return paths, errors.Join(errs...)
}

// ListInstalledApps returns installed packages for a running instance.
func (m *Manager) ListInstalledApps(ctx context.Context, id string, thirdPartyOnly bool) ([]InstalledApp, error) {
	ports, err := m.resolveRunningPorts([]string{id})
	if err != nil {
		return nil, err
	}
	backend, ok := m.adb.(appManageADB)
	if !ok {
		return nil, errors.New("当前 ADB 后端不支持应用列表查询")
	}
	pkgs, err := backend.ListPackages(ctx, ports[id], thirdPartyOnly)
	if err != nil {
		return nil, err
	}
	apps := make([]InstalledApp, 0, len(pkgs))
	for _, pkg := range pkgs {
		apps = append(apps, InstalledApp{PackageName: pkg, ThirdParty: thirdPartyOnly})
	}
	return apps, nil
}

// ManageInstalledApp performs launch/stop/clear/uninstall on an app in a running instance.
func (m *Manager) ManageInstalledApp(ctx context.Context, id, pkg, op string) error {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" {
		return errors.New("应用包名不能为空")
	}
	ports, err := m.resolveRunningPorts([]string{id})
	if err != nil {
		return err
	}
	port := ports[id]

	switch strings.ToLower(strings.TrimSpace(op)) {
	case "launch":
		return m.LaunchApp(ctx, id, pkg, "")
	case "stop":
		backend, ok := m.adb.(appManageADB)
		if !ok {
			return errors.New("当前 ADB 后端不支持强行停止应用")
		}
		if err := backend.ForceStopPackage(ctx, port, pkg); err != nil {
			return err
		}
		m.emitLog(id, "已强行停止应用: "+pkg)
		return nil
	case "clear":
		backend, ok := m.adb.(appManageADB)
		if !ok {
			return errors.New("当前 ADB 后端不支持清除应用数据")
		}
		if err := backend.ClearPackageData(ctx, port, pkg); err != nil {
			return err
		}
		m.emitLog(id, "已清除应用数据: "+pkg)
		return nil
	case "uninstall":
		backend, ok := m.adb.(appManageADB)
		if !ok {
			return errors.New("当前 ADB 后端不支持卸载应用")
		}
		if err := backend.UninstallPackage(ctx, port, pkg); err != nil {
			return err
		}
		m.emitLog(id, "已卸载应用: "+pkg)
		return nil
	default:
		return fmt.Errorf("不支持的应用操作: %q", op)
	}
}

func sanitizeFileStem(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "instance"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteByte('_')
		case r < 32:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ". ")
	if out == "" {
		return "instance"
	}
	return out
}
