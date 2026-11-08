package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"virdeck/config"
	"virdeck/core/catalog"
	"virdeck/core/downloader"
	"virdeck/core/emu"
)

func loadSettings(path string) Settings {
	def := Settings{
		MaxConcurrent: DefaultMaxConcurrent,
		ScreenLayout:  ScreenLayoutPreferences{Size: config.Values.ScreenLayout.Size, AutoFill: config.Values.ScreenLayout.AutoFill},
		ADBServerPort: emu.DefaultADBServerPort,
	}
	if path == "" {
		return def
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	s := def
	if err := json.Unmarshal(raw, &s); err != nil {
		return def
	}
	if s.MaxConcurrent <= 0 {
		s.MaxConcurrent = DefaultMaxConcurrent
	}
	if s.ADBServerPort <= 0 {
		s.ADBServerPort = emu.DefaultADBServerPort
	}
	if !validScreenSize(s.ScreenLayout.Size) {
		s.ScreenLayout.Size = def.ScreenLayout.Size
	}
	return s
}

func saveSettings(path string, s Settings) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// GetSettings returns the current global settings.
func (m *Manager) GetSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

// UpdateSettings updates and persists global settings (download proxy, max concurrency, license consent).
func (m *Manager) UpdateSettings(s Settings) error {
	if s.MaxConcurrent <= 0 {
		s.MaxConcurrent = DefaultMaxConcurrent
	}
	if s.ADBServerPort <= 0 {
		s.ADBServerPort = emu.DefaultADBServerPort
	}
	s.DownloadProxy = strings.TrimSpace(s.DownloadProxy)
	if s.ScreenLayout.Size == "" {
		s.ScreenLayout.Size = config.Values.ScreenLayout.Size
	}
	if !validScreenSize(s.ScreenLayout.Size) {
		return errors.New("无效的投屏窗口大小")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := saveSettings(m.settingsPath, s); err != nil {
		return err
	}
	m.settings = s
	m.cfg.MaxConcurrent = s.MaxConcurrent
	if m.dl != nil {
		m.dl.SetProxy(s.DownloadProxy)
	}
	return nil
}

// ListCatalog returns the full 3-tier system image catalog merged with local installation status.
func (m *Manager) ListCatalog(ctx context.Context) ([]ImageItem, error) {
	installed := m.emu.ListSystemImages()
	if m.cat == nil {
		return nil, nil
	}
	return m.cat.List(installed), nil
}

// SyncCatalog fetches official Android SDK sys-img2-3.xml manifests and updates the catalog cache.
func (m *Manager) SyncCatalog(ctx context.Context) (int, error) {
	if m.cat == nil || m.dl == nil {
		return 0, errors.New("镜像目录服务未初始化")
	}
	m.emitLog("", "正在从 Google 官方仓库同步系统镜像列表...")
	n, err := m.cat.SyncFromOfficial(ctx, m.dl)
	if err != nil {
		m.emitLog("", fmt.Sprintf("同步官方镜像列表失败: %v", err))
		return n, err
	}
	m.emitLog("", fmt.Sprintf("已同步 %d 个官方系统镜像条目", n))
	m.emit(Event{
		Type:      EventStateChanged,
		Message:   fmt.Sprintf("已更新官方镜像目录（共 %d 项）", n),
		Timestamp: nowStamp(),
	})
	return n, nil
}

// SaveCustomImage adds or updates a custom system image entry in the catalog.
func (m *Manager) SaveCustomImage(ctx context.Context, item ImageItem) error {
	if m.cat == nil {
		return errors.New("镜像目录未初始化")
	}
	if err := m.cat.SaveCustom(item); err != nil {
		return err
	}
	m.emitLog("", fmt.Sprintf("已保存自定义镜像条目: %s", item.ID))
	return nil
}

// RemoveCustomImage removes a custom system image entry from the catalog.
func (m *Manager) RemoveCustomImage(ctx context.Context, id string) error {
	if m.cat == nil {
		return nil
	}
	return m.cat.RemoveCustom(id)
}

// DeleteSystemImage deletes the locally installed files of a system image,
// refusing if any registered instance uses that image.
func (m *Manager) DeleteSystemImage(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	for _, inst := range m.registry.List() {
		if inst.SystemImage == id {
			return fmt.Errorf("实例 %q 正在使用该系统镜像 (%s)，请先删除相关实例后再删除镜像", inst.Name, id)
		}
	}
	apiDir, tag, abi := catalog.SplitPackageID(id)
	if apiDir == "" || tag == "" || abi == "" {
		return fmt.Errorf("无效的系统镜像 ID: %s", id)
	}

	deletedAny := false
	for _, root := range m.candidateSDKRoots() {
		target := filepath.Join(root, "system-images", apiDir, tag, abi)
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("删除镜像目录 %s 失败: %w", target, err)
			}
			deletedAny = true
		}
	}
	if !deletedAny {
		return fmt.Errorf("本地未找到该镜像的安装目录: %s", id)
	}
	m.emitLog("", fmt.Sprintf("已删除本地系统镜像: %s", id))
	m.emit(Event{
		Type:      EventStateChanged,
		Message:   fmt.Sprintf("已删除系统镜像 %s", id),
		Timestamp: nowStamp(),
	})
	return nil
}

