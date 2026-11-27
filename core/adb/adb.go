package adb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"virdeck/config"
)

// Client wraps the Android Debug Bridge (adb) CLI for single and batch instance operations.
type Client struct {
	mu                    sync.RWMutex
	adbPath               string
	env                   []string
	serverMu              sync.Mutex
	authRecoveryAttempted bool
}

// NewClient creates a new adb Client. If adbPath is empty, "adb" from PATH is used.
func NewClient(adbPath string) *Client {
	if strings.TrimSpace(adbPath) == "" {
		adbPath = "adb"
	}
	return &Client{adbPath: adbPath}
}

// SetPath updates the adb executable path.
func (c *Client) SetPath(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(p) != "" {
		c.adbPath = p
	}
}

// SetEnv configures environment variables (such as ANDROID_ADB_SERVER_PORT=5137) injected into every adb command.
func (c *Client) SetEnv(env []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.env = env
}

// Path returns the configured adb executable path.
func (c *Client) Path() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.adbPath
}

// Serial returns the standard adb serial string for an emulator console port (e.g. "emulator-5554").
func Serial(port int) string {
	return fmt.Sprintf("emulator-%d", port)
}

func isInstallCommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-s" || a == "-P" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a == "install" || a == "install-multiple"
	}
	return false
}

func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	timeout := config.Values.Runtime.CommandTimeoutSeconds
	if isInstallCommand(args) {
		timeout = config.Values.Runtime.InstallTimeoutSeconds
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	c.mu.RLock()
	bin := c.adbPath
	env := append([]string(nil), c.env...)
	c.mu.RUnlock()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	if len(env) > 0 {
		cmd.Env = env
	}
	hideConsole(cmd)
	var out adbOutputTail
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := out.text()
	if err != nil {
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		if text != "" {
			return text, fmt.Errorf("adb %s: %w (%s)", strings.Join(args, " "), err, text)
		}
		return "", fmt.Errorf("adb %s: %w", strings.Join(args, " "), err)
	}
	return text, nil
}

const maxADBRunBytes = 32 << 20 // 32 MiB cap for binary stdout such as screencap

var errADBBytesOverflow = errors.New("adb 输出超过 32 MiB 安全上限")

type limitedBytesBuffer struct {
	limit    int
	buf      []byte
	overflow bool
}

