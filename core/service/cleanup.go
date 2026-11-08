package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type CleanupSize struct {
	Files int
	Bytes int64
}
type CleanupReport struct {
	Downloads, Partials, AVDCache CleanupSize
	AVDBlocked                    bool
	AVDBlockReason                string
}
type CleanupOptions struct{ Downloads, Partials, AVDCache bool }
type cleanupFile struct {
	base, rel, kind string
	size            int64
}

// ScanCleanup only counts allowlisted disposable files, never user-data disks.
func (m *Manager) ScanCleanup(ctx context.Context) (CleanupReport, error) {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	report, _, err := m.cleanupCandidates(ctx)
	return report, err
}

func (m *Manager) avdCacheBlocked(ctx context.Context) (bool, string) {
	if len(m.running) > 0 || len(m.usedPorts) > 0 {
		return true, "有实例正在运行或启动，请全部停止后重新扫描。"
	}
	for _, inst := range m.registry.List() {
		if inst.State == Running || inst.State == Starting {
			return true, "有实例正在运行或启动，请全部停止后重新扫描。"
		}
	}
	active, err := m.adb.HasEmulators(ctx)
	if err != nil {
		return true, "ADB 检测失败，无法确认实例已停止，请检查 ADB 后重新扫描。"
	}
	if active {
		return true, "检测到模拟器（包括离线或启动中的实例），请全部停止后重新扫描。"
	}
	return false, ""
}

func (m *Manager) cleanupCandidates(ctx context.Context) (CleanupReport, []cleanupFile, error) {
	blocked, reason := m.avdCacheBlocked(ctx)
	report := CleanupReport{AVDBlocked: blocked, AVDBlockReason: reason}
	var files []cleanupFile
	add := func(root *os.Root, base, rel, kind string, total *CleanupSize) error {
		info, err := root.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, cleanupFile{base: base, rel: rel, kind: kind, size: info.Size()})
		total.Files++
		total.Bytes += info.Size()
		return nil
	}
	avdHome := m.cfg.AVDHome
	if avdHome == "" {
		if ctrl, ok := m.emu.(interface{ AVDHome() string }); ok {
			avdHome = ctrl.AVDHome()
		} else {
			avdHome = filepath.Join(m.dataDir(), "avd")
		}
	}
	for _, base := range []string{filepath.Join(m.dataDir(), "downloads"), avdHome} {
		if err := ctx.Err(); err != nil {
			return report, files, err
		}
		info, err := os.Lstat(base)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return report, files, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return report, files, fmt.Errorf("清理目录不是普通目录: %s", base)
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			return report, files, err
		}
		entries, err := fs.ReadDir(root.FS(), ".")
		if err == nil {
			for _, entry := range entries {
				if err = ctx.Err(); err != nil {
					break
				}
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				name := entry.Name()
				if base == avdHome {
					if report.AVDBlocked || !entry.IsDir() || !strings.HasSuffix(name, ".avd") {
						continue
					}
					for _, cache := range []string{"cache.img", "cache.img.qcow2"} {
						if err = add(root, base, filepath.Join(name, cache), "avd", &report.AVDCache); err != nil {
							break
						}
					}
				} else if !entry.IsDir() {
					switch {
					case strings.HasSuffix(strings.ToLower(name), ".zip"):
						err = add(root, base, name, "download", &report.Downloads)
					case strings.HasSuffix(name, ".part"), strings.HasSuffix(name, ".meta"):
						err = add(root, base, name, "partial", &report.Partials)
					}
				}
				if err != nil {
					break
				}
			}
		}
		root.Close()
		if err != nil {
			return report, files, err
		}
	}
	return report, files, nil
}

// CleanCaches rescans on execution, excluding installation and instance mutations.
func (m *Manager) CleanCaches(ctx context.Context, opts CleanupOptions) (CleanupSize, error) {
	var removed CleanupSize
	if !m.maintenance.TryLock() {
		return removed, errors.New("有下载、安装或实例操作正在进行，请完成后再清理")
	}
	defer m.maintenance.Unlock()
	m.mu.Lock()
	report, files, err := m.cleanupCandidates(ctx)
	if err != nil {
		m.mu.Unlock()
		return removed, err
	}
	if opts.AVDCache && report.AVDBlocked {
		m.mu.Unlock()
		return removed, errors.New(report.AVDBlockReason)
	}
	var errs []error
	for _, file := range files {
		if !(file.kind == "download" && opts.Downloads || file.kind == "partial" && opts.Partials || file.kind == "avd" && opts.AVDCache) {
			continue
		}
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		root, err := os.OpenRoot(file.base)
		if err == nil {
			err = root.Remove(file.rel)
			root.Close()
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("清理 %s: %w", file.rel, err))
			continue
		}
		removed.Files++
		removed.Bytes += file.size
	}
	m.mu.Unlock()
	m.emitLog("", fmt.Sprintf("已清理 %d 个缓存文件（%d 字节）", removed.Files, removed.Bytes))
	return removed, errors.Join(errs...)
}
