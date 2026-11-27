package service

import (
	"context"

	"virdeck/core/apk"
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
	EventPipelineStage    = "pipeline_stage"
	EventInputSyncChanged = "input_sync_changed"
)

// Instance describes a managed Android Virtual Device (AVD).
// Port is 0 when Stopped, and dynamically assigned from the even port pool (5554..5584) when Starting/Running.
type Instance = instance.Record

// RecentApp records an application recently installed or launched on an instance.
type RecentApp = instance.RecentApp

// APKMetadata describes offline package and ABI metadata inspected from an APK archive.
type APKMetadata = apk.Metadata

// PipelineStage reports live step progress during an InstallApp pipeline run.
type PipelineStage struct {
	InstanceID string `json:"instanceId"`
	StageText  string `json:"stageText"`
	Step       int    `json:"step"`
	TotalSteps int    `json:"totalSteps"`
	Percent    int    `json:"percent"`
}

// InstallAppRequest configures the unified start -> pm ready -> install -> resolve launcher -> launch -> open screen pipeline.
type InstallAppRequest struct {
	InstanceIDs      []string `json:"instanceIds"`
	APKPath          string   `json:"apkPath"`
	GrantPermissions bool     `json:"grantPermissions,omitempty"`
	LaunchApp        bool     `json:"launchApp,omitempty"`
	OpenScreen       bool     `json:"openScreen,omitempty"`
}

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
	Resolution  string `json:"resolution,omitempty"` // "720p", "1080p", "tablet", or custom "<W>x<H>@<DPI>"
	StorageGB   int    `json:"storageGB,omitempty"`
	QuickBoot   bool   `json:"quickBoot,omitempty"`
	Proxy       string `json:"proxy,omitempty"`
	GPUMode     string `json:"gpuMode,omitempty"`
	Note        string `json:"note,omitempty"`
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
	AutoDeleteDownloads bool                          `json:"autoDeleteDownloads,omitempty"`
	LicenseAccepted     bool                          `json:"licenseAccepted"`
}

// EnvStatus reports discovered Android SDK/CLI tools, WHPX/AEHD hardware acceleration status,
// path health, and locally installed system images.
type EnvStatus struct {
	DataDir          string   `json:"dataDir"`
	EffectiveDataDir string   `json:"effectiveDataDir,omitempty"`
	PathCompatMode   string   `json:"pathCompatMode,omitempty"`
	SDKRoot          string   `json:"sdkRoot"`
	AVDHome          string   `json:"avdHome"`
	EmulatorPath     string   `json:"emulatorPath"`
	ADBPath          string   `json:"adbPath"`
	AVDManagerPath   string   `json:"avdManagerPath"`
	SDKManagerPath   string   `json:"sdkManagerPath"`
	ScrcpyPath       string   `json:"scrcpyPath"`
	AccelOK          bool     `json:"accelOK"`
	AccelMessage     string   `json:"accelMessage"`
	PathASCIIOK      bool     `json:"pathAsciiOK"`
	PathWritableOK   bool     `json:"pathWritableOK"`
	PathWarning      string   `json:"pathWarning,omitempty"`
	SystemImages     []string `json:"systemImages"`
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
	SuggestPureASCIIDataDir() string
	SetDataDirOverride(ctx context.Context, newDir string) (string, error)
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
	Start(ctx context.Context, ids ...string) error
	Stop(ctx context.Context, ids ...string) error
	WaitInstanceReady(ctx context.Context, id string) (Instance, error)
	ClearInstanceSnapshot(ctx context.Context, id string) error
	Delete(ctx context.Context, ids ...string) error
	SetProxy(ctx context.Context, id, proxy string) error
	SetHeadless(ctx context.Context, id string, headless bool) error
	InspectAPK(apkPath string) (APKMetadata, error)
	InstallApp(ctx context.Context, req InstallAppRequest) error
	LaunchApp(ctx context.Context, instanceID, pkg, component string) error
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
	DeleteScreenLayoutPreset(context.Context, string) error
	SetScreenAutoFill(context.Context, bool) error
	SetScreenRememberPosition(context.Context, bool) error
	GetInputSyncStatus() InputSyncStatus
	StartInputSync(ctx context.Context, cfg InputSyncConfig) (InputSyncStatus, error)
	StopInputSync(ctx context.Context) error
	SetInputSyncPaused(paused bool) (InputSyncStatus, error)
	SetInputSyncSlaveEnabled(instanceID string, enabled bool) (InputSyncStatus, error)
	BroadcastSyncKey(ctx context.Context, key string) error
	BroadcastSyncText(ctx context.Context, text string) error
	SetLocation(ctx context.Context, ids []string, lat, lon float64) error
	CaptureScreenshots(ctx context.Context, ids []string, outDir string) (map[string]string, error)
	ListInstalledApps(ctx context.Context, id string, thirdPartyOnly bool) ([]InstalledApp, error)
	ManageInstalledApp(ctx context.Context, id, pkg, op string) error
	CleanInstanceDisk(ctx context.Context, id string) (DiskCleanResult, error)
	BackupInstance(ctx context.Context, id, destZip string) (BackupResult, error)
	BackupInstanceWithOptions(ctx context.Context, id, destZip string, opts BackupOptions) (BackupResult, error)
	RestoreInstance(ctx context.Context, srcZip, nameOverride string) (Instance, error)
	RestoreInstanceWithOptions(ctx context.Context, srcZip, nameOverride string, opts RestoreOptions) (Instance, error)
	Subscribe() (<-chan Event, func())
}

