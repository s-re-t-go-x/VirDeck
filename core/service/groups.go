package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type GroupSnapshot struct {
	Groups      []Group           `json:"groups"`
	Memberships map[string]string `json:"memberships"`
}

func (m *Manager) groupPath() string {
	if m.settingsPath != "" {
		return filepath.Join(filepath.Dir(m.settingsPath), "groups.json")
	}
	return filepath.Join(filepath.Dir(m.cfg.RegistryPath), "groups.json")
}
func (m *Manager) readGroups() (GroupSnapshot, error) {
	data := GroupSnapshot{Groups: []Group{}, Memberships: map[string]string{}}
	raw, err := os.ReadFile(m.groupPath())
	if errors.Is(err, os.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return data, err
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return data, err
	}
	if data.Memberships == nil {
		data.Memberships = map[string]string{}
	}
	knownGroups := map[string]bool{}
	for _, g := range data.Groups {
		knownGroups[g.ID] = true
	}
	for id, g := range data.Memberships {
		_, exists := m.registry.Get(id)
		if !exists || !knownGroups[g] {
			delete(data.Memberships, id)
		}
	}
	return data, nil
}
func (m *Manager) writeGroups(data GroupSnapshot) error {
	path := m.groupPath()
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", raw, 0644); err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		_ = os.Remove(path + ".tmp")
		return err
	}
	return nil
}
func (m *Manager) ListGroups(ctx context.Context) (GroupSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return GroupSnapshot{}, err
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	return m.readGroups()
}
func groupName(data GroupSnapshot, id, name string) (string, error) {
	name = strings.TrimSpace(name)
	if err := validateDisplayName(name); err != nil {
		return "", err
	}
	if utf8.RuneCountInString(name) > 32 {
		return "", errors.New("分组名称最多 32 个字符")
	}
	for _, g := range data.Groups {
		if g.ID != id && strings.EqualFold(g.Name, name) {
			return "", errors.New("分组名称已存在")
		}
	}
	if name == "全部分组" || name == "未分组" || name == "新建分组" {
		return "", errors.New("请使用其他分组名称")
	}
	return name, nil
}
func (m *Manager) CreateGroup(ctx context.Context, name string) (Group, error) {
	if err := ctx.Err(); err != nil {
		return Group{}, err
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	data, err := m.readGroups()
	if err != nil {
		return Group{}, err
	}
	name, err = groupName(data, "", name)
	if err != nil {
		return Group{}, err
	}
	g := Group{ID: newShortID(), Name: name}
	data.Groups = append(data.Groups, g)
	if err := m.writeGroups(data); err != nil {
		return Group{}, err
	}
	return g, nil
}
func (m *Manager) RenameGroup(ctx context.Context, id, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	data, err := m.readGroups()
	if err != nil {
		return err
	}
	name, err = groupName(data, id, name)
	if err != nil {
		return err
	}
	for i, g := range data.Groups {
		if g.ID == id {
			data.Groups[i].Name = name
			return m.writeGroups(data)
		}
	}
	return errors.New("分组不存在")
}
func (m *Manager) DeleteGroup(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	data, err := m.readGroups()
	if err != nil {
		return err
	}
	found := false
	var groups []Group
	for _, g := range data.Groups {
		if g.ID == id {
			found = true
		} else {
			groups = append(groups, g)
		}
	}
	if !found {
		return errors.New("分组不存在")
	}
	data.Groups = groups
	for inst, g := range data.Memberships {
		if g == id {
			delete(data.Memberships, inst)
		}
	}
	return m.writeGroups(data)
}
func (m *Manager) AssignGroup(ctx context.Context, ids []string, groupID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return errors.New("请先选择实例")
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	data, err := m.readGroups()
	if err != nil {
		return err
	}
	if groupID != "" {
		found := false
		for _, g := range data.Groups {
			if g.ID == groupID {
				found = true
				break
			}
		}
		if !found {
			return errors.New("分组不存在")
		}
	}
	for _, id := range ids {
		if _, ok := m.registry.Get(id); !ok {
			return fmt.Errorf("实例不存在: %s", id)
		}
	}
	for _, id := range ids {
		if groupID == "" {
			delete(data.Memberships, id)
		} else {
			data.Memberships[id] = groupID
		}
	}
	return m.writeGroups(data)
}