// DownloadSystemImage downloads, verifies SHA-1, extracts a system image into VirDeckData/sdk/system-images,
// and generates package.xml + platforms placeholder.
func (m *Manager) DownloadSystemImage(ctx context.Context, id string) error {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	if m.dl == nil || m.cat == nil {
		return errors.New("下载器未初始化")
	}
	item, ok := m.cat.Get(id, m.emu.ListSystemImages())
	if !ok {
		return fmt.Errorf("未在镜像目录中找到 %s", id)
	}

	urlStr := strings.TrimSpace(item.URL)
	expectedSHA1 := strings.TrimSpace(item.SHA1)

	// If URL or SHA-1 is missing (or to get the latest official SHA-1), query official sys-img XML first.
	if urlStr == "" || (item.Source == catalog.SourceRecommended && expectedSHA1 == "") {
		if _, err := m.cat.SyncFromOfficial(ctx, m.dl); err == nil {
			if refreshed, ok2 := m.cat.Get(id, m.emu.ListSystemImages()); ok2 {
				if refreshed.URL != "" {
					urlStr = refreshed.URL
				}
				if refreshed.SHA1 != "" {
					expectedSHA1 = refreshed.SHA1
				}
			}
		}
	}
	if urlStr == "" {
		return fmt.Errorf("镜像 %s 缺少下载直链，请先同步官方列表或在自定义镜像中填写 URL", id)
	}

	m.emitLog("", fmt.Sprintf("开始下载系统镜像 %s (%s)...", item.Name, urlStr))
	zipPath, err := m.dl.Download(ctx, id, item.Name, urlStr, expectedSHA1, m.onDownloadProgress)
	if err != nil {
		m.emitDownloadFailed(id, item.Name, err)
		return err
	}

	if err := m.installSystemImageFromZip(ctx, id, item.Name, zipPath); err != nil {
		m.emitDownloadFailed(id, item.Name, err)
		return err
	}
	return nil
}

// ImportSystemImageZip imports a user-supplied local .zip archive for system image id.
func (m *Manager) ImportSystemImageZip(ctx context.Context, id, zipPath string) error {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	id = strings.TrimSpace(id)
	zipPath = strings.TrimSpace(zipPath)
	if id == "" || zipPath == "" {
		return errors.New("必须指定目标系统镜像包 ID 和本地 .zip 文件路径")
	}
	apiDir, tag, abi := catalog.SplitPackageID(id)
	if apiDir == "" || tag == "" || abi == "" {
		return fmt.Errorf("系统镜像 ID 格式无效 (%s)，应为 system-images;android-<API>;<tag>;<abi>", id)
	}
	m.emitLog("", fmt.Sprintf("正在导入本地系统镜像压缩包: %s -> %s", filepath.Base(zipPath), id))
	if err := m.installSystemImageFromZip(ctx, id, id, zipPath); err != nil {
		m.emitDownloadFailed(id, id, err)
		return err
	}
	return nil
}

