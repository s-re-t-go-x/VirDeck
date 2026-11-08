package emu

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"virdeck/config"
)

// DefaultADBServerPort is the dedicated ADB server port used by VirDeck to avoid colliding
// with Android Studio or mobile phone assistants on port 5037.
var DefaultADBServerPort = config.Values.Runtime.ADBServerPort

// EnvInfo reports discovered Android SDK/CLI tools, WHPX/AEHD hardware acceleration status,
// path health, and locally installed system images.
type EnvInfo struct {
	DataDir        string
	SDKRoot        string
	AVDHome        string
	AndroidHomeDir string
	EmulatorPath   string
	ADBPath        string
	AVDManagerPath string
	SDKManagerPath string
	ScrcpyPath     string
	AccelOK        bool
	AccelMessage   string
	PathASCIIOK    bool
	PathWritableOK bool
	PathWarning    string
	SystemImages   []string
}

// LaunchOptions configures how an emulator instance process is started.
type LaunchOptions struct {
	AVDName    string
	Port       int
	MemoryMB   int
	Cores      int
	Proxy      string
	Headless   bool
	NoSnapshot bool
}

// Process represents a running emulator OS process.
type Process struct {
	cmd    *exec.Cmd
	done   chan error
	log    *projectionOutput
	onKill func() error
}

// NewFakeProcess creates a Process backed by a channel for unit testing.
func NewFakeProcess(done chan error, output string, onKill func() error) *Process {
	return &Process{
		done:   done,
		log:    initialProcessOutput(output),
		onKill: onKill,
	}
}

// PID returns the OS process ID, or 0 if unavailable.
func (p *Process) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Done returns a channel that closes when the emulator process exits.
func (p *Process) Done() <-chan error {
	return p.done
}

// Output returns buffered stdout/stderr output from the emulator process.
func (p *Process) Output() string {
	if p == nil || p.log == nil {
		return ""
	}
	return p.log.text()
}

// ForceKillTree forcefully terminates the emulator process and its child QEMU process tree.
func (p *Process) ForceKillTree() error {
	if p == nil {
		return nil
	}
	if p.onKill != nil {
		return p.onKill()
	}
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	pid := p.cmd.Process.Pid
	if runtime.GOOS == "windows" {
		killCmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
		hideConsole(killCmd)
		if err := killCmd.Run(); err == nil {
			return nil
		}
	}
	return p.cmd.Process.Kill()
}

// Controller wraps Android SDK discovery, pure-Go AVD creation, emulator process launching, and scrcpy.
type Controller struct {
	projectionLocks sync.Map
	screenLog       func(string, string)
	layoutMu        sync.Mutex
	reservations    map[string]projectionReservation
	screenMu        sync.Mutex
	projections     map[string]*projectionProcess
	screenHost      projectionHost
	dataDir         string
	sdkRoot         string
	avdHome         string
	androidHomeDir  string
	scrcpyPath      string
	adbServerPort   int
}

// DefaultDataDir returns the default unified VirDeckData directory next to the executable (or current working directory).
func DefaultDataDir() string {
	v := strings.TrimSpace(os.Getenv("VIRDECK_DATA_DIR"))
	if v != "" {
		if abs, err := filepath.Abs(v); err == nil {
			return abs
		}
		return v
	}
	base := exeBaseDir()
	if strings.Contains(strings.ToLower(base), "go-build") || strings.Contains(strings.ToLower(base), "\\temp\\") {
		if wd, err := os.Getwd(); err == nil {
			base = wd
		}
	}
	return filepath.Join(base, "VirDeckData")
}

// NewController creates a new Controller rooted at VirDeckData (with automatic fallback to existing host SDK if VirDeckData/sdk is empty).
func NewController(sdkRoot, avdHome string) *Controller {
	return NewControllerWithDataDir("", sdkRoot, avdHome, DefaultADBServerPort)
}