func (b *limitedBytesBuffer) Write(p []byte) (int, error) {
	if len(b.buf)+len(p) > b.limit {
		b.overflow = true
		return 0, errADBBytesOverflow
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (c *Client) runBytes(ctx context.Context, args ...string) ([]byte, error) {
	timeout := config.Values.Runtime.CommandTimeoutSeconds
	if isInstallCommand(args) {
		timeout = config.Values.Runtime.InstallTimeoutSeconds
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	c.mu.RLock()
	bin := c.adbPath
	env := append([]string(nil), c.env...)
	c.mu.RUnlock()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	if len(env) > 0 {
		cmd.Env = env
	}
	hideConsole(cmd)
	stdout := &limitedBytesBuffer{limit: maxADBRunBytes}
	var stderr adbOutputTail
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.overflow {
		return nil, errADBBytesOverflow
	}
	if err != nil {
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		errText := stderr.text()
		if errText != "" {
			return nil, fmt.Errorf("adb %s: %w (%s)", strings.Join(args, " "), err, errText)
		}
		return nil, fmt.Errorf("adb %s: %w", strings.Join(args, " "), err)
	}
	return stdout.buf, nil
}

// IsBootCompleted checks whether Android's sys.boot_completed property equals "1".
func (c *Client) IsBootCompleted(ctx context.Context, port int) bool {
	ready, _ := c.bootStatus(ctx, port)
	return ready
}

func (c *Client) bootStatus(ctx context.Context, port int) (bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := c.run(callCtx, "-s", Serial(port), "shell", "getprop", "sys.boot_completed")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(out) != "1" {
		return false, fmt.Errorf("ADB 已连接，Android 尚未完成开机 (sys.boot_completed=%q)", out)
	}
	return true, nil
}

// recoverAuthentication reloads keys for the app-owned server once. Never restart
// the default/shared server, and serialize parallel instances' recovery attempts.
func (c *Client) recoverAuthentication(ctx context.Context) error {
	c.serverMu.Lock()
	defer c.serverMu.Unlock()
	if c.authRecoveryAttempted {
		return nil
	}
	c.mu.RLock()
	env := append([]string(nil), c.env...)
	c.mu.RUnlock()
	port, keys := "", ""
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(k) {
		case "ANDROID_ADB_SERVER_PORT":
			port = v
		case "ADB_VENDOR_KEYS":
			keys = v
		}
	}
	n, _ := strconv.Atoi(port)
	if n <= 0 || n == 5037 || keys == "" {
		return nil
	}
	c.authRecoveryAttempted = true
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := c.run(callCtx, "-P", port, "kill-server"); err != nil {
		return err
	}
	_, err := c.run(callCtx, "-P", port, "start-server")
	return err
}

// WaitForBoot preserves the last diagnostic, including an unauthorized device,
// instead of turning every ADB failure into an indistinguishable boot timeout.
func (c *Client) WaitForBoot(ctx context.Context, port int, interval time.Duration) error {
	if interval <= 0 {
		interval = 1500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr error
	for {
		if ctx.Err() != nil {
			if lastErr != nil {
				return fmt.Errorf("等待 %s 启动完成失败，最近检测结果：%v: %w", Serial(port), lastErr, ctx.Err())
			}
			return ctx.Err()
		}
		ready, err := c.bootStatus(ctx, port)
		if ready {
			return nil
		}
		if ctx.Err() == nil {
			lastErr = err
		}
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "unauthorized") && ctx.Err() == nil {
			if recoveryErr := c.recoverAuthentication(ctx); recoveryErr != nil {
				lastErr = fmt.Errorf("%v；重新加载 ADB 授权失败: %w", err, recoveryErr)
			}
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

// EmuKill sends the graceful "emu kill" command so the emulator flushes disk and saves quickboot state.
func (c *Client) EmuKill(ctx context.Context, port int) error {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.run(callCtx, "-s", Serial(port), "emu", "kill")
	return err
}

// InstallOptions configures APK installation flags on ADB.
type InstallOptions struct {
	GrantPermissions bool
}

// InstallAPK installs or replaces an APK on a single emulator port.
func (c *Client) InstallAPK(ctx context.Context, port int, apkPath string) (string, error) {
	return c.InstallAPKWithOptions(ctx, port, apkPath, InstallOptions{})
}

// InstallAPKWithOptions installs or replaces an APK on a single emulator port with optional runtime permission grant (-g).
func (c *Client) InstallAPKWithOptions(ctx context.Context, port int, apkPath string, opts InstallOptions) (string, error) {
	apkPath = strings.TrimSpace(apkPath)
	if apkPath == "" || strings.HasPrefix(apkPath, "-") {
		return "", fmt.Errorf("无效的 APK 路径: %q", apkPath)
	}
	args := []string{"-s", Serial(port), "install", "-r"}
	if opts.GrantPermissions {
		args = append(args, "-g")
	}
	args = append(args, apkPath)
	return c.run(ctx, args...)
}

// InstallMultipleAPKsWithOptions installs split APKs atomically using `adb install-multiple -r [-g]`.
func (c *Client) InstallMultipleAPKsWithOptions(ctx context.Context, port int, apkPaths []string, opts InstallOptions) (string, error) {
	cleaned := make([]string, 0, len(apkPaths))
	for _, p := range apkPaths {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "-") {
			return "", fmt.Errorf("无效的分包 APK 路径: %q", p)
		}
		cleaned = append(cleaned, p)
	}
	if len(cleaned) == 0 {
		return "", errors.New("分包 APK 列表不能为空")
	}
	if len(cleaned) == 1 {
		return c.InstallAPKWithOptions(ctx, port, cleaned[0], opts)
	}
	args := []string{"-s", Serial(port), "install-multiple", "-r"}
	if opts.GrantPermissions {
		args = append(args, "-g")
	}
	args = append(args, cleaned...)
	return c.run(ctx, args...)
}

// TranslateInstallError turns common ADB INSTALL_FAILED_* errors into user-friendly Chinese diagnostics.
func TranslateInstallError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "INSTALL_FAILED_VERSION_DOWNGRADE"):
		return "安装失败：设备已安装更高版本，不允许降级覆盖（如需降级请先卸载旧版）"
	case strings.Contains(msg, "INSTALL_FAILED_UPDATE_INCOMPATIBLE"):
		return "安装失败：应用签名与已安装版本冲突，需先卸载旧版再安装（注意：卸载会清除该应用本地数据）"
	case strings.Contains(msg, "INSTALL_FAILED_INSUFFICIENT_STORAGE"):
		return "安装失败：模拟器内部存储空间不足，请清理空间或调大实例存储容量"
	case strings.Contains(msg, "INSTALL_FAILED_NO_MATCHING_ABIS"):
		return "安装失败：该 APK 不包含与当前模拟器架构匹配的原生库（请更换支持 ARM 转译的 Android 11/14 镜像）"
	default:
		return msg
	}
}

// CheckPackageManagerReady waits until Android's package manager service responds to "pm path android".
func (c *Client) CheckPackageManagerReady(ctx context.Context, port int) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var lastErr error
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		callCtx, callCancel := context.WithTimeout(deadlineCtx, 4*time.Second)
		out, err := c.run(callCtx, "-s", Serial(port), "shell", "pm", "path", "android")
		callCancel()
		if err == nil && strings.Contains(out, "package:") {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("包管理服务尚未就绪: %q", strings.TrimSpace(out))
		}
		select {
		case <-deadlineCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("包管理服务等待超时: %v (%w)", lastErr, deadlineCtx.Err())
			}
			return deadlineCtx.Err()
		case <-ticker.C:
		}
	}
}