func (m *Manager) installSystemImageFromZip(ctx context.Context, id, displayName, zipPath string) error {
	apiDir, tag, abi := catalog.SplitPackageID(id)
	if apiDir == "" || tag == "" || abi == "" {
		return fmt.Errorf("无效的系统镜像包 ID: %s", id)
	}
	sdkRoot := m.managedSDKRoot()
	destDir := filepath.Join(sdkRoot, "system-images", apiDir, tag, abi)

	if err := downloader.ExtractZipAtomically(ctx, zipPath, destDir, m.onDownloadProgress, id, displayName); err != nil {
		return err
	}
	if err := emu.EnsureSystemImageMetadata(sdkRoot, id); err != nil {
		return err
	}
	if m.cat != nil {
		m.cat.SetSDKRoot(sdkRoot)
	}
	m.emitLog("", fmt.Sprintf("系统镜像 %s 安装就绪 (%s)", displayName, destDir))
	m.emit(Event{
		Type:      EventStateChanged,
		Message:   fmt.Sprintf("系统镜像 %s 已安装完成", displayName),
		Timestamp: nowStamp(),
	})
	return nil
}

// EnsureBaseTool downloads and installs a missing base runtime tool ("adb" | "emulator" | "scrcpy" | "all").
func (m *Manager) EnsureBaseTool(ctx context.Context, tool string) error {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if tool == "all" {
		st, _ := m.CheckEnv(ctx)
		if st.ADBPath == "" {
			if err := m.EnsureBaseTool(ctx, "adb"); err != nil {
				return err
			}
		}
		if st.EmulatorPath == "" {
			if err := m.EnsureBaseTool(ctx, "emulator"); err != nil {
				return err
			}
		}
		if st.ScrcpyPath == "" {
			if err := m.EnsureBaseTool(ctx, "scrcpy"); err != nil {
				return err
			}
		}
		return nil
	}

	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	if m.dl == nil {
		return errors.New("下载器未初始化")
	}

	urlStr, sha1Hex, title, destDir, err := m.resolveBaseToolSource(ctx, tool)
	if err != nil {
		return err
	}

	m.emitLog("", fmt.Sprintf("开始下载基础组件 %s (%s)...", title, urlStr))
	zipPath, err := m.dl.Download(ctx, "tool:"+tool, title, urlStr, sha1Hex, m.onDownloadProgress)
	if err != nil {
		m.emitDownloadFailed("tool:"+tool, title, err)
		return err
	}

	if err := downloader.ExtractZipAtomically(ctx, zipPath, destDir, m.onDownloadProgress, "tool:"+tool, title); err != nil {
		m.emitDownloadFailed("tool:"+tool, title, err)
		return err
	}

	_, _ = m.CheckEnv(ctx)
	m.emitLog("", fmt.Sprintf("基础组件 %s 已安装至 %s", title, destDir))
	m.emit(Event{
		Type:      EventStateChanged,
		Message:   fmt.Sprintf("组件 %s 已安装就绪", title),
		Timestamp: nowStamp(),
	})
	return nil
}

// ImportBaseToolZip imports a user-provided local .zip archive for "adb", "emulator", or "scrcpy".
func (m *Manager) ImportBaseToolZip(ctx context.Context, tool, zipPath string) error {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	tool = strings.ToLower(strings.TrimSpace(tool))
	zipPath = strings.TrimSpace(zipPath)
	if zipPath == "" {
		return errors.New("请选择要导入的本地 .zip 压缩包")
	}
	var destDir, title string
	switch tool {
	case "adb", "platform-tools":
		title = "ADB (platform-tools)"
		destDir = filepath.Join(m.managedSDKRoot(), "platform-tools")
	case "emulator":
		title = "Android Emulator"
		destDir = filepath.Join(m.managedSDKRoot(), "emulator")
	case "scrcpy":
		title = "scrcpy 投屏组件"
		destDir = filepath.Join(m.dataDir(), "scrcpy")
	default:
		return fmt.Errorf("未知的基础工具类型: %s (可选: adb, emulator, scrcpy)", tool)
	}
	m.emitLog("", fmt.Sprintf("正在导入本地组件包 %s -> %s", filepath.Base(zipPath), destDir))
	if err := downloader.ExtractZipAtomically(ctx, zipPath, destDir, m.onDownloadProgress, "tool:"+tool, title); err != nil {
		m.emitDownloadFailed("tool:"+tool, title, err)
		return err
	}
	_, _ = m.CheckEnv(ctx)
	m.emitLog("", fmt.Sprintf("组件 %s 导入成功", title))
	return nil
}

