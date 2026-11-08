package service

import (
	"context"
	"errors"
	"fmt"
	"virdeck/config"
	"virdeck/core/emu"
)

type ProjectionSettings = emu.ProjectionSettings

func copySettings(s Settings) Settings {
	if s.ProjectionOverrides != nil {
		p := make(map[string]ProjectionSettings, len(s.ProjectionOverrides))
		for id, v := range s.ProjectionOverrides {
			p[id] = v
		}
		s.ProjectionOverrides = p
	}
	return s
}
func (m *Manager) GetProjectionSettings(id string) (ProjectionSettings, bool, error) {
	if _, ok := m.registry.Get(id); !ok {
		return ProjectionSettings{}, false, errors.New("实例不存在")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.settings.ProjectionOverrides[id]; ok {
		return config.NormalizeProjection(p), true, nil
	}
	return config.NormalizeProjection(m.settings.Projection), false, nil
}
func (m *Manager) SetProjectionSettings(ctx context.Context, id string, p *ProjectionSettings) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := m.registry.Get(id); !ok {
		return errors.New("实例不存在")
	}
	if p != nil {
		if err := config.ValidateProjection(*p); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := copySettings(m.settings)
	if p == nil {
		delete(next.ProjectionOverrides, id)
	} else {
		if next.ProjectionOverrides == nil {
			next.ProjectionOverrides = map[string]ProjectionSettings{}
		}
		next.ProjectionOverrides[id] = *p
	}
	if err := saveSettings(m.settingsPath, next); err != nil {
		return err
	}
	m.settings = next
	return nil
}

func (m *Manager) ReopenScreen(ctx context.Context, id string) error {
	inst, ok := m.registry.Get(id)
	if !ok {
		return errors.New("实例不存在")
	}
	if inst.State != Running || inst.Port <= 0 {
		return errors.New("实例尚未启动完成")
	}
	p, _, err := m.GetProjectionSettings(id)
	if err != nil {
		return err
	}
	m.emitLog(id, "正在重新投屏以应用画面和声音设置，安卓实例保持运行")
	err = m.emu.RestartProjection(ctx, id, inst.Port, fmt.Sprintf("%s (emulator-%d)", inst.Name, inst.Port), p)
	if err != nil {
		m.emitLog(id, "重新投屏失败："+err.Error())
	}
	return err
}
