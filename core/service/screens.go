package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"virdeck/config"
	"virdeck/core/emu"
	"virdeck/core/screenlayout"
)

type ScreenLayoutPreferences struct {
	DisplayID    string `json:"displayId,omitempty"`
	Size         string `json:"size"`
	ReferenceID  string `json:"referenceId,omitempty"`
	ClientHeight int    `json:"clientHeight,omitempty"`
	AutoFill     bool   `json:"autoFill"`
}
type ScreenWindowInfo struct{ InstanceID, Name, DisplayID string }
type ScreenLayoutSnapshot struct {
	Displays   []screenlayout.Display
	OpenIDs    []string
	Windows    []ScreenWindowInfo
	SavedCount int
	Warning    string
}
type ScreenLayoutRequest struct {
	SelectedOnly                 bool
	IDs                          []string
	DisplayID, Size, ReferenceID string
}

func validScreenSize(size string) bool {
	return size == "auto" || size == "small" || size == "medium" || size == "large" || size == "reference"
}
func (m *Manager) screenScopeIDs(req ScreenLayoutRequest) ([]string, error) {
	ids := []string{}
	known := map[string]bool{}
	for _, i := range m.registry.List() {
		known[i.ID] = true
		if !req.SelectedOnly {
			ids = append(ids, i.ID)
		}
	}
	if req.SelectedOnly {
		seen := map[string]bool{}
		for _, id := range req.IDs {
			if known[id] && !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
		if len(ids) == 0 {
			return nil, errors.New("请先选择需要排列的实例")
		}
	}
	return ids, nil
}
func (m *Manager) InspectScreenLayout(ctx context.Context) (ScreenLayoutSnapshot, error) {
	var snap ScreenLayoutSnapshot
	if err := ctx.Err(); err != nil {
		return snap, err
	}
	var err error
	snap.Displays, err = m.emu.ScrcpyDisplays()
	if err != nil {
		return snap, err
	}
	open, err := m.emu.ScrcpyWindows()
	if err != nil {
		return snap, err
	}
	known := map[string]string{}
	for _, i := range m.registry.List() {
		known[i.ID] = i.Name
	}
	seen := map[string]bool{}
	for _, p := range open {
		if name, ok := known[p.InstanceID]; ok && !seen[p.InstanceID] {
			info := ScreenWindowInfo{InstanceID: p.InstanceID, Name: name}
			if d := emu.ProjectionDisplay(p.Window, snap.Displays); d != nil {
				info.DisplayID = d.ID
			}
			snap.Windows = append(snap.Windows, info)
			snap.OpenIDs = append(snap.OpenIDs, p.InstanceID)
			seen[p.InstanceID] = true
		}
	}
	records, err := m.readScreenLayout()
	if err != nil {
		snap.Warning = "读取已保存布局失败：" + err.Error()
	} else {
		snap.SavedCount = len(records)
	}
	return snap, nil
}
func layoutHeight(size string) int {
	d := config.Values.ScreenLayout
	switch size {
	case "small":
		return d.SmallHeight
	case "medium":
		return d.MediumHeight
	case "large":
		return d.LargeHeight
	}
	return 0
}
func (m *Manager) ArrangeScreenLayout(ctx context.Context, req ScreenLayoutRequest) (int, error) {
	m.screenLayoutMu.Lock()
	defer m.screenLayoutMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !validScreenSize(req.Size) {
		return 0, errors.New("无效的投屏窗口大小")
	}
	if req.Size == "reference" && req.ReferenceID == "" {
		return 0, errors.New("请选择基准投屏窗口")
	}
	ids, err := m.screenScopeIDs(req)
	if err != nil {
		return 0, err
	}
	ref := ""
	if req.Size == "reference" {
		ref = req.ReferenceID
	}
	height := layoutHeight(req.Size)
	// Persist a usable height for future new-window fills even if the reference closes.
	remembered := height
	if height == 0 {
		open, _ := m.emu.ScrcpyWindows()
		byID := map[string]emu.ProjectionWindow{}
		for _, p := range open {
			if _, ok := byID[p.InstanceID]; !ok {
				byID[p.InstanceID] = p
			}
		}
		ordered := ids
		if ref != "" {
			ordered = []string{ref}
		}
		for _, id := range ordered {
			p, ok := byID[id]
			if !ok {
				continue
			}
			scale := p.Window.Scale
			if scale <= 0 {
				scale = 1
			}
			remembered = int(math.Round(float64(p.Window.ClientHeight) / scale))
			break
		}
	}
	n, err := m.emu.ArrangeProjection(ctx, ids, emu.ScreenLayoutOptions{DisplayID: req.DisplayID, ReferenceID: ref, Height: height, Gap: config.Values.ScreenLayout.Gap})
	if n > 0 || err == nil {
		m.mu.Lock()
		next := m.settings
		next.ScreenLayout.DisplayID = req.DisplayID
		next.ScreenLayout.Size = req.Size
		next.ScreenLayout.ReferenceID = ref
		next.ScreenLayout.ClientHeight = remembered
		saveErr := saveSettings(m.settingsPath, next)
		if saveErr == nil {
			m.settings = next
		}
		m.mu.Unlock()
		if saveErr != nil {
			err = errors.Join(err, fmt.Errorf("保存排版偏好: %w", saveErr))
		}
	}
	return n, err
}
func (m *Manager) screenLayoutPath() string {
	if m.settingsPath != "" {
		return filepath.Join(filepath.Dir(m.settingsPath), "screen_layout.json")
	}
	return filepath.Join(filepath.Dir(m.cfg.RegistryPath), "screen_layout.json")
}

type savedScreenLayout struct {
	Version int                   `json:"version"`
	Windows []emu.SavedProjection `json:"windows"`
}

func (m *Manager) readScreenLayout() ([]emu.SavedProjection, error) {
	raw, err := os.ReadFile(m.screenLayoutPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var data savedScreenLayout
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	if data.Version != 1 {
		return nil, errors.New("不支持的布局配置版本")
	}
	return data.Windows, nil
}
func (m *Manager) SaveScreenLayout(ctx context.Context, req ScreenLayoutRequest) (int, error) {
	m.screenLayoutMu.Lock()
	defer m.screenLayoutMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	ids, err := m.screenScopeIDs(req)
	if err != nil {
		return 0, err
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	displays, err := m.emu.ScrcpyDisplays()
	if err != nil {
		return 0, err
	}
	open, err := m.emu.ScrcpyWindows()
	if err != nil {
		return 0, err
	}
	var records []emu.SavedProjection
	seen := map[string]bool{}
	for _, p := range open {
		if !allowed[p.InstanceID] || seen[p.InstanceID] {
			continue
		}
		d := emu.ProjectionDisplay(p.Window, displays)
		if d == nil {
			continue
		}
		records = append(records, emu.SavedProjection{InstanceID: p.InstanceID, DisplayID: d.ID, Bounds: p.Window.Bounds, WorkArea: d.WorkArea, Scale: d.Scale})
		seen[p.InstanceID] = true
	}
	if len(records) == 0 {
		return 0, errors.New("当前范围没有可保存的已打开投屏")
	}
	raw, err := json.MarshalIndent(savedScreenLayout{Version: 1, Windows: records}, "", "  ")
	if err != nil {
		return 0, err
	}
	path := m.screenLayoutPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path+".tmp", raw, 0644); err != nil {
		return 0, err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		_ = os.Remove(path + ".tmp")
		return 0, err
	}
	return len(records), nil
}
func (m *Manager) RestoreScreenLayout(ctx context.Context, req ScreenLayoutRequest) (int, error) {
	m.screenLayoutMu.Lock()
	defer m.screenLayoutMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	ids, err := m.screenScopeIDs(req)
	if err != nil {
		return 0, err
	}
	records, err := m.readScreenLayout()
	if err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, errors.New("尚未保存投屏布局")
	}
	return m.emu.RestoreProjection(ctx, ids, records)
}
func (m *Manager) SetScreenAutoFill(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.settings
	next.ScreenLayout.AutoFill = enabled
	if err := saveSettings(m.settingsPath, next); err != nil {
		return err
	}
	m.settings = next
	return nil
}
func (m *Manager) fillNewScreen(ctx context.Context, id string) error {
	m.screenLayoutMu.Lock()
	defer m.screenLayoutMu.Unlock()
	pref := m.GetSettings().ScreenLayout
	if !pref.AutoFill || pref.ClientHeight <= 0 {
		return nil
	}
	ref := pref.ReferenceID
	if ref == id {
		ref = ""
	}
	_, err := m.emu.ArrangeProjection(ctx, []string{id}, emu.ScreenLayoutOptions{DisplayID: pref.DisplayID, ReferenceID: ref, Height: pref.ClientHeight, Gap: config.Values.ScreenLayout.Gap, FillOnly: true})
	if err != nil {
		m.emitLog(id, "投屏已打开，自动补位："+err.Error())
		return fmt.Errorf("投屏已打开，自动补位：%w", err)
	}
	m.emitLog(id, "新投屏已自动补位，已有窗口保持原位")
	return nil
}
