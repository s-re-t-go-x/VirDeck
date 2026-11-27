package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"virdeck/core/catalog"
	"virdeck/core/emu"
)

func (m *Manager) avdAndSDKPaths(ctx context.Context) (avdHome, sdkRoot string) {
	if p, ok := m.emu.(interface {
		AVDHome() string
		SDKRoot() string
	}); ok {
		avdHome = strings.TrimSpace(p.AVDHome())
		sdkRoot = strings.TrimSpace(p.SDKRoot())
	}
	if avdHome == "" {
		avdHome = strings.TrimSpace(m.cfg.AVDHome)
	}
	if sdkRoot == "" {
		sdkRoot = strings.TrimSpace(m.cfg.SDKRoot)
	}
	if (avdHome == "" || sdkRoot == "") && m.emu != nil {
		info := m.emu.InspectEnv(ctx)
		if avdHome == "" {
			avdHome = strings.TrimSpace(info.AVDHome)
		}
		if sdkRoot == "" {
			sdkRoot = strings.TrimSpace(info.SDKRoot)
		}
	}
	if avdHome == "" && m.cfg.DataDir != "" {
		avdHome = filepath.Join(m.cfg.DataDir, "avd")
	}
	return avdHome, sdkRoot
}

// CleanInstanceDisk trims storage on a running instance or compacts qcow2 disks when stopped.
func (m *Manager) CleanInstanceDisk(ctx context.Context, id string) (DiskCleanResult, error) {
	inst, ok := m.registry.Get(id)
	if !ok {
		return DiskCleanResult{}, fmt.Errorf("实例不存在: %s", id)
	}
	if inst.State == Starting {
		return DiskCleanResult{}, errors.New("实例正在启动中，请稍后再执行磁盘清理")
	}
	if inst.State == Running && inst.Port > 0 {
		backend, ok := m.adb.(trimADB)
		if !ok {
			return DiskCleanResult{}, errors.New("当前 ADB 后端不支持在线磁盘整理")
		}
		if _, err := backend.TrimStorage(ctx, inst.Port); err != nil {
			return DiskCleanResult{}, err
		}
		res := DiskCleanResult{
			Mode:    "online_fstrim",
			Message: "已执行在线 fstrim 磁盘整理（停止实例后可进一步离线压缩 qcow2 镜像）",
		}
		m.emitLog(id, res.Message)
		return res, nil
	}
	if inst.Port > 0 {
		return DiskCleanResult{}, errors.New("实例仍在占住端口，请先彻底停止后再清理磁盘")
	}

	m.maintenance.RLock()
	defer m.maintenance.RUnlock()

	offlineInst, release, err := m.acquireOfflineOp(ctx, id, "磁盘清理")
	if err != nil {
		return DiskCleanResult{}, err
	}
	defer release()

	avdHome, sdkRoot := m.avdAndSDKPaths(ctx)
	before, after, usedQemu, err := emu.CompactAVDDisk(ctx, avdHome, sdkRoot, offlineInst.InternalName())
	if err != nil {
		return DiskCleanResult{}, err
	}
	reclaimed := before - after
	if reclaimed < 0 {
		reclaimed = 0
	}
	msg := fmt.Sprintf("离线磁盘清理完成：%.1f MB → %.1f MB（释放 %.1f MB）",
		float64(before)/(1024*1024), float64(after)/(1024*1024), float64(reclaimed)/(1024*1024))
	if !usedQemu {
		msg += "（未检测到 qemu-img，仅清理缓存镜像）"
	}
	res := DiskCleanResult{
		Mode:           "offline_compact",
		BeforeBytes:    before,
		AfterBytes:     after,
		ReclaimedBytes: reclaimed,
		Message:        msg,
	}
	m.emitLog(id, msg)
	return res, nil
}

// BackupInstance archives an offline instance directory and metadata into destZip.
// Network proxy credentials are excluded by default unless opted in via BackupInstanceWithOptions.
func (m *Manager) BackupInstance(ctx context.Context, id, destZip string) (BackupResult, error) {
	return m.BackupInstanceWithOptions(ctx, id, destZip, BackupOptions{})
}

// BackupInstanceWithOptions archives an offline instance directory and metadata into destZip.
func (m *Manager) BackupInstanceWithOptions(ctx context.Context, id, destZip string, opts BackupOptions) (BackupResult, error) {
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()

	inst, release, err := m.acquireOfflineOp(ctx, id, "备份")
	if err != nil {
		return BackupResult{}, err
	}
	defer release()

	outZip := strings.TrimSpace(destZip)
	if outZip == "" {
		outZip = filepath.Join(m.cfg.DataDir, "backups",
			fmt.Sprintf("%s_%s_%s.zip", sanitizeFileStem(inst.Name), inst.ID, time.Now().Format("20060102_150405")))
	}
	avdHome, _ := m.avdAndSDKPaths(ctx)
	proxy := ""
	if opts.IncludeProxy {
		proxy = inst.Proxy
	}
	meta := emu.AVDBackupMeta{
		Version:      1,
		InstanceName: inst.Name,
		SystemImage:  inst.SystemImage,
		MemoryMB:     inst.MemoryMB,
		Cores:        inst.Cores,
		Resolution:   inst.Resolution,
		StorageGB:    inst.StorageGB,
		QuickBoot:    inst.QuickBoot,
		Proxy:        proxy,
		GPUMode:      inst.GPUMode,
		Note:         inst.Note,
		Headless:     inst.Headless,
		CreatedAt:    time.Now().Format(time.RFC3339),
	}
	size, err := emu.BackupAVD(avdHome, inst.InternalName(), outZip, meta)
	if err != nil {
		return BackupResult{}, err
	}
	m.emitLog(id, fmt.Sprintf("已备份实例 %s → %s (%.1f MB)", inst.Name, outZip, float64(size)/(1024*1024)))
	return BackupResult{Path: outZip, SizeBytes: size}, nil
}