var (
	validPackageRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)
	validComponentRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+/[A-Za-z0-9_.$]+$`)
)

func validateSingleToken(val, label string) (string, error) {
	val = strings.TrimSpace(val)
	if val == "" || strings.HasPrefix(val, "-") {
		return "", fmt.Errorf("无效的%s: %q", label, val)
	}
	for _, r := range val {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", fmt.Errorf("无效的%s: %q", label, val)
		}
	}
	return val, nil
}

func validatePackageName(val string) (string, error) {
	val, err := validateSingleToken(val, "应用包名")
	if err != nil {
		return "", err
	}
	if !validPackageRe.MatchString(val) {
		return "", fmt.Errorf("无效的应用包名: %q", val)
	}
	return val, nil
}

func validateComponentName(val string) (string, error) {
	val, err := validateSingleToken(val, "启动入口")
	if err != nil {
		return "", err
	}
	if !validComponentRe.MatchString(val) {
		return "", fmt.Errorf("无效的启动入口: %q", val)
	}
	return val, nil
}

// parseResolvedComponent scans resolve-activity --brief output from bottom to top for a valid <pkg>/<activity> component.
func parseResolvedComponent(out, pkg string) string {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !validComponentRe.MatchString(line) {
			continue
		}
		left, _, ok := strings.Cut(line, "/")
		if !ok || left == "" {
			continue
		}
		if pkg == "" || left == pkg {
			return line
		}
	}
	return ""
}

// ResolveLauncherActivity queries Android's package manager for the main LAUNCHER activity of pkg.
// Returns "", nil when the package has no launcher activity (e.g., background service or IME); never falls back to monkey.
func (c *Client) ResolveLauncherActivity(ctx context.Context, port int, pkg string) (string, error) {
	safePkg, err := validatePackageName(pkg)
	if err != nil {
		return "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := c.run(callCtx, "-s", Serial(port), "shell", "cmd", "package", "resolve-activity", "--brief", "-c", "android.intent.category.LAUNCHER", safePkg)
	if err != nil {
		lower := strings.ToLower(out + " " + err.Error())
		if strings.Contains(lower, "no activity found") {
			return "", nil
		}
		return "", err
	}
	return parseResolvedComponent(out, safePkg), nil
}

// LaunchActivity launches an Android component ("<pkg>/<activity>") via "am start -n".
func (c *Client) LaunchActivity(ctx context.Context, port int, component string) error {
	safeComp, err := validateComponentName(component)
	if err != nil {
		return err
	}
	argComp := safeComp
	if strings.ContainsRune(safeComp, '$') {
		argComp = transferQuote(safeComp)
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := c.run(callCtx, "-s", Serial(port), "shell", "am", "start", "-n", argComp)
	if err != nil {
		return err
	}
	if strings.Contains(out, "Error:") || strings.Contains(out, "Exception") {
		return fmt.Errorf("拉起应用失败: %s", strings.TrimSpace(out))
	}
	return nil
}

// Shell executes an adb shell command on a single emulator port.
func (c *Client) Shell(ctx context.Context, port int, shellCmd string) (string, error) {
	return c.run(ctx, "-s", Serial(port), "shell", shellCmd)
}

// GetABIList queries ro.product.cpu.abilist on a booted emulator instance.
func (c *Client) GetABIList(ctx context.Context, port int) ([]string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := c.run(callCtx, "-s", Serial(port), "shell", "getprop", "ro.product.cpu.abilist")
	if err != nil {
		return nil, err
	}
	var abis []string
	for _, part := range strings.Split(out, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			abis = append(abis, trimmed)
		}
	}
	return abis, nil
}

// BatchInstallAPK concurrently installs an APK across multiple instances mapped by instanceID -> port.
func (c *Client) BatchInstallAPK(ctx context.Context, targets map[string]int, apkPath string) error {
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	for id, port := range targets {
		wg.Add(1)
		go func(instID string, p int) {
			defer wg.Done()
			if _, err := c.InstallAPK(ctx, p, apkPath); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("[%s] %w", instID, err))
				mu.Unlock()
			}
		}(id, port)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// BatchShell concurrently executes a shell command across multiple instances mapped by instanceID -> port.
func (c *Client) BatchShell(ctx context.Context, targets map[string]int, shellCmd string) (map[string]string, error) {
	var (
		mu      sync.Mutex
		results = make(map[string]string, len(targets))
		errs    []error
		wg      sync.WaitGroup
	)
	for id, port := range targets {
		wg.Add(1)
		go func(instID string, p int) {
			defer wg.Done()
			out, err := c.Shell(ctx, p, shellCmd)
			mu.Lock()
			results[instID] = out
			if err != nil {
				errs = append(errs, fmt.Errorf("[%s] %w", instID, err))
			}
			mu.Unlock()
		}(id, port)
	}
	wg.Wait()
	return results, errors.Join(errs...)
}

// DiscoverEmulators queries "adb devices" and "adb -s emulator-<port> emu avd name"
// to discover currently running emulators on the host, returning map[avdName]port.
func (c *Client) DiscoverEmulators(ctx context.Context) map[string]int {
	found, _, err := c.DiscoverEmulatorsStrict(ctx)
	if err != nil && len(found) == 0 {
		return nil
	}
	return found
}

// DiscoverEmulatorsStrict queries running emulator serials and their AVD names.
// unknownCount tracks offline, booting, or unresponsive emulator-<port> entries so
// callers can refuse destructive lock cleanup whenever ADB discovery is incomplete.
func (c *Client) DiscoverEmulatorsStrict(ctx context.Context) (active map[string]int, unknownCount int, err error) {
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	out, err := c.run(callCtx, "devices")
	if err != nil {
		return nil, 0, err
	}
	if !strings.Contains(out, "List of devices attached") {
		return nil, 0, errors.New("ADB 设备列表格式异常")
	}
	active = make(map[string]int)
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "emulator-") {
			continue
		}
		portStr := strings.TrimPrefix(fields[0], "emulator-")
		port, perr := strconv.Atoi(portStr)
		if perr != nil || port <= 0 {
			unknownCount++
			continue
		}
		if len(fields) < 2 || fields[1] != "device" {
			unknownCount++
			continue
		}
		nameOut, nerr := c.run(callCtx, "-s", fields[0], "emu", "avd", "name")
		if nerr != nil {
			unknownCount++
			continue
		}
		lines := strings.Split(strings.ReplaceAll(nameOut, "\r\n", "\n"), "\n")
		avdName := ""
		if len(lines) > 0 {
			avdName = strings.TrimSpace(lines[0])
		}
		if avdName == "" || avdName == "OK" {
			unknownCount++
			continue
		}
		active[avdName] = port
	}
	return active, unknownCount, nil
}

// HasEmulators counts offline/booting emulator serials without requiring an AVD name.
// Errors must never be interpreted as an idle machine by cache cleanup.
func (c *Client) HasEmulators(ctx context.Context) (bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := c.run(callCtx, "devices")
	if err != nil {
		return false, err
	}
	if !strings.Contains(out, "List of devices attached") {
		return false, errors.New("ADB 设备列表格式异常")
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "emulator-") {
			return true, nil
		}
	}
	return false, nil
}

// SetLocation sets the simulated GPS coordinates on a running emulator.
// Note: "adb emu geo fix" expects longitude first and latitude second.
func (c *Client) SetLocation(ctx context.Context, port int, lon, lat float64) error {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		return fmt.Errorf("纬度须在 -90 到 90 之间: %v", lat)
	}
	if math.IsNaN(lon) || math.IsInf(lon, 0) || lon < -180 || lon > 180 {
		return fmt.Errorf("经度须在 -180 到 180 之间: %v", lon)
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := c.run(callCtx, "-s", Serial(port), "emu", "geo", "fix",
		strconv.FormatFloat(lon, 'f', 6, 64),
		strconv.FormatFloat(lat, 'f', 6, 64),
	)
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToUpper(out), "KO:") {
		return fmt.Errorf("设置虚拟定位失败: %s", strings.TrimSpace(out))
	}
	return nil
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// Screenshot captures the current device display as PNG bytes via "adb exec-out screencap -p".
func (c *Client) Screenshot(ctx context.Context, port int) ([]byte, error) {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, err := c.runBytes(callCtx, "-s", Serial(port), "exec-out", "screencap", "-p")
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(raw, pngSignature) {
		return nil, fmt.Errorf("截图输出不是有效的 PNG 格式（长度 %d 字节）", len(raw))
	}
	return raw, nil
}

// ListPackages lists installed Android package names sorted alphabetically.
func (c *Client) ListPackages(ctx context.Context, port int, thirdPartyOnly bool) ([]string, error) {
	args := []string{"-s", Serial(port), "shell", "pm", "list", "packages"}
	if thirdPartyOnly {
		args = append(args, "-3")
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := c.run(callCtx, args...)
	if err != nil {
		return nil, err
	}
	return parsePackageList(out), nil
}

func parsePackageList(out string) []string {
	seen := make(map[string]struct{})
	var pkgs []string
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "package:") {
			continue
		}
		pkg := strings.TrimSpace(strings.TrimPrefix(line, "package:"))
		if pkg == "" {
			continue
		}
		if _, exists := seen[pkg]; exists {
			continue
		}
		seen[pkg] = struct{}{}
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	return pkgs
}

// ForceStopPackage terminates all processes for pkg via "am force-stop".
func (c *Client) ForceStopPackage(ctx context.Context, port int, pkg string) error {
	safePkg, err := validatePackageName(pkg)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_, err = c.run(callCtx, "-s", Serial(port), "shell", "am", "force-stop", safePkg)
	return err
}

// ClearPackageData wipes app data and cache via "pm clear".
func (c *Client) ClearPackageData(ctx context.Context, port int, pkg string) error {
	safePkg, err := validatePackageName(pkg)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := c.run(callCtx, "-s", Serial(port), "shell", "pm", "clear", safePkg)
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(out), "failed") {
		return fmt.Errorf("清除应用数据失败: %s", strings.TrimSpace(out))
	}
	return nil
}

// UninstallPackage removes pkg via "adb uninstall".
func (c *Client) UninstallPackage(ctx context.Context, port int, pkg string) error {
	safePkg, err := validatePackageName(pkg)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := c.run(callCtx, "-s", Serial(port), "uninstall", safePkg)
	if err != nil {
		return err
	}
	if strings.Contains(out, "Failure") {
		return fmt.Errorf("卸载应用失败: %s", strings.TrimSpace(out))
	}
	return nil
}

// TrimStorage runs "adb shell sm fstrim" on a booted emulator to reclaim free blocks.
func (c *Client) TrimStorage(ctx context.Context, port int) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return c.run(callCtx, "-s", Serial(port), "shell", "sm", "fstrim")
}
