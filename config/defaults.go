// Package config keeps shipped defaults separate from implementation and user settings.
package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
)

//go:embed defaults.json
var builtin []byte

type CreateDefaults struct {
	Name       string `json:"name"`
	Count      int    `json:"count"`
	MemoryMB   int    `json:"memoryMB"`
	Cores      int    `json:"cores"`
	Resolution string `json:"resolution"`
	Headless   bool   `json:"headless"`
	Image      string `json:"image"`
}
type RuntimeDefaults struct {
	MinPort            int `json:"minPort"`
	MaxPort            int `json:"maxPort"`
	MaxConcurrent      int `json:"maxConcurrent"`
	ADBServerPort      int `json:"adbServerPort"`
	StopGraceSeconds   int `json:"stopGraceSeconds"`
	BootTimeoutSeconds int `json:"bootTimeoutSeconds"`
}
type Sources struct {
	RepositoryXML string `json:"repositoryXML"`
	PlayStoreXML  string `json:"playStoreXML"`
	GoogleAPIsXML string `json:"googleAPIsXML"`
	DefaultXML    string `json:"defaultXML"`
	PlatformTools string `json:"platformTools"`
	Scrcpy        string `json:"scrcpy"`
}
type ImagePreset struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	APILevel string `json:"apiLevel"`
	TagID    string `json:"tagId"`
	ABI      string `json:"abi"`
	URL      string `json:"url"`
	Favorite bool   `json:"favorite"`
	Note     string `json:"note"`
}
type Defaults struct {
	CloneCount int             `json:"cloneCount"`
	Create     CreateDefaults  `json:"create"`
	Runtime    RuntimeDefaults `json:"runtime"`
	Sources    Sources         `json:"sources"`
	Images     []ImagePreset   `json:"images"`
}

func decode(raw []byte, d *Defaults) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(d); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("配置包含多余内容")
	}
	if d.Create.Count < 1 || d.Create.Count > 32 || d.Create.MemoryMB < 1024 || d.Create.MemoryMB > 16384 || d.Create.Cores < 1 || d.Create.Cores > 8 || d.Create.Name == "" || (d.Create.Resolution != "720p" && d.Create.Resolution != "1080p") || d.Create.Image == "" {
		return errors.New("新建实例默认配置无效")
	}
	r := d.Runtime
	if r.MinPort < 1024 || r.MaxPort > 65534 || r.MinPort > r.MaxPort || r.MinPort%2 != 0 || r.MaxPort%2 != 0 || r.MaxConcurrent < 1 || r.ADBServerPort < 1 || r.ADBServerPort > 65535 || r.StopGraceSeconds < 1 || r.BootTimeoutSeconds < 1 {
		return errors.New("运行环境默认配置无效")
	}
	if d.CloneCount < 1 || d.CloneCount > 32 {
		return errors.New("默认克隆数量无效")
	}
	if len(d.Images) == 0 {
		return errors.New("默认镜像列表不能为空")
	}
	return nil
}

// Values is decoded only from the data embedded at compile time. End users do
// not need defaults.json; their writable settings live under VirDeckData/config.
var Values, LoadError = initial()

func initial() (Defaults, error) {
	var d Defaults
	err := decode(builtin, &d)
	return d, err
}