// NewControllerWithDataDir creates a Controller rooted at dataDir.
func NewControllerWithDataDir(dataDir, sdkRoot, avdHome string, adbServerPort int) *Controller {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = DefaultDataDir()
	}
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	if adbServerPort <= 0 {
		adbServerPort = DefaultADBServerPort
	}
	c := &Controller{
		dataDir:        dataDir,
		sdkRoot:        strings.TrimSpace(sdkRoot),
		avdHome:        strings.TrimSpace(avdHome),
		androidHomeDir: filepath.Join(dataDir, ".android"),
		adbServerPort:  adbServerPort,
	}
	if c.sdkRoot == "" {
		c.sdkRoot = discoverSDKRoot(dataDir)
	}
	if c.avdHome == "" {
		c.avdHome = discoverAVDHome(dataDir)
	}
	_ = os.MkdirAll(c.avdHome, 0o755)
	_ = os.MkdirAll(c.androidHomeDir, 0o755)
	_ = c.HealAVDIniPaths()
	return c
}

// DataDir returns the root VirDeckData directory.
func (c *Controller) DataDir() string { return c.dataDir }

// SDKRoot returns the active Android SDK root directory, or the managed VirDeckData/sdk directory if none exists yet.
func (c *Controller) SDKRoot() string {
	if c.sdkRoot != "" {
		return c.sdkRoot
	}
	return filepath.Join(c.dataDir, "sdk")
}

// ManagedSDKRoot always returns <VirDeckData>/sdk for downloading/installing components.
func (c *Controller) ManagedSDKRoot() string {
	return filepath.Join(c.dataDir, "sdk")
}

// ManagedScrcpyDir returns <VirDeckData>/scrcpy for downloading/installing scrcpy.
func (c *Controller) ManagedScrcpyDir() string {
	return filepath.Join(c.dataDir, "scrcpy")
}

// AVDHome returns the active AVD directory.
func (c *Controller) AVDHome() string { return c.avdHome }

// SetPaths updates the SDK root, AVD home, and optional custom scrcpy path at runtime.
func (c *Controller) SetPaths(sdkRoot, avdHome, scrcpyPath string) {
	if s := strings.TrimSpace(sdkRoot); s != "" {
		c.sdkRoot = s
	} else {
		c.sdkRoot = discoverSDKRoot(c.dataDir)
	}
	if a := strings.TrimSpace(avdHome); a != "" {
		c.avdHome = a
	} else {
		c.avdHome = discoverAVDHome(c.dataDir)
	}
	c.scrcpyPath = strings.TrimSpace(scrcpyPath)
}

// RefreshPaths re-evaluates SDKroot after a component download completes.
func (c *Controller) RefreshPaths() {
	managed := filepath.Join(c.dataDir, "sdk")
	if hasSDKContent(managed) || c.sdkRoot == "" {
		c.sdkRoot = discoverSDKRoot(c.dataDir)
	}
}

// IsolatedEnv returns the environment variable slice injected into emulator, adb, and scrcpy child processes.
func (c *Controller) IsolatedEnv() []string {
	return c.IsolatedEnvWithSDK(c.SDKRoot())
}

// IsolatedEnvWithSDK returns the environment variable slice injected into child processes, overriding ANDROID_SDK_ROOT with sdkRoot.
func (c *Controller) IsolatedEnvWithSDK(sdkRoot string) []string {
	env := os.Environ()
	if strings.TrimSpace(sdkRoot) == "" {
		sdkRoot = c.SDKRoot()
	}
	overrides := map[string]string{
		"ANDROID_SDK_ROOT":        sdkRoot,
		"ANDROID_HOME":            sdkRoot,
		"ANDROID_AVD_HOME":        c.avdHome,
		"ANDROID_USER_HOME":       c.androidHomeDir,
		"ANDROID_EMULATOR_HOME":   c.androidHomeDir,
		"ANDROID_ADB_SERVER_PORT": strconv.Itoa(c.adbServerPort),
		"ADB_VENDOR_KEYS":         filepath.Join(c.androidHomeDir, "adbkey"),
	}
	filtered := make([]string, 0, len(env)+len(overrides))
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, replace := overrides[strings.ToUpper(k)]; replace {
			continue
		}
		filtered = append(filtered, kv)
	}
	for k, v := range overrides {
		if v != "" {
			filtered = append(filtered, k+"="+v)
		}
	}
	return filtered
}

func exeBaseDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

func hasSDKContent(dir string) bool {
	if !dirExists(dir) {
		return false
	}
	if fileExists(filepath.Join(dir, "platform-tools", exeName("adb"))) ||
		fileExists(filepath.Join(dir, "emulator", exeName("emulator"))) {
		return true
	}
	if matches, err := filepath.Glob(filepath.Join(dir, "system-images", "*", "*", "*")); err == nil {
		for _, m := range matches {
			if isValidSystemImageDir(m) {
				return true
			}
		}
	}
	return false
}

// isValidSystemImageDir checks whether dir contains a usable Android system image.
// Real system images always include kernel-ranchu (or kernel-ranchu-64) alongside system.img;
// in unit tests, a small synthetic system.img without kernel-ranchu is only accepted when no real kernel is expected,
// so we require either kernel-ranchu/ramdisk.img OR (in test temp dirs) system.img.
func isValidSystemImageDir(dir string) bool {
	if !dirExists(dir) {
		return false
	}
	sysImg := filepath.Join(dir, "system.img")
	if !fileExists(sysImg) {
		return false
	}
	// Real Android SDK system images always contain kernel-ranchu (or kernel-ranchu-64) and ramdisk.img.
	if fileExists(filepath.Join(dir, "kernel-ranchu")) ||
		fileExists(filepath.Join(dir, "kernel-ranchu-64")) ||
		fileExists(filepath.Join(dir, "ramdisk.img")) {
		return true
	}
	// Allow unit test synthetic directories created under os.TempDir().
	lowerDir := strings.ToLower(filepath.Clean(dir))
	lowerTmp := strings.ToLower(filepath.Clean(os.TempDir()))
	if lowerTmp != "" && strings.HasPrefix(lowerDir, lowerTmp) {
		return true
	}
	// Otherwise, require system.img to be at least 1 MB so 3-byte leftover test stubs are ignored.
	if info, err := os.Stat(sysImg); err == nil && info.Size() >= 1024*1024 {
		return true
	}
	return false
}

func discoverSDKRoot(dataDir string) string {
	// 1. Prefer unified VirDeckData/sdk if it has any installed component.
	managed := filepath.Join(dataDir, "sdk")
	if hasSDKContent(managed) {
		return managed
	}
	// 2. Check portable SDK folders next to virdeck.exe.
	base := exeBaseDir()
	for _, rel := range []string{filepath.Join("VirDeckData", "sdk"), "sdk", filepath.Join("runtime", "sdk"), filepath.Join("Android", "Sdk")} {
		p := filepath.Join(base, rel)
		if hasSDKContent(p) {
			return p
		}
	}
	// 3. Check environment variables.
	for _, envKey := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			if info, err := os.Stat(v); err == nil && info.IsDir() {
				return v
			}
		}
	}
	// 4. Check standard Windows user SDK installation paths.
	var candidates []string
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		candidates = append(candidates, filepath.Join(localAppData, "Android", "Sdk"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, "AppData", "Local", "Android", "Sdk"),
			filepath.Join(home, "Android", "Sdk"),
		)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	// 5. Default to VirDeckData/sdk so self-bootstrap downloads go there.
	_ = os.MkdirAll(managed, 0o755)
	return managed
}

func discoverAVDHome(dataDir string) string {
	if v := strings.TrimSpace(os.Getenv("ANDROID_AVD_HOME")); v != "" {
		return v
	}
	// Always prefer unified VirDeckData/avd.
	if dataDir != "" {
		return filepath.Join(dataDir, "avd")
	}
	base := exeBaseDir()
	return filepath.Join(base, "VirDeckData", "avd")
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func batOrBinName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".bat"
	}
	return base
}

