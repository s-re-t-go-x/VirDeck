package service

import (
	"context"
	"errors"
	"strings"
)

// InstanceSettings are editable launch preferences; identity and image stay fixed.
type InstanceSettings struct {
	Name     string
	MemoryMB int
	Cores    int
	Proxy    string
	Headless bool
}

func (m *Manager) EditInstance(ctx context.Context, id string, spec InstanceSettings) error {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Proxy = strings.TrimSpace(spec.Proxy)
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
		item.Name, item.MemoryMB, item.Cores, item.Proxy, item.Headless = spec.Name, spec.MemoryMB, spec.Cores, spec.Proxy, spec.Headless
		return nil
	})
	if err != nil {
		return err
	}
	msg := "实例 " + updated.Name + " 配置已保存"
	if updated.State == Running || updated.State == Starting || updated.Port > 0 {
		msg += "（名称立即生效，其余配置下次启动生效）"
	}
	m.emitStateChanged(updated, msg)
	return nil
}
