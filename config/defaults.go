// Package config keeps shipped defaults separate from implementation and user settings.
package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"runtime"
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
	ProjectionAudioCodec  string `json:"projectionAudioCodec"`
	CommandTimeoutSeconds int    `json:"commandTimeoutSeconds"`
	InstallTimeoutSeconds int    `json:"installTimeoutSeconds"`
	EnvTimeoutSeconds     int    `json:"envTimeoutSeconds"`
	DownloadIdleSeconds   int    `json:"downloadIdleSeconds"`
	MinPort               int    `json:"minPort"`
	MaxPort               int    `json:"maxPort"`
	MaxConcurrent         int    `json:"maxConcurrent"`
	ADBServerPort         int    `json:"adbServerPort"`
	StopGraceSeconds      int    `json:"stopGraceSeconds"`
	BootTimeoutSeconds    int    `json:"bootTimeoutSeconds"`
}
type Sources struct {
	RepositoryXML string `json:"repositoryXML"`
	PlayStoreXML  string `json:"playStoreXML"`
	GoogleAPIsXML string `json:"googleAPIsXML"`
	DefaultXML    string `json:"defaultXML"`
	PlatformTools string `json:"platformTools"`
	Scrcpy        string `json:"scrcpy"`
}

type ToolSources struct {
	PlatformTools string `json:"platformTools"`
	Scrcpy        string `json:"scrcpy"`
}
type ImagePreset struct {
	// FavoriteVersion identifies the release that first seeded this default favorite.
	FavoriteVersion int    `json:"favoriteVersion,omitempty"`
	SHA1            string `json:"sha1,omitempty"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	APILevel        string `json:"apiLevel"`
	TagID           string `json:"tagId"`
	ABI             string `json:"abi"`
	URL             string `json:"url"`
	Favorite        bool   `json:"favorite"`
	Note            string `json:"note"`
}
type ScreenLayoutDefaults struct {
	AutoFill             bool   `json:"autoFill"`
	Size                 string `json:"size"`
	Gap                  int    `json:"gap"`
	SmallHeight          int    `json:"smallHeight"`
	MediumHeight         int    `json:"mediumHeight"`
	LargeHeight          int    `json:"largeHeight"`
	WindowTimeoutSeconds int    `json:"windowTimeoutSeconds"`
}

type ProjectionPreset struct {
	Name     string             `json:"name"`
	Hint     string             `json:"hint"`
	Settings ProjectionDefaults `json:"settings"`
}

type ProjectionDefaults struct {
	MaxFPS      int  `json:"maxFPS"`
	MaxSize     int  `json:"maxSize"`
	BitRateMbps int  `json:"bitRateMbps"`
	Audio       bool `json:"audio"`
}

const (
	MinProjectionFPS  = 5
	MinProjectionSize = 320
)

// NormalizeProjection upgrades the old zero-as-unlimited fields to compiled defaults.
// Other saved preferences, including bitrate and audio, remain unchanged.
func NormalizeProjection(p ProjectionDefaults) ProjectionDefaults {
	if p.MaxFPS == 0 {
		p.MaxFPS = Values.Projection.MaxFPS
	}
	if p.MaxSize == 0 {
		p.MaxSize = Values.Projection.MaxSize
	}
	if p.BitRateMbps == 0 {
		p.BitRateMbps = Values.Projection.BitRateMbps
	}
	return p
}

func ValidateProjection(p ProjectionDefaults) error {
	if p.MaxFPS < MinProjectionFPS || p.MaxFPS > 240 {
		return errors.New("投屏帧率需为 5–240 FPS")
	}
	if p.MaxSize < MinProjectionSize || p.MaxSize > 8192 {
		return errors.New("投屏最长边需为 320–8192 像素")
	}
	if p.BitRateMbps < 1 || p.BitRateMbps > 100 {
		return errors.New("投屏码率需为 1–100 Mbps")
	}
	return nil
}

type Defaults struct {
	HostSources       map[string]ToolSources `json:"hostSources"`
	ARMImages         []ImagePreset          `json:"armImages"`
	ProjectionPresets []ProjectionPreset     `json:"projectionPresets"`
	Projection        ProjectionDefaults     `json:"projection"`
	ScreenLayout      ScreenLayoutDefaults   `json:"screenLayout"`
	CloneCount        int                    `json:"cloneCount"`
	Create            CreateDefaults         `json:"create"`
	Runtime           RuntimeDefaults        `json:"runtime"`
	Sources           Sources                `json:"sources"`
	Images            []ImagePreset          `json:"images"`
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
	if r.ProjectionAudioCodec != "aac" && r.ProjectionAudioCodec != "opus" {
		return errors.New("默认音频编码无效")
	}
	if r.CommandTimeoutSeconds < 1 || r.InstallTimeoutSeconds < 1 || r.EnvTimeoutSeconds < 1 || r.DownloadIdleSeconds < 1 {
		return errors.New("默认等待超时配置无效")
	}
	if r.MinPort < 1024 || r.MaxPort > 65534 || r.MinPort > r.MaxPort || r.MinPort%2 != 0 || r.MaxPort%2 != 0 || r.MaxConcurrent < 1 || r.ADBServerPort < 1 || r.ADBServerPort > 65535 || r.StopGraceSeconds < 1 || r.BootTimeoutSeconds < 1 {
		return errors.New("运行环境默认配置无效")
	}
	if err := ValidateProjection(d.Projection); err != nil {
		return err
	}
	for _, p := range d.ProjectionPresets {
		if p.Name == "" {
			return errors.New("投屏预设名称不能为空")
		}
		if err := ValidateProjection(p.Settings); err != nil {
			return err
		}
	}
	layout := d.ScreenLayout
	if layout.Size != "auto" && layout.Size != "small" && layout.Size != "medium" && layout.Size != "large" {
		return errors.New("默认投屏尺寸无效")
	}
	if layout.Gap < 0 || layout.Gap > 100 || layout.SmallHeight < 1 || layout.MediumHeight < layout.SmallHeight || layout.LargeHeight < layout.MediumHeight || layout.WindowTimeoutSeconds < 1 || layout.WindowTimeoutSeconds > 60 {
		return errors.New("默认投屏排版参数无效")
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
	return ForHost(runtime.GOOS, runtime.GOARCH)
}

// ForHost decodes an independent copy of the shipped defaults for a host.
func ForHost(goos, goarch string) (Defaults, error) {
	var d Defaults
	if err := decode(builtin, &d); err != nil {
		return d, err
	}
	if tools, ok := d.HostSources[goos+"/"+goarch]; ok {
		d.Sources.PlatformTools, d.Sources.Scrcpy = tools.PlatformTools, tools.Scrcpy
	} else {
		d.Sources.PlatformTools, d.Sources.Scrcpy = "", ""
	}
	if goarch == "arm64" {
		d.Images = append([]ImagePreset(nil), d.ARMImages...)
		if len(d.Images) > 0 {
			d.Create.Image = d.Images[0].ID
		}
	} else if goos != "windows" {
		var images []ImagePreset
		for _, p := range d.Images {
			if p.ABI == "x86_64" {
				images = append(images, p)
			}
		}
		d.Images = images
	}
	return d, nil
}