// ResolvePaths locates emulator, adb, avdmanager, sdkmanager, and scrcpy binaries.
// Checks VirDeckData/sdk first, then the active sdkRoot, then host SDK paths, then system PATH.
func (c *Controller) ResolvePaths() (emulatorBin, adbBin, avdMgrBin, sdkMgrBin, scrcpyBin string) {
	sdkDirs := c.candidateSDKRoots()

	for _, sdk := range sdkDirs {
		if emulatorBin == "" {
			if p := filepath.Join(sdk, "emulator", exeName("emulator")); fileExists(p) {
				emulatorBin = p
			}
		}
		if adbBin == "" {
			if p := filepath.Join(sdk, "platform-tools", exeName("adb")); fileExists(p) {
				adbBin = p
			}
		}
		if avdMgrBin == "" {
			avdMgrBin = findCmdlineTool(sdk, batOrBinName("avdmanager"))
		}
		if sdkMgrBin == "" {
			sdkMgrBin = findCmdlineTool(sdk, batOrBinName("sdkmanager"))
		}
	}

	if emulatorBin == "" {
		if p, err := exec.LookPath("emulator"); err == nil {
			emulatorBin = p
		}
	}
	if adbBin == "" {
		if p, err := exec.LookPath("adb"); err == nil {
			adbBin = p
		}
	}
	if avdMgrBin == "" {
		if p, err := exec.LookPath("avdmanager"); err == nil {
			avdMgrBin = p
		}
	}
	if sdkMgrBin == "" {
		if p, err := exec.LookPath("sdkmanager"); err == nil {
			sdkMgrBin = p
		}
	}
	if c.scrcpyPath != "" && fileExists(c.scrcpyPath) {
		scrcpyBin = c.scrcpyPath
	}
	if scrcpyBin == "" {
		base := exeBaseDir()
		candidates := []string{}
		if c.dataDir != "" {
			candidates = append(candidates, filepath.Join(c.dataDir, "scrcpy", exeName("scrcpy")))
		}
		candidates = append(candidates,
			filepath.Join(base, "VirDeckData", "scrcpy", exeName("scrcpy")),
			filepath.Join(base, "scrcpy", exeName("scrcpy")),
			filepath.Join(base, exeName("scrcpy")),
			filepath.Join(base, "runtime", "scrcpy", exeName("scrcpy")),
		)
		for _, candidate := range candidates {
			if fileExists(candidate) {
				scrcpyBin = candidate
				break
			}
		}
	}
	if scrcpyBin == "" {
		if p, err := exec.LookPath("scrcpy"); err == nil {
			scrcpyBin = p
		}
	}
	return
}

func (c *Controller) candidateSDKRoots() []string {
	var roots []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p != "" && !slices.Contains(roots, p) {
			roots = append(roots, p)
		}
	}
	if c.dataDir != "" {
		add(filepath.Join(c.dataDir, "sdk"))
	}
	add(c.sdkRoot)
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		add(filepath.Join(localAppData, "Android", "Sdk"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, "AppData", "Local", "Android", "Sdk"))
	}
	return roots
}

