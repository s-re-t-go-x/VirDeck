package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"virdeck/core/emu"
)

// InstanceSettings are editable launch preferences; identity and image stay fixed.
type InstanceSettings struct {
	Name       string
	MemoryMB   int
	Cores      int
	Resolution string
	Proxy      string
	GPUMode    string
	Note       string
	Headless   bool
	QuickBoot  bool
}

func normalizeAndValidateGPUMode(mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	switch mode {
	case "", "auto", "host", "angle_indirect", "swiftshader_indirect", "swiftshader":
		if mode == "auto" {
			return "", nil
		}
		return mode, nil
	default:
		return "", errors.New("不支持的 GPU 渲染模式")
	}
}

func normalizeAndValidateNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if strings.ContainsAny(note, "\r\n") {
		note = strings.ReplaceAll(strings.ReplaceAll(note, "\r", " "), "\n", " ")
	}
	if utf8.RuneCountInString(note) > 80 {
		return "", errors.New("实例备注最多 80 个字符")
	}
	return note, nil
}

func (m *Manager) EditInstance(ctx context.Context, id string, spec InstanceSettings) error {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Proxy = strings.TrimSpace(spec.Proxy)
	spec.Resolution = strings.TrimSpace(spec.Resolution)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDisplayName(spec.Name); err != nil {
		return err
	}
	if spec.MemoryMB < 1024 || spec.MemoryMB > 16384 {
		return errors.New("内存需在 1024–16384 MB 之间")
	}
	if spec.Cores < 1 || spec.Cores > 8 {
		return errors.New("CPU 核数需在 1–8 之间")
	}
	if spec.Resolution != "" {
		if _, err := emu.ParseResolution(spec.Resolution); err != nil {
			return err
		}
	}
	gpuMode, err := normalizeAndValidateGPUMode(spec.GPUMode)
	if err != nil {
		return err
	}
	note, err := normalizeAndValidateNote(spec.Note)
	if err != nil {
		return err
	}
	m.namesMu.Lock()
	defer m.namesMu.Unlock()
	m.maintenance.RLock()
	defer m.maintenance.RUnlock()
	for _, item := range m.registry.List() {
		if item.ID != id && strings.EqualFold(item.Name, spec.Name) {
			return errors.New("该实例名称已存在")
		}
	}
	updated, err := m.registry.Update(id, func(item *Instance) error {
		if item.AVDName == "" {
			item.AVDName = item.Name
		}
		launchChanged := item.MemoryMB != spec.MemoryMB ||
			item.Cores != spec.Cores ||
			strings.TrimSpace(item.Proxy) != spec.Proxy ||
			item.Headless != spec.Headless ||
			strings.TrimSpace(item.GPUMode) != gpuMode ||
			(spec.Resolution != "" && strings.TrimSpace(item.Resolution) != spec.Resolution) ||
			item.QuickBoot != spec.QuickBoot
		item.Name, item.MemoryMB, item.Cores, item.Proxy, item.Headless, item.QuickBoot = spec.Name, spec.MemoryMB, spec.Cores, spec.Proxy, spec.Headless, spec.QuickBoot
		item.GPUMode = gpuMode
		item.Note = note
		if spec.Resolution != "" {
			item.Resolution = spec.Resolution
		}
		if item.QuickBoot && launchChanged {
			item.ForceColdNext = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if avdCfg, ok := m.emu.(interface {
		UpdateAVDConfig(avdName string, memoryMB, cores int, resolution string, quickBoot bool) error
	}); ok {
		if cerr := avdCfg.UpdateAVDConfig(updated.InternalName(), updated.MemoryMB, updated.Cores, updated.Resolution, updated.QuickBoot); cerr != nil && !errors.Is(cerr, os.ErrNotExist) {
			return cerr
		}
	}
	msg := "实例 " + updated.Name + " 配置已保存"
	if updated.State == Running || updated.State == Starting || updated.Port > 0 {
		msg += "（名称立即生效，其余配置下次启动生效）"
	}
	m.emitStateChanged(updated, msg)
	return nil
}
