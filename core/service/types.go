package service

import (
	"context"

	"virdeck/core/catalog"
	"virdeck/core/downloader"
	"virdeck/core/instance"
)

// State represents the runtime lifecycle state of an AVD instance.
type State = instance.State

const (
	Stopped  State = instance.Stopped
	Starting State = instance.Starting
	Running  State = instance.Running
	Failed   State = instance.Failed
)

// EventType constants for Event.Type.
const (
	EventStateChanged     = "state_changed"
	EventLog              = "log"
	EventDownloadProgress = "download_progress"
)

// Instance describes a managed Android Virtual Device (AVD).
// Port is 0 when Stopped, and dynamically assigned from the even port pool (5554..5584) when Starting/Running.
type Instance = instance.Record

// ImageItem describes a system image in the catalog.
type ImageItem = catalog.ImageItem

// DownloadProgress describes real-time download/verify/extract progress.
type DownloadProgress = downloader.Progress

// CreateSpec defines parameters for creating one or more clean AVD instances.
type CreateSpec struct {
	NamePrefix  string `json:"namePrefix"`
	Count       int    `json:"count"`
	SystemImage string `json:"systemImage"`
	MemoryMB    int    `json:"memoryMB"`
	Cores       int    `json:"cores"`
	Resolution  string `json:"resolution,omitempty"` // "720p" (default) or "1080p"
	Proxy       string `json:"proxy,omitempty"`
	Headless    bool   `json:"headless"`
}

// Settings holds persisted global settings in VirDeckData/config/settings.json.
type Settings struct {
	LoadWarning         string                        `json:"-"`
	Projection          ProjectionSettings            `json:"projection"`
	ProjectionOverrides map[string]ProjectionSettings `json:"projectionOverrides,omitempty"`
	ScreenLayout        ScreenLayoutPreferences       `json:"screenLayout"`
	MaxConcurrent       int                           `json:"maxConcurrent"`
	ADBServerPort       int                           `json:"adbServerPort"`
	DownloadProxy       string                        `json:"downloadProxy,omitempty"`
	LicenseAccepted     bool                          `json:"licenseAccepted"`
}

// EnvStatus reports discovered Android SDK/CLI tools, WHPX/AEHD hardware acceleration status,
// path health, and locally installed system images.
type EnvStatus struct {
	DataDir        string   `json:"dataDir"`
	SDKRoot        string   `json:"sdkRoot"`
	AVDHome        string   `json:"avdHome"`
	EmulatorPath   string   `json:"emulatorPath"`
	ADBPath        string   `json:"adbPath"`
	AVDManagerPath string   `json:"avdManagerPath"`
	SDKManagerPath string   `json:"sdkManagerPath"`
	ScrcpyPath     string   `json:"scrcpyPath"`
	AccelOK        bool     `json:"accelOK"`
	AccelMessage   string   `json:"accelMessage"`
	PathASCIIOK    bool     `json:"pathAsciiOK"`
	PathWritableOK bool     `json:"pathWritableOK"`
	PathWarning    string   `json:"pathWarning,omitempty"`
	SystemImages   []string `json:"systemImages"`
}

// Event is emitted on state transitions, download progress, or operational logs.
type Event struct {
	Type       string `json:"type"` // "state_changed" | "log" | "download_progress"
	InstanceID string `json:"instanceId,omitempty"`
	Message    string `json:"message,omitempty"`
	Timestamp  string `json:"timestamp"`
	Data       any    `json:"data,omitempty"`
}

// Service is the UI-agnostic core interface. All UI and CLI adapters interact exclusively through it.
type Service interface {
	ListGroups(context.Context) (GroupSnapshot, error)
	CreateGroup(context.Context, string) (Group, error)
	RenameGroup(context.Context, string, string) error
	DeleteGroup(context.Context, string) error
	AssignGroup(context.Context, []string, string) error
	EditInstance(ctx context.Context, id string, spec InstanceSettings) error
	RenameInstance(ctx context.Context, id, name string) error
	ScanCleanup(ctx context.Context) (CleanupReport, error)
	CleanCaches(ctx context.Context, opts CleanupOptions) (CleanupSize, error)
	CheckEnv(ctx context.Context) (EnvStatus, error)
	ListSystemImages(ctx context.Context) ([]string, error)
	ListCatalog(ctx context.Context) ([]ImageItem, error)
	SetImageFavorite(ctx context.Context, id string, selected bool, note string) error
	SyncCatalog(ctx context.Context) (int, error)
	SaveCustomImage(ctx context.Context, item ImageItem) error
	RemoveCustomImage(ctx context.Context, id string) error
	DeleteSystemImage(ctx context.Context, id string) error
	DownloadSystemImage(ctx context.Context, id string) error
	ImportSystemImageZip(ctx context.Context, id, zipPath string) error
	EnsureBaseTool(ctx context.Context, tool string) error
	ImportBaseToolZip(ctx context.Context, tool, zipPath string) error
	GetSettings() Settings
	UpdateSettings(s Settings) error
	List(ctx context.Context) ([]Instance, error)
	Create(ctx context.Context, spec CreateSpec) ([]Instance, error)
	Clone(ctx context.Context, srcID string, count int) ([]Instance, error)
	Start(ctx context.Context, ids ...string) error
	Stop(ctx context.Context, ids ...string) error
	Delete(ctx context.Context, ids ...string) error
	SetProxy(ctx context.Context, id, proxy string) error
	SetHeadless(ctx context.Context, id string, headless bool) error
	InstallAPK(ctx context.Context, ids []string, apkPath string) error
	Shell(ctx context.Context, ids []string, cmd string) (map[string]string, error)
	SendFiles(context.Context, SendFilesRequest) (map[string]string, error)
	ReceiveFiles(context.Context, string, string, string) (string, error)
	ListTransferFiles(context.Context, string, string) ([]TransferFile, error)
	OpenScreen(ctx context.Context, id string) error
	ReopenScreen(context.Context, string) error
	GetProjectionSettings(string) (ProjectionSettings, bool, error)
	SetProjectionSettings(context.Context, string, *ProjectionSettings) error
	InspectScreenLayout(ctx context.Context) (ScreenLayoutSnapshot, error)
	ArrangeScreenLayout(ctx context.Context, request ScreenLayoutRequest) (int, error)
	SaveScreenLayout(context.Context, ScreenLayoutRequest) (int, error)
	RestoreScreenLayout(context.Context, ScreenLayoutRequest) (int, error)
	SetScreenAutoFill(context.Context, bool) error
	Subscribe() (<-chan Event, func())
}