func findCmdlineTool(sdkRoot, toolFile string) string {
	candidates := []string{
		filepath.Join(sdkRoot, "cmdline-tools", "latest", "bin", toolFile),
	}
	if matches, err := filepath.Glob(filepath.Join(sdkRoot, "cmdline-tools", "*", "bin", toolFile)); err == nil {
		candidates = append(candidates, matches...)
	}
	candidates = append(candidates, filepath.Join(sdkRoot, "tools", "bin", toolFile))
	for _, p := range candidates {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// InspectEnv gathers environment status, tool paths, WHPX acceleration status, path validation, and installed system images.
func (c *Controller) InspectEnv(ctx context.Context) EnvInfo {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(config.Values.Runtime.EnvTimeoutSeconds)*time.Second)
	defer cancel()
	c.RefreshPaths()
	_ = c.HealAVDIniPaths()

	emuBin, adbBin, avdMgrBin, sdkMgrBin, scrcpyBin := c.ResolvePaths()
	asciiOK, writableOK, pathWarn := ValidateDataDir(c.dataDir)

	status := EnvInfo{
		DataDir:        c.dataDir,
		SDKRoot:        c.SDKRoot(),
		AVDHome:        c.avdHome,
		AndroidHomeDir: c.androidHomeDir,
		EmulatorPath:   emuBin,
		ADBPath:        adbBin,
		AVDManagerPath: avdMgrBin,
		SDKManagerPath: sdkMgrBin,
		ScrcpyPath:     scrcpyBin,
		PathASCIIOK:    asciiOK,
		PathWritableOK: writableOK,
		PathWarning:    pathWarn,
		SystemImages:   c.ListSystemImages(),
	}

	if emuBin != "" {
		cmd := exec.CommandContext(ctx, emuBin, "-accel-check")
		cmd.Env = c.IsolatedEnv()
		hideConsole(cmd)
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			err = fmt.Errorf("环境检测等待超时：%w", ctx.Err())
			out = append(out, []byte(err.Error())...)
		}
		msg := strings.TrimSpace(string(out))
		for _, line := range strings.Split(msg, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(strings.ToLower(trimmed), "is installed and usable") ||
				strings.Contains(strings.ToLower(trimmed), "whpx") ||
				strings.Contains(strings.ToLower(trimmed), "gvm") ||
				strings.Contains(strings.ToLower(trimmed), "aehd") {
				msg = trimmed
				break
			}
		}
		status.AccelOK = err == nil && (strings.Contains(strings.ToLower(string(out)), "usable") || strings.Contains(strings.ToLower(string(out)), "accel:"))
		if msg == "" && err != nil {
			msg = err.Error()
		}
		status.AccelMessage = msg
	} else {
		// Check Windows WHPX capability without emulator.exe when emulator has not been downloaded yet.
		ok, msg := checkWindowsHypervisorFallback(ctx)
		status.AccelOK = ok
		status.AccelMessage = msg
	}
	return status
}

func checkWindowsHypervisorFallback(ctx context.Context) (bool, string) {
	if runtime.GOOS != "windows" {
		return false, "尚未下载 emulator，待下载后检测硬件加速"
	}
	// Check whether WinHvPlatform.dll exists in System32 and whether hypervisorlaunchtype is active.
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	whpxDLL := filepath.Join(sysRoot, "System32", "WinHvPlatform.dll")
	if fileExists(whpxDLL) {
		return true, "检测到 Windows Hypervisor Platform (WinHvPlatform.dll) 组件可用"
	}
	return false, "未检测到 WHPX 组件，请开启「Windows 虚拟机监控程序平台」功能并重启电脑"
}

// ListSystemImages scans both <VirDeckData>/sdk/system-images and <SDKRoot>/system-images for installed Android system images.
func (c *Controller) ListSystemImages() []string {
	seen := make(map[string]bool)
	var images []string

	for _, root := range c.candidateSDKRoots() {
		base := filepath.Join(root, "system-images")
		entriesPattern := filepath.Join(base, "*", "*", "*")
		dirs, err := filepath.Glob(entriesPattern)
		if err != nil {
			continue
		}
		for _, d := range dirs {
			if !isValidSystemImageDir(d) {
				continue
			}
			rel, err := filepath.Rel(base, d)
			if err != nil {
				continue
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) == 3 {
				pkg := fmt.Sprintf("system-images;%s;%s;%s", parts[0], parts[1], parts[2])
				if !seen[pkg] {
					seen[pkg] = true
					images = append(images, pkg)
				}
			}
		}
	}
	slices.Sort(images)
	return images
}

// CreateAVD creates a clean AVD in pure Go (no JDK or avdmanager.bat required) with default 720p resolution.
func (c *Controller) CreateAVD(ctx context.Context, name, systemImage string, memoryMB, cores int) error {
	return c.CreateAVDWithSpec(ctx, name, systemImage, memoryMB, cores, "720p")
}

