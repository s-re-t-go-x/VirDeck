package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// State represents the runtime lifecycle state of an AVD instance.
type State string

const (
	Stopped  State = "stopped"
	Starting State = "starting"
	Running  State = "running"
	Failed   State = "failed"
)

// Record describes a managed Android Virtual Device (AVD).
// Port is 0 when Stopped, and dynamically assigned from the even port pool (5554..5584) when Starting/Running.
type Record struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AVDName     string `json:"avdName"`
	SystemImage string `json:"systemImage"`
	MemoryMB    int    `json:"memoryMB"`
	Cores       int    `json:"cores"`
	Port        int    `json:"port"`
	Proxy       string `json:"proxy,omitempty"`
	// AppliedProxy is the proxy the running emulator process was launched with.
	// It is empty when the instance is stopped; compare with Proxy to detect a pending restart.
	AppliedProxy string `json:"appliedProxy,omitempty"`
	Headless     bool   `json:"headless"`
	State        State  `json:"state"`
	CreatedAt    string `json:"createdAt"`
	LastError    string `json:"lastError,omitempty"`
}

var ErrNotFound = errors.New("instance not found")

// Registry stores and persists instance metadata to a JSON file atomically.
type Registry struct {
	mu    sync.RWMutex
	path  string
	items []Record
}

// NewRegistry loads or initializes the registry at path.
func NewRegistry(path string) (*Registry, error) {
	r := &Registry{path: path}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			r.items = []Record{}
			return r.saveLocked()
		}
		return fmt.Errorf("read registry %s: %w", r.path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		r.items = nil
		return nil
	}
	var items []Record
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("parse registry %s: %w", r.path, err)
	}
	// Reset transient runtime fields on startup.
	dirty := false
	for i := range items {
		if items[i].AVDName == "" {
			items[i].AVDName = items[i].Name
			dirty = true
		}
		if items[i].State == Running || items[i].State == Starting {
			items[i].State = Stopped
			dirty = true
		}
		if items[i].AppliedProxy != "" {
			items[i].AppliedProxy = ""
			dirty = true
		}
		if items[i].Port != 0 {
			items[i].Port = 0
			dirty = true
		}
	}
	r.items = items
	if dirty {
		return r.saveLocked()
	}
	return nil
}

func (r *Registry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(r.items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp registry: %w", err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp registry: %w", err)
	}
	return nil
}

// List returns a copy of all registered instances.
func (r *Registry) List() []Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.items)
}

// Get returns an instance by ID.
func (r *Registry) Get(id string) (Record, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, it := range r.items {
		if it.ID == id {
			return it, true
		}
	}
	return Record{}, false
}

// HasName reports whether an instance with the given AVD name already exists.
func (r *Registry) HasName(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, it := range r.items {
		if strings.EqualFold(it.Name, name) {
			return true
		}
	}
	return false
}

// Add appends one or more instances and persists the registry.
func (r *Registry) Add(insts ...Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, insts...)
	return r.saveLocked()
}

// Update modifies a single instance in place and persists the registry.
func (r *Registry) Update(id string, fn func(*Record) error) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.items {
		if r.items[i].ID == id {
			previous := r.items[i]
			if err := fn(&r.items[i]); err != nil {
				r.items[i] = previous
				return Record{}, err
			}
			updated := r.items[i]
			if err := r.saveLocked(); err != nil {
				r.items[i] = previous
				return Record{}, err
			}
			return updated, nil
		}
	}
	return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Delete removes an instance by ID and persists the registry.
func (r *Registry) Delete(id string) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, it := range r.items {
		if it.ID == id {
			r.items = slices.Delete(r.items, i, i+1)
			if err := r.saveLocked(); err != nil {
				return Record{}, err
			}
			return it, nil
		}
	}
	return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// InternalName preserves legacy records while keeping display names out of emulator paths.
func (r Record) InternalName() string {
	if r.AVDName != "" {
		return r.AVDName
	}
	return r.Name
}