func (m *Manager) resolveBaseToolSource(ctx context.Context, tool string) (urlStr, sha1Hex, title, destDir string, err error) {
	sdkRoot := m.managedSDKRoot()
	switch tool {
	case "adb", "platform-tools":
		title = "ADB (platform-tools)"
		destDir = filepath.Join(sdkRoot, "platform-tools")
		urlStr = downloader.FallbackPlatformToolsURL
		if pkgs, ferr := m.dl.FetchRepositoryPackages(ctx, downloader.GoogleRepositoryXMLURL); ferr == nil {
			for _, p := range pkgs {
				if p.PackagePath == "platform-tools" && p.URL != "" {
					urlStr = p.URL
					sha1Hex = p.SHA1
					break
				}
			}
		}
		return
	case "emulator":
		title = "Android Emulator"
		destDir = filepath.Join(sdkRoot, "emulator")
		pkgs, ferr := m.dl.FetchRepositoryPackages(ctx, downloader.GoogleRepositoryXMLURL)
		if ferr != nil {
			return "", "", "", "", fmt.Errorf("解析官方仓库获取 emulator 下载地址失败: %w（可检查下载代理或使用本地 .zip 导入）", ferr)
		}
		for _, p := range pkgs {
			if p.PackagePath == "emulator" && p.URL != "" {
				urlStr = p.URL
				sha1Hex = p.SHA1
				return
			}
		}
		return "", "", "", "", errors.New("未在官方仓库索引中找到 Windows x64 版 emulator")
	case "scrcpy":
		title = "scrcpy 投屏工具"
		destDir = filepath.Join(m.dataDir(), "scrcpy")
		urlStr = downloader.FallbackScrcpyURL
		return
	default:
		return "", "", "", "", fmt.Errorf("不支持的工具类型: %s", tool)
	}
}

func (m *Manager) onDownloadProgress(p DownloadProgress) {
	m.emit(Event{
		Type:      EventDownloadProgress,
		Message:   fmt.Sprintf("[%s] %s %.1f%%", p.Stage, p.Name, p.Percent),
		Timestamp: nowStamp(),
		Data:      p,
	})
}

func (m *Manager) emitDownloadFailed(id, name string, err error) {
	m.emitLog("", fmt.Sprintf("下载/安装 %s 失败: %v", name, err))
	m.emit(Event{
		Type:      EventDownloadProgress,
		Message:   fmt.Sprintf("下载 %s 失败: %v", name, err),
		Timestamp: nowStamp(),
		Data: DownloadProgress{
			TargetID: id,
			Name:     name,
			Stage:    "failed",
			Error:    err.Error(),
		},
	})
}

func (m *Manager) dataDir() string {
	if m.cfg.DataDir != "" {
		return m.cfg.DataDir
	}
	return emu.DefaultDataDir()
}

func (m *Manager) managedSDKRoot() string {
	if m.cfg.SDKRoot != "" {
		return m.cfg.SDKRoot
	}
	return filepath.Join(m.dataDir(), "sdk")
}

func (m *Manager) candidateSDKRoots() []string {
	var roots []string
	roots = append(roots, m.managedSDKRoot())
	st := m.emu.InspectEnv(context.Background())
	if st.SDKRoot != "" && st.SDKRoot != roots[0] {
		roots = append(roots, st.SDKRoot)
	}
	return roots
}

func (m *Manager) SetImageFavorite(ctx context.Context, id string, selected bool, note string) error {
	if m.cat == nil {
		return errors.New("镜像目录未初始化")
	}
	return m.cat.SetFavorite(id, selected, note)
}