// RestoreInstance restores a VirDeck backup archive as a new stopped instance.
// Network proxy settings in the archive are ignored by default unless opted in via RestoreInstanceWithOptions.
func (m *Manager) RestoreInstance(ctx context.Context, srcZip, nameOverride string) (Instance, error) {
	return m.RestoreInstanceWithOptions(ctx, srcZip, nameOverride, RestoreOptions{})
}

// RestoreInstanceWithOptions restores a VirDeck backup archive as a new stopped instance.
func (m *Manager) RestoreInstanceWithOptions(ctx context.Context, srcZip, nameOverride string, opts RestoreOptions) (Instance, error) {
	srcZip = strings.TrimSpace(srcZip)
	if srcZip == "" {
		return Instance{}, errors.New("请指定要还原的备份文件 (.zip)")
	}
	nameOverride = strings.TrimSpace(nameOverride)
	if nameOverride != "" {
		if err := validateDisplayName(nameOverride); err != nil {
			return Instance{}, err
		}
	}

	m.namesMu.Lock()
	defer m.namesMu.Unlock()
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()

	if nameOverride != "" && m.registry.HasName(nameOverride) {
		return Instance{}, errors.New("该实例名称已存在")
	}

	avdHome, _ := m.avdAndSDKPaths(ctx)
	id := newShortID()
	avdName := "avd_" + id

	meta, err := emu.RestoreAVD(avdHome, avdName, srcZip)
	if err != nil {
		return Instance{}, err
	}

	if meta.SystemImage = strings.TrimSpace(meta.SystemImage); meta.SystemImage != "" {
		apiDir, tagDir, abiDir := catalog.SplitPackageID(meta.SystemImage)
		if apiDir == "" || tagDir == "" || abiDir == "" || !strings.HasPrefix(apiDir, "android-") {
			return Instance{}, fmt.Errorf("备份包包含非法的系统镜像标识: %q", meta.SystemImage)
		}
	}

	displayName := nameOverride
	if displayName == "" {
		base := sanitizeImportedDisplayName(meta.InstanceName)
		if base == "" {
			base = "还原实例"
		} else {
			base = base + "-还原"
		}
		if validateDisplayName(base) == nil && !m.registry.HasName(base) {
			displayName = base
		} else {
			stem := strings.TrimSuffix(base, "-还原")
			if stem == "" {
				stem = "还原实例"
			}
			displayName = m.nextAvailableName(stem+"-还原", 1)
			if validateDisplayName(displayName) != nil {
				displayName = m.nextAvailableName("还原实例", 1)
			}
		}
	}

	memMB := meta.MemoryMB
	if memMB <= 0 {
		memMB = DefaultMemoryMB
	} else if memMB < 1024 {
		memMB = 1024
	} else if memMB > 16384 {
		memMB = 16384
	}
	cores := meta.Cores
	if cores <= 0 {
		cores = DefaultCores
	} else if cores > 8 {
		cores = 8
	}
	storageGB := meta.StorageGB
	if storageGB <= 0 {
		storageGB = 6
	} else if storageGB < 4 {
		storageGB = 4
	} else if storageGB > 128 {
		storageGB = 128
	}
	res := strings.TrimSpace(meta.Resolution)
	if res == "" {
		res = configDefaultResolution()
	} else if _, rerr := emu.ParseResolution(res); rerr != nil {
		res = configDefaultResolution()
	}
	gpuMode, gerr := normalizeAndValidateGPUMode(meta.GPUMode)
	if gerr != nil {
		gpuMode = ""
	}
	note, nerr := normalizeAndValidateNote(sanitizeImportedDisplayName(meta.Note))
	if nerr != nil {
		note = ""
	}
	proxy := ""
	if opts.IncludeProxy {
		proxy = strings.TrimSpace(meta.Proxy)
	}
	if proxy != "" {
		u, uerr := url.Parse(proxy)
		scheme := ""
		if u != nil {
			scheme = strings.ToLower(u.Scheme)
		}
		if uerr != nil || u.Host == "" || strings.HasPrefix(proxy, "-") || (scheme != "http" && scheme != "https" && scheme != "socks5" && scheme != "socks5h") {
			proxy = ""
		}
	}
	inst := Instance{
		ID:          id,
		AVDName:     avdName,
		Name:        displayName,
		SystemImage: meta.SystemImage,
		MemoryMB:    memMB,
		Cores:       cores,
		Resolution:  res,
		StorageGB:   storageGB,
		QuickBoot:   meta.QuickBoot,
		Proxy:       proxy,
		GPUMode:     gpuMode,
		Note:        note,
		Port:        0,
		Headless:    meta.Headless,
		State:       Stopped,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	if err := m.registry.Add(inst); err != nil {
		return Instance{}, err
	}
	m.emitStateChanged(inst, "已从备份还原实例 "+inst.Name)
	return inst, nil
}

func sanitizeImportedDisplayName(s string) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func configDefaultResolution() string {
	return "720p"
}
