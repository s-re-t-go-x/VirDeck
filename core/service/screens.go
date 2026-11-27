package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
	"virdeck/config"
	"virdeck/core/emu"
	"virdeck/core/screenlayout"
)

type ScreenLayoutPreferences struct {
	DisplayID        string `json:"displayId,omitempty"`
	Size             string `json:"size"`
	ReferenceID      string `json:"referenceId,omitempty"`
	ClientHeight     int    `json:"clientHeight,omitempty"`
	Columns          int    `json:"columns,omitempty"`
	AutoFill         bool   `json:"autoFill"`
	RememberPosition bool   `json:"rememberPosition,omitempty"`
}
type ScreenWindowInfo struct{ InstanceID, Name, DisplayID string }

type NamedScreenLayout struct {
	Name      string                `json:"name"`
	GroupID   string                `json:"groupId,omitempty"`
	UpdatedAt string                `json:"updatedAt,omitempty"`
	Windows   []emu.SavedProjection `json:"windows"`
}

type ScreenLayoutPresetSummary struct {
	Name        string `json:"name"`
	GroupID     string `json:"groupId,omitempty"`
	WindowCount int    `json:"windowCount"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type ScreenLayoutSnapshot struct {
	Displays   []screenlayout.Display
	OpenIDs    []string
	Windows    []ScreenWindowInfo
	SavedCount int
	Presets    []ScreenLayoutPresetSummary
	Warning    string
}
type ScreenLayoutRequest struct {
	SelectedOnly                 bool
	IDs                          []string
	DisplayID, Size, ReferenceID string
	GroupID, PresetName          string
	Columns                      int
}

func validScreenSize(size string) bool {
	return size == "auto" || size == "small" || size == "medium" || size == "large" || size == "reference"
}
func (m *Manager) screenScopeIDs(req ScreenLayoutRequest) ([]string, error) {
	ids := []string{}
	known := map[string]bool{}
	allList := m.registry.List()
	for _, i := range allList {
		known[i.ID] = true
		if !req.SelectedOnly && strings.TrimSpace(req.GroupID) == "" {
			ids = append(ids, i.ID)
		}
	}
	if !req.SelectedOnly && strings.TrimSpace(req.GroupID) != "" {
		gid := strings.TrimSpace(req.GroupID)
		groups, err := m.ListGroups(context.Background())
		if err != nil {
			return nil, err
		}
		for _, i := range allList {
			if groups.Memberships[i.ID] == gid {
				ids = append(ids, i.ID)
			}
		}
		if len(ids) == 0 {
			return nil, errors.New("该分组内暂无实例")
		}
		return ids, nil
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
	if errors.Is(err, screenlayout.ErrUnsupported) {
		snap.Warning = err.Error()
	} else if err != nil {
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
	fileData, err := m.readScreenLayoutFile()
	if err != nil {
		snap.Warning = strings.TrimSpace(snap.Warning + "\n读取已保存布局失败：" + err.Error())
	} else {
		snap.SavedCount = len(fileData.Windows)
		for _, p := range fileData.Presets {
			snap.Presets = append(snap.Presets, ScreenLayoutPresetSummary{
				Name:        p.Name,
				GroupID:     p.GroupID,
				WindowCount: len(p.Windows),
				UpdatedAt:   p.UpdatedAt,
			})
		}
		sort.SliceStable(snap.Presets, func(i, j int) bool {
			return snap.Presets[i].Name < snap.Presets[j].Name
		})
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
	if req.Columns < 0 || req.Columns > 16 {
		return 0, errors.New("每行窗口数需在 0–16 之间")
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
	n, err := m.emu.ArrangeProjection(ctx, ids, emu.ScreenLayoutOptions{DisplayID: req.DisplayID, ReferenceID: ref, Height: height, Gap: config.Values.ScreenLayout.Gap, Columns: req.Columns})
	if n > 0 || err == nil {
		m.mu.Lock()
		next := m.settings
		next.ScreenLayout.DisplayID = req.DisplayID
		next.ScreenLayout.Size = req.Size
		next.ScreenLayout.ReferenceID = ref
		next.ScreenLayout.ClientHeight = remembered
		next.ScreenLayout.Columns = req.Columns
		saveErr := saveSettings(m.settingsPath, next)
		if saveErr == nil {
			m.settings = next
		}
		m.mu.Unlock()
		if saveErr != nil {
			err = errors.Join(err, fmt.Errorf("保存排版偏好: %w", saveErr))
		}
		if n > 0 && next.ScreenLayout.RememberPosition {
			m.captureLastWindowPositionsLocked(ids)
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
	Version       int                            `json:"version"`
	Windows       []emu.SavedProjection          `json:"windows,omitempty"`
	Presets       []NamedScreenLayout            `json:"presets,omitempty"`
	LastPositions map[string]emu.SavedProjection `json:"lastPositions,omitempty"`
}

func (m *Manager) readScreenLayoutFile() (savedScreenLayout, error) {
	raw, err := os.ReadFile(m.screenLayoutPath())
	if errors.Is(err, os.ErrNotExist) {
		return savedScreenLayout{Version: 1}, nil
	}
	if err != nil {
		return savedScreenLayout{}, err
	}
	var data savedScreenLayout
	if err := json.Unmarshal(raw, &data); err != nil {
		return savedScreenLayout{}, err
	}
	if data.Version != 1 {
		return savedScreenLayout{}, errors.New("不支持的布局配置版本")
	}
	return data, nil
}

func (m *Manager) writeScreenLayoutFile(data savedScreenLayout) error {
	if data.Version == 0 {
		data.Version = 1
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.screenLayoutPath(), raw, 0o700, 0o600)
}

func (m *Manager) readScreenLayout() ([]emu.SavedProjection, error) {
	data, err := m.readScreenLayoutFile()
	if err != nil {
		return nil, err
	}
	return data.Windows, nil
}

func (m *Manager) SaveScreenLayout(ctx context.Context, req ScreenLayoutRequest) (int, error) {
	m.screenLayoutMu.Lock()
	defer m.screenLayoutMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	presetName := strings.TrimSpace(req.PresetName)
	if presetName != "" && utf8.RuneCountInString(presetName) > 40 {
		return 0, errors.New("布局快照名称最多 40 个字符")
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
	data, err := m.readScreenLayoutFile()
	if err != nil {
		data = savedScreenLayout{Version: 1}
	}
	if presetName == "" {
		data.Windows = records
	} else {
		entry := NamedScreenLayout{
			Name:      presetName,
			GroupID:   strings.TrimSpace(req.GroupID),
			UpdatedAt: time.Now().Format(time.RFC3339),
			Windows:   records,
		}
		replaced := false
		for i := range data.Presets {
			if strings.EqualFold(data.Presets[i].Name, presetName) {
				data.Presets[i] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			data.Presets = append(data.Presets, entry)
		}
	}
	if data.LastPositions == nil {
		data.LastPositions = make(map[string]emu.SavedProjection)
	}
	for _, rec := range records {
		data.LastPositions[rec.InstanceID] = rec
	}
	if err := m.writeScreenLayoutFile(data); err != nil {
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
	data, err := m.readScreenLayoutFile()
	if err != nil {
		return 0, err
	}
	presetName := strings.TrimSpace(req.PresetName)
	var records []emu.SavedProjection
	if presetName == "" {
		records = data.Windows
		if len(records) == 0 {
			return 0, errors.New("尚未保存投屏布局")
		}
	} else {
		found := false
		for _, p := range data.Presets {
			if strings.EqualFold(p.Name, presetName) {
				records = p.Windows
				found = true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("未找到命名布局快照: %s", presetName)
		}
	}
	return m.emu.RestoreProjection(ctx, ids, records)
}

func (m *Manager) DeleteScreenLayoutPreset(ctx context.Context, name string) error {
	m.screenLayoutMu.Lock()
	defer m.screenLayoutMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("请指定要删除的布局快照名称")
	}
	data, err := m.readScreenLayoutFile()
	if err != nil {
		return err
	}
	filtered := make([]NamedScreenLayout, 0, len(data.Presets))
	removed := false
	for _, p := range data.Presets {
		if strings.EqualFold(p.Name, name) {
			removed = true
			continue
		}
		filtered = append(filtered, p)
	}
	if !removed {
		return fmt.Errorf("未找到命名布局快照: %s", name)
	}
	data.Presets = filtered
	return m.writeScreenLayoutFile(data)
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

func (m *Manager) SetScreenRememberPosition(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.settings
	next.ScreenLayout.RememberPosition = enabled
	if err := saveSettings(m.settingsPath, next); err != nil {
		return err
	}
	m.settings = next
	return nil
}

func (m *Manager) captureLastWindowPositions(ids []string) {
	m.screenLayoutMu.Lock()
	defer m.screenLayoutMu.Unlock()
	m.captureLastWindowPositionsLocked(ids)
}

func (m *Manager) captureLastWindowPositionsLocked(ids []string) {
	if len(ids) == 0 {
		return
	}
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	displays, err := m.emu.ScrcpyDisplays()
	if err != nil {
		return
	}
	open, err := m.emu.ScrcpyWindows()
	if err != nil {
		return
	}
	data, err := m.readScreenLayoutFile()
	if err != nil {
		data = savedScreenLayout{Version: 1}
	}
	if data.LastPositions == nil {
		data.LastPositions = make(map[string]emu.SavedProjection)
	}
	changed := false
	for _, p := range open {
		if !allowed[p.InstanceID] {
			continue
		}
		d := emu.ProjectionDisplay(p.Window, displays)
		if d == nil {
			continue
		}
		data.LastPositions[p.InstanceID] = emu.SavedProjection{
			InstanceID: p.InstanceID,
			DisplayID:  d.ID,
			Bounds:     p.Window.Bounds,
			WorkArea:   d.WorkArea,
			Scale:      d.Scale,
		}
		changed = true
	}
	if changed {
		_ = m.writeScreenLayoutFile(data)
	}
}

func (m *Manager) tryRestoreLastScreenPosition(ctx context.Context, id string) bool {
	if !m.GetSettings().ScreenLayout.RememberPosition {
		return false
	}
	m.screenLayoutMu.Lock()
	defer m.screenLayoutMu.Unlock()
	data, err := m.readScreenLayoutFile()
	if err != nil || len(data.LastPositions) == 0 {
		return false
	}
	saved, ok := data.LastPositions[id]
	if !ok {
		return false
	}
	n, rerr := m.emu.RestoreProjection(ctx, []string{id}, []emu.SavedProjection{saved})
	if rerr == nil && n > 0 {
		m.emitLog(id, "已自动恢复该实例上次投屏窗口位置")
		return true
	}
	return false
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