// InstalledApp describes a package discovered on an instance.
type InstalledApp struct {
	PackageName string `json:"packageName"`
	ThirdParty  bool   `json:"thirdParty"`
}

// DiskCleanResult summarizes online fstrim or offline qcow2 compaction results.
type DiskCleanResult struct {
	Mode           string `json:"mode"` // "online_fstrim" | "offline_compact"
	BeforeBytes    int64  `json:"beforeBytes"`
	AfterBytes     int64  `json:"afterBytes"`
	ReclaimedBytes int64  `json:"reclaimedBytes"`
	Message        string `json:"message"`
}

// BackupOptions configures optional behaviors for BackupInstanceWithOptions.
type BackupOptions struct {
	IncludeProxy bool `json:"includeProxy,omitempty"`
}

// RestoreOptions configures optional behaviors for RestoreInstanceWithOptions.
type RestoreOptions struct {
	IncludeProxy bool `json:"includeProxy,omitempty"`
}

// BackupResult describes a written instance backup archive.
type BackupResult struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
}

// InputSyncConfig configures multi-device mouse and control synchronization.
type InputSyncConfig struct {
	MasterID   string   `json:"masterId"`
	SlaveIDs   []string `json:"slaveIds"`
	JitterPx   int      `json:"jitterPx,omitempty"`
	MaxDelayMS int      `json:"maxDelayMs,omitempty"`
}

// InputSyncSlaveStatus describes the connection and enable state of one synchronized target.
type InputSyncSlaveStatus struct {
	InstanceID string `json:"instanceId"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Connected  bool   `json:"connected"`
	Error      string `json:"error,omitempty"`
}

// InputSyncStatus describes the live state of multi-device input synchronization.
type InputSyncStatus struct {
	Active           bool                   `json:"active"`
	Paused           bool                   `json:"paused"`
	CaptureSupported bool                   `json:"captureSupported"`
	CaptureHint      string                 `json:"captureHint,omitempty"`
	MasterID         string                 `json:"masterId,omitempty"`
	MasterName       string                 `json:"masterName,omitempty"`
	JitterPx         int                    `json:"jitterPx,omitempty"`
	MaxDelayMS       int                    `json:"maxDelayMs,omitempty"`
	Slaves           []InputSyncSlaveStatus `json:"slaves,omitempty"`
}
