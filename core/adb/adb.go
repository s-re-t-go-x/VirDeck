package adb

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
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

func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	timeout := config.Values.Runtime.CommandTimeoutSeconds
	if len(args) > 2 && args[2] == "install" {
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

// InstallAPK installs or replaces an APK on a single emulator port.
func (c *Client) InstallAPK(ctx context.Context, port int, apkPath string) (string, error) {
	apkPath = strings.TrimSpace(apkPath)
	if apkPath == "" || strings.HasPrefix(apkPath, "-") {
		return "", fmt.Errorf("无效的 APK 路径: %q", apkPath)
	}
	return c.run(ctx, "-s", Serial(port), "install", "-r", apkPath)
}

// Shell executes an adb shell command on a single emulator port.
func (c *Client) Shell(ctx context.Context, port int, shellCmd string) (string, error) {
	return c.run(ctx, "-s", Serial(port), "shell", shellCmd)
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
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	out, err := c.run(callCtx, "devices")
	if err != nil {
		return nil
	}
	found := make(map[string]int)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "emulator-") {
			continue
		}
		portStr := strings.TrimPrefix(fields[0], "emulator-")
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 {
			continue
		}
		nameOut, err := c.run(callCtx, "-s", fields[0], "emu", "avd", "name")
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(nameOut, "\r\n", "\n"), "\n")
		if len(lines) > 0 {
			avdName := strings.TrimSpace(lines[0])
			if avdName != "" && avdName != "OK" {
				found[avdName] = port
			}
		}
	}
	return found
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