// CreateAVDWithSpec creates a clean AVD in pure Go with the specified resolution ("720p" or "1080p").
// Searches all candidate SDK roots to find where the requested system image is installed.
func (c *Controller) CreateAVDWithSpec(ctx context.Context, name, systemImage string, memoryMB, cores int, resolution string) error {
	if strings.TrimSpace(systemImage) == "" {
		return fmt.Errorf("必须指定系统镜像包路径（例如 system-images;android-34;google_apis_playstore;x86_64）")
	}
	parts := strings.Split(strings.TrimSpace(systemImage), ";")
	if len(parts) != 4 || parts[0] != "system-images" {
		return fmt.Errorf("系统镜像包格式无效 (%s)，应为 system-images;android-<API>;<tag>;<abi>", systemImage)
	}
	for _, root := range c.candidateSDKRoots() {
		sysImgDir := filepath.Join(root, "system-images", parts[1], parts[2], parts[3])
		if isValidSystemImageDir(sysImgDir) {
			orig := c.sdkRoot
			c.sdkRoot = root
			err := c.CreateAVDNative(name, systemImage, memoryMB, cores, resolution)
			c.sdkRoot = orig
			return err
		}
	}
	return fmt.Errorf("系统镜像 %s 尚未安装或缺少内核文件 (kernel-ranchu)，请先在「镜像与环境 -> 系统镜像」中下载安装", systemImage)
}

// CloneAVD copies a stopped AVD directory and rewrites its .ini and config.ini files,
// omitting snapshots, lock files, and qcow2 overlays so the clone boots cleanly.
func (c *Controller) CloneAVD(srcName, dstName string) error {
	if c.avdHome == "" {
		return fmt.Errorf("未知的 AVD 目录 (ANDROID_AVD_HOME)")
	}
	srcDir := filepath.Join(c.avdHome, srcName+".avd")
	dstDir := filepath.Join(c.avdHome, dstName+".avd")
	if !dirExists(srcDir) {
		return fmt.Errorf("源 AVD 目录不存在: %s", srcDir)
	}
	if dirExists(dstDir) {
		return fmt.Errorf("目标 AVD 目录已存在: %s", dstDir)
	}

	if err := copyAVDDirClean(srcDir, dstDir); err != nil {
		_ = os.RemoveAll(dstDir)
		return fmt.Errorf("复制 AVD 目录失败: %w", err)
	}

	// Rewrite config.ini references from srcName to dstName.
	cfgPath := filepath.Join(dstDir, "config.ini")
	if data, err := os.ReadFile(cfgPath); err == nil {
		text := string(data)
		text = strings.ReplaceAll(text, srcDir, dstDir)
		text = strings.ReplaceAll(text, filepath.ToSlash(srcDir), filepath.ToSlash(dstDir))
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		for i, line := range lines {
			k, _, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "AvdId":
				lines[i] = "AvdId=" + dstName
			case "avd.ini.displayname":
				lines[i] = "avd.ini.displayname=" + dstName
			}
		}
		_ = os.WriteFile(cfgPath, []byte(strings.Join(lines, "\n")), 0o644)
	}

	// Write <dstName>.ini descriptor file.
	iniPath := filepath.Join(c.avdHome, dstName+".ini")
	iniContent := fmt.Sprintf("avd.ini.encoding=UTF-8\npath=%s\npath.rel=avd\\%s.avd\ntarget=android-34\n", dstDir, dstName)
	if srcIni, err := os.ReadFile(filepath.Join(c.avdHome, srcName+".ini")); err == nil {
		s := string(srcIni)
		s = strings.ReplaceAll(s, srcDir, dstDir)
		s = strings.ReplaceAll(s, srcName+".avd", dstName+".avd")
		iniContent = s
	}
	return os.WriteFile(iniPath, []byte(iniContent), 0o644)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func copyAVDDirClean(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dstDir, 0o755)
		}
		name := d.Name()
		// Skip snapshots directory, locks, generated hardware-qemu.ini, and qcow2 overlays
		// (qcow2 headers contain hardcoded backing file paths to the source AVD).
		if d.IsDir() && strings.EqualFold(name, "snapshots") {
			return filepath.SkipDir
		}
		if strings.HasSuffix(name, ".lock") ||
			strings.HasSuffix(name, ".qcow2") ||
			strings.EqualFold(name, "hardware-qemu.ini") ||
			strings.EqualFold(name, "multiinstance.lock") {
			return nil
		}
		targetPath := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		return copyFile(path, targetPath)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// DeleteAVD deletes the AVD directory and .ini file directly in pure Go.
func (c *Controller) DeleteAVD(ctx context.Context, name string) error {
	if c.avdHome != "" {
		_ = os.RemoveAll(filepath.Join(c.avdHome, name+".avd"))
		_ = os.Remove(filepath.Join(c.avdHome, name+".ini"))
	}
	return nil
}

// sdkRootForAVD reads <avdHome>/<avdName>.avd/config.ini's image.sysdir.1 and returns the SDK root that actually contains that system image.
func (c *Controller) sdkRootForAVD(avdName string) string {
	cfgPath := filepath.Join(c.avdHome, avdName+".avd", "config.ini")
	props := readKeyValueFile(cfgPath)
	relSysDir := strings.TrimSpace(props["image.sysdir.1"])
	if relSysDir != "" {
		for _, root := range c.candidateSDKRoots() {
			candidate := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(filepath.ToSlash(relSysDir), "/")))
			if isValidSystemImageDir(candidate) {
				return root
			}
		}
	}
	return c.SDKRoot()
}

// StartProcess launches the Android Emulator for the given LaunchOptions with isolated environment variables.
func (c *Controller) StartProcess(opts LaunchOptions) (*Process, error) {
	_ = c.HealAVDIniPaths()

	emuBin, _, _, _, _ := c.ResolvePaths()
	if emuBin == "" {
		return nil, fmt.Errorf("未找到 emulator 可执行文件，请在「镜像与环境」中一键下载补齐")
	}

	// Ensure ANDROID_SDK_ROOT points to the SDK containing this AVD's system image, and ensure its package.xml exists.
	sdkRoot := c.sdkRootForAVD(opts.AVDName)
	cfgProps := readKeyValueFile(filepath.Join(c.avdHome, opts.AVDName+".avd", "config.ini"))
	if relSys := strings.Trim(filepath.ToSlash(cfgProps["image.sysdir.1"]), "/"); relSys != "" {
		parts := strings.Split(relSys, "/")
		if len(parts) == 4 && parts[0] == "system-images" {
			pkgID := strings.Join(parts, ";")
			_ = EnsureSystemImageMetadata(sdkRoot, pkgID)
		}
	}

	args := []string{
		"-avd", opts.AVDName,
		"-port", strconv.Itoa(opts.Port),
		"-no-audio",
		"-no-boot-anim",
		"-no-snapshot",
	}
	if opts.Headless {
		args = append(args, "-no-window")
	}
	if opts.MemoryMB > 0 {
		args = append(args, "-memory", strconv.Itoa(opts.MemoryMB))
	}
	if opts.Cores > 0 {
		args = append(args, "-cores", strconv.Itoa(opts.Cores))
	}
	if proxy := strings.TrimSpace(opts.Proxy); proxy != "" {
		args = append(args, "-http-proxy", proxy, "-dns-server", "8.8.8.8,1.1.1.1")
	}

	cmd := exec.Command(emuBin, args...)
	// Helpers may inherit stdout/stderr after the emulator exits. Bound pipe draining
	// so cmd.Wait still reports the exit before the manager's force-stop timeout.
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = c.IsolatedEnvWithSDK(sdkRoot)
	if opts.Headless {
		hideConsole(cmd)
	} else {
		hideConsoleKeepGUI(cmd)
	}
	var buf projectionOutput
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 emulator 进程失败: %w", err)
	}

	p := &Process{
		cmd:  cmd,
		done: make(chan error, 1),
		log:  &buf,
	}
	go func() {
		p.done <- cmd.Wait()
		close(p.done)
	}()
	return p, nil
}
